//go:build !windows

package utils

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// listAllPortPIDs 跑一次 lsof，返回「监听端口 -> PID 列表」全表。
//
// 按端口逐个调用 lsof 在批量启动上千节点时是上千次 fork，会把启动流程拖死。
// 这里用 -F 机器可读格式一次拿全，输出形如：
//
//	p1234
//	n*:10808
//	n127.0.0.1:10809
func listAllPortPIDs() map[int][]int {
	// lsof 在部分文件无权限访问时会以退出码 1 结束（非 root 下很常见），
	// 但仍会输出可见部分。这里只要拿到了内容就继续解析，避免整张表被丢弃——
	// 全表查询一旦返回空，所有端口都会被误判为未占用。
	out, err := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn").Output()
	if err != nil && len(out) == 0 {
		return nil
	}
	return parseLsofPortPIDs(string(out))
}

// parseLsofPortPIDs 解析 lsof -Fpn 输出。与执行分离以便直接测试解析逻辑。
func parseLsofPortPIDs(out string) map[int][]int {
	seen := make(map[int]map[int]bool)
	currentPID := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(line[1:])
			if err != nil || pid <= 0 {
				currentPID = 0
				continue
			}
			currentPID = pid
		case 'n':
			if currentPID == 0 {
				continue
			}
			port := portFromAddress(line[1:])
			if port <= 0 {
				continue
			}
			if seen[port] == nil {
				seen[port] = make(map[int]bool)
			}
			seen[port][currentPID] = true
		}
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

// portFromAddress 从 lsof 的地址字段取出端口号。
// 需同时处理 *:10808、127.0.0.1:10808 和 [::1]:10808 三种写法。
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
