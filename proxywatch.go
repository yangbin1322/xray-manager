package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 系统代理 / TUN 与目标的联动。
//
// 策略是「断网保护」：目标意外失效（进程崩溃、启动后验证不通被自动停止、
// 出口 IP 变化被停用……）时宁可断网也不让流量走直连，只提示用户；
// 只有用户手动停止或删除目标时才自动关闭代理（beginManualStop）。
//
// 目标意外失效的来源很多，这里由后台轮询统一发现并提示；
// 手动操作的入口是有限的几个导出方法，由它们显式标记。

const (
	proxyWatchInterval = time.Second
	// 进程探测连续失败多少次才判定目标已挂。
	// 分片调谐、链式代理重建时进程会短暂不在，单次探测失败不代表目标停了。
	proxyWatchMaxMisses = 3
	// TUN 进程在这段时间内再次退出就不再自动重启，避免反复崩溃时无限重试
	tunRestartCooldown = 30 * time.Second
)

// proxyWatchState 当前代理模式跟随的目标
type proxyWatchState struct {
	mu     sync.Mutex
	id     string // 目标 ID，空表示未开启代理
	port   int
	alias  string
	misses int
	warned bool // 已提示过目标失效，恢复前不重复提示

	// 上次自动重启 TUN 的时间。不随 set/clear 重置：重启本身就会 clear+set
	lastTunRestart time.Time
}

func (w *proxyWatchState) set(id string, t proxyTarget) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.id, w.port, w.alias, w.misses, w.warned = id, t.port, t.alias, 0, false
}

func (w *proxyWatchState) clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.id, w.port, w.alias, w.misses, w.warned = "", 0, "", 0, false
}

func (w *proxyWatchState) get() (id string, port int, alias string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.id, w.port, w.alias
}

// ProxyModeChange 推送给前端的代理模式变更事件
type ProxyModeChange struct {
	Message string `json:"message"` // 提示文本，空表示仅刷新状态
	Level   string `json:"level"`   // 提示级别：success / warning / error
}

// proxyWatchAction 后台检查得出的处理动作
type proxyWatchAction int

const (
	watchNone     proxyWatchAction = iota
	watchWarnDown                  // 目标失效：保持代理（断网保护），提示用户
	watchRecover                   // 失效的目标恢复运行
	watchRepoint                   // 目标仍在运行但本地端口变了：代理改指向新端口
	watchTunDied                   // TUN 进程自己退出：流量已在走直连，需要重新拉起
)

// proxyWatchLoop 定期检查代理目标的状态。
func (a *MyService) proxyWatchLoop(ctx context.Context) {
	ticker := time.NewTicker(proxyWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			action, detail := a.checkProxyTarget(a.GetProxyStatus().Mode)
			a.handleProxyWatch(action, detail)
		}
	}
}

// checkProxyTarget 判断代理目标当前的状态，返回处理动作与说明。
// mode 为当前代理模式（off / system / tun）。
func (a *MyService) checkProxyTarget(mode string) (proxyWatchAction, string) {
	// 手动停止进行中：目标的启用标记可能已清掉，但关闭代理要等操作完成后
	// 由 beginManualStop 处理，这里不能抢先当成意外失效
	if a.manualStops.Load() > 0 {
		return watchNone, ""
	}

	w := &a.proxyWatch
	id, port, alias := w.get()
	if id == "" {
		return watchNone, ""
	}
	if mode == "off" {
		// 跟随目标还在但代理已不在：只可能是 TUN 进程自己退出了（被杀、网卡被移除等）
		return watchTunDied, fmt.Sprintf("TUN 进程意外退出（目标 %s）", alias)
	}

	a.mu.RLock()
	target, err := a.resolveProxyTargetLocked(id)
	a.mu.RUnlock()

	running := false
	switch {
	case err != nil:
		// 未经手动停止却不在运行：被自动停止（验证不通、出口 IP 变化等）、编辑后未重启或被移除
	case target.port != port:
		if a.processManager.IsRunning(target.port) {
			return watchRepoint, fmt.Sprintf("目标 %s 的本地端口已变更为 %d", alias, target.port)
		}
	default:
		running = a.processManager.IsRunning(port)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if running {
		w.misses = 0
		if w.warned {
			w.warned = false
			return watchRecover, fmt.Sprintf("目标 %s 已恢复运行，代理恢复正常", alias)
		}
		return watchNone, ""
	}
	w.misses++
	if w.misses < proxyWatchMaxMisses || w.warned {
		return watchNone, ""
	}
	w.warned = true
	return watchWarnDown, fmt.Sprintf("目标 %s 已停止运行", alias)
}

// handleProxyWatch 执行后台检查得出的动作。
func (a *MyService) handleProxyWatch(action proxyWatchAction, detail string) {
	switch action {
	case watchWarnDown:
		a.notifyProxyMode(detail+"。为防止流量走直连，代理保持开启（当前无法上网），请重新启动该目标或手动关闭代理", "error")

	case watchRecover:
		a.notifyProxyMode(detail, "success")

	case watchRepoint, watchTunDied:
		id, _, _ := a.proxyWatch.get()
		if action == watchTunDied {
			w := &a.proxyWatch
			w.mu.Lock()
			tooSoon := time.Since(w.lastTunRestart) < tunRestartCooldown
			if !tooSoon {
				w.lastTunRestart = time.Now()
			}
			w.mu.Unlock()
			if tooSoon {
				w.clear()
				a.notifyProxyMode(detail+"，且短时间内反复退出，已停止自动重启，请查看日志", "error")
				return
			}
		}
		mode := a.GetProxyStatus().Mode
		var err error
		if mode == "system" {
			err = a.EnableSystemProxy(id)
		} else {
			// TUN 模式，或 TUN 进程已退出（mode 为 off）：按当前目标重新拉起
			err = a.EnableTunMode(id)
		}
		if err != nil {
			if action == watchTunDied {
				a.proxyWatch.clear()
			}
			a.notifyProxyMode(fmt.Sprintf("%s，重新开启失败: %v", detail, err), "error")
			return
		}
		a.notifyProxyMode(detail+"，已自动重新开启", "warning")
	}
}

// beginManualStop 标记一次手动停止 / 删除操作，返回的函数在操作结束（解锁后）调用。
//
// 用法：在方法开头、加锁之前写 defer a.beginManualStop()()。
// 操作期间后台检查暂停，避免把手动停止误判为意外失效；
// 结束时若正是本次操作停止或删除了代理目标，自动关闭系统代理 / TUN，网络恢复直连。
// 目标在操作前就已意外失效（断网保护中）时，停止其他节点不会触发关闭。
func (a *MyService) beginManualStop() func() {
	a.manualStops.Add(1)
	id, _, _ := a.proxyWatch.get()
	beforeRunning, beforeExists := a.proxyTargetState(id)
	return func() {
		defer a.manualStops.Add(-1)
		if id == "" {
			return
		}
		afterRunning, afterExists := a.proxyTargetState(id)
		if (beforeRunning && !afterRunning) || (beforeExists && !afterExists) {
			a.releaseProxy(id)
		}
	}
}

// proxyTargetState 返回目标是否在运行、是否还存在。
func (a *MyService) proxyTargetState(id string) (running, exists bool) {
	if id == "" {
		return false, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if _, err := a.resolveProxyTargetLocked(id); err == nil {
		return true, true
	}
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == id {
			return false, true
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == id {
			return false, true
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == id {
			return false, true
		}
	}
	return false, false
}

// releaseProxy 目标被手动停止 / 删除后关闭代理。
func (a *MyService) releaseProxy(id string) {
	curID, _, alias := a.proxyWatch.get()
	if curID != id {
		return // 期间代理已被切换到别的目标
	}
	mode := a.GetProxyStatus().Mode
	switch mode {
	case "tun":
		_ = a.DisableTunMode()
	case "system":
		_ = a.DisableSystemProxy()
	}
	a.proxyWatch.clear()

	label := map[string]string{"tun": "TUN", "system": "系统代理"}[mode]
	if label == "" {
		return
	}
	a.notifyProxyMode(fmt.Sprintf("已手动停止 %s，自动关闭%s", alias, label), "success")
}

// notifyProxyMode 记录日志并通知前端刷新代理状态、弹出提示。
func (a *MyService) notifyProxyMode(message, level string) {
	a.log("[代理] " + message)
	if a.app == nil {
		return
	}
	a.app.Event.EmitEvent(&application.CustomEvent{
		Name: "proxyModeChanged",
		Data: ProxyModeChange{Message: message, Level: level},
	})
}
