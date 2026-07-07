//go:build linux

package config

import (
	"fmt"
	"os"
)

// platformGetCurrentProxy 读取环境变量中的代理设置
func platformGetCurrentProxy() string {
	for _, key := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}

// platformEnableProxy Linux 下暂不支持系统代理
func platformEnableProxy(port int) error {
	return fmt.Errorf("Linux 暂不支持自动设置系统代理，请手动设置 http_proxy=http://127.0.0.1:%d", port)
}

// platformDisableProxy Linux 下暂不支持系统代理
func platformDisableProxy() error {
	return fmt.Errorf("Linux 暂不支持自动取消系统代理，请手动取消环境变量")
}
