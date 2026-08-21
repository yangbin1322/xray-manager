package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"xray-manager/internal/models"
)

const configFileName = "xray-manager-config.json"

// Manager 配置管理器
type Manager struct {
	configPath string
}

// NewManager 创建配置管理器
func NewManager() (*Manager, error) {
	// 获取可执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exeDir := filepath.Dir(exePath)
	configPath := filepath.Join(exeDir, configFileName)

	return &Manager{
		configPath: configPath,
	}, nil
}

// Load 加载配置
func (m *Manager) Load() (*models.Config, error) {
	// 如果配置文件不存在，返回默认配置
	if _, err := os.Stat(m.configPath); os.IsNotExist(err) {
		return &models.Config{
			AutoStart: false,
			Rules:     []models.ProxyRule{},
			HTTPAPI:   defaultHTTPAPIConfig(),
		}, nil
	}

	// 读取配置文件
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return nil, err
	}

	// 解析配置
	var config models.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if !config.HTTPAPI.Configured {
		config.HTTPAPI = defaultHTTPAPIConfig()
	}

	return &config, nil
}

func defaultHTTPAPIConfig() models.HTTPAPIConfig {
	return models.HTTPAPIConfig{
		Configured: true,
		Enabled:    true,
		Host:       "127.0.0.1",
		Port:       9090,
	}
}

// Save 保存配置
func (m *Manager) Save(config *models.Config) error {
	// 序列化配置
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return writeFileAtomic(m.configPath, data)
}

// writeFileAtomic 先写临时文件并 fsync，再原子替换目标文件。
//
// 直接 os.WriteFile 只保证数据进了页缓存：此时断电会留下一个长度正确、
// 内容却全是 NUL 的文件，下次启动解析失败。配置每次改动都要全量重写，
// 节点多时文件可达数 MB，写入窗口不算短，遇上断电并非小概率事件。
// 而这份文件丢了就是用户的全部节点、订阅、分组都没了。
func writeFileAtomic(path string, data []byte) error {
	temporaryPath := path + ".tmp"
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}

	// Windows 上 rename 无法覆盖已存在的文件，需先删除
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}

// SaveTo 保存配置到指定文件
func (m *Manager) SaveTo(config *models.Config, filePath string) error {
	// 序列化配置
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// 写入配置文件
	return writeFileAtomic(filePath, data)
}

// LoadFrom 从指定文件加载配置
func (m *Manager) LoadFrom(filePath string) (*models.Config, error) {
	// 读取配置文件
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// 解析配置
	var config models.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// GetConfigPath 获取配置文件路径
func (m *Manager) GetConfigPath() string {
	return m.configPath
}
