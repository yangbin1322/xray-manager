//go:build windows

package utils

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd
}

// GetPortPIDs 获取监听指定 TCP 端口的进程 PID 列表
func GetPortPIDs(port int) []int {
	out, err := hiddenCommand("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}

	portSuffix := fmt.Sprintf(":%d", port)
	pidSet := make(map[int]bool)

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// 格式: TCP  0.0.0.0:10808  0.0.0.0:0  LISTENING  1234
		if len(fields) < 5 || fields[0] != "TCP" {
			continue
		}
		if !strings.HasSuffix(fields[1], portSuffix) {
			continue
		}
		if !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		if pid, err := strconv.Atoi(fields[4]); err == nil && pid > 0 {
			pidSet[pid] = true
		}
	}

	pids := make([]int, 0, len(pidSet))
	for pid := range pidSet {
		pids = append(pids, pid)
	}
	return pids
}

// GetProcessName 获取指定 PID 的进程名（如 xray.exe），获取失败返回空字符串
func GetProcessName(pid int) string {
	out, err := hiddenCommand("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	// 格式: "xray.exe","1234","Console","1","12,345 K"
	if !strings.HasPrefix(line, "\"") {
		return ""
	}
	parts := strings.SplitN(line, "\",\"", 2)
	if len(parts) < 1 {
		return ""
	}
	return strings.Trim(parts[0], "\"")
}

// KillPID 强制终止指定 PID 的进程（含子进程）
func KillPID(pid int) error {
	if err := hiddenCommand("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run(); err != nil {
		return fmt.Errorf("taskkill PID %d 失败: %v", pid, err)
	}
	return nil
}
