package main

import (
	"context"
	"fmt"
	"sync"
	"time"
	"xray-manager/internal/config"
	"xray-manager/internal/group"
	"xray-manager/internal/logger"
	"xray-manager/internal/models"
	"xray-manager/internal/process"
	"xray-manager/internal/speedtest"
	"xray-manager/internal/subscription"
	"xray-manager/internal/utils"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// MyService 应用结构
type MyService struct {
	app                 *application.App
	configManager       *config.Manager
	processManager      *process.Manager
	autostartManager    *config.AutoStartManager
	speedTestManager    *speedtest.Tester
	subscriptionManager *subscription.Manager
	groupManager        *group.Manager
	logFilter           *logger.Filter
	config              *models.Config
	mu                  sync.RWMutex
}

func NewMyService() *MyService {
	return &MyService{}
}

// ServiceStartup  在应用启动时调用
func (a *MyService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	a.app = application.Get()

	// 初始化日志过滤器
	a.logFilter = logger.NewFilter(1000, func(entry logger.LogEntry) {
		// 发送日志事件到前端
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "logEntry", Data: entry})
	})

	// 初始化配置管理器
	configManager, err := config.NewManager()
	if err != nil {
		a.logError("初始化配置管理器失败", err)
		return err
	}
	a.configManager = configManager

	// 初始化进程管理器
	a.processManager = process.NewManager(func(message string) {
		// 日志回调函数
		a.logFilter.AddLog(message)
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: message})
	}, a.loadRules)

	// 初始化测速管理器
	a.speedTestManager = speedtest.NewTester(func(message string) {
		a.logFilter.AddLog(message)
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: message})
	})

	// 初始化分组管理器
	a.groupManager = group.NewManager(func(message string) {
		a.logFilter.AddLog(message)
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: message})
	})

	// 初始化订阅管理器
	a.subscriptionManager = subscription.NewManager(
		func(message string) {
			a.logFilter.AddLog(message)
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: message})
		},
		a.handleSubscriptionUpdate,
	)

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

		// 初始化分组数据
		if a.config.Groups == nil {
			a.config.Groups = []models.Group{}
		}
		a.groupManager.LoadGroups(a.config.Groups)

		// 初始化订阅数据
		if a.config.Subscriptions == nil {
			a.config.Subscriptions = []models.Subscription{}
		}

		// 恢复订阅的自动更新任务
		for i := range a.config.Subscriptions {
			sub := &a.config.Subscriptions[i]
			if sub.AutoUpdate && sub.Enabled {
				a.subscriptionManager.RestartAutoUpdate(sub)
			}
		}

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

	return nil
}

// ServiceShutdown 在应用关闭时调用
func (a *MyService) ServiceShutdown() error {
	a.log("正在停止所有进程...")
	a.processManager.StopAll()

	// 停止所有订阅更新任务
	if a.subscriptionManager != nil {
		a.subscriptionManager.StopAll()
	}

	// 保存配置
	if err := a.saveConfig(); err != nil {
		a.logError("保存配置失败", err)
	}

	a.log("Xray 管理器已关闭")
	return nil
}

// GetRules 获取所有规则
func (a *MyService) GetRules() []models.ProxyRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Rules
}

// generateUniqueRuleID 生成唯一的规则ID
func generateUniqueRuleID(existingRules []models.ProxyRule) string {
	// 使用时间戳（纳秒）确保唯一性
	timestamp := time.Now().UnixNano()
	id := fmt.Sprintf("rule_%d", timestamp)

	// 双重检查：如果ID已存在（极小概率），添加随机后缀
	for {
		exists := false
		for _, rule := range existingRules {
			if rule.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
		// ID冲突，添加随机数
		id = fmt.Sprintf("rule_%d_%d", timestamp, time.Now().UnixNano()%1000)
	}

	return id
}

// AddRule 添加规则
func (a *MyService) AddRule(rule models.ProxyRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 生成唯一 ID - 使用时间戳 + 随机数确保唯一性
	rule.ID = generateUniqueRuleID(a.config.Rules)
	rule.Enabled = false
	rule.ProcessID = 0
	rule.RealIP = ""

	// 如果没有设置来源，默认为手动添加
	if rule.Source == "" {
		rule.Source = "manual"
	}

	// 根据 GroupID 设置 GroupName
	if rule.GroupID != "" {
		for _, group := range a.config.Groups {
			if group.ID == rule.GroupID {
				rule.GroupName = group.Name
				break
			}
		}
	}

	a.config.Rules = append(a.config.Rules, rule)

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("添加规则: %s", rule.Alias))
	return nil
}

// UpdateRule 更新规则
func (a *MyService) UpdateRule(id string, updatedRule models.ProxyRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, rule := range a.config.Rules {
		if rule.ID == id {
			// 保留原有状态
			updatedRule.ID = rule.ID
			updatedRule.Enabled = rule.Enabled
			updatedRule.ProcessID = rule.ProcessID
			updatedRule.RealIP = rule.RealIP

			// 保留订阅相关字段（如果是订阅节点）
			if rule.Source == "subscription" {
				updatedRule.Source = rule.Source
				updatedRule.SubscriptionURL = rule.SubscriptionURL
			} else if updatedRule.Source == "" {
				updatedRule.Source = "manual"
			}

			// 根据 GroupID 设置 GroupName
			if updatedRule.GroupID != "" {
				for _, group := range a.config.Groups {
					if group.ID == updatedRule.GroupID {
						updatedRule.GroupName = group.Name
						break
					}
				}
			} else {
				updatedRule.GroupName = ""
			}

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
func (a *MyService) DeleteRule(id string) error {
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
func (a *MyService) StartRule(id string) error {
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
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: rule})

			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// StopRule 停止规则
func (a *MyService) StopRule(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.ID == id {
			if !rule.Enabled {
				return fmt.Errorf("规则 %s 未运行", rule.Alias)
			}

			if err := a.processManager.Stop(rule.LocalPort); err != nil {
				rule.Enabled = false
				return err
			}

			rule.Enabled = false

			if err := a.saveConfig(); err != nil {
				return err
			}

			// 发送规则更新事件
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: rule})

			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// SetAutoStart 设置开机自启
func (a *MyService) SetAutoStart(enabled bool) error {
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
func (a *MyService) GetAutoStart() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.AutoStart
}

// saveConfig 保存配置（内部方法，不加锁）
func (a *MyService) saveConfig() error {
	if a.configManager != nil {
		return a.configManager.Save(a.config)
	}
	return nil
}

// log 输出日志
func (a *MyService) log(message string) {
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: fmt.Sprintf("[系统] %s", message)})
}

// ExportConfig 导出配置
func (a *MyService) ExportConfig() (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// 选择保存路径
	filePath, err := a.app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    "导出配置",
		Filename: "xray-config-export.json",
		Filters: []application.FileFilter{
			{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	}).PromptForSingleSelection()

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
func (a *MyService) ImportConfig() error {
	// 选择导入文件
	filePath, err := a.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title: "导出配置",
		Filters: []application.FileFilter{
			{DisplayName: "JSON Files (*.json)", Pattern: "*.json"},
			{DisplayName: "All Files (*.*)", Pattern: "*.*"},
		},
	}).PromptForSingleSelection()
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
		// 生成新的唯一 ID
		rule.ID = generateUniqueRuleID(a.config.Rules)
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
func (a *MyService) logError(message string, err error) {
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: fmt.Sprintf("[错误] %s: %v", message, err)})

}

// 触发重新加载规则事件
func (a *MyService) loadRules() {
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	return
}

// ==================== 端口检测相关 API ====================

// CheckPortAvailable 检查端口是否可用
func (a *MyService) CheckPortAvailable(port int) bool {
	return utils.CheckPortAvailable(port)
}

// RecommendPort 推荐可用端口
func (a *MyService) RecommendPort() int {
	return utils.RecommendPort()
}

// ==================== 测速相关 API ====================

// TestRuleSpeed 测试单个规则的速度
func (a *MyService) TestRuleSpeed(ruleID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 查找规则
	var targetRule *models.ProxyRule
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == ruleID {
			targetRule = &a.config.Rules[i]
			break
		}
	}

	if targetRule == nil {
		return fmt.Errorf("规则 %s 不存在", ruleID)
	}

	// 更新测速状态
	targetRule.TestStatus = "testing"
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: targetRule})

	// 异步执行测速
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		result := a.speedTestManager.TestRule(ctx, targetRule)

		a.mu.Lock()
		defer a.mu.Unlock()

		// 更新规则数据
		if result.Success {
			targetRule.Latency = result.Latency
			targetRule.DownloadSpeed = result.DownloadSpeed
			targetRule.TestStatus = "success"
		} else {
			targetRule.TestStatus = "failed"
		}
		targetRule.LastTestTime = result.Timestamp

		// 保存配置
		_ = a.saveConfig()

		// 发送更新事件
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: targetRule})
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "speedTestResult", Data: result})
	}()

	return nil
}

// TestAllRulesSpeed 测试所有规则的速度
func (a *MyService) TestAllRulesSpeed() error {
	a.mu.Lock()

	// 收集所有规则
	rules := make([]*models.ProxyRule, 0, len(a.config.Rules))
	for i := range a.config.Rules {
		rules = append(rules, &a.config.Rules[i])
		a.config.Rules[i].TestStatus = "testing"
	}
	a.mu.Unlock()

	a.log(fmt.Sprintf("开始批量测速，共 %d 个节点", len(rules)))

	// 异步执行批量测速
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		results := a.speedTestManager.TestRules(ctx, rules, 3)

		a.mu.Lock()
		defer a.mu.Unlock()

		// 更新所有规则数据
		for _, result := range results {
			for i := range a.config.Rules {
				if a.config.Rules[i].ID == result.RuleID {
					if result.Success {
						a.config.Rules[i].Latency = result.Latency
						a.config.Rules[i].DownloadSpeed = result.DownloadSpeed
						a.config.Rules[i].TestStatus = "success"
					} else {
						a.config.Rules[i].TestStatus = "failed"
					}
					a.config.Rules[i].LastTestTime = result.Timestamp

					// 发送更新事件
					a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: &a.config.Rules[i]})
					break
				}
			}
		}

		// 保存配置
		_ = a.saveConfig()

		a.log("批量测速完成")
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "allSpeedTestComplete"})
	}()

	return nil
}

// ==================== 订阅相关 API ====================

// AddSubscription 添加订阅
func (a *MyService) AddSubscription(name, url string, autoUpdate bool, updateInterval int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 创建订阅对象
	sub := &models.Subscription{
		ID:             fmt.Sprintf("sub_%d", time.Now().UnixNano()),
		Name:           name,
		URL:            url,
		Enabled:        true,
		AutoUpdate:     autoUpdate,
		UpdateInterval: updateInterval,
	}

	// 为订阅创建分组
	group, err := a.groupManager.CreateGroupForSubscription(name, sub.ID)
	if err != nil {
		return fmt.Errorf("创建分组失败: %v", err)
	}
	sub.GroupID = group.ID

	// 添加订阅并获取节点
	rules, err := a.subscriptionManager.AddSubscription(sub)
	if err != nil {
		return err
	}

	// 添加节点到配置
	for i := range rules {
		rules[i].ID = generateUniqueRuleID(a.config.Rules)
		rules[i].Enabled = false
		rules[i].ProcessID = 0
		rules[i].GroupID = group.ID
		rules[i].GroupName = group.Name
		rules[i].SubscriptionURL = url
		rules[i].Source = "subscription"
		rules[i].LocalPort = utils.FindAvailablePort(10800 + len(a.config.Rules))

		a.config.Rules = append(a.config.Rules, rules[i])
	}

	// 保存订阅和分组
	a.config.Subscriptions = append(a.config.Subscriptions, *sub)
	a.config.Groups = append(a.config.Groups, *group)

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("订阅添加成功: %s，导入 %d 个节点", name, len(rules)))
	return nil
}

// UpdateSubscriptionByID 更新指定订阅
func (a *MyService) UpdateSubscriptionByID(subID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 查找订阅
	var targetSub *models.Subscription
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			targetSub = &a.config.Subscriptions[i]
			break
		}
	}

	if targetSub == nil {
		return fmt.Errorf("订阅 %s 不存在", subID)
	}

	// 更新订阅
	rules, err := a.subscriptionManager.UpdateSubscription(targetSub)
	if err != nil {
		return err
	}

	a.log(fmt.Sprintf("订阅更新完成: %s，节点数: %d", targetSub.Name, len(rules)))
	return a.saveConfig()
}

// GetSubscriptions 获取所有订阅
func (a *MyService) GetSubscriptions() []models.Subscription {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.Subscriptions
}

// DeleteSubscription 删除订阅
func (a *MyService) DeleteSubscription(subID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 查找订阅
	subIndex := -1
	var groupID string
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			subIndex = i
			groupID = a.config.Subscriptions[i].GroupID
			break
		}
	}

	if subIndex == -1 {
		return fmt.Errorf("订阅不存在")
	}

	// 删除该订阅的所有节点
	newRules := make([]models.ProxyRule, 0)
	for _, rule := range a.config.Rules {
		if rule.GroupID != groupID {
			newRules = append(newRules, rule)
		} else if rule.Enabled {
			// 停止正在运行的节点
			_ = a.processManager.Stop(rule.LocalPort)
		}
	}
	a.config.Rules = newRules

	// 删除订阅
	a.config.Subscriptions = append(
		a.config.Subscriptions[:subIndex],
		a.config.Subscriptions[subIndex+1:]...,
	)

	// 删除分组
	_ = a.groupManager.DeleteGroup(groupID)
	newGroups := make([]models.Group, 0)
	for _, group := range a.config.Groups {
		if group.ID != groupID {
			newGroups = append(newGroups, group)
		}
	}
	a.config.Groups = newGroups

	a.subscriptionManager.RemoveSubscription(subID)

	return a.saveConfig()
}

// handleSubscriptionUpdate 处理订阅更新
func (a *MyService) handleSubscriptionUpdate(subID string, newRules []models.ProxyRule) error {
	// 这个方法在订阅自动更新时被调用
	// 需要智能合并新旧节点

	a.mu.Lock()
	defer a.mu.Unlock()

	// 查找订阅
	var targetSub *models.Subscription
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			targetSub = &a.config.Subscriptions[i]
			break
		}
	}

	if targetSub == nil {
		return fmt.Errorf("订阅不存在")
	}

	// 获取该订阅的现有节点
	oldRules := make(map[string]*models.ProxyRule)
	for i := range a.config.Rules {
		if a.config.Rules[i].GroupID == targetSub.GroupID {
			key := fmt.Sprintf("%s:%d", a.config.Rules[i].ServerAddr, a.config.Rules[i].ServerPort)
			oldRules[key] = &a.config.Rules[i]
		}
	}

	// 合并新节点
	for i := range newRules {
		key := fmt.Sprintf("%s:%d", newRules[i].ServerAddr, newRules[i].ServerPort)

		if existingRule, exists := oldRules[key]; exists {
			// 节点已存在，更新配置但保留状态
			existingRule.Alias = newRules[i].Alias
			existingRule.Settings = newRules[i].Settings
			delete(oldRules, key)
		} else {
			// 新节点，添加到配置
			newRules[i].ID = generateUniqueRuleID(a.config.Rules)
			newRules[i].Enabled = false
			newRules[i].GroupID = targetSub.GroupID
			newRules[i].SubscriptionURL = targetSub.URL
			newRules[i].Source = "subscription"
			newRules[i].LocalPort = utils.FindAvailablePort(10800 + len(a.config.Rules))

			a.config.Rules = append(a.config.Rules, newRules[i])
		}
	}

	// 删除不再存在的节点
	for _, oldRule := range oldRules {
		if oldRule.Enabled {
			_ = a.processManager.Stop(oldRule.LocalPort)
		}

		// 从配置中删除
		for i := range a.config.Rules {
			if a.config.Rules[i].ID == oldRule.ID {
				a.config.Rules = append(a.config.Rules[:i], a.config.Rules[i+1:]...)
				break
			}
		}
	}

	return a.saveConfig()
}

// ==================== 分组相关 API ====================

// CreateGroup 创建分组
func (a *MyService) CreateGroup(name, description string) error {
	group, err := a.groupManager.CreateGroup(name, description, "manual")
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.config.Groups = append(a.config.Groups, *group)
	return a.saveConfig()
}

// GetGroups 获取所有分组
func (a *MyService) GetGroups() []models.Group {
	return a.groupManager.GetAllGroups()
}

// DeleteGroup 删除分组
func (a *MyService) DeleteGroup(groupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 检查是否有规则使用该分组
	for _, rule := range a.config.Rules {
		if rule.GroupID == groupID {
			return fmt.Errorf("该分组下还有节点，无法删除")
		}
	}

	if err := a.groupManager.DeleteGroup(groupID); err != nil {
		return err
	}

	// 从配置中删除
	for i := range a.config.Groups {
		if a.config.Groups[i].ID == groupID {
			a.config.Groups = append(a.config.Groups[:i], a.config.Groups[i+1:]...)
			break
		}
	}

	return a.saveConfig()
}

// StartAllRulesInGroup 启动分组中的所有规则
func (a *MyService) StartAllRulesInGroup(groupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	count := 0
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.GroupID == groupID && !rule.Enabled {
			if err := a.processManager.Start(rule); err != nil {
				a.logError(fmt.Sprintf("启动规则 %s 失败", rule.Alias), err)
				continue
			}
			rule.Enabled = true
			count++
		}
	}

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("已启动分组中的 %d 个规则", count))
	return nil
}

// StopAllRulesInGroup 停止分组中的所有规则
func (a *MyService) StopAllRulesInGroup(groupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	count := 0
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.GroupID == groupID && rule.Enabled {
			if err := a.processManager.Stop(rule.LocalPort); err != nil {
				a.logError(fmt.Sprintf("停止规则 %s 失败", rule.Alias), err)
				continue
			}
			rule.Enabled = false
			count++
		}
	}

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("已停止分组中的 %d 个规则", count))
	return nil
}

// ==================== 日志相关 API ====================

// GetLogs 获取所有日志
func (a *MyService) GetLogs() []logger.LogEntry {
	if a.logFilter == nil {
		return []logger.LogEntry{}
	}
	return a.logFilter.GetLogs()
}

// SearchLogs 搜索日志
func (a *MyService) SearchLogs(keyword string, level string) []logger.LogEntry {
	if a.logFilter == nil {
		return []logger.LogEntry{}
	}
	return a.logFilter.SearchLogs(keyword, logger.LogLevel(level))
}

// FilterLogsByLevel 按级别过滤日志
func (a *MyService) FilterLogsByLevel(level string) []logger.LogEntry {
	if a.logFilter == nil {
		return []logger.LogEntry{}
	}
	return a.logFilter.FilterByLevel(logger.LogLevel(level))
}

// ClearLogs 清空日志
func (a *MyService) ClearLogs() {
	if a.logFilter != nil {
		a.logFilter.Clear()
	}
}
