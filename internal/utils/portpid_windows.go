//go:build windows

package utils

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd
}

// listAllPortPIDs 跑一次 netstat，返回「监听端口 -> PID 列表」全表。
//
// netstat 本身就是全量输出，按端口逐个调用只是在重复同样的开销：
// 批量启动上千节点时那是上千次 fork，会把启动流程拖死。
func listAllPortPIDs() map[int][]int {
	out, err := hiddenCommand("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}

	seen := make(map[int]map[int]bool)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// 格式: TCP  0.0.0.0:10808  0.0.0.0:0  LISTENING  1234
		if len(fields) < 5 || fields[0] != "TCP" {
			continue
		}
		if !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 0 {
			continue
		}
		port := portFromAddress(fields[1])
		if port <= 0 {
			continue
		}
		if seen[port] == nil {
			seen[port] = make(map[int]bool)
		}
		seen[port][pid] = true
	}

	result := make(map[int][]int, len(seen))
	for port, pidSet := range seen {
		pids := make([]int, 0, len(pidSet))
		for pid := range pidSet {
			pids = append(pids, pid)
		}
		result[port] = pids
	}
	return result
}

// portFromAddress 从 netstat 的本地地址列取出端口号。
// 需同时处理 IPv4 (0.0.0.0:10808) 和 IPv6 ([::]:10808) 两种写法。
func portFromAddress(address string) int {
	idx := strings.LastIndex(address, ":")
	if idx < 0 {
		return 0
	}
	port, err := strconv.Atoi(address[idx+1:])
	if err != nil {
		return 0
	}
	return port
}

// GetProcessName 获取指定 PID 的进程名（如 xray.exe），获取失败返回空字符串。
//
// 直接走 Win32 进程快照，不再 fork tasklist：tasklist 依赖 RPC，在部分机器上
// 会卡到超时（实测单次 303 秒后报错返回空），足以让启动流程整个僵住。
// 快照是纯本地调用，微秒级完成。
func GetProcessName(pid int) string {
	if pid <= 0 {
		return ""
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == uint32(pid) {
			return windows.UTF16ToString(entry.ExeFile[:])
		}
	}
	return ""
}

// KillPID 强制终止指定 PID 的进程（含子进程）
func KillPID(pid int) error {
	if err := hiddenCommand("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run(); err != nil {
		return fmt.Errorf("taskkill PID %d 失败: %v", pid, err)
	}
	return nil
}
