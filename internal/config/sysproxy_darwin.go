//go:build darwin

package config

import (
	"fmt"
	"os/exec"
	"strings"
)

// getActiveNetworkServices 获取活动的网络服务名
func getActiveNetworkServices() []string {
	// 获取所有网络服务
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return []string{"Wi-Fi"}
	}

	var services []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.Contains(line, "asterisk") {
			continue
		}
		// 跳过标题行
		if strings.Contains(line, "network service") {
			continue
		}
		services = append(services, line)
	}

	if len(services) == 0 {
		return []string{"Wi-Fi"}
	}
	return services
}

// enableDarwinProxy 设置 macOS 系统代理
func enableDarwinProxy(port int) error {
	services := getActiveNetworkServices()
	portStr := fmt.Sprintf("%d", port)

	for _, service := range services {
		// 设置 HTTP 代理
		if err := exec.Command("networksetup", "-setwebproxy", service, "127.0.0.1", portStr).Run(); err != nil {
			continue
		}
		// 启用 HTTP 代理
		_ = exec.Command("networksetup", "-setwebproxystate", service, "on").Run()

		// 设置 HTTPS 代理
		_ = exec.Command("networksetup", "-setsecurewebproxy", service, "127.0.0.1", portStr).Run()
		_ = exec.Command("networksetup", "-setsecurewebproxystate", service, "on").Run()

		// 设置 SOCKS 代理
		_ = exec.Command("networksetup", "-setsocksfirewallproxy", service, "127.0.0.1", portStr).Run()
		_ = exec.Command("networksetup", "-setsocksfirewallproxystate", service, "on").Run()
	}

	return nil
}

// disableDarwinProxy 取消 macOS 系统代理
func disableDarwinProxy() error {
	services := getActiveNetworkServices()

	for _, service := range services {
		_ = exec.Command("networksetup", "-setwebproxystate", service, "off").Run()
		_ = exec.Command("networksetup", "-setsecurewebproxystate", service, "off").Run()
		_ = exec.Command("networksetup", "-setsocksfirewallproxystate", service, "off").Run()
	}

	return nil
}
