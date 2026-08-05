package singbox

import (
	"testing"

	"xray-manager/internal/models"
)

// realityRule 构造一个与真实订阅一致的 vless+REALITY 节点
func realityRule() *models.ProxyRule {
	rule := sampleRule("reality-node", "vless", "169.40.42.15", 443)
	rule.Settings.VLessUserID = "d65cc14c-f53f-4fe2-b262-97856601319c"
	rule.Settings.VLessFlow = "xtls-rprx-vision"
	rule.Settings.Network = "tcp"
	rule.Settings.Security = "reality"
	rule.Settings.TLS = &models.TLSSettings{ServerName: "yahoo.com"}
	rule.Settings.Reality = &models.RealitySettings{
		PublicKey:  "e2RLf57Li_-MDZGE9ss1BWPgP54mqRb5PfXhW2jcVVg",
		ShortID:    "c39cc7310a",
		ServerName: "yahoo.com",
	}
	return rule
}

// REALITY 参数必须完整写进 sing-box 配置，丢失会退化成普通 TLS 握手，
// 表现为 "unknown version: 72"（服务端返回明文，首字节 'H'）。
func TestBuildTLSIncludesReality(t *testing.T) {
	tls := buildTLS(realityRule(), false)
	if tls == nil {
		t.Fatal("expected TLS config for a REALITY node")
	}

	reality, ok := tls["reality"].(map[string]interface{})
	if !ok {
		t.Fatal("reality section missing from TLS config")
	}
	if reality["enabled"] != true {
		t.Error("reality.enabled should be true")
	}
	if got := reality["public_key"]; got != "e2RLf57Li_-MDZGE9ss1BWPgP54mqRb5PfXhW2jcVVg" {
		t.Errorf("reality.public_key = %v, want the configured pbk", got)
	}
	if got := reality["short_id"]; got != "c39cc7310a" {
		t.Errorf("reality.short_id = %v, want c39cc7310a", got)
	}
	if got := tls["server_name"]; got != "yahoo.com" {
		t.Errorf("server_name = %v, want yahoo.com", got)
	}

	// sing-box 的 REALITY 依赖 uTLS，缺指纹会拒绝加载配置
	utls, ok := tls["utls"].(map[string]interface{})
	if !ok {
		t.Fatal("utls section missing; sing-box rejects REALITY without it")
	}
	if utls["enabled"] != true {
		t.Error("utls.enabled should be true")
	}
	if utls["fingerprint"] != "chrome" {
		t.Errorf("utls.fingerprint = %v, want the chrome default", utls["fingerprint"])
	}
}

// 分享链接里的 fp 参数应被采用，而不是一律用默认值
func TestBuildTLSUsesConfiguredFingerprint(t *testing.T) {
	rule := realityRule()
	rule.Settings.Reality.Fingerprint = "firefox"

	utls, _ := buildTLS(rule, false)["utls"].(map[string]interface{})
	if utls == nil || utls["fingerprint"] != "firefox" {
		t.Fatalf("expected the configured firefox fingerprint, got %v", utls)
	}
}

// SNI 只记在 TLS 段、REALITY 段缺失时应回退取用（与 Xray 侧行为一致）
func TestBuildTLSFallsBackToTLSServerName(t *testing.T) {
	rule := realityRule()
	rule.Settings.Reality.ServerName = ""
	rule.Settings.TLS = &models.TLSSettings{ServerName: "www.microsoft.com", Fingerprint: "safari"}

	tls := buildTLS(rule, false)
	if got := tls["server_name"]; got != "www.microsoft.com" {
		t.Errorf("server_name = %v, want the TLS-section fallback", got)
	}
	utls, _ := tls["utls"].(map[string]interface{})
	if utls == nil || utls["fingerprint"] != "safari" {
		t.Errorf("fingerprint should fall back to the TLS section, got %v", utls)
	}
}

// REALITY 自带证书校验，insecure 与之冲突，必须剔除
func TestBuildTLSDropsInsecureForReality(t *testing.T) {
	rule := realityRule()
	rule.Settings.TLS.AllowInsecure = true

	if _, exists := buildTLS(rule, false)["insecure"]; exists {
		t.Error("insecure must not be set alongside REALITY")
	}
}

// 非 REALITY 节点不应被塞进 reality/utls 段
func TestBuildTLSPlainTLSUnaffected(t *testing.T) {
	rule := sampleRule("tls-node", "vless", "1.2.3.4", 443)
	rule.Settings.Security = "tls"
	rule.Settings.TLS = &models.TLSSettings{ServerName: "example.com", AllowInsecure: true}

	tls := buildTLS(rule, false)
	if _, exists := tls["reality"]; exists {
		t.Error("plain TLS node must not carry a reality section")
	}
	if _, exists := tls["utls"]; exists {
		t.Error("plain TLS node must not carry a utls section")
	}
	if tls["insecure"] != true {
		t.Error("insecure should be preserved for plain TLS nodes")
	}
}

// 端到端：经 hy2 前置代理的 REALITY 节点（触发本次故障的组合）
func TestChainConfigPreservesReality(t *testing.T) {
	pre := sampleRule("pre", "hysteria2", "142.4.37.148", 35721)
	cfg, err := BuildChainConfig(11000, []*models.ProxyRule{pre, realityRule()})
	if err != nil {
		t.Fatalf("BuildChainConfig: %v", err)
	}

	var found bool
	for _, outbound := range cfg.Outbounds {
		tls, ok := outbound["tls"].(map[string]interface{})
		if !ok {
			continue
		}
		if reality, ok := tls["reality"].(map[string]interface{}); ok {
			if reality["public_key"] == "" || reality["public_key"] == nil {
				t.Error("chain outbound lost the REALITY public key")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no REALITY outbound found in the chain config")
	}
}
