package singbox

import (
	"strings"
	"testing"

	"xray-manager/internal/models"
)

// ssRule 构造一个 SS 节点，默认裸 TCP
func ssRule() *models.ProxyRule {
	rule := sampleRule("ss-node", "shadowsocks", "38.207.132.234", 443)
	rule.Settings.SSMethod = "aes-256-gcm"
	rule.Settings.SSPassword = "fRE1qjMQes"
	return rule
}

// 裸 SS 不应该带插件字段
func TestShadowsocksPlainHasNoPlugin(t *testing.T) {
	out, err := BuildOutbound(ssRule(), "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := out["plugin"]; ok {
		t.Error("plain shadowsocks should not carry a plugin")
	}
}

// SS + ws + tls 必须映射成 v2ray-plugin。
// 早前这里被静默丢弃，节点退化成裸 TCP，表现为启动后 "EOF"。
func TestShadowsocksWSTLSMapsToPlugin(t *testing.T) {
	rule := ssRule()
	rule.Settings.Network = "ws"
	rule.Settings.Security = "tls"
	rule.Settings.WS = &models.WSSettings{Path: "/ws"}
	rule.Settings.TLS = &models.TLSSettings{ServerName: "example.com"}

	out, err := BuildOutbound(rule, "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["plugin"] != "v2ray-plugin" {
		t.Errorf("plugin = %v, want v2ray-plugin", out["plugin"])
	}

	opts, _ := out["plugin_opts"].(string)
	for _, want := range []string{"mode=websocket", "tls", "host=example.com", "path=/ws"} {
		if !strings.Contains(opts, want) {
			t.Errorf("plugin_opts %q missing %q", opts, want)
		}
	}

	// sing-box 的 shadowsocks 出站没有这两个字段，写了会拒绝加载
	if _, ok := out["tls"]; ok {
		t.Error("shadowsocks outbound must not carry a tls field")
	}
	if _, ok := out["transport"]; ok {
		t.Error("shadowsocks outbound must not carry a transport field")
	}
}

// host 缺省会让插件用 "cloudfront.com" 作 SNI，必须回落到服务器地址
func TestShadowsocksWSFallsBackToServerAddr(t *testing.T) {
	rule := ssRule()
	rule.Settings.Network = "ws"
	rule.Settings.Security = "tls"

	out, err := BuildOutbound(rule, "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts, _ := out["plugin_opts"].(string)
	if !strings.Contains(opts, "host=38.207.132.234") {
		t.Errorf("plugin_opts %q should fall back to the server address", opts)
	}
	if !strings.Contains(opts, "path=/") {
		t.Errorf("plugin_opts %q should default the path to /", opts)
	}
}

// ws 不带 TLS 也要走插件，否则同样退化成裸 TCP
func TestShadowsocksWSWithoutTLS(t *testing.T) {
	rule := ssRule()
	rule.Settings.Network = "ws"

	out, err := BuildOutbound(rule, "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts, _ := out["plugin_opts"].(string)
	if !strings.Contains(opts, "mode=websocket") {
		t.Errorf("plugin_opts %q should still request websocket", opts)
	}
	if strings.Contains(opts, "tls") {
		t.Errorf("plugin_opts %q should not enable tls", opts)
	}
}

// 内核表达不了的组合必须在启动前明确报错，而不是静默降级
func TestShadowsocksUnsupportedTransportsRejected(t *testing.T) {
	cases := []struct {
		name     string
		network  string
		security string
		insecure bool
		wantHint string
	}{
		{"grpc", "grpc", "tls", false, "grpc"},
		{"h2", "h2", "tls", false, "h2"},
		{"reality", "ws", "reality", false, "REALITY"},
		{"tcp+tls", "tcp", "tls", false, "tcp"},
		{"empty+tls", "", "tls", false, "该"},
		{"allowInsecure", "ws", "tls", true, "allowInsecure"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := ssRule()
			rule.Settings.Network = tc.network
			rule.Settings.Security = tc.security
			rule.Settings.TLS = &models.TLSSettings{AllowInsecure: tc.insecure}

			_, err := BuildOutbound(rule, "proxy")
			if err == nil {
				t.Fatal("expected an error for an unsupported transport combination")
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q should mention %q", err, tc.wantHint)
			}
		})
	}
}

// 值里的分隔符不转义会被 ParsePluginOptions 拆成别的键
func TestShadowsocksPluginOptEscaping(t *testing.T) {
	rule := ssRule()
	rule.Settings.Network = "ws"
	rule.Settings.WS = &models.WSSettings{Path: "/a;b=c"}

	out, err := BuildOutbound(rule, "proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts, _ := out["plugin_opts"].(string)
	if !strings.Contains(opts, `path=/a\;b\=c`) {
		t.Errorf("plugin_opts %q should escape ; and = in the path", opts)
	}
}
