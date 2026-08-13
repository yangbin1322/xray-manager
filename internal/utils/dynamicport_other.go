//go:build !windows

package utils

import "fmt"

// SystemDynamicPortRange 非 Windows 平台不做检测。
//
// 这个坑是 Windows 特有的：临时端口占用表现为 Bound 状态、常规工具查不到，
// 且默认范围被第三方工具改低的情况也只在 Windows 上常见。
func SystemDynamicPortRange() (DynamicPortRange, error) {
	return DynamicPortRange{}, fmt.Errorf("当前平台不支持读取动态端口范围")
}
