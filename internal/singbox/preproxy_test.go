package singbox

import (
	"testing"
	"xray-manager/internal/models"
)

func sampleRule(id, protocol, addr string, port int) *models.ProxyRule {
	rule := &models.ProxyRule{
		ID:         id,
		Alias:      id,
		LocalType:  "mixed",
		LocalPort:  1080,
		Protocol:   protocol,
		ServerAddr: addr,
		ServerPort: port,
		Settings:   models.ProxySettings{},
	}
	if protocol == "hysteria2" {
		rule.Settings.Hy2Password = "pass"
	}
	if protocol == "vmess" {
		rule.Settings.VMessUserID = "00000000-0000-0000-0000-000000000001"
	}
	return rule
}

func TestBuildLoadBalanceConfig_WithPreProxy(t *testing.T) {
	pre := sampleRule("pre", "hysteria2", "1.1.1.1", 443)
	n1 := sampleRule("n1", "hysteria2", "2.2.2.2", 443)
	cfg, err := BuildLoadBalanceConfig(10900, []*models.ProxyRule{n1}, pre)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var hasPre, hasDetour bool
	for _, o := range cfg.Outbounds {
		tag, _ := o["tag"].(string)
		if tag == "pre_proxy" {
			hasPre = true
		}
		if tag == "proxy_0" {
			if o["detour"] == "pre_proxy" {
				hasDetour = true
			}
		}
	}
	if !hasPre || !hasDetour {
		t.Fatalf("hasPre=%v hasDetour=%v outbounds=%v", hasPre, hasDetour, cfg.Outbounds)
	}
}

func TestBuildChainConfig_Detour(t *testing.T) {
	pre := sampleRule("pre", "hysteria2", "1.1.1.1", 443)
	target := sampleRule("target", "hysteria2", "2.2.2.2", 443)
	cfg, err := BuildChainConfig(10800, []*models.ProxyRule{pre, target})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, o := range cfg.Outbounds {
		if o["tag"] == "chain_1" && o["detour"] != "chain_0" {
			t.Fatalf("chain_1 detour want chain_0, got %v", o["detour"])
		}
	}
}
