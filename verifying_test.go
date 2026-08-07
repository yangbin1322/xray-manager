package main

import (
	"testing"

	"xray-manager/internal/models"
)

// 未启动的节点不能显示为「验证中」。
//
// 停止节点的入口有十几处，逐个补清标记会漏；实测「全部停止」后
// 仍有 773 个节点停在验证中、状态灯一直黄着。
// GetRules 是界面读取的唯一出口，在这里统一兜底。
func TestGetRulesClearsVerifyingOnStoppedNodes(t *testing.T) {
	service := &MyService{config: &models.Config{Rules: []models.ProxyRule{
		{ID: "stopped-but-verifying", Enabled: false, Verifying: true},
		{ID: "running-and-verifying", Enabled: true, Verifying: true},
		{ID: "running", Enabled: true},
		{ID: "stopped", Enabled: false},
	}}}

	rules := service.GetRules()

	byID := make(map[string]models.ProxyRule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}

	if byID["stopped-but-verifying"].Verifying {
		t.Error("stopped node must not report as verifying")
	}
	// 已启动的节点仍在验证中是正常状态，不该被清掉
	if !byID["running-and-verifying"].Verifying {
		t.Error("running node should keep its verifying flag")
	}
	if byID["running"].Verifying || byID["stopped"].Verifying {
		t.Error("nodes without the flag should stay unset")
	}
}

// GetRules 返回副本，调用方改动不应影响内部配置
func TestGetRulesReturnsCopy(t *testing.T) {
	service := &MyService{config: &models.Config{Rules: []models.ProxyRule{
		{ID: "a", Alias: "原名", Enabled: true},
	}}}

	rules := service.GetRules()
	rules[0].Alias = "被改过"

	if service.config.Rules[0].Alias != "原名" {
		t.Error("mutating the returned slice must not affect the stored config")
	}
}
