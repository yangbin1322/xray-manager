//go:build windows

package utils

import (
	"fmt"
	"strconv"
	"strings"
)

// SystemDynamicPortRange 读取 Windows 的 TCP 动态端口范围。
//
// 输出形如（字段名随系统语言变化，因此按「冒号后取数字」解析而非匹配字段名）：
//
//	Protocol tcp Dynamic Port Range
//	---------------------------------
//	Start Port      : 49152
//	Number of Ports : 16384
func SystemDynamicPortRange() (DynamicPortRange, error) {
	out, err := hiddenCommand("netsh", "int", "ipv4", "show", "dynamicport", "tcp").Output()
	if err != nil {
		return DynamicPortRange{}, err
	}

	var values []int
	for _, line := range strings.Split(string(out), "\n") {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		field := strings.TrimSpace(line[idx+1:])
		n, convErr := strconv.Atoi(field)
		if convErr != nil {
			continue
		}
		values = append(values, n)
	}
	// 两个数值依次是起始端口与端口数量
	if len(values) < 2 {
		return DynamicPortRange{}, fmt.Errorf("无法解析 netsh 输出的动态端口范围")
	}
	return DynamicPortRange{Start: values[0], Count: values[1]}, nil
}
