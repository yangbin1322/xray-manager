//go:build !windows

package utils

import (
	"fmt"
	"os"
)

// IsElevated 当前进程是否以 root 运行（创建 TUN 网卡需要）。
func IsElevated() bool {
	return os.Geteuid() == 0
}

// RelaunchElevated 非 Windows 平台不做提权，提示用户自行以 root 运行。
func RelaunchElevated(args []string) error {
	return fmt.Errorf("TUN 模式需要 root 权限，请使用 sudo 运行本程序")
}
