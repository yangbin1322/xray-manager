package singbox

import (
	"testing"
	"xray-manager/internal/models"
)

func TestBuildTunConfig(t *testing.T) {
	cfg := BuildTunConfig(10808, []string{`C:\bin\sing-box.exe`})

	if len(cfg.Inbounds) != 1 || cfg.Inbounds[0]["type"] != "tun" {
		t.Fatalf("inbounds = %v, want single tun inbound", cfg.Inbounds)
	}

	var proxyPort interface{}
	for _, o := range cfg.Outbounds {
		if o["tag"] == "proxy" {
			if o["type"] != "socks" || o["server"] != "127.0.0.1" {
				t.Fatalf("proxy outbound = %v, want socks to loopback", o)
			}
			proxyPort = o["server_port"]
		}
	}
	if proxyPort != 10808 {
		t.Fatalf("proxy server_port = %v, want 10808", proxyPort)
	}

	// 内核进程必须直连，否则节点连服务器的流量会被转回自己形成回环
	rules, _ := cfg.Route["rules"].([]map[string]interface{})
	bypassed := false
	for _, r := range rules {
		if paths, ok := r["process_path"].([]string); ok && len(paths) == 1 && r["outbound"] == "direct" {
			bypassed = true
		}
	}
	if !bypassed {
		t.Fatalf("missing process_path bypass rule: %v", rules)
	}
	if cfg.Route["final"] != "proxy" {
		t.Fatalf("route.final = %v, want proxy", cfg.Route["final"])
	}
	if _, err := cfg.ToJSON(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildChainConfig_SingleNode(t *testing.T) {
	node := sampleRule("only", "vmess", "2.2.2.2", 443)
	cfg, err := BuildChainConfig(10800, []*models.ProxyRule{node})
	if err != nil {
		t.Fatalf("单节点链式代理应可构建: %v", err)
	}
	if cfg.Route["final"] != "chain_0" {
		t.Fatalf("route.final = %v, want chain_0", cfg.Route["final"])
	}
	for _, o := range cfg.Outbounds {
		if _, ok := o["detour"]; ok {
			t.Fatalf("单节点链不应有 detour: %v", o)
		}
	}
}
