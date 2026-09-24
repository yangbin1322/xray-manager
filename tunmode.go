package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
	"xray-manager/internal/utils"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 以管理员身份重启时传给新实例的命令行参数
const (
	argTun     = "--tun"      // 新实例恢复节点后对该目标开启 TUN
	argWaitPID = "--wait-pid" // 新实例先等旧实例退出，避免两者抢同一批本地端口
)

// pendingTunTarget 启动参数里要求开启 TUN 的目标 ID（仅本次启动有效，不持久化）
var pendingTunTarget string

// parseStartupArgs 解析以管理员身份重启时带过来的参数，并等待旧实例退出。
// 必须在创建应用之前调用：旧实例退出前会停掉所有节点、释放端口。
func parseStartupArgs(args []string) {
	waitPID := 0
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case argTun:
			pendingTunTarget = args[i+1]
		case argWaitPID:
			waitPID, _ = strconv.Atoi(args[i+1])
		}
	}
	if waitPID > 0 && waitPID != os.Getpid() {
		waitProcessExit(waitPID, 30*time.Second)
	}
}

// waitProcessExit 等待指定进程退出，超时后放弃等待。
func waitProcessExit(pid int, timeout time.Duration) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return // 已经不存在
	}
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// ProxyStatus 当前代理模式
type ProxyStatus struct {
	Mode   string `json:"mode"`   // off / system / tun
	Target string `json:"target"` // TUN 转发目标名称
}

// GetProxyStatus 获取当前代理模式（系统代理与 TUN 二选一）
func (a *MyService) GetProxyStatus() ProxyStatus {
	if a.tunManager != nil && a.tunManager.IsRunning() {
		_, alias := a.tunManager.Target()
		return ProxyStatus{Mode: "tun", Target: alias}
	}
	if a.sysProxyManager != nil && a.sysProxyManager.IsEnabled() {
		return ProxyStatus{Mode: "system"}
	}
	return ProxyStatus{Mode: "off"}
}

// TunNeedsElevation 开启 TUN 前是否需要先以管理员身份重启（创建虚拟网卡需要管理员/root 权限）
func (a *MyService) TunNeedsElevation() bool {
	return !utils.IsElevated()
}

// EnableTunMode 开启 TUN，把全局流量转发到指定节点 / 链式代理 / 故障转移。
// 系统代理与 TUN 二选一，开启前先关闭系统代理。
func (a *MyService) EnableTunMode(ruleID string) error {
	if a.tunManager == nil {
		return fmt.Errorf("进程管理器未初始化")
	}
	if !utils.IsElevated() {
		return fmt.Errorf("TUN 模式需要以管理员身份运行")
	}

	a.mu.RLock()
	target, err := a.resolveProxyTargetLocked(ruleID)
	a.mu.RUnlock()
	if err != nil {
		return err
	}

	// 切换期间摘除跟随目标，切换失败时不会被误报为代理意外退出
	a.proxyWatch.clear()
	if a.sysProxyManager.IsEnabled() {
		if err := a.sysProxyManager.DisableSystemProxy(); err != nil {
			return fmt.Errorf("关闭系统代理失败: %v", err)
		}
		a.log("[系统代理] 已取消系统代理（切换到 TUN）")
	}
	if err := a.tunManager.Start(target.port, target.alias); err != nil {
		return err
	}
	a.proxyWatch.set(ruleID, target)
	return nil
}

// DisableTunMode 关闭 TUN
func (a *MyService) DisableTunMode() error {
	// 先摘除跟随目标，否则后台检查会把主动关闭误判为 TUN 意外退出
	a.proxyWatch.clear()
	if a.tunManager != nil {
		a.tunManager.Stop()
	}
	return nil
}

// RestartAsAdminForTun 以管理员身份重启本程序，重启后对指定目标开启 TUN。
// 用户在 UAC 弹窗中拒绝时返回错误，当前实例继续运行。
func (a *MyService) RestartAsAdminForTun(ruleID string) error {
	args := []string{argTun, ruleID, argWaitPID, strconv.Itoa(os.Getpid())}
	if err := utils.RelaunchElevated(args); err != nil {
		return err
	}
	a.log("[TUN] 正在以管理员身份重启...")
	// 延后退出，让本次调用先把结果返回给前端
	go func() {
		time.Sleep(300 * time.Millisecond)
		a.app.Quit()
	}()
	return nil
}

// applyStartupTun 启动参数要求开启 TUN 时（以管理员身份重启后），在节点恢复完成后开启。
func (a *MyService) applyStartupTun() {
	if pendingTunTarget == "" {
		return
	}
	id := pendingTunTarget
	pendingTunTarget = ""
	if err := a.EnableTunMode(id); err != nil {
		a.log(fmt.Sprintf("[TUN] 自动开启失败: %v", err))
		return
	}
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "proxyModeChanged"})
}
