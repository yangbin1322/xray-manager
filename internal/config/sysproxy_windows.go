//go:build windows

package config

import (
	"fmt"
	"os/exec"
)

// enableWindowsProxy 通过修改注册表设置 Windows 系统代理
func enableWindowsProxy(port int) error {
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

// disableWindowsProxy 取消 Windows 系统代理
func disableWindowsProxy() error {
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
	// 使用 rundll32 通知系统刷新 Internet 设置
	cmd := exec.Command("rundll32.exe", "wininet.dll,InternetSetOptionW", "39", "0", "0", "0")
	_ = cmd.Run()
}
