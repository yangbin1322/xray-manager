package config

import (
	"fmt"
	"runtime"
)

// SysProxyManager 系统代理管理器
type SysProxyManager struct {
	enabled bool
	port    int
}

// NewSysProxyManager 创建系统代理管理器
func NewSysProxyManager() *SysProxyManager {
	return &SysProxyManager{}
}

// EnableSystemProxy 设置系统代理
func (m *SysProxyManager) EnableSystemProxy(port int) error {
	m.port = port

	var err error
	switch runtime.GOOS {
	case "windows":
		err = enableWindowsProxy(port)
	case "darwin":
		err = enableDarwinProxy(port)
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	if err != nil {
		return err
	}
	m.enabled = true
	return nil
}

// DisableSystemProxy 取消系统代理
func (m *SysProxyManager) DisableSystemProxy() error {
	var err error
	switch runtime.GOOS {
	case "windows":
		err = disableWindowsProxy()
	case "darwin":
		err = disableDarwinProxy()
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	if err != nil {
		return err
	}
	m.enabled = false
	m.port = 0
	return nil
}

// IsEnabled 检查系统代理是否已启用
func (m *SysProxyManager) IsEnabled() bool {
	return m.enabled
}

// GetPort 获取当前代理端口
func (m *SysProxyManager) GetPort() int {
	return m.port
}
