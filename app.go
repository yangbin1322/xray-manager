package main

import (
	"context"
	"fmt"
	"sync"
	"xray-manager/internal/config"
	"xray-manager/internal/models"
	"xray-manager/internal/process"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 应用结构
type App struct {
	ctx              context.Context
	configManager    *config.Manager
	processManager   *process.Manager
	autostartManager *config.AutoStartManager
	config           *models.Config
	mu               sync.RWMutex
}

// NewApp 创建新应用实例
func NewApp() *App {
	return &App{
		config: &models.Config{
			AutoStart: false,
			Rules:     []models.ProxyRule{},
		},
	}
}

// startup 在应用启动时调用
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 初始化配置管理器
	configManager, err := config.NewManager()
	if err != nil {
		a.logError("初始化配置管理器失败", err)
		return
	}
	a.configManager = configManager

	// 初始化进程管理器
	a.processManager = process.NewManager(func(message string) {
		// 日志回调函数
		runtime.EventsEmit(ctx, "log", message)
	})

	// 初始化开机自启管理器
	autostartManager, err := config.NewAutoStartManager("XrayManager")
	if err != nil {
		a.logError("初始化开机自启管理器失败", err)
	} else {
		a.autostartManager = autostartManager
	}

	// 加载配置
	loadedConfig, err := a.configManager.Load()
	if err != nil {
		a.logError("加载配置失败", err)
	} else {
		a.config = loadedConfig
		a.log("配置加载成功")

		// 自动启动已启用的规则
		for i := range a.config.Rules {
			rule := &a.config.Rules[i]
			if rule.Enabled {
				a.log(fmt.Sprintf("自动启动规则: %s", rule.Alias))
				if err := a.processManager.Start(rule); err != nil {
					a.logError(fmt.Sprintf("启动规则 %s 失败", rule.Alias), err)
					rule.Enabled = false
				}
			}
		}
	}

	a.log("Xray 管理器已启动")
}

// shutdown 在应用关闭时调用
func (a *App) shutdown(ctx context.Context) {
	a.log("正在停止所有进程...")
	a.processManager.StopAll()

	// 保存配置
	if err := a.saveConfig(); err != nil {
		a.logError("保存配置失败", err)
	}

	a.log("Xray 管理器已关闭")
}

// GetRules 获取所有规则
func (a *App) GetRules() []models.ProxyRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Rules
}

// AddRule 添加规则
func (a *App) AddRule(rule models.ProxyRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 生成唯一 ID
	rule.ID = fmt.Sprintf("rule_%d", len(a.config.Rules)+1)
	rule.Enabled = false
	rule.ProcessID = 0
	rule.RealIP = ""

	a.config.Rules = append(a.config.Rules, rule)

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("添加规则: %s", rule.Alias))
	return nil
}

// UpdateRule 更新规则
func (a *App) UpdateRule(id string, updatedRule models.ProxyRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, rule := range a.config.Rules {
		if rule.ID == id {
			// 保留原有状态
			updatedRule.ID = rule.ID
			updatedRule.Enabled = rule.Enabled
			updatedRule.ProcessID = rule.ProcessID
			updatedRule.RealIP = rule.RealIP

			a.config.Rules[i] = updatedRule

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("更新规则: %s", updatedRule.Alias))
			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// DeleteRule 删除规则
func (a *App) DeleteRule(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, rule := range a.config.Rules {
		if rule.ID == id {
			// 如果规则正在运行，先停止
			if rule.Enabled {
				if err := a.processManager.Stop(rule.LocalPort); err != nil {
					return err
				}
			}

			// 删除规则
			a.config.Rules = append(a.config.Rules[:i], a.config.Rules[i+1:]...)

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("删除规则: %s", rule.Alias))
			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// StartRule 启动规则
func (a *App) StartRule(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.ID == id {
			if rule.Enabled {
				return fmt.Errorf("规则 %s 已经在运行", rule.Alias)
			}

			if err := a.processManager.Start(rule); err != nil {
				return err
			}

			rule.Enabled = true

			if err := a.saveConfig(); err != nil {
				return err
			}

			// 发送规则更新事件
			runtime.EventsEmit(a.ctx, "ruleUpdated", rule)

			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// StopRule 停止规则
func (a *App) StopRule(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.ID == id {
			if !rule.Enabled {
				return fmt.Errorf("规则 %s 未运行", rule.Alias)
			}

			if err := a.processManager.Stop(rule.LocalPort); err != nil {
				return err
			}

			rule.Enabled = false

			if err := a.saveConfig(); err != nil {
				return err
			}

			// 发送规则更新事件
			runtime.EventsEmit(a.ctx, "ruleUpdated", rule)

			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// SetAutoStart 设置开机自启
func (a *App) SetAutoStart(enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.config.AutoStart = enabled

	if a.autostartManager != nil {
		if enabled {
			if err := a.autostartManager.Enable(); err != nil {
				return err
			}
			a.log("开机自启已启用")
		} else {
			if err := a.autostartManager.Disable(); err != nil {
				return err
			}
			a.log("开机自启已禁用")
		}
	}

	return a.saveConfig()
}

// GetAutoStart 获取开机自启状态
func (a *App) GetAutoStart() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.AutoStart
}

// saveConfig 保存配置（内部方法，不加锁）
func (a *App) saveConfig() error {
	if a.configManager != nil {
		return a.configManager.Save(a.config)
	}
	return nil
}

// log 输出日志
func (a *App) log(message string) {
	runtime.EventsEmit(a.ctx, "log", fmt.Sprintf("[系统] %s", message))
}

// ExportConfig 导出配置
func (a *App) ExportConfig() (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// 选择保存路径
	filePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出配置",
		DefaultFilename: "xray-config-export.json",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})

	if err != nil {
		return "", fmt.Errorf("选择文件失败: %v", err)
	}

	if filePath == "" {
		return "", fmt.Errorf("用户取消操作")
	}

	// 导出配置到文件
	if err := a.configManager.SaveTo(a.config, filePath); err != nil {
		return "", fmt.Errorf("导出配置失败: %v", err)
	}

	a.log(fmt.Sprintf("配置已导出到: %s", filePath))
	return filePath, nil
}

// ImportConfig 导入配置
func (a *App) ImportConfig() error {
	// 选择导入文件
	filePath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "导入配置",
		Filters: []runtime.FileFilter{
			{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	})

	if err != nil {
		return fmt.Errorf("选择文件失败: %v", err)
	}

	if filePath == "" {
		return fmt.Errorf("用户取消操作")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// 加载配置文件
	importedConfig, err := a.configManager.LoadFrom(filePath)
	if err != nil {
		return fmt.Errorf("导入配置失败: %v", err)
	}

	// 合并规则（追加到现有规则）
	existingPortMap := make(map[int]bool)
	for _, rule := range a.config.Rules {
		existingPortMap[rule.LocalPort] = true
	}

	importedCount := 0
	skippedCount := 0

	for _, rule := range importedConfig.Rules {
		// 检查端口是否已存在
		if existingPortMap[rule.LocalPort] {
			a.log(fmt.Sprintf("[警告] 跳过规则 %s (端口 %d 已被占用)", rule.Alias, rule.LocalPort))
			skippedCount++
			continue
		}

		// 生成新的 ID
		rule.ID = fmt.Sprintf("rule_%d", len(a.config.Rules)+1)
		rule.Enabled = false
		rule.ProcessID = 0
		rule.RealIP = ""

		a.config.Rules = append(a.config.Rules, rule)
		existingPortMap[rule.LocalPort] = true
		importedCount++
	}

	// 保存合并后的配置
	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("导入完成: 成功 %d 条，跳过 %d 条", importedCount, skippedCount))
	return nil
}

// logError 输出错误日志
func (a *App) logError(message string, err error) {
	runtime.EventsEmit(a.ctx, "log", fmt.Sprintf("[错误] %s: %v", message, err))
}
