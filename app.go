package main

import (
	"context"
	"fmt"
	"gost-manager/internal/config"
	"gost-manager/internal/models"
	"gost-manager/internal/process"
	"sync"

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
			Rules:     []models.ForwardRule{},
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
	autostartManager, err := config.NewAutoStartManager("GostManager")
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

	a.log("Gost 管理器已启动")
}

// shutdown 在应用关闭时调用
func (a *App) shutdown(ctx context.Context) {
	a.log("正在停止所有进程...")
	a.processManager.StopAll()

	// 保存配置
	if err := a.saveConfig(); err != nil {
		a.logError("保存配置失败", err)
	}

	a.log("Gost 管理器已关闭")
}

// GetRules 获取所有规则
func (a *App) GetRules() []models.ForwardRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Rules
}

// AddRule 添加规则
func (a *App) AddRule(rule models.ForwardRule) error {
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
func (a *App) UpdateRule(id string, updatedRule models.ForwardRule) error {
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

// logError 输出错误日志
func (a *App) logError(message string, err error) {
	runtime.EventsEmit(a.ctx, "log", fmt.Sprintf("[错误] %s: %v", message, err))
}
