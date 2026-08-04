package parser

import "testing"

// 用户实际遇到的链接：伪装 SNI（iosapps.itunes.apple.com）+ allowInsecure=1。
// 此前 Trojan 解析器完全忽略 allowInsecure，Xray 会拿伪装 SNI 去校验证书，
// 必然握手失败——同样的链接在 v2rayN 里能用，因为 v2rayN 认这个参数。
func TestParseTrojanKeepsAllowInsecure(t *testing.T) {
	link := "trojan://73729e94-c0ed-4d6f-831b-fcfd4b7c8af6@pokemonus-02.yunjnet.com:56017" +
		"?security=tls&sni=iosapps.itunes.apple.com&allowInsecure=1&type=tcp&headerType=none#测试节点"

	rule, err := NewShareLinkParser().ParseLink(link)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if rule.Protocol != "trojan" {
		t.Fatalf("协议应为 trojan，实际 %s", rule.Protocol)
	}
	if rule.ServerAddr != "pokemonus-02.yunjnet.com" || rule.ServerPort != 56017 {
		t.Fatalf("地址解析错误: %s:%d", rule.ServerAddr, rule.ServerPort)
	}
	if rule.Settings.TrojanPassword != "73729e94-c0ed-4d6f-831b-fcfd4b7c8af6" {
		t.Fatalf("密码解析错误: %s", rule.Settings.TrojanPassword)
	}
	if rule.Settings.TLS == nil {
		t.Fatal("TLS 配置为空")
	}
	if rule.Settings.TLS.ServerName != "iosapps.itunes.apple.com" {
		t.Fatalf("SNI 解析错误: %s", rule.Settings.TLS.ServerName)
	}
	if !rule.Settings.TLS.AllowInsecure {
		t.Fatal("allowInsecure=1 未被解析，TLS 校验会因 SNI 与证书不匹配而失败")
	}
}

// 不同客户端用的参数名不一样，都要认
func TestParseInsecureFlagAliases(t *testing.T) {
	base := "trojan://pw@example.com:443?security=tls&sni=fake.example.com"
	cases := []struct {
		query string
		want  bool
	}{
		{"&allowInsecure=1", true},
		{"&allowInsecure=true", true},
		{"&allow_insecure=1", true},
		{"&insecure=1", true},
		{"&skip-cert-verify=true", true},
		{"", false}, // 不带就不该开
		{"&allowInsecure=0", false},
		{"&allowInsecure=false", false},
	}
	for _, c := range cases {
		rule, err := NewShareLinkParser().ParseLink(base + c.query + "#n")
		if err != nil {
			t.Fatalf("%q 解析失败: %v", c.query, err)
		}
		got := rule.Settings.TLS != nil && rule.Settings.TLS.AllowInsecure
		if got != c.want {
			t.Errorf("参数 %q：allowInsecure 应为 %v，实际 %v", c.query, c.want, got)
		}
	}
}

// vless 同样是 Xray 系协议，也常带伪装 SNI
func TestParseVlessKeepsAllowInsecure(t *testing.T) {
	link := "vless://11111111-1111-1111-1111-111111111111@example.com:443" +
		"?encryption=none&security=tls&sni=fake.apple.com&allowInsecure=1&type=tcp#n"
	rule, err := NewShareLinkParser().ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if rule.Settings.TLS == nil || !rule.Settings.TLS.AllowInsecure {
		t.Fatal("vless 的 allowInsecure 未被解析")
	}
}

// 导出的链接要能把这个标志带回来，否则复制出去的链接又不可用了
func TestEncodeTrojanKeepsAllowInsecure(t *testing.T) {
	p := NewShareLinkParser()
	link := "trojan://pw@example.com:443?security=tls&sni=fake.example.com&allowInsecure=1#n"
	rule, err := p.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := p.EncodeLink(rule)
	if err != nil {
		t.Fatal(err)
	}
	back, err := p.ParseLink(encoded)
	if err != nil {
		t.Fatalf("导出的链接无法再解析: %v", err)
	}
	if back.Settings.TLS == nil || !back.Settings.TLS.AllowInsecure {
		t.Fatalf("往返后 allowInsecure 丢失: %s", encoded)
	}
}
