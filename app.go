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
	"xray-manager/internal/parser"
	"xray-manager/internal/process"
	"xray-manager/internal/speedtest"
	"xray-manager/internal/subscription"
	"xray-manager/internal/utils"
	"xray-manager/internal/xray"

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
	sysProxyManager     *config.SysProxyManager
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

	// 初始化系统代理管理器
	a.sysProxyManager = config.NewSysProxyManager()

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
		if a.config.LoadBalancers == nil {
			a.config.LoadBalancers = []models.LoadBalanceNode{}
		}
		if a.config.ChainProxies == nil {
			a.config.ChainProxies = []models.ChainProxy{}
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

	// 关闭系统代理
	if a.sysProxyManager != nil && a.sysProxyManager.IsEnabled() {
		_ = a.sysProxyManager.DisableSystemProxy()
	}

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

	filePath, err := a.app.Dialog.OpenFile().PromptForSingleSelection()
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

	// 建立分组ID映射（旧ID -> 新ID）
	groupIDMap := make(map[string]string)
	importedGroupsCount := 0

	// 先导入分组
	for _, group := range importedConfig.Groups {
		// 检查分组名称是否已存在
		exists := false
		for _, existingGroup := range a.config.Groups {
			if existingGroup.Name == group.Name && existingGroup.Source == group.Source {
				// 分组已存在，使用现有分组ID
				groupIDMap[group.ID] = existingGroup.ID
				exists = true
				break
			}
		}
		fmt.Println(exists, group.ID, group.Source, group.Name)

		if !exists {
			// 分组不存在，创建新分组
			oldID := group.ID
			group.ID = fmt.Sprintf("group_%d", time.Now().UnixNano())
			group.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
			groupIDMap[oldID] = group.ID
			a.config.Groups = append(a.config.Groups, group)
			importedGroupsCount++
		}
	}

	// 建立订阅ID映射（旧ID -> 新ID）
	subscriptionIDMap := make(map[string]string)
	importedSubscriptionsCount := 0

	// 再导入订阅
	for _, sub := range importedConfig.Subscriptions {
		// 更新订阅的分组ID
		if newGroupID, ok := groupIDMap[sub.GroupID]; ok {
			sub.GroupID = newGroupID
		}

		// 检查订阅URL是否已存在
		exists := false
		for _, existingSub := range a.config.Subscriptions {
			if existingSub.URL == sub.URL {
				// 订阅已存在，使用现有订阅ID
				subscriptionIDMap[sub.ID] = existingSub.ID
				exists = true
				break
			}
		}

		if !exists {
			// 订阅不存在，创建新订阅
			oldID := sub.ID
			sub.ID = fmt.Sprintf("sub_%d", time.Now().UnixNano())
			subscriptionIDMap[oldID] = sub.ID
			a.config.Subscriptions = append(a.config.Subscriptions, sub)
			importedSubscriptionsCount++
		}
	}

	// 合并规则（追加到现有规则）
	existingPortMap := make(map[int]bool)
	for _, rule := range a.config.Rules {
		existingPortMap[rule.LocalPort] = true
	}

	importedCount := 0
	for _, rule := range importedConfig.Rules {
		// 更新规则的分组ID
		if newGroupID, ok := groupIDMap[rule.GroupID]; ok {
			rule.GroupID = newGroupID
			// 更新分组名称
			for _, group := range a.config.Groups {
				if group.ID == newGroupID {
					rule.GroupName = group.Name
					break
				}
			}
		}

		// 生成新的唯一 ID
		rule.ID = generateUniqueRuleID(a.config.Rules)
		rule.Enabled = false
		rule.ProcessID = 0
		rule.RealIP = ""

		a.config.Rules = append(a.config.Rules, rule)
		existingPortMap[rule.LocalPort] = true
		importedCount++
	}

	// 同步更新分组管理器的缓存
	a.groupManager.LoadGroups(a.config.Groups)

	// 同步更新订阅管理器
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].AutoUpdate {
			a.subscriptionManager.RestartAutoUpdate(&a.config.Subscriptions[i])
		}
	}

	// 保存合并后的配置
	if err := a.saveConfig(); err != nil {
		return err
	}
	a.log(fmt.Sprintf("导入完成: 分组 %d 个，订阅 %d 个，规则 %d 条", importedGroupsCount, importedSubscriptionsCount, importedCount))
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

// ==================== 规则排序 API (Feature 2) ====================

// SaveRuleOrder 保存规则排序
func (a *MyService) SaveRuleOrder(orderedIDs []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 按照传入的 ID 顺序重新排列规则
	ruleMap := make(map[string]models.ProxyRule)
	for _, rule := range a.config.Rules {
		ruleMap[rule.ID] = rule
	}

	newRules := make([]models.ProxyRule, 0, len(a.config.Rules))
	usedIDs := make(map[string]bool)

	for _, id := range orderedIDs {
		if rule, ok := ruleMap[id]; ok {
			newRules = append(newRules, rule)
			usedIDs[id] = true
		}
	}

	// 添加不在排序列表中的规则（保持原有位置）
	for _, rule := range a.config.Rules {
		if !usedIDs[rule.ID] {
			newRules = append(newRules, rule)
		}
	}

	a.config.Rules = newRules

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log("规则排序已保存")
	return nil
}

// ==================== 批量导入 API (Feature 4) ====================

// ImportShareLinks 批量导入分享链接
func (a *MyService) ImportShareLinks(text string) (int, error) {
	p := parser.NewShareLinkParser()
	rules, errors := p.ParseMultipleLinks(text)

	// 记录解析错误
	for _, errMsg := range errors {
		a.log(fmt.Sprintf("[导入] %s", errMsg))
	}

	if len(rules) == 0 {
		return 0, fmt.Errorf("未解析到有效的代理链接")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	count := 0
	for i := range rules {
		rules[i].ID = generateUniqueRuleID(a.config.Rules)
		rules[i].Enabled = false
		rules[i].ProcessID = 0
		rules[i].Source = "manual"
		rules[i].LocalPort = utils.FindAvailablePort(10800 + len(a.config.Rules))
		a.config.Rules = append(a.config.Rules, rules[i])
		count++
	}

	if err := a.saveConfig(); err != nil {
		return count, err
	}

	a.log(fmt.Sprintf("[导入] 批量导入完成，成功 %d 个节点，失败 %d 个", count, len(errors)))
	return count, nil
}

// ==================== 系统代理 API (Feature 5) ====================

// EnableSystemProxy 设置系统代理（支持普通节点、链式代理、负载均衡）
func (a *MyService) EnableSystemProxy(ruleID string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// 先查找普通规则
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == ruleID {
			rule := &a.config.Rules[i]
			if !rule.Enabled {
				return fmt.Errorf("请先启动节点再设置为系统代理")
			}
			if err := a.sysProxyManager.EnableSystemProxy(rule.LocalPort); err != nil {
				return fmt.Errorf("设置系统代理失败: %v", err)
			}
			a.log(fmt.Sprintf("[系统代理] 已设置 %s (端口:%d) 为系统代理", rule.Alias, rule.LocalPort))
			return nil
		}
	}

	// 查找链式代理
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == ruleID {
			chain := &a.config.ChainProxies[i]
			if !chain.Enabled {
				return fmt.Errorf("请先启动链式代理再设置为系统代理")
			}
			if err := a.sysProxyManager.EnableSystemProxy(chain.LocalPort); err != nil {
				return fmt.Errorf("设置系统代理失败: %v", err)
			}
			a.log(fmt.Sprintf("[系统代理] 已设置链式代理 %s (端口:%d) 为系统代理", chain.Alias, chain.LocalPort))
			return nil
		}
	}

	// 查找负载均衡
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == ruleID {
			lb := &a.config.LoadBalancers[i]
			if !lb.Enabled {
				return fmt.Errorf("请先启动负载均衡再设置为系统代理")
			}
			if err := a.sysProxyManager.EnableSystemProxy(lb.LocalPort); err != nil {
				return fmt.Errorf("设置系统代理失败: %v", err)
			}
			a.log(fmt.Sprintf("[系统代理] 已设置负载均衡 %s (端口:%d) 为系统代理", lb.Alias, lb.LocalPort))
			return nil
		}
	}

	return fmt.Errorf("节点 %s 不存在", ruleID)
}

// DisableSystemProxy 取消系统代理
func (a *MyService) DisableSystemProxy() error {
	if err := a.sysProxyManager.DisableSystemProxy(); err != nil {
		return fmt.Errorf("取消系统代理失败: %v", err)
	}

	a.log("[系统代理] 已取消系统代理")
	return nil
}

// GetSystemProxyStatus 获取系统代理状态
func (a *MyService) GetSystemProxyStatus() bool {
	return a.sysProxyManager.IsEnabled()
}

// ==================== 选中节点测速 API (Feature 6) ====================

// TestSelectedRulesSpeed 测试选中的规则速度
func (a *MyService) TestSelectedRulesSpeed(ruleIDs []string) error {
	a.mu.Lock()

	// 收集选中的规则
	rules := make([]*models.ProxyRule, 0)
	idSet := make(map[string]bool)
	for _, id := range ruleIDs {
		idSet[id] = true
	}

	for i := range a.config.Rules {
		if idSet[a.config.Rules[i].ID] {
			a.config.Rules[i].TestStatus = "testing"
			rules = append(rules, &a.config.Rules[i])
		}
	}
	a.mu.Unlock()

	if len(rules) == 0 {
		return fmt.Errorf("未找到选中的节点")
	}

	a.log(fmt.Sprintf("开始测速选中的 %d 个节点", len(rules)))

	// 异步执行批量测速
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		results := a.speedTestManager.TestRules(ctx, rules, 3)

		a.mu.Lock()
		defer a.mu.Unlock()

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
					a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: &a.config.Rules[i]})
					break
				}
			}
		}

		_ = a.saveConfig()
		a.log("选中节点测速完成")
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "allSpeedTestComplete"})
	}()

	return nil
}

// ==================== 负载均衡 API (Feature 7) ====================

// GetLoadBalancers 获取所有负载均衡节点
func (a *MyService) GetLoadBalancers() []models.LoadBalanceNode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.LoadBalancers
}

// AddLoadBalancer 添加负载均衡节点
func (a *MyService) AddLoadBalancer(lb models.LoadBalanceNode) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	lb.ID = fmt.Sprintf("lb_%d", time.Now().UnixNano())
	lb.Enabled = false
	lb.ProcessID = 0

	if len(lb.NodeIDs) == 0 {
		return fmt.Errorf("负载均衡节点需要至少一个子节点")
	}

	// 映射分组名称
	if lb.GroupID != "" {
		for _, g := range a.config.Groups {
			if g.ID == lb.GroupID {
				lb.GroupName = g.Name
				break
			}
		}
	}

	a.config.LoadBalancers = append(a.config.LoadBalancers, lb)

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("添加负载均衡节点: %s", lb.Alias))
	return nil
}

// DeleteLoadBalancer 删除负载均衡节点
func (a *MyService) DeleteLoadBalancer(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, lb := range a.config.LoadBalancers {
		if lb.ID == id {
			if lb.Enabled {
				_ = a.processManager.Stop(lb.LocalPort)
			}
			a.config.LoadBalancers = append(a.config.LoadBalancers[:i], a.config.LoadBalancers[i+1:]...)
			return a.saveConfig()
		}
	}

	return fmt.Errorf("负载均衡节点不存在")
}

// UpdateLoadBalancer 更新负载均衡节点
func (a *MyService) UpdateLoadBalancer(lb models.LoadBalanceNode) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == lb.ID {
			wasEnabled := a.config.LoadBalancers[i].Enabled

			// 如果正在运行且端口或节点变了，需要先停止
			if wasEnabled {
				_ = a.processManager.Stop(a.config.LoadBalancers[i].LocalPort)
				lb.Enabled = false
				lb.ProcessID = 0
			} else {
				lb.Enabled = false
				lb.ProcessID = 0
			}

			// 映射分组名称
			if lb.GroupID != "" {
				for _, g := range a.config.Groups {
					if g.ID == lb.GroupID {
						lb.GroupName = g.Name
						break
					}
				}
			}

			if len(lb.NodeIDs) == 0 {
				return fmt.Errorf("负载均衡节点需要至少一个子节点")
			}

			a.config.LoadBalancers[i] = lb

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("更新负载均衡节点: %s", lb.Alias))
			return nil
		}
	}

	return fmt.Errorf("负载均衡节点不存在")
}

// StartLoadBalancer 启动负载均衡节点
func (a *MyService) StartLoadBalancer(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.ID == id {
			if lb.Enabled {
				return fmt.Errorf("负载均衡节点已在运行")
			}

			// 收集子节点
			nodes := make([]*models.ProxyRule, 0)
			for _, nodeID := range lb.NodeIDs {
				for j := range a.config.Rules {
					if a.config.Rules[j].ID == nodeID {
						nodes = append(nodes, &a.config.Rules[j])
						break
					}
				}
			}

			if len(nodes) == 0 {
				return fmt.Errorf("未找到有效的子节点")
			}

			// 构建负载均衡配置
			xrayConfig, err := xray.BuildLoadBalanceConfig(lb, nodes)
			if err != nil {
				return err
			}

			configJSON, err := xrayConfig.ToJSON()
			if err != nil {
				return err
			}

			// 创建临时规则用于启动进程
			tempRule := &models.ProxyRule{
				ID:        lb.ID,
				Alias:     lb.Alias,
				LocalType: lb.LocalType,
				LocalPort: lb.LocalPort,
				Protocol:  "vmess", // 占位，不影响实际配置
			}

			// 使用 processManager 启动
			if err := a.processManager.StartWithConfig(tempRule, configJSON); err != nil {
				return err
			}

			lb.Enabled = true
			return a.saveConfig()
		}
	}

	return fmt.Errorf("负载均衡节点不存在")
}

// StopLoadBalancer 停止负载均衡节点
func (a *MyService) StopLoadBalancer(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.ID == id {
			if !lb.Enabled {
				return fmt.Errorf("负载均衡节点未运行")
			}

			if err := a.processManager.Stop(lb.LocalPort); err != nil {
				lb.Enabled = false
				return err
			}

			lb.Enabled = false
			return a.saveConfig()
		}
	}

	return fmt.Errorf("负载均衡节点不存在")
}

// ==================== 链式代理 API (Feature 8, 9) ====================

// GetChainProxies 获取所有链式代理
func (a *MyService) GetChainProxies() []models.ChainProxy {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.ChainProxies
}

// AddChainProxy 添加链式代理
func (a *MyService) AddChainProxy(chain models.ChainProxy) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	chain.ID = fmt.Sprintf("chain_%d", time.Now().UnixNano())
	chain.Enabled = false
	chain.ProcessID = 0

	if len(chain.ChainNodes) < 2 {
		return fmt.Errorf("链式代理需要至少2个节点")
	}

	// 映射分组名称
	if chain.GroupID != "" {
		for _, g := range a.config.Groups {
			if g.ID == chain.GroupID {
				chain.GroupName = g.Name
				break
			}
		}
	}

	a.config.ChainProxies = append(a.config.ChainProxies, chain)

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("添加链式代理: %s", chain.Alias))
	return nil
}

// DeleteChainProxy 删除链式代理
func (a *MyService) DeleteChainProxy(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, chain := range a.config.ChainProxies {
		if chain.ID == id {
			if chain.Enabled {
				_ = a.processManager.Stop(chain.LocalPort)
			}
			a.config.ChainProxies = append(a.config.ChainProxies[:i], a.config.ChainProxies[i+1:]...)
			return a.saveConfig()
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// UpdateChainProxy 更新链式代理
func (a *MyService) UpdateChainProxy(chain models.ChainProxy) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == chain.ID {
			wasEnabled := a.config.ChainProxies[i].Enabled

			// 如果正在运行，需要先停止
			if wasEnabled {
				_ = a.processManager.Stop(a.config.ChainProxies[i].LocalPort)
				chain.Enabled = false
				chain.ProcessID = 0
			} else {
				chain.Enabled = false
				chain.ProcessID = 0
			}

			// 映射分组名称
			if chain.GroupID != "" {
				for _, g := range a.config.Groups {
					if g.ID == chain.GroupID {
						chain.GroupName = g.Name
						break
					}
				}
			}

			if len(chain.ChainNodes) < 2 {
				return fmt.Errorf("链式代理需要至少2个节点")
			}

			a.config.ChainProxies[i] = chain

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("更新链式代理: %s", chain.Alias))
			return nil
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// StartChainProxy 启动链式代理
func (a *MyService) StartChainProxy(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.ID == id {
			if chain.Enabled {
				return fmt.Errorf("链式代理已在运行")
			}

			// 解析链中的节点（支持负载均衡节点）
			chainRules, err := a.resolveChainNodes(chain.ChainNodes)
			if err != nil {
				return err
			}

			// 构建链式代理配置
			xrayConfig, err := xray.BuildChainConfig(chain.LocalType, chain.LocalPort, chainRules)
			if err != nil {
				return err
			}

			configJSON, err := xrayConfig.ToJSON()
			if err != nil {
				return err
			}

			// 创建临时规则用于启动进程
			tempRule := &models.ProxyRule{
				ID:        chain.ID,
				Alias:     chain.Alias,
				LocalType: chain.LocalType,
				LocalPort: chain.LocalPort,
				Protocol:  "vmess", // 占位
			}

			if err := a.processManager.StartWithConfig(tempRule, configJSON); err != nil {
				return err
			}

			chain.Enabled = true
			return a.saveConfig()
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// StopChainProxy 停止链式代理
func (a *MyService) StopChainProxy(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.ID == id {
			if !chain.Enabled {
				return fmt.Errorf("链式代理未运行")
			}

			if err := a.processManager.Stop(chain.LocalPort); err != nil {
				chain.Enabled = false
				return err
			}

			chain.Enabled = false
			return a.saveConfig()
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// resolveChainNodes 解析链中的节点（支持负载均衡节点）
func (a *MyService) resolveChainNodes(nodeIDs []string) ([]*models.ProxyRule, error) {
	var chainRules []*models.ProxyRule

	for _, nodeID := range nodeIDs {
		// 先查找是否为负载均衡节点
		isLB := false
		for _, lb := range a.config.LoadBalancers {
			if lb.ID == nodeID {
				isLB = true
				// 取负载均衡中的第一个可用节点
				for _, subNodeID := range lb.NodeIDs {
					for j := range a.config.Rules {
						if a.config.Rules[j].ID == subNodeID {
							chainRules = append(chainRules, &a.config.Rules[j])
							goto nextNode
						}
					}
				}
				return nil, fmt.Errorf("负载均衡节点 %s 中没有可用的子节点", lb.Alias)
			}
		}

		if !isLB {
			// 查找普通节点
			found := false
			for j := range a.config.Rules {
				if a.config.Rules[j].ID == nodeID {
					chainRules = append(chainRules, &a.config.Rules[j])
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("节点 %s 不存在", nodeID)
			}
		}
	nextNode:
	}

	return chainRules, nil
}
