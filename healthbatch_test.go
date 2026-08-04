package main

import (
	"fmt"
	"sync"
	"testing"
	"xray-manager/internal/models"
)

// 批量写回必须覆盖普通节点、故障转移和链式代理三类，且不漏不错配
func TestApplyHealthResultsBatch(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{
			{ID: "r1"}, {ID: "r2"}, {ID: "r3"},
		},
		LoadBalancers: []models.LoadBalanceNode{{ID: "lb1"}},
		ChainProxies:  []models.ChainProxy{{ID: "c1"}},
	})

	svc.applyHealthResultsLocked([]models.HealthCheckResult{
		{RuleID: "r1", Status: "online", Latency: 42, Timestamp: "t1"},
		{RuleID: "r3", Status: "timeout", Latency: 0, Timestamp: "t3"},
		{RuleID: "lb1", Status: "online", Latency: 10, Timestamp: "tl"},
		{RuleID: "c1", Status: "high_latency", Latency: 900, Timestamp: "tc"},
		{RuleID: "unknown", Status: "online"}, // 不存在的 ID 应被安全忽略
	})

	if svc.config.Rules[0].HealthStatus != "online" || svc.config.Rules[0].HealthLatency != 42 {
		t.Fatalf("r1 未正确写回: %+v", svc.config.Rules[0])
	}
	// 没有结果的节点不能被改动
	if svc.config.Rules[1].HealthStatus != "" {
		t.Fatalf("r2 没有结果，不应被修改: %+v", svc.config.Rules[1])
	}
	if svc.config.Rules[2].HealthStatus != "timeout" {
		t.Fatalf("r3 未正确写回: %+v", svc.config.Rules[2])
	}
	if svc.config.LoadBalancers[0].HealthStatus != "online" {
		t.Fatalf("故障转移未写回: %+v", svc.config.LoadBalancers[0])
	}
	if svc.config.ChainProxies[0].HealthStatus != "high_latency" {
		t.Fatalf("链式代理未写回: %+v", svc.config.ChainProxies[0])
	}
}

// 缓冲区必须线程安全，且一条结果都不能丢——
// 检测协程是并发的，丢结果会让节点状态永远停在"检测中"
func TestHealthResultBufferLosesNothing(t *testing.T) {
	const total = 1000
	rules := make([]models.ProxyRule, total)
	for i := range rules {
		rules[i] = models.ProxyRule{ID: fmt.Sprintf("r%d", i)}
	}
	svc := newTestService(&models.Config{Rules: rules})

	// 并发写入缓冲区（模拟检测协程），但不触发需要 app 事件的 flush
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc.healthResultMu.Lock()
			svc.healthResultBuf = append(svc.healthResultBuf,
				models.HealthCheckResult{RuleID: fmt.Sprintf("r%d", i), Status: "online"})
			svc.healthResultMu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(svc.healthResultBuf) != total {
		t.Fatalf("缓冲区应有 %d 条结果，实际 %d 条（并发写入丢数据）", total, len(svc.healthResultBuf))
	}

	svc.applyHealthResultsLocked(svc.healthResultBuf)
	for i := range svc.config.Rules {
		if svc.config.Rules[i].HealthStatus != "online" {
			t.Fatalf("节点 %s 的结果丢失", svc.config.Rules[i].ID)
		}
	}
}
