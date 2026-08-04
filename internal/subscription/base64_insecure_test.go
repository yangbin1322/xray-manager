package subscription

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 订阅解析器此前为 vmess/vless/ss/trojan 各自写了一份实现，与 internal/parser
// 那套重复。两份代码分叉后，allowInsecure、REALITY 参数等只在分享链接那边支持，
// 于是"同一个链接直接粘贴能用、从 base64 订阅导入却不能用"。
// 现在统一走同一个解析器。
func TestBase64SubscriptionKeepsAllowInsecure(t *testing.T) {
	links := strings.Join([]string{
		"trojan://73729e94-c0ed-4d6f-831b-fcfd4b7c8af6@pokemonus-02.yunjnet.com:56017" +
			"?security=tls&sni=iosapps.itunes.apple.com&allowInsecure=1&type=tcp#美国04",
		"vless://11111111-1111-1111-1111-111111111111@example.com:443" +
			"?encryption=none&security=tls&sni=fake.apple.com&allowInsecure=1&type=tcp#VLESS节点A",
	}, "\n")
	content := []byte(base64.StdEncoding.EncodeToString([]byte(links)))

	rules, err := NewParser(nil).ParseBase64(content)
	if err != nil {
		t.Fatalf("解析 base64 订阅失败: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("应解析出 2 个节点，实际 %d 个", len(rules))
	}

	for _, r := range rules {
		if r.Settings.TLS == nil {
			t.Fatalf("节点「%s」TLS 配置为空", r.Alias)
		}
		if !r.Settings.TLS.AllowInsecure {
			t.Errorf("节点「%s」的 allowInsecure 未被解析，伪装 SNI 会导致握手失败", r.Alias)
		}
		if r.Source != "subscription" {
			t.Errorf("节点「%s」来源应为 subscription，实际 %s", r.Alias, r.Source)
		}
	}

	// 别名要保留链接里的 #name，不能被占位名覆盖
	if rules[0].Alias != "美国04" {
		t.Errorf("别名应为「美国04」，实际 %q", rules[0].Alias)
	}
	// SNI 也要正确带过来
	if rules[0].Settings.TLS.ServerName != "iosapps.itunes.apple.com" {
		t.Errorf("SNI 错误: %q", rules[0].Settings.TLS.ServerName)
	}
}

// REALITY 参数同样要在订阅路径上保留
func TestBase64SubscriptionKeepsRealityParams(t *testing.T) {
	link := "vless://adf8d585-7c01-40be-84f6-d4d2a31caa49@1.2.3.4:443" +
		"?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome" +
		"&pbk=gKMEfvBQK0mE8ZlZmYFvvNSnRRl3TjLPKGpXfLwMFXM&sid=6ba85179e30d4fc2&type=tcp#REALITY节点"
	content := []byte(base64.StdEncoding.EncodeToString([]byte(link)))

	rules, err := NewParser(nil).ParseBase64(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 {
		t.Fatalf("应解析出 1 个节点，实际 %d", len(rules))
	}
	r := rules[0].Settings.Reality
	if r == nil || r.PublicKey == "" {
		t.Fatal("订阅导入的 REALITY 节点丢失了公钥，将无法启动")
	}
	if r.ShortID != "6ba85179e30d4fc2" {
		t.Errorf("sid 丢失: %q", r.ShortID)
	}
}

// 没带 #别名 时应给出带序号的占位名，方便在成百上千个节点里区分
func TestBase64SubscriptionFallbackAlias(t *testing.T) {
	links := strings.Join([]string{
		"trojan://pw@a.example.com:443?security=tls",
		"trojan://pw@b.example.com:443?security=tls",
	}, "\n")
	content := []byte(base64.StdEncoding.EncodeToString([]byte(links)))

	rules, err := NewParser(nil).ParseBase64(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("应解析 2 个节点，实际 %d", len(rules))
	}
	if rules[0].Alias == rules[1].Alias {
		t.Fatalf("无别名节点应有可区分的占位名，实际都是 %q", rules[0].Alias)
	}
	for _, r := range rules {
		if !strings.HasPrefix(r.Alias, "Trojan_") {
			t.Errorf("占位别名格式不对: %q", r.Alias)
		}
	}
}
