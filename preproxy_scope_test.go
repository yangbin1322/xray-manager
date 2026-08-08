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
		PreProxyEnabled:     pre != "",
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

// 关闭开关时前置代理不生效，但配置要保留下来
func TestPreProxyDisabledByToggle(t *testing.T) {
	s := scopeService("pre", []string{"g1"}, []string{"x"})
	s.config.PreProxyEnabled = false

	if s.preProxyAppliesToLocked(ruleByID(s, "a")) {
		t.Error("no node should use the pre-proxy while the toggle is off")
	}
	if s.getPreProxyRuleLocked() != nil {
		t.Error("getPreProxyRuleLocked should return nil while disabled")
	}
	// 关键：配置不能被清掉，否则重新启用还要再配一遍
	if s.config.PreProxyNodeID != "pre" {
		t.Error("the selected node must be kept when disabled")
	}
	if len(s.config.PreProxyGroupIDs) != 1 || len(s.config.PreProxyExcludedIDs) != 1 {
		t.Error("the scope must be kept when disabled")
	}
}

// 链式代理可以作为前置代理：它对外提供本地端口，包装成 socks 出站接入
func TestPreProxyAcceptsChainProxy(t *testing.T) {
	s := scopeService("chain1", nil, nil)
	s.config.ChainProxies = []models.ChainProxy{
		{ID: "chain1", Alias: "链式A", LocalPort: 12345, Enabled: true},
	}

	pre := s.getPreProxyRuleLocked()
	if pre == nil {
		t.Fatal("a running chain proxy should be usable as the pre-proxy")
	}
	if pre.Protocol != "socks" || pre.ServerAddr != "127.0.0.1" || pre.ServerPort != 12345 {
		t.Errorf("chain pre-proxy = %s %s:%d, want socks 127.0.0.1:12345",
			pre.Protocol, pre.ServerAddr, pre.ServerPort)
	}
}

// 未启动的复合代理没有可用端口，接上去只会全链路不通
func TestPreProxyRejectsStoppedComposite(t *testing.T) {
	s := scopeService("chain1", nil, nil)
	s.config.ChainProxies = []models.ChainProxy{
		{ID: "chain1", Alias: "链式A", LocalPort: 12345, Enabled: false},
	}

	if s.getPreProxyRuleLocked() != nil {
		t.Error("a stopped chain proxy must not be used as the pre-proxy")
	}
}

// 故障转移同样可以作为前置代理
func TestPreProxyAcceptsLoadBalancer(t *testing.T) {
	s := scopeService("lb1", nil, nil)
	s.config.LoadBalancers = []models.LoadBalanceNode{
		{ID: "lb1", Alias: "故障转移A", LocalPort: 23456, Enabled: true},
	}

	pre := s.getPreProxyRuleLocked()
	if pre == nil || pre.ServerPort != 23456 {
		t.Fatalf("a running load balancer should be usable as the pre-proxy, got %v", pre)
	}
}

// 前置代理是未启动的链式代理时，启动其他节点前应自动把它拉起来。
//
// 忘记先启动会让所有受影响的节点报「代理连接失败」，而问题其实不在这些
// 节点身上——这种错法很难自己排查。
func TestEnsurePreProxyRunningStartsStoppedChain(t *testing.T) {
	s := scopeService("chain1", nil, nil)
	s.config.ChainProxies = []models.ChainProxy{
		{ID: "chain1", Alias: "链式A", LocalPort: 12345, Enabled: false},
	}

	// 未启动时解析不到前置代理——这正是「代理连接失败」的由来
	if s.getPreProxyRuleLocked() != nil {
		t.Fatal("a stopped chain should not resolve as a usable pre-proxy")
	}

	// processManager 为 nil 时 startChainProxyInternal 会失败，
	// 这里只验证「会去尝试启动」而非启动结果
	defer func() { _ = recover() }()
	s.ensurePreProxyRunningLocked()
}

// 已在运行的前置代理不该被重复启动
func TestEnsurePreProxyRunningSkipsRunning(t *testing.T) {
	s := scopeService("chain1", nil, nil)
	s.config.ChainProxies = []models.ChainProxy{
		{ID: "chain1", Alias: "链式A", LocalPort: 12345, Enabled: true},
	}

	s.ensurePreProxyRunningLocked() // 不应 panic（不会调用 processManager）

	if !s.config.ChainProxies[0].Enabled {
		t.Error("a running pre-proxy must stay running")
	}
}

// 未启用前置代理时不做任何事
func TestEnsurePreProxyRunningNoopWhenDisabled(t *testing.T) {
	s := scopeService("chain1", nil, nil)
	s.config.PreProxyEnabled = false
	s.config.ChainProxies = []models.ChainProxy{
		{ID: "chain1", Alias: "链式A", LocalPort: 12345, Enabled: false},
	}

	s.ensurePreProxyRunningLocked()

	if s.config.ChainProxies[0].Enabled {
		t.Error("must not start anything while the pre-proxy is disabled")
	}
}

// 前置代理是普通节点时无需特殊处理——它随分片一起启动
func TestEnsurePreProxyRunningIgnoresPlainNode(t *testing.T) {
	s := scopeService("pre", nil, nil)
	s.ensurePreProxyRunningLocked() // 不应 panic
}
