package parser

import (
	"strings"
	"testing"
)

// 早期版本解析 vless 分享链接时丢弃了 pbk/sid/fp，
// 导致 REALITY 节点存进配置后永远无法启动（Xray 拒绝加载）。
func TestParseVlessRealityKeepsParameters(t *testing.T) {
	link := "vless://adf8d585-7c01-40be-84f6-d4d2a31caa49@1.2.3.4:443" +
		"?encryption=none&security=reality&sni=www.microsoft.com" +
		"&fp=chrome&pbk=gKMEfvBQK0mE8ZlZmYFvvNSnRRl3TjLPKGpXfLwMFXM" +
		"&sid=6ba85179e30d4fc2&spx=%2F&type=tcp&flow=xtls-rprx-vision#REALITY节点"

	rule, err := NewShareLinkParser().ParseLink(link)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if rule.Settings.Security != "reality" {
		t.Fatalf("security 应为 reality，实际 %q", rule.Settings.Security)
	}
	r := rule.Settings.Reality
	if r == nil {
		t.Fatal("REALITY 参数未被保留，节点将无法启动")
	}
	if r.PublicKey != "gKMEfvBQK0mE8ZlZmYFvvNSnRRl3TjLPKGpXfLwMFXM" {
		t.Fatalf("pbk 丢失或错误: %q", r.PublicKey)
	}
	if r.ShortID != "6ba85179e30d4fc2" {
		t.Fatalf("sid 丢失或错误: %q", r.ShortID)
	}
	if r.Fingerprint != "chrome" {
		t.Fatalf("fp 丢失或错误: %q", r.Fingerprint)
	}
	if r.SpiderX != "/" {
		t.Fatalf("spx 丢失或错误: %q", r.SpiderX)
	}
	if r.ServerName != "www.microsoft.com" {
		t.Fatalf("sni 丢失或错误: %q", r.ServerName)
	}
}

// 导出的链接要能原样再导入，否则复制分享链接会丢掉 REALITY 参数
func TestEncodeVlessRealityRoundTrip(t *testing.T) {
	link := "vless://adf8d585-7c01-40be-84f6-d4d2a31caa49@1.2.3.4:443" +
		"?encryption=none&security=reality&sni=www.microsoft.com" +
		"&fp=chrome&pbk=gKMEfvBQK0mE8ZlZmYFvvNSnRRl3TjLPKGpXfLwMFXM" +
		"&sid=6ba85179e30d4fc2&type=tcp&flow=xtls-rprx-vision#REALITY节点"

	rule, err := NewShareLinkParser().ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := NewShareLinkParser().EncodeLink(rule)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pbk=", "sid=", "security=reality"} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("导出链接缺少 %s: %s", want, encoded)
		}
	}

	back, err := NewShareLinkParser().ParseLink(encoded)
	if err != nil {
		t.Fatalf("导出的链接无法再解析: %v", err)
	}
	if back.Settings.Reality == nil || back.Settings.Reality.PublicKey != rule.Settings.Reality.PublicKey {
		t.Fatal("往返后 REALITY 公钥丢失")
	}
}
