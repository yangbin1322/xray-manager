//go:build darwin

package process

import "os/exec"

func setPlatformSpecificAttrs(cmd *exec.Cmd) {
	// macOS 不需要特殊配置
	// 如果需要，可以添加 macOS 特定的设置
}
