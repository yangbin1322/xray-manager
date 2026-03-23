package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// AutoStartManager 开机自启管理器
type AutoStartManager struct {
	appName string
	exePath string
}

// NewAutoStartManager 创建开机自启管理器
func NewAutoStartManager(appName string) (*AutoStartManager, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	return &AutoStartManager{
		appName: appName,
		exePath: exePath,
	}, nil
}

// Enable 启用开机自启
func (m *AutoStartManager) Enable() error {
	switch runtime.GOOS {
	case "windows":
		return m.enableWindows()
	case "linux":
		return m.enableLinux()
	case "darwin":
		return m.enableMacOS()
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
}

// Disable 禁用开机自启
func (m *AutoStartManager) Disable() error {
	switch runtime.GOOS {
	case "windows":
		return m.disableWindows()
	case "linux":
		return m.disableLinux()
	case "darwin":
		return m.disableMacOS()
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
}

// IsEnabled 检查系统是否已启用开机自启
func (m *AutoStartManager) IsEnabled() bool {
	switch runtime.GOOS {
	case "windows":
		return m.isEnabledWindows()
	case "linux":
		return m.isEnabledLinux()
	case "darwin":
		return m.isEnabledMacOS()
	default:
		return false
	}
}

func (m *AutoStartManager) isEnabledWindows() bool {
	regPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	cmd := exec.Command("reg", "query", fmt.Sprintf("HKCU\\%s", regPath), "/v", m.appName)
	return cmd.Run() == nil
}

func (m *AutoStartManager) isEnabledLinux() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	desktopFile := filepath.Join(homeDir, ".config", "autostart", fmt.Sprintf("%s.desktop", m.appName))
	_, err = os.Stat(desktopFile)
	return err == nil
}

func (m *AutoStartManager) isEnabledMacOS() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	plistFile := filepath.Join(homeDir, "Library", "LaunchAgents", fmt.Sprintf("com.%s.plist", m.appName))
	_, err = os.Stat(plistFile)
	return err == nil
}

// Windows 开机自启实现
func (m *AutoStartManager) enableWindows() error {
	// 使用注册表添加开机自启
	regPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	cmd := exec.Command("reg", "add", fmt.Sprintf("HKCU\\%s", regPath), "/v", m.appName, "/t", "REG_SZ", "/d", m.exePath, "/f")
	return cmd.Run()
}

func (m *AutoStartManager) disableWindows() error {
	// 从注册表删除开机自启
	regPath := `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`
	cmd := exec.Command("reg", "delete", fmt.Sprintf("HKCU\\%s", regPath), "/v", m.appName, "/f")
	return cmd.Run()
}

// Linux 开机自启实现（使用 systemd 或 .desktop 文件）
func (m *AutoStartManager) enableLinux() error {
	// 使用 .desktop 文件实现开机自启
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	autostartDir := filepath.Join(homeDir, ".config", "autostart")
	if err := os.MkdirAll(autostartDir, 0755); err != nil {
		return err
	}

	desktopFile := filepath.Join(autostartDir, fmt.Sprintf("%s.desktop", m.appName))
	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=%s
Exec=%s
Hidden=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
`, m.appName, m.exePath)

	return os.WriteFile(desktopFile, []byte(content), 0644)
}

func (m *AutoStartManager) disableLinux() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	desktopFile := filepath.Join(homeDir, ".config", "autostart", fmt.Sprintf("%s.desktop", m.appName))
	if _, err := os.Stat(desktopFile); err == nil {
		return os.Remove(desktopFile)
	}

	return nil
}

// macOS 开机自启实现（使用 LaunchAgents）
func (m *AutoStartManager) enableMacOS() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	launchAgentsDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return err
	}

	plistFile := filepath.Join(launchAgentsDir, fmt.Sprintf("com.%s.plist", m.appName))
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`, m.appName, m.exePath)

	return os.WriteFile(plistFile, []byte(content), 0644)
}

func (m *AutoStartManager) disableMacOS() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	plistFile := filepath.Join(homeDir, "Library", "LaunchAgents", fmt.Sprintf("com.%s.plist", m.appName))
	if _, err := os.Stat(plistFile); err == nil {
		return os.Remove(plistFile)
	}

	return nil
}
