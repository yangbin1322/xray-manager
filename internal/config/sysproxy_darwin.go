//go:build darwin

package config

import (
	"fmt"
	"os/exec"
	"strings"
)

// getActiveNetworkServices 获取活动的网络服务名
func getActiveNetworkServices() []string {
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

// platformEnableProxy 设置 macOS 系统代理
func platformEnableProxy(port int) error {
	services := getActiveNetworkServices()
	portStr := fmt.Sprintf("%d", port)

	for _, service := range services {
		if err := exec.Command("networksetup", "-setwebproxy", service, "127.0.0.1", portStr).Run(); err != nil {
			continue
		}
		_ = exec.Command("networksetup", "-setwebproxystate", service, "on").Run()
		_ = exec.Command("networksetup", "-setsecurewebproxy", service, "127.0.0.1", portStr).Run()
		_ = exec.Command("networksetup", "-setsecurewebproxystate", service, "on").Run()
		_ = exec.Command("networksetup", "-setsocksfirewallproxy", service, "127.0.0.1", portStr).Run()
		_ = exec.Command("networksetup", "-setsocksfirewallproxystate", service, "on").Run()
	}

	return nil
}

// platformDisableProxy 取消 macOS 系统代理
func platformDisableProxy() error {
	services := getActiveNetworkServices()

	for _, service := range services {
		_ = exec.Command("networksetup", "-setwebproxystate", service, "off").Run()
		_ = exec.Command("networksetup", "-setsecurewebproxystate", service, "off").Run()
		_ = exec.Command("networksetup", "-setsocksfirewallproxystate", service, "off").Run()
	}

	return nil
}
