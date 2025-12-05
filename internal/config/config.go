package config

import (
	"encoding/json"
	"gost-manager/internal/models"
	"os"
	"path/filepath"
)

const configFileName = "gost-manager-config.json"

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
			Rules:     []models.ForwardRule{},
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

	return &config, nil
}

// Save 保存配置
func (m *Manager) Save(config *models.Config) error {
	// 序列化配置
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	// 写入配置文件
	return os.WriteFile(m.configPath, data, 0644)
}

// GetConfigPath 获取配置文件路径
func (m *Manager) GetConfigPath() string {
	return m.configPath
}
