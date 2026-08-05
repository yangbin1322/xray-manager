//go:build !windows

package utils

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// AvailableMemory 返回当前可用物理内存（字节）。取不到时返回 0。
func AvailableMemory() uint64 {
	if runtime.GOOS == "darwin" {
		return availableMemoryDarwin()
	}
	return availableMemoryLinux()
}

// availableMemoryLinux 读取 /proc/meminfo 的 MemAvailable。
func availableMemoryLinux() uint64 {
	out, err := exec.Command("sh", "-c", "grep MemAvailable /proc/meminfo").Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	// 格式: MemAvailable:   12345678 kB
	if len(fields) < 2 {
		return 0
	}
	kb, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

// availableMemoryDarwin 用 vm_stat 的空闲页数估算。
func availableMemoryDarwin() uint64 {
	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}
	pageSize := uint64(4096)
	var freePages uint64
	for _, line := range strings.Split(string(out), "\n") {
		// 首行形如: Mach Virtual Memory Statistics: (page size of 16384 bytes)
		if strings.Contains(line, "page size of") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "of" && i+1 < len(fields) {
					if size, err := strconv.ParseUint(fields[i+1], 10, 64); err == nil {
						pageSize = size
					}
					break
				}
			}
			continue
		}
		// 空闲页与可回收页都算作可用
		if strings.HasPrefix(line, "Pages free:") || strings.HasPrefix(line, "Pages inactive:") {
			value := strings.TrimSuffix(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), ".")
			if pages, err := strconv.ParseUint(value, 10, 64); err == nil {
				freePages += pages
			}
		}
	}
	return freePages * pageSize
}
