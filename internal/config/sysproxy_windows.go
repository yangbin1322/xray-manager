//go:build windows

package config

import (
	"fmt"
	"os/exec"
	"strings"
)

// platformEnableProxy 通过修改注册表设置 Windows 系统代理
func platformEnableProxy(port int) error {
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// 启用代理
	cmd := exec.Command("reg", "add",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("启用代理失败: %v", err)
	}

	// 设置代理地址
	cmd = exec.Command("reg", "add",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer", "/t", "REG_SZ", "/d", proxyAddr, "/f")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("设置代理地址失败: %v", err)
	}

	// 通知系统代理设置已更改
	refreshWindowsProxy()

	return nil
}

// platformDisableProxy 取消 Windows 系统代理
func platformDisableProxy() error {
	cmd := exec.Command("reg", "add",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("取消代理失败: %v", err)
	}

	// 通知系统代理设置已更改
	refreshWindowsProxy()

	return nil
}

// refreshWindowsProxy 通知系统刷新代理设置
func refreshWindowsProxy() {
	cmd := exec.Command("rundll32.exe", "wininet.dll,InternetSetOptionW", "39", "0", "0", "0")
	_ = cmd.Run()
}

// platformGetCurrentProxy 读取 Windows 注册表中当前生效的系统代理
func platformGetCurrentProxy() string {
	// 先检查代理是否启用
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable").Output()
	if err != nil || !strings.Contains(string(out), "0x1") {
		return ""
	}

	// 读取代理地址
	out, err = exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer").Output()
	if err != nil {
		return ""
	}

	// 输出格式: "    ProxyServer    REG_SZ    127.0.0.1:10808"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ProxyServer") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		addr := fields[len(fields)-1]
		// 可能是 "http=...;https=..." 形式，取 http 的地址
		if strings.Contains(addr, "=") {
			for _, part := range strings.Split(addr, ";") {
				if strings.HasPrefix(part, "http=") {
					addr = strings.TrimPrefix(part, "http=")
					break
				}
			}
			if strings.Contains(addr, "=") {
				return ""
			}
		}
		if addr == "" {
			return ""
		}
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		return addr
	}
	return ""
}
