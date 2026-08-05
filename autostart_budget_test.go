package main

import (
	"testing"

	"xray-manager/internal/models"
	"xray-manager/internal/process"
	"xray-manager/internal/utils"
)

// 预算不应无故砍掉正常规模的节点数
func TestAutoStartBudgetAllowsNormalNodeCount(t *testing.T) {
	rules := make([]models.ProxyRule, 20)
	for i := range rules {
		rules[i] = models.ProxyRule{ID: "r", Enabled: true, LocalPort: 11000 + i}
	}
	service := &MyService{config: &models.Config{Rules: rules}}

	service.mu.Lock()
	budget := service.autoStartBudgetLocked()
	service.mu.Unlock()

	if budget != 20 {
		t.Errorf("budget = %d, want all 20 nodes to be startable", budget)
	}
}

// 只统计已启用的节点，未启用的不占预算
func TestAutoStartBudgetIgnoresDisabledNodes(t *testing.T) {
	service := &MyService{config: &models.Config{
		Rules: []models.ProxyRule{
			{ID: "a", Enabled: true},
			{ID: "b", Enabled: false},
			{ID: "c", Enabled: false},
		},
	}}

	service.mu.Lock()
	budget := service.autoStartBudgetLocked()
	service.mu.Unlock()

	// 只有 1 个启用节点，远低于任何机器的上限，应原样放行
	if budget != 1 {
		t.Errorf("budget = %d, want 1 (only the enabled node counts)", budget)
	}
}

// 节点数超出内存容量时，预算必须被压到容量上限而不是原样返回
func TestAutoStartBudgetCapsAtMemoryLimit(t *testing.T) {
	available := utils.AvailableMemory()
	if available == 0 {
		t.Skip("cannot read available memory on this machine")
	}

	// 构造一个必然超出内存的节点数
	huge := 100000
	rules := make([]models.ProxyRule, huge)
	for i := range rules {
		rules[i] = models.ProxyRule{ID: "r", Enabled: true}
	}
	service := &MyService{config: &models.Config{Rules: rules}}

	service.mu.Lock()
	budget := service.autoStartBudgetLocked()
	service.mu.Unlock()

	if budget >= huge {
		t.Fatalf("budget = %d, must be capped below the requested %d", budget, huge)
	}
	// 应与 CheckCapacity 给出的上限一致
	err := process.CheckCapacity(huge)
	capacityErr, ok := err.(*process.CapacityError)
	if !ok {
		t.Fatalf("expected a capacity error for %d nodes, got %v", huge, err)
	}
	if budget != capacityErr.Allowed {
		t.Errorf("budget = %d, want %d (matching CheckCapacity)", budget, capacityErr.Allowed)
	}
}
