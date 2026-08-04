package main

import (
	"fmt"
	"testing"
	"xray-manager/internal/models"
)

// 批量删除必须一次摘除全部目标，不能漏、不能误删别的节点
func TestDeleteNodesRemovesOnlyTargets(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{
			{ID: "r1", Alias: "保留1", LocalPort: 10801},
			{ID: "r2", Alias: "删除1", LocalPort: 10802},
			{ID: "r3", Alias: "删除2", LocalPort: 10803},
			{ID: "r4", Alias: "保留2", LocalPort: 10804},
		},
		LoadBalancers: []models.LoadBalanceNode{
			{ID: "lb1", Alias: "删除LB", LocalPort: 10901},
			{ID: "lb2", Alias: "保留LB", LocalPort: 10902},
		},
		ChainProxies: []models.ChainProxy{{ID: "c1", Alias: "删除链", LocalPort: 11001}},
		SessionRelays: []models.SessionRelay{
			{ID: "sr1", Alias: "删除会话", LocalPort: 11101},
			{ID: "sr2", Alias: "保留会话", LocalPort: 11102},
		},
	})

	if err := svc.DeleteNodes([]string{"r2", "r3", "lb1", "c1", "sr1"}); err != nil {
		t.Fatalf("批量删除失败: %v", err)
	}

	gotRules := map[string]bool{}
	for _, r := range svc.config.Rules {
		gotRules[r.ID] = true
	}
	if len(svc.config.Rules) != 2 || !gotRules["r1"] || !gotRules["r4"] {
		t.Fatalf("普通节点应只剩 r1/r4，实际 %+v", svc.config.Rules)
	}
	if len(svc.config.LoadBalancers) != 1 || svc.config.LoadBalancers[0].ID != "lb2" {
		t.Fatalf("故障转移应只剩 lb2，实际 %+v", svc.config.LoadBalancers)
	}
	if len(svc.config.ChainProxies) != 0 {
		t.Fatalf("链式代理应被清空，实际 %+v", svc.config.ChainProxies)
	}
	if len(svc.config.SessionRelays) != 1 || svc.config.SessionRelays[0].ID != "sr2" {
		t.Fatalf("会话代理应只剩 sr2，实际 %+v", svc.config.SessionRelays)
	}
}

// 删除的节点若是全局前置代理，应自动解除引用，避免留下悬空配置
func TestDeleteNodesClearsPreProxyReference(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules:          []models.ProxyRule{{ID: "pre", Alias: "前置", LocalPort: 10801}},
		PreProxyNodeID: "pre",
	})

	if err := svc.DeleteNodes([]string{"pre"}); err != nil {
		t.Fatal(err)
	}
	if svc.config.PreProxyNodeID != "" {
		t.Fatalf("删除前置代理节点后应清空引用，实际 %q", svc.config.PreProxyNodeID)
	}
}

// 空列表和不存在的 ID 都不应报错或误删
func TestDeleteNodesHandlesEmptyAndUnknown(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{{ID: "r1", LocalPort: 10801}},
	})

	if err := svc.DeleteNodes(nil); err != nil {
		t.Fatalf("空列表不应报错: %v", err)
	}
	if err := svc.DeleteNodes([]string{"nope"}); err != nil {
		t.Fatalf("未知 ID 不应报错: %v", err)
	}
	if len(svc.config.Rules) != 1 {
		t.Fatalf("不应误删已有节点，实际剩 %d 个", len(svc.config.Rules))
	}
}

// 大批量删除的正确性（这是本次优化的主要场景）
func TestDeleteNodesLargeBatch(t *testing.T) {
	const total = 500
	rules := make([]models.ProxyRule, total)
	ids := make([]string, 0, total/2)
	for i := 0; i < total; i++ {
		rules[i] = models.ProxyRule{ID: fmt.Sprintf("r%d", i), LocalPort: 20000 + i}
		if i%2 == 0 {
			ids = append(ids, rules[i].ID)
		}
	}
	svc := newTestService(&models.Config{Rules: rules})

	if err := svc.DeleteNodes(ids); err != nil {
		t.Fatal(err)
	}
	if len(svc.config.Rules) != total/2 {
		t.Fatalf("应剩 %d 个节点，实际 %d 个", total/2, len(svc.config.Rules))
	}
	for _, r := range svc.config.Rules {
		var n int
		fmt.Sscanf(r.ID, "r%d", &n)
		if n%2 == 0 {
			t.Fatalf("偶数编号节点应已被删除，却发现 %s", r.ID)
		}
	}
}
