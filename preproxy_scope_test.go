package main

import (
	"testing"

	"xray-manager/internal/models"
)

func scopeService(pre string, groups, excluded []string) *MyService {
	return &MyService{config: &models.Config{
		Rules: []models.ProxyRule{
			{ID: "pre", Alias: "前置", GroupID: "g1"},
			{ID: "a", Alias: "A", GroupID: "g1"},
			{ID: "b", Alias: "B", GroupID: "g2"},
			{ID: "c", Alias: "C"}, // 未分组
		},
		Groups:              []models.Group{{ID: "g1"}, {ID: "g2"}},
		PreProxyNodeID:      pre,
		PreProxyGroupIDs:    groups,
		PreProxyExcludedIDs: excluded,
	}}
}

func ruleByID(s *MyService, id string) *models.ProxyRule {
	for i := range s.config.Rules {
		if s.config.Rules[i].ID == id {
			return &s.config.Rules[i]
		}
	}
	return nil
}

// 未限定分组时对全部节点生效（兼容旧配置：此前前置代理是全局的）
func TestPreProxyAppliesToAllWhenNoGroups(t *testing.T) {
	s := scopeService("pre", nil, nil)
	for _, id := range []string{"a", "b", "c"} {
		if !s.preProxyAppliesToLocked(ruleByID(s, id)) {
			t.Errorf("node %s should use the pre-proxy when no group scope is set", id)
		}
	}
}

// 前置代理节点自身必须直连，否则 detour 指向自己会成环
func TestPreProxyNeverAppliesToItself(t *testing.T) {
	s := scopeService("pre", nil, nil)
	if s.preProxyAppliesToLocked(ruleByID(s, "pre")) {
		t.Error("the pre-proxy node must not route through itself")
	}
}

// 限定分组后，只有范围内的节点走前置代理
func TestPreProxyRespectsGroupScope(t *testing.T) {
	s := scopeService("pre", []string{"g1"}, nil)

	if !s.preProxyAppliesToLocked(ruleByID(s, "a")) {
		t.Error("node in the selected group should use the pre-proxy")
	}
	if s.preProxyAppliesToLocked(ruleByID(s, "b")) {
		t.Error("node outside the selected group must go direct")
	}
	if s.preProxyAppliesToLocked(ruleByID(s, "c")) {
		t.Error("ungrouped node must go direct when a group scope is set")
	}
}

// 可以选多个分组
func TestPreProxyAcceptsMultipleGroups(t *testing.T) {
	s := scopeService("pre", []string{"g1", "g2"}, nil)
	for _, id := range []string{"a", "b"} {
		if !s.preProxyAppliesToLocked(ruleByID(s, id)) {
			t.Errorf("node %s should use the pre-proxy", id)
		}
	}
	if s.preProxyAppliesToLocked(ruleByID(s, "c")) {
		t.Error("ungrouped node should still go direct")
	}
}

// 例外节点即使在范围内也直连
func TestPreProxyExclusionWinsOverGroup(t *testing.T) {
	s := scopeService("pre", []string{"g1"}, []string{"a"})
	if s.preProxyAppliesToLocked(ruleByID(s, "a")) {
		t.Error("excluded node must go direct even though its group is in scope")
	}
}

// 未启用前置代理时一律直连
func TestPreProxyDisabled(t *testing.T) {
	s := scopeService("", []string{"g1"}, nil)
	if s.preProxyAppliesToLocked(ruleByID(s, "a")) {
		t.Error("no node should use a pre-proxy when none is configured")
	}
}

// 范围指纹要能区分内容变化，且不受顺序影响——
// 顺序变动被误判为变更会触发无谓的分片重建
func TestPreProxyScopeFingerprint(t *testing.T) {
	a := scopeService("pre", []string{"g1", "g2"}, []string{"x", "y"})
	b := scopeService("pre", []string{"g2", "g1"}, []string{"y", "x"})
	if a.preProxyScopeLocked() != b.preProxyScopeLocked() {
		t.Error("scope fingerprint must ignore ordering")
	}

	c := scopeService("pre", []string{"g1"}, []string{"x", "y"})
	if a.preProxyScopeLocked() == c.preProxyScopeLocked() {
		t.Error("scope fingerprint must change when the group set changes")
	}
}

// 删除节点/分组后要从生效范围里摘掉，避免留下匹配不到的残留 ID
func TestDropDeletedFromPreProxyScope(t *testing.T) {
	s := scopeService("pre", []string{"g1", "g2"}, []string{"a", "b"})

	s.dropDeletedFromPreProxyScopeLocked(map[string]bool{"a": true}, map[string]bool{"g2": true})

	if len(s.config.PreProxyExcludedIDs) != 1 || s.config.PreProxyExcludedIDs[0] != "b" {
		t.Errorf("excluded = %v, want only b", s.config.PreProxyExcludedIDs)
	}
	if len(s.config.PreProxyGroupIDs) != 1 || s.config.PreProxyGroupIDs[0] != "g1" {
		t.Errorf("groups = %v, want only g1", s.config.PreProxyGroupIDs)
	}
}
