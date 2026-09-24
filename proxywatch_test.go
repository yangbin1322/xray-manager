package main

import (
	"strings"
	"testing"
	"xray-manager/internal/models"
	"xray-manager/internal/process"
)

func newProxyWatchTestService(enabled bool) *MyService {
	a := &MyService{
		config: &models.Config{Rules: []models.ProxyRule{
			{ID: "rule_1", Alias: "节点A", LocalPort: 18081, Enabled: enabled},
			{ID: "rule_2", Alias: "节点B", LocalPort: 18082, Enabled: true},
		}},
		processManager: process.NewManager(func(string) {}, func() {}),
	}
	a.proxyWatch.set("rule_1", proxyTarget{port: 18081, alias: "节点A"})
	return a
}

// expectWarnAfterMisses 前 N-1 次探测不动作，第 N 次提示目标失效且不关闭代理
func expectWarnAfterMisses(t *testing.T, a *MyService, mode string) {
	t.Helper()
	for i := 1; i < proxyWatchMaxMisses; i++ {
		if action, detail := a.checkProxyTarget(mode); action != watchNone {
			t.Fatalf("第 %d 次探测失败不应立即动作: %v %q", i, action, detail)
		}
	}
	action, detail := a.checkProxyTarget(mode)
	if action != watchWarnDown {
		t.Fatalf("连续 %d 次探测失败应提示目标失效, got %v %q", proxyWatchMaxMisses, action, detail)
	}
	// 断网保护：只提示一次，不摘除跟随目标
	if action, _ := a.checkProxyTarget(mode); action != watchNone {
		t.Fatalf("已提示过不应重复提示, got %v", action)
	}
	if id, _, _ := a.proxyWatch.get(); id != "rule_1" {
		t.Fatal("目标意外失效时应保持代理（断网保护），不应摘除跟随目标")
	}
}

func TestCheckProxyTarget_NoTarget(t *testing.T) {
	a := &MyService{config: &models.Config{}}
	if action, _ := a.checkProxyTarget("system"); action != watchNone {
		t.Fatalf("未开启代理时不应动作: %v", action)
	}
}

// 进程崩溃：保持代理，只提示
func TestCheckProxyTarget_ProcessGoneKeepsProxy(t *testing.T) {
	expectWarnAfterMisses(t, newProxyWatchTestService(true), "tun")
}

// 未经手动操作被停用（验证不通、出口 IP 变化等自动停止）：同样保持代理
func TestCheckProxyTarget_AutoStoppedKeepsProxy(t *testing.T) {
	expectWarnAfterMisses(t, newProxyWatchTestService(false), "system")
}

// 手动停止进行中：后台检查暂停，交给 beginManualStop 处理
func TestCheckProxyTarget_PausedDuringManualStop(t *testing.T) {
	a := newProxyWatchTestService(false)
	a.manualStops.Add(1)
	for i := 0; i < proxyWatchMaxMisses+1; i++ {
		if action, _ := a.checkProxyTarget("tun"); action != watchNone {
			t.Fatalf("手动停止期间不应动作, got %v", action)
		}
	}
}

func TestCheckProxyTarget_TunExited(t *testing.T) {
	a := newProxyWatchTestService(true)
	if action, detail := a.checkProxyTarget("off"); action != watchTunDied || !strings.Contains(detail, "TUN") {
		t.Fatalf("TUN 意外退出应尝试重新拉起, got %v %q", action, detail)
	}
}

// 手动停止代理目标：关闭代理
func TestBeginManualStop_TargetStopped(t *testing.T) {
	a := newProxyWatchTestService(true)
	done := a.beginManualStop()
	a.config.Rules[0].Enabled = false
	done()
	if id, _, _ := a.proxyWatch.get(); id != "" {
		t.Fatal("手动停止目标后应关闭代理")
	}
	if a.manualStops.Load() != 0 {
		t.Fatal("操作结束后计数应归零")
	}
}

// 手动删除代理目标：关闭代理（即使目标此前已意外失效）
func TestBeginManualStop_TargetDeleted(t *testing.T) {
	a := newProxyWatchTestService(false)
	done := a.beginManualStop()
	a.config.Rules = a.config.Rules[1:]
	done()
	if id, _, _ := a.proxyWatch.get(); id != "" {
		t.Fatal("手动删除目标后应关闭代理")
	}
}

// 停止其他节点：不影响代理
func TestBeginManualStop_OtherNode(t *testing.T) {
	a := newProxyWatchTestService(true)
	done := a.beginManualStop()
	a.config.Rules[1].Enabled = false
	done()
	if id, _, _ := a.proxyWatch.get(); id != "rule_1" {
		t.Fatal("停止无关节点不应关闭代理")
	}
}

// 目标已意外失效（断网保护中），此时停止其他节点不能顺带把代理关掉导致流量走直连
func TestBeginManualStop_OtherNodeWhileTargetDown(t *testing.T) {
	a := newProxyWatchTestService(false)
	done := a.beginManualStop()
	a.config.Rules[1].Enabled = false
	done()
	if id, _, _ := a.proxyWatch.get(); id != "rule_1" {
		t.Fatal("目标已失效时停止其他节点不应关闭代理")
	}
}
