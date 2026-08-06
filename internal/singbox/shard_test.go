package singbox

import (
	"encoding/json"
	"fmt"
	"testing"

	"xray-manager/internal/models"
)

// shardNode 构造一个可用于分片的节点
func shardNode(id string, port int, protocol string) *models.ProxyRule {
	rule := sampleRule(id, protocol, "1.2.3.4", 443)
	rule.LocalPort = port
	switch protocol {
	case "trojan":
		rule.Settings.TrojanPassword = "pw"
	case "vless":
		rule.Settings.VLessUserID = "d65cc14c-f53f-4fe2-b262-97856601319c"
	case "shadowsocks":
		rule.Settings.SSMethod = "aes-256-gcm"
		rule.Settings.SSPassword = "pw"
	}
	return rule
}

// 每个节点都要拿到自己的入站端口、出站和路由规则
func TestBuildShardConfigWiresEachNode(t *testing.T) {
	nodes := []*models.ProxyRule{
		shardNode("a", 11000, "trojan"),
		shardNode("b", 11001, "vless"),
		shardNode("c", 11002, "shadowsocks"),
	}

	config, skipped, err := BuildShardConfig(nodes, 0)
	if err != nil {
		t.Fatalf("BuildShardConfig: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped nodes, got %v", skipped)
	}

	if len(config.Inbounds) != 3 {
		t.Errorf("got %d inbounds, want 3", len(config.Inbounds))
	}
	// 3 个节点出站 + 1 个 direct 兜底
	if len(config.Outbounds) != 4 {
		t.Errorf("got %d outbounds, want 4 (3 nodes + direct)", len(config.Outbounds))
	}

	rules, _ := config.Route["rules"].([]map[string]interface{})
	if len(rules) != 3 {
		t.Fatalf("got %d route rules, want 3", len(rules))
	}

	// 每个节点的端口必须绑到自己的出站上
	ports := map[string]int{"a": 11000, "b": 11001, "c": 11002}
	for _, node := range nodes {
		var inboundPort int
		for _, in := range config.Inbounds {
			if in["tag"] == ShardInboundTag(node.ID) {
				inboundPort = in["listen_port"].(int)
			}
		}
		if inboundPort != ports[node.ID] {
			t.Errorf("node %s: inbound port = %d, want %d", node.ID, inboundPort, ports[node.ID])
		}

		var matched bool
		for _, rule := range rules {
			ins, _ := rule["inbound"].([]string)
			if len(ins) == 1 && ins[0] == ShardInboundTag(node.ID) {
				if rule["outbound"] != ShardOutboundTag(node.ID) {
					t.Errorf("node %s routes to %v, want its own outbound", node.ID, rule["outbound"])
				}
				matched = true
			}
		}
		if !matched {
			t.Errorf("node %s has no route rule", node.ID)
		}
	}
}

// 单个坏节点必须被跳过，而不是让同片其余节点一起失败
func TestBuildShardConfigSkipsBadNodeAndKeepsRest(t *testing.T) {
	good1 := shardNode("good1", 11010, "trojan")
	good2 := shardNode("good2", 11011, "vless")
	bad := shardNode("bad", 11012, "no-such-protocol")

	config, skipped, err := BuildShardConfig([]*models.ProxyRule{good1, bad, good2}, 0)
	if err != nil {
		t.Fatalf("a single bad node must not fail the shard: %v", err)
	}
	if len(skipped) != 1 || skipped[0].NodeID != "bad" {
		t.Fatalf("expected only the bad node to be skipped, got %v", skipped)
	}
	if skipped[0].Err == nil {
		t.Error("skipped node should carry the reason")
	}
	if len(config.Inbounds) != 2 {
		t.Errorf("got %d inbounds, want the 2 good nodes", len(config.Inbounds))
	}
}

// 没有本地端口的节点无法监听，必须跳过
func TestBuildShardConfigSkipsNodeWithoutPort(t *testing.T) {
	good := shardNode("good", 11020, "trojan")
	noPort := shardNode("noport", 0, "trojan")

	config, skipped, err := BuildShardConfig([]*models.ProxyRule{good, noPort}, 0)
	if err != nil {
		t.Fatalf("BuildShardConfig: %v", err)
	}
	if len(skipped) != 1 || skipped[0].NodeID != "noport" {
		t.Fatalf("expected the port-less node to be skipped, got %v", skipped)
	}
	if len(config.Inbounds) != 1 {
		t.Errorf("got %d inbounds, want 1", len(config.Inbounds))
	}
}

// 同片内两个节点抢同一个端口会让整个进程起不来，必须提前挡下
func TestBuildShardConfigSkipsDuplicatePort(t *testing.T) {
	first := shardNode("first", 11030, "trojan")
	clash := shardNode("clash", 11030, "vless")

	config, skipped, err := BuildShardConfig([]*models.ProxyRule{first, clash}, 0)
	if err != nil {
		t.Fatalf("BuildShardConfig: %v", err)
	}
	if len(skipped) != 1 || skipped[0].NodeID != "clash" {
		t.Fatalf("expected the second node on the duplicate port to be skipped, got %v", skipped)
	}
	if len(config.Inbounds) != 1 {
		t.Errorf("got %d inbounds, want 1", len(config.Inbounds))
	}
}

// 全部节点都无法构建时才算整片失败
func TestBuildShardConfigFailsWhenAllNodesBad(t *testing.T) {
	bad1 := shardNode("bad1", 11040, "no-such-protocol")
	bad2 := shardNode("bad2", 11041, "also-invalid")

	if _, skipped, err := BuildShardConfig([]*models.ProxyRule{bad1, bad2}, 0); err == nil {
		t.Errorf("expected an error when every node fails, skipped=%v", skipped)
	}
	if _, _, err := BuildShardConfig(nil, 0); err == nil {
		t.Error("expected an error for an empty shard")
	}
}

// REALITY 等出站参数必须原样进入分片配置（不能因为换了构建路径就丢失）
func TestBuildShardConfigPreservesReality(t *testing.T) {
	node := shardNode("reality", 11050, "vless")
	node.Settings.Security = "reality"
	node.Settings.TLS = &models.TLSSettings{ServerName: "yahoo.com"}
	node.Settings.Reality = &models.RealitySettings{
		PublicKey: "e2RLf57Li_-MDZGE9ss1BWPgP54mqRb5PfXhW2jcVVg",
		ShortID:   "c39cc7310a",
	}

	config, _, err := BuildShardConfig([]*models.ProxyRule{node}, 0)
	if err != nil {
		t.Fatalf("BuildShardConfig: %v", err)
	}

	var found bool
	for _, out := range config.Outbounds {
		if out["tag"] != ShardOutboundTag("reality") {
			continue
		}
		tls, ok := out["tls"].(map[string]interface{})
		if !ok {
			t.Fatal("REALITY node lost its TLS section in the shard config")
		}
		reality, ok := tls["reality"].(map[string]interface{})
		if !ok {
			t.Fatal("REALITY node lost its reality section in the shard config")
		}
		if reality["public_key"] != "e2RLf57Li_-MDZGE9ss1BWPgP54mqRb5PfXhW2jcVVg" {
			t.Errorf("public_key = %v, want the configured key", reality["public_key"])
		}
		found = true
	}
	if !found {
		t.Fatal("REALITY outbound not found in the shard config")
	}
}

// apiPort > 0 时才挂 Clash API（流量统计按分片查询）
func TestBuildShardConfigClashAPI(t *testing.T) {
	nodes := []*models.ProxyRule{shardNode("a", 11060, "trojan")}

	withAPI, _, err := BuildShardConfig(nodes, 31000)
	if err != nil {
		t.Fatal(err)
	}
	if withAPI.Experimental == nil {
		t.Error("expected a clash_api section when apiPort > 0")
	}

	withoutAPI, _, err := BuildShardConfig(nodes, 0)
	if err != nil {
		t.Fatal(err)
	}
	if withoutAPI.Experimental != nil {
		t.Error("expected no clash_api section when apiPort is 0")
	}
}

// 分片规模下配置必须能正常序列化，且体积可控
func TestBuildShardConfigAtScale(t *testing.T) {
	const count = 300
	nodes := make([]*models.ProxyRule, 0, count)
	for i := 0; i < count; i++ {
		nodes = append(nodes, shardNode(fmt.Sprintf("n%d", i), 11000+i, "trojan"))
	}

	config, skipped, err := BuildShardConfig(nodes, 31000)
	if err != nil {
		t.Fatalf("BuildShardConfig: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected no skipped nodes, got %d", len(skipped))
	}
	if len(config.Inbounds) != count {
		t.Errorf("got %d inbounds, want %d", len(config.Inbounds), count)
	}

	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("%d nodes -> %d KB of JSON", count, len(raw)/1024)
}

// 设置全局前置代理后，节点应通过 detour 共用同一份前置出站——
// 而不是退化成一节点一份配置、一节点一个进程。
func TestBuildShardConfigWithPreProxy(t *testing.T) {
	pre := shardNode("pre", 11100, "trojan")
	nodes := []*models.ProxyRule{
		shardNode("a", 11101, "trojan"),
		shardNode("b", 11102, "vless"),
	}

	config, skipped, err := BuildShardConfigWithPreProxy(nodes, pre, 0)
	if err != nil {
		t.Fatalf("BuildShardConfigWithPreProxy: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped nodes: %v", skipped)
	}

	// 前置出站只有一份
	preCount := 0
	for _, out := range config.Outbounds {
		if out["tag"] == "pre_proxy" {
			preCount++
		}
	}
	if preCount != 1 {
		t.Errorf("found %d pre_proxy outbounds, want exactly 1 shared one", preCount)
	}

	// 每个节点都 detour 到它
	for _, node := range nodes {
		var found bool
		for _, out := range config.Outbounds {
			if out["tag"] != ShardOutboundTag(node.ID) {
				continue
			}
			found = true
			if out["detour"] != "pre_proxy" {
				t.Errorf("node %s detour = %v, want pre_proxy", node.ID, out["detour"])
			}
		}
		if !found {
			t.Errorf("node %s has no outbound", node.ID)
		}
	}
}

// 节点自身就是前置代理时不能 detour 到自己，否则成环
func TestBuildShardConfigPreProxyAvoidsSelfLoop(t *testing.T) {
	pre := shardNode("pre", 11110, "trojan")
	nodes := []*models.ProxyRule{pre, shardNode("other", 11111, "trojan")}

	config, _, err := BuildShardConfigWithPreProxy(nodes, pre, 0)
	if err != nil {
		t.Fatalf("BuildShardConfigWithPreProxy: %v", err)
	}
	for _, out := range config.Outbounds {
		if out["tag"] == ShardOutboundTag("pre") {
			if _, hasDetour := out["detour"]; hasDetour {
				t.Error("the pre-proxy node must not detour through itself")
			}
		}
	}
}

// 不传前置代理时行为与 BuildShardConfig 一致
func TestBuildShardConfigWithoutPreProxyHasNoDetour(t *testing.T) {
	nodes := []*models.ProxyRule{shardNode("a", 11120, "trojan")}
	config, _, err := BuildShardConfigWithPreProxy(nodes, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range config.Outbounds {
		if _, hasDetour := out["detour"]; hasDetour {
			t.Errorf("outbound %v should not have a detour without a pre-proxy", out["tag"])
		}
		if out["tag"] == "pre_proxy" {
			t.Error("no pre_proxy outbound should exist")
		}
	}
}
