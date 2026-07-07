package config

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

	if err := platformEnableProxy(port); err != nil {
		return err
	}
	m.enabled = true
	return nil
}

// DisableSystemProxy 取消系统代理
func (m *SysProxyManager) DisableSystemProxy() error {
	if err := platformDisableProxy(); err != nil {
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

// GetCurrentSystemProxy 读取操作系统当前生效的代理地址（如 http://127.0.0.1:10808）
// 未设置代理时返回空字符串
func (m *SysProxyManager) GetCurrentSystemProxy() string {
	return platformGetCurrentProxy()
}
