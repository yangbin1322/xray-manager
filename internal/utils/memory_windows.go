//go:build windows

package utils

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// AvailableMemory 返回当前可用物理内存（字节）。取不到时返回 0。
func AvailableMemory() uint64 {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	if ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&status))); ret == 0 {
		return 0
	}
	return status.AvailPhys
}
