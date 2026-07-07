//go:build !windows

package utils

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// GetPortPIDs 获取监听指定 TCP 端口的进程 PID 列表
func GetPortPIDs(port int) []int {
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}

	pidSet := make(map[int]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
			pidSet[pid] = true
		}
	}

	pids := make([]int, 0, len(pidSet))
	for pid := range pidSet {
		pids = append(pids, pid)
	}
	return pids
}

// GetProcessName 获取指定 PID 的进程名，获取失败返回空字符串
func GetProcessName(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	// ps 返回的可能是完整路径，取最后一段
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// KillPID 强制终止指定 PID 的进程
func KillPID(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("kill PID %d 失败: %v", pid, err)
	}
	return nil
}
