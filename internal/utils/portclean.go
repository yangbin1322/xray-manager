package utils

import (
	"fmt"
	"strings"
	"time"
)

// coreProcessNames 本应用管理的代理内核进程名（小写，不含扩展名）
var coreProcessNames = map[string]bool{
	"xray":     true,
	"sing-box": true,
}

// isCoreProcess 判断进程名是否为本应用管理的代理内核
func isCoreProcess(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, ".exe"))
	return coreProcessNames[name]
}

// EnsurePortFree 确保端口未被占用：
// 若端口上有残留的 xray/sing-box 进程则直接终止；若被其他程序占用则返回错误。
// logFunc 可为 nil。
func EnsurePortFree(port int, logFunc func(string)) error {
	log := func(msg string) {
		if logFunc != nil {
			logFunc(msg)
		}
	}

	pids := GetPortPIDs(port)
	if len(pids) == 0 {
		return nil
	}

	for _, pid := range pids {
		name := GetProcessName(pid)
		if name == "" || isCoreProcess(name) {
			log(fmt.Sprintf("[端口清理] 端口 %d 被残留进程占用 (PID:%d, 进程:%s)，正在终止", port, pid, name))
			if err := KillPID(pid); err != nil {
				log(fmt.Sprintf("[端口清理] 终止 PID %d 失败: %v", pid, err))
			}
		} else {
			return fmt.Errorf("端口 %d 被其他程序占用 (PID:%d, 进程:%s)，请更换端口或手动结束该进程", port, pid, name)
		}
	}

	// 等待端口释放（最多 3 秒）
	for i := 0; i < 15; i++ {
		if len(GetPortPIDs(port)) == 0 {
			log(fmt.Sprintf("[端口清理] 端口 %d 已释放", port))
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("端口 %d 清理后仍被占用", port)
}

// WaitPortReleased 等待端口释放，超时后强制终止残留的内核进程并再次确认
func WaitPortReleased(port int, timeout time.Duration, logFunc func(string)) {
	log := func(msg string) {
		if logFunc != nil {
			logFunc(msg)
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(GetPortPIDs(port)) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 超时仍有进程占用，强制终止内核进程
	for _, pid := range GetPortPIDs(port) {
		name := GetProcessName(pid)
		if name == "" || isCoreProcess(name) {
			log(fmt.Sprintf("[端口清理] 停止后端口 %d 仍被占用 (PID:%d, 进程:%s)，强制终止", port, pid, name))
			_ = KillPID(pid)
		}
	}
}
