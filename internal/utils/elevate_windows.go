//go:build windows

package utils

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// IsElevated 当前进程是否以管理员身份运行（创建 TUN 网卡需要）。
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RelaunchElevated 通过 UAC 以管理员身份启动本程序的新实例。
// 用户在 UAC 弹窗里点「否」时返回错误，调用方应保留当前实例。
func RelaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %v", err)
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = windows.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err := windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("以管理员身份重启失败: %v", err)
	}
	return nil
}
