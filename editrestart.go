package main

import (
	"fmt"
	"reflect"
	"time"
	"xray-manager/internal/models"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 编辑正在运行的节点 / 链式代理 / 故障转移后自动重启，让修改立即生效。
//
// 重启沿用原 LocalPort（或新端口），系统代理 / TUN 的跟随检查会在进程短暂
// 不在时防抖、端口变更时自动改指向，不会因为这次重启被关闭。

// ruleConnChanged 编辑是否改动了影响内核配置的字段。
// 只改别名、备注、分组等展示字段时不必重启，避免无谓地打断连接。
func ruleConnChanged(old, updated *models.ProxyRule) bool {
	return old.LocalPort != updated.LocalPort ||
		old.LocalType != updated.LocalType ||
		old.Protocol != updated.Protocol ||
		old.ServerAddr != updated.ServerAddr ||
		old.ServerPort != updated.ServerPort ||
		old.BypassPreProxy != updated.BypassPreProxy ||
		!reflect.DeepEqual(old.Settings, updated.Settings)
}

// restartRuleLocked 停掉节点在 oldPort 上的进程，按当前配置重新启动。需持有 a.mu。
// 失败时节点保持停止并记录原因，返回的错误说明修改已保存。
func (a *MyService) restartRuleLocked(rule *models.ProxyRule, oldPort int) error {
	if err := a.processManager.Stop(oldPort); err != nil {
		a.logError(fmt.Sprintf("重启节点 %s 时停止旧进程出现警告", rule.Alias), err)
	}
	rule.ProcessID = 0
	rule.RealIP = ""
	rule.Verifying = false

	err := a.runWithReleasedPortLocked(rule.LocalPort, func() error { return a.startRuleInternal(rule) })
	if err != nil {
		rule.Enabled = false
		rule.LastError = err.Error()
		a.reservePortLocked(rule.LocalPort)
	} else {
		rule.Enabled = true
		rule.LastError = ""
		a.log(fmt.Sprintf("[重启] 节点 %s 已按新配置重启", rule.Alias))
	}
	_ = a.saveConfig()
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: rule})
	if err != nil {
		return fmt.Errorf("修改已保存，但重启节点失败: %v", err)
	}
	return nil
}

// restartCompositeLocked 编辑后重新启动链式代理 / 故障转移（旧进程已由调用方停止）。需持有 a.mu。
func (a *MyService) restartCompositeLocked(kind, alias string, port int, start func() error,
	setFailed func(reason string)) error {
	if err := a.runWithReleasedPortLocked(port, start); err != nil {
		setFailed(err.Error())
		_ = a.saveConfig()
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
		return fmt.Errorf("修改已保存，但重启%s失败: %v", kind, err)
	}
	_ = a.saveConfig()
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	a.log(fmt.Sprintf("[重启] %s %s 已按新配置重启", kind, alias))
	return nil
}

// restartLoadBalancerLocked 编辑后重新启动故障转移。
func (a *MyService) restartLoadBalancerLocked(lb *models.LoadBalanceNode) error {
	return a.restartCompositeLocked("故障转移", lb.Alias, lb.LocalPort,
		func() error { return a.startLoadBalancerInternal(lb) },
		func(reason string) {
			lb.Enabled = false
			lb.LastError = reason
			lb.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
		})
}

// restartChainProxyLocked 编辑后重新启动链式代理。
func (a *MyService) restartChainProxyLocked(chain *models.ChainProxy) error {
	return a.restartCompositeLocked("链式代理", chain.Alias, chain.LocalPort,
		func() error { return a.startChainProxyInternal(chain) },
		func(reason string) {
			chain.Enabled = false
			chain.LastError = reason
			chain.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
		})
}
