package utils

import (
	"fmt"
	"time"
)

// EnsurePortFree 确保端口未被占用。
// 无法证明占用进程属于当前实例时绝不终止进程，避免多客户端之间互相误杀。
// logFunc 可为 nil。
func EnsurePortFree(port int, logFunc func(string)) error {
	// 快速路径：端口本就空闲（绝大多数情况），无需 fork netstat 查 PID
	if CheckPortAvailable(port) {
		return nil
	}

	pids := GetPortPIDs(port)
	if len(pids) == 0 {
		return nil
	}

	if len(pids) > 0 {
		pid := pids[0]
		name := GetProcessName(pid)
		return fmt.Errorf("端口 %d 已被占用 (PID:%d, 进程:%s)，可能属于其他客户端，请更换端口", port, pid, name)
	}
	return fmt.Errorf("端口 %d 已被占用，请更换端口", port)
}

// WaitPortReleased 等待端口释放，超时后强制终止残留的内核进程并再次确认。
// 优先使用轻量的 net.Listen 探测（纳秒级），仅在超时后才回退到较慢的 netstat 查 PID，
// 避免在停止流程中反复 fork netstat 子进程导致批量操作卡顿。
func WaitPortReleased(port int, timeout time.Duration, logFunc func(string)) {
	log := func(msg string) {
		if logFunc != nil {
			logFunc(msg)
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if CheckPortAvailable(port) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 超时端口仍不可用时仅记录，不终止未知进程，避免误杀其他客户端。
	for _, pid := range GetPortPIDs(port) {
		name := GetProcessName(pid)
		log(fmt.Sprintf("[端口清理] 停止后端口 %d 仍被占用 (PID:%d, 进程:%s)，保留该进程以避免影响其他客户端", port, pid, name))
	}
}
