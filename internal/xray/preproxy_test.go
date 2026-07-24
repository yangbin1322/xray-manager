package xray

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
	if protocol == "vmess" || protocol == "vless" {
		rule.Settings.VMessUserID = "00000000-0000-0000-0000-000000000001"
	}
	if protocol == "shadowsocks" {
		rule.Settings.SSMethod = "aes-128-gcm"
		rule.Settings.SSPassword = "pass"
	}
	if protocol == "trojan" {
		rule.Settings.TrojanPassword = "pass"
		rule.Settings.Hy2Password = "pass"
	}
	return rule
}

func TestBuildChainConfig_PreProxyOrder(t *testing.T) {
	pre := sampleRule("pre", "vmess", "1.1.1.1", 443)
	target := sampleRule("target", "vmess", "2.2.2.2", 443)
	cfg, err := BuildChainConfig("mixed", 10800, []*models.ProxyRule{pre, target})
	if err != nil {
		t.Fatalf("BuildChainConfig: %v", err)
	}
	if len(cfg.Outbounds) < 2 {
		t.Fatalf("expected at least 2 outbounds, got %d", len(cfg.Outbounds))
	}
	// chain_1 (target) should proxy through chain_0 (pre)
	var found bool
	for _, o := range cfg.Outbounds {
		if o.Tag == "chain_1" {
			found = true
			if o.ProxySettings == nil || o.ProxySettings.Tag != "chain_0" {
				t.Fatalf("chain_1 proxySettings.tag want chain_0, got %+v", o.ProxySettings)
			}
			if !o.ProxySettings.TransportLayer {
				t.Fatal("expected TransportLayer true")
			}
		}
		if o.Tag == "chain_0" && o.ProxySettings != nil {
			t.Fatal("entry hop should not have proxySettings")
		}
	}
	if !found {
		t.Fatal("chain_1 outbound not found")
	}
	if cfg.Routing == nil || len(cfg.Routing.Rules) == 0 || cfg.Routing.Rules[0].OutboundTag != "chain_1" {
		t.Fatalf("routing should point to chain_1, got %+v", cfg.Routing)
	}
}

func TestBuildLoadBalanceConfig_WithPreProxy(t *testing.T) {
	pre := sampleRule("pre", "vmess", "1.1.1.1", 443)
	n1 := sampleRule("n1", "vmess", "2.2.2.2", 443)
	n2 := sampleRule("pre", "vmess", "1.1.1.1", 443) // same ID as pre — should skip detour
	lb := &models.LoadBalanceNode{ID: "lb1", Alias: "lb", LocalPort: 10900}
	cfg, err := BuildLoadBalanceConfig(lb, []*models.ProxyRule{n1, n2}, pre)
	if err != nil {
		t.Fatalf("BuildLoadBalanceConfig: %v", err)
	}
	var hasPre, n1Proxied, n2Direct bool
	for _, o := range cfg.Outbounds {
		if o.Tag == "pre_proxy" {
			hasPre = true
		}
		if o.Tag == "proxy_0" {
			if o.ProxySettings == nil || o.ProxySettings.Tag != "pre_proxy" {
				t.Fatalf("proxy_0 should use pre_proxy, got %+v", o.ProxySettings)
			}
			n1Proxied = true
		}
		if o.Tag == "proxy_1" {
			if o.ProxySettings != nil {
				t.Fatalf("proxy_1 is pre itself, should not detour, got %+v", o.ProxySettings)
			}
			n2Direct = true
		}
	}
	if !hasPre || !n1Proxied || !n2Direct {
		t.Fatalf("flags hasPre=%v n1Proxied=%v n2Direct=%v", hasPre, n1Proxied, n2Direct)
	}
}

func TestBuildLoadBalanceConfig_NoPreProxy(t *testing.T) {
	n1 := sampleRule("n1", "vmess", "2.2.2.2", 443)
	lb := &models.LoadBalanceNode{ID: "lb1", Alias: "lb", LocalPort: 10900}
	cfg, err := BuildLoadBalanceConfig(lb, []*models.ProxyRule{n1}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, o := range cfg.Outbounds {
		if o.Tag == "pre_proxy" {
			t.Fatal("should not have pre_proxy")
		}
		if o.ProxySettings != nil {
			t.Fatalf("unexpected proxySettings on %s", o.Tag)
		}
	}
}
