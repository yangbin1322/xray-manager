package main

import (
	"encoding/json"
	"testing"

	"xray-manager/internal/models"
)

// 用户取消勾选「启用前置代理」后，重启不能又被打回勾选状态。
//
// 回归：PreProxyEnabled 曾是普通 bool，无法区分「老配置里没这个字段」和
// 「用户明确关掉了」——两者都是 false。启动时的旧配置迁移按
// 「选了节点即启用」补齐，于是每次重启都把用户取消的勾选打回去。
func TestDisabledPreProxySurvivesRestart(t *testing.T) {
	// 用户保留了所选节点，但关掉了开关
	saved := models.Config{
		PreProxyNodeID:  "rule_1",
		PreProxyEnabled: models.BoolPtr(false),
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}

	// 模拟重启：重新读盘 + 跑一遍启动时的迁移
	var cfg models.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	svc := newTestService(&cfg)
	svc.migrateLegacyPreProxyLocked()

	if svc.preProxyEnabledLocked() {
		t.Error("用户取消的勾选在重启后被打回了启用状态")
	}
	if svc.config.PreProxyNodeID != "rule_1" {
		t.Errorf("所选节点应保留，实际「%s」", svc.config.PreProxyNodeID)
	}
	// 前端读到的也必须是未启用
	if got := svc.GetPreProxy(); got.Enabled {
		t.Error("GetPreProxy 返回的仍是已启用")
	}
}

// 老版本没有 preProxyEnabled 字段，选了节点就等于启用，升级后不能静默失效
func TestLegacyConfigWithoutEnabledFieldStaysEnabled(t *testing.T) {
	var cfg models.Config
	if err := json.Unmarshal([]byte(`{"preProxyNodeId":"rule_1"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.PreProxyEnabled != nil {
		t.Fatalf("老配置里该字段应缺失（nil），实际 %v", *cfg.PreProxyEnabled)
	}

	svc := newTestService(&cfg)
	svc.migrateLegacyPreProxyLocked()

	if !svc.preProxyEnabledLocked() {
		t.Error("老配置选了节点即视为启用，升级后不应静默失效")
	}
}

// 没选节点的老配置不该被补成启用
func TestLegacyConfigWithoutNodeStaysDisabled(t *testing.T) {
	svc := newTestService(&models.Config{})
	svc.migrateLegacyPreProxyLocked()

	if svc.preProxyEnabledLocked() {
		t.Error("没有选节点时不应视为启用")
	}
}

// 关掉开关后再存一次，落盘的必须是明确的 false，而不是把字段省略掉
func TestSetPreProxyPersistsExplicitFalse(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{{ID: "rule_1", Alias: "前置"}},
	})

	if err := svc.SetPreProxyConfig(models.PreProxyConfig{
		NodeID: "rule_1", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	if svc.config.PreProxyEnabled == nil {
		t.Fatal("用户明确关掉的开关不能存成「未设置」，否则重启会被迁移逻辑打回启用")
	}
	if *svc.config.PreProxyEnabled {
		t.Error("应存为 false")
	}

	// 序列化后字段必须still在，不能被 omitempty 吃掉
	raw, err := json.Marshal(svc.config)
	if err != nil {
		t.Fatal(err)
	}
	var back models.Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.PreProxyEnabled == nil {
		t.Error("落盘再读回后字段丢失了，重启仍会被打回启用")
	}
}
