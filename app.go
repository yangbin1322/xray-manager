package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
	"xray-manager/internal/config"
	"xray-manager/internal/group"
	"xray-manager/internal/healthcheck"
	"xray-manager/internal/logger"
	"xray-manager/internal/models"
	"xray-manager/internal/parser"
	"xray-manager/internal/process"
	"xray-manager/internal/singbox"
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
	healthCheckManager  *healthcheck.Manager
	logFilter           *logger.Filter
	sysProxyManager     *config.SysProxyManager
	config              *models.Config
	mu                  sync.RWMutex

	trafficDirty bool // 流量统计有未保存的变更
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
	// 订阅更新代理解析器（支持直连/系统代理/指定节点）
	a.subscriptionManager.SetProxyResolver(a.resolveSubscriptionProxy)

	// 流量统计回调
	a.processManager.SetTrafficCallback(a.handleTraffic)

	// 初始化健康检查管理器
	a.healthCheckManager = healthcheck.NewManager(
		func(message string) {
			a.logFilter.AddLog(message)
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: message})
		},
		a.getRulesSnapshot,
		a.handleHealthCheckResult,
		func() {
			// 一轮普通节点检测完成后，顺带检测已启动的故障转移/链式代理
			a.checkProxyTargets(a.collectProxyHealthTargets(nil))
			a.mu.Lock()
			_ = a.saveConfig()
			a.mu.Unlock()
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "healthCheckComplete"})
		},
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

		// 迁移历史配置：本地代理类型统一为混合端口（同时支持 HTTP/SOCKS5）
		for i := range a.config.Rules {
			a.config.Rules[i].LocalType = "mixed"
		}
		for i := range a.config.LoadBalancers {
			a.config.LoadBalancers[i].LocalType = "mixed"
		}
		for i := range a.config.ChainProxies {
			a.config.ChainProxies[i].LocalType = "mixed"
		}

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

		// 启动时同步进程状态（修复崩溃后的状态不一致）
		a.config.Rules = a.processManager.SyncState(a.config.Rules)
		// 同步故障转移和链式代理的状态（保留启用标记以便重启）
		for i := range a.config.LoadBalancers {
			lb := &a.config.LoadBalancers[i]
			if lb.Enabled && (lb.ProcessID <= 0 || !a.processManager.IsRunning(lb.LocalPort)) {
				a.log(fmt.Sprintf("[状态同步] 故障转移 %s 进程不存在，重置进程状态（保留启用标记以便重启）", lb.Alias))
				lb.ProcessID = 0
			}
		}
		for i := range a.config.ChainProxies {
			chain := &a.config.ChainProxies[i]
			if chain.Enabled && (chain.ProcessID <= 0 || !a.processManager.IsRunning(chain.LocalPort)) {
				a.log(fmt.Sprintf("[状态同步] 链式代理 %s 进程不存在，重置进程状态（保留启用标记以便重启）", chain.Alias))
				chain.ProcessID = 0
			}
		}
		_ = a.saveConfig()

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

		// 自动启动已启用的故障转移节点
		for i := range a.config.LoadBalancers {
			lb := &a.config.LoadBalancers[i]
			if lb.Enabled {
				a.log(fmt.Sprintf("自动启动故障转移: %s", lb.Alias))
				if err := a.startLoadBalancerInternal(lb); err != nil {
					a.logError(fmt.Sprintf("启动故障转移 %s 失败", lb.Alias), err)
					lb.Enabled = false
				}
			}
		}

		// 自动启动已启用的链式代理
		for i := range a.config.ChainProxies {
			chain := &a.config.ChainProxies[i]
			if chain.Enabled {
				a.log(fmt.Sprintf("自动启动链式代理: %s", chain.Alias))
				if err := a.startChainProxyInternal(chain); err != nil {
					a.logError(fmt.Sprintf("启动链式代理 %s 失败", chain.Alias), err)
					chain.Enabled = false
				}
			}
		}

		// 保存自动启动后的状态
		_ = a.saveConfig()

		// 启动后台健康检查（按配置）
		a.healthCheckManager.Configure(a.config.HealthCheck)
	}

	// 定期保存流量统计（避免每次流量更新都写盘）
	go a.trafficSaveLoop(ctx)

	a.log("Xray 管理器已启动")

	return nil
}

// trafficSaveLoop 定期落盘流量统计
func (a *MyService) trafficSaveLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			if a.trafficDirty {
				_ = a.saveConfig()
				a.trafficDirty = false
			}
			a.mu.Unlock()
		}
	}
}

// ServiceShutdown 在应用关闭时调用
func (a *MyService) ServiceShutdown() error {
	// 停止健康检查
	if a.healthCheckManager != nil {
		a.healthCheckManager.Stop()
	}

	// 先保存配置（保留 Enabled 状态，以便下次启动时恢复）
	if err := a.saveConfig(); err != nil {
		a.logError("保存配置失败", err)
	}

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

// applyRuleUpdateLocked 将 updatedRule 的可编辑字段应用到 index 处的规则，
// 保留运行时状态（启用/进程/IP/流量/健康/时间）与订阅归属。需已持有锁。
func (a *MyService) applyRuleUpdateLocked(index int, updatedRule models.ProxyRule) {
	orig := a.config.Rules[index]

	// 保留运行时状态
	updatedRule.ID = orig.ID
	updatedRule.Enabled = orig.Enabled
	updatedRule.ProcessID = orig.ProcessID
	updatedRule.RealIP = orig.RealIP
	updatedRule.Traffic = orig.Traffic
	updatedRule.HealthStatus = orig.HealthStatus
	updatedRule.HealthLatency = orig.HealthLatency
	updatedRule.LastHealthCheck = orig.LastHealthCheck
	updatedRule.LastStartTime = orig.LastStartTime
	updatedRule.LastStopTime = orig.LastStopTime
	updatedRule.Latency = orig.Latency
	updatedRule.DownloadSpeed = orig.DownloadSpeed
	updatedRule.LastTestTime = orig.LastTestTime
	updatedRule.TestStatus = orig.TestStatus

	// 保留订阅相关字段（如果是订阅节点）
	if orig.Source == "subscription" {
		updatedRule.Source = orig.Source
		updatedRule.SubscriptionURL = orig.SubscriptionURL
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

	a.config.Rules[index] = updatedRule
}

// UpdateRule 更新规则
func (a *MyService) UpdateRule(id string, updatedRule models.ProxyRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.Rules {
		if a.config.Rules[i].ID == id {
			a.applyRuleUpdateLocked(i, updatedRule)

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("更新规则: %s", a.config.Rules[i].Alias))
			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// UpdateNodes 批量更新普通节点（只改配置，不重启进程；保留运行时状态）。
// 只保存一次配置，返回成功更新数。
func (a *MyService) UpdateNodes(updatedRules []models.ProxyRule) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 建立 ID -> 索引
	idx := make(map[string]int, len(a.config.Rules))
	for i := range a.config.Rules {
		idx[a.config.Rules[i].ID] = i
	}

	count := 0
	for _, ur := range updatedRules {
		if i, ok := idx[ur.ID]; ok {
			a.applyRuleUpdateLocked(i, ur)
			count++
		}
	}

	if err := a.saveConfig(); err != nil {
		return count, err
	}

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	a.log(fmt.Sprintf("批量更新完成，共 %d 个节点", count))
	return count, nil
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

// nodeRef 批量操作时对一个节点的引用（普通节点/故障转移/链式代理）
type nodeRef struct {
	id       string
	nodeType string // rule / lb / chain
	localPort int
	alias    string
}

// collectNodeRefs 按 ID 收集节点引用及其类型（需要已持有读锁）
func (a *MyService) collectNodeRefs(ids []string) []nodeRef {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	var refs []nodeRef
	for i := range a.config.Rules {
		if idSet[a.config.Rules[i].ID] {
			refs = append(refs, nodeRef{a.config.Rules[i].ID, "rule", a.config.Rules[i].LocalPort, a.config.Rules[i].Alias})
		}
	}
	for i := range a.config.LoadBalancers {
		if idSet[a.config.LoadBalancers[i].ID] {
			refs = append(refs, nodeRef{a.config.LoadBalancers[i].ID, "lb", a.config.LoadBalancers[i].LocalPort, a.config.LoadBalancers[i].Alias})
		}
	}
	for i := range a.config.ChainProxies {
		if idSet[a.config.ChainProxies[i].ID] {
			refs = append(refs, nodeRef{a.config.ChainProxies[i].ID, "chain", a.config.ChainProxies[i].LocalPort, a.config.ChainProxies[i].Alias})
		}
	}
	return refs
}

// StartNodes 批量启动节点（普通节点/故障转移/链式代理），并发执行、只保存一次配置。
// 相比前端逐个调用 StartRule，避免了 N 次串行的进程启动 + N 次写盘导致的卡顿。
func (a *MyService) StartNodes(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	a.mu.Lock()
	refs := a.collectNodeRefs(ids)
	a.mu.Unlock()

	// 逐个启动（持锁，因为启动需读写 config 中的节点状态）。
	// 主要收益来自：端口检测已优化为轻量探测 + 仅在最后统一保存一次配置，
	// 避免了原先前端逐个调用时每次都 saveConfig 写盘的开销。
	a.mu.Lock()
	for _, ref := range refs {
		switch ref.nodeType {
		case "rule":
			for i := range a.config.Rules {
				r := &a.config.Rules[i]
				if r.ID == ref.id && !r.Enabled {
					if err := a.processManager.Start(r); err != nil {
						a.logError(fmt.Sprintf("启动规则 %s 失败", r.Alias), err)
					} else {
						r.Enabled = true
					}
					break
				}
			}
		case "lb":
			for i := range a.config.LoadBalancers {
				lb := &a.config.LoadBalancers[i]
				if lb.ID == ref.id && !lb.Enabled {
					if err := a.startLoadBalancerInternal(lb); err != nil {
						a.logError(fmt.Sprintf("启动故障转移 %s 失败", lb.Alias), err)
					}
					break
				}
			}
		case "chain":
			for i := range a.config.ChainProxies {
				c := &a.config.ChainProxies[i]
				if c.ID == ref.id && !c.Enabled {
					if err := a.startChainProxyInternal(c); err != nil {
						a.logError(fmt.Sprintf("启动链式代理 %s 失败", c.Alias), err)
					}
					break
				}
			}
		}
	}
	err := a.saveConfig()
	a.mu.Unlock()

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	a.log(fmt.Sprintf("批量启动完成，共 %d 个节点", len(refs)))
	return err
}

// StopNodes 批量停止节点，并发执行、只保存一次配置。
func (a *MyService) StopNodes(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	a.mu.Lock()
	refs := a.collectNodeRefs(ids)
	a.mu.Unlock()

	// 停止进程是慢操作，锁外并发执行（processManager 自带锁保证进程 map 安全）
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for _, ref := range refs {
		wg.Add(1)
		sem <- struct{}{}
		go func(ref nodeRef) {
			defer wg.Done()
			defer func() { <-sem }()
			_ = a.processManager.Stop(ref.localPort)
		}(ref)
	}
	wg.Wait()

	// 统一更新状态并保存一次
	a.mu.Lock()
	refSet := make(map[string]bool, len(refs))
	for _, ref := range refs {
		refSet[ref.id] = true
	}
	for i := range a.config.Rules {
		if refSet[a.config.Rules[i].ID] {
			a.config.Rules[i].Enabled = false
			a.config.Rules[i].ProcessID = 0
			a.config.Rules[i].RealIP = ""
		}
	}
	for i := range a.config.LoadBalancers {
		if refSet[a.config.LoadBalancers[i].ID] {
			a.config.LoadBalancers[i].Enabled = false
			a.config.LoadBalancers[i].ProcessID = 0
		}
	}
	for i := range a.config.ChainProxies {
		if refSet[a.config.ChainProxies[i].ID] {
			a.config.ChainProxies[i].Enabled = false
			a.config.ChainProxies[i].ProcessID = 0
		}
	}
	err := a.saveConfig()
	a.mu.Unlock()

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	a.log(fmt.Sprintf("批量停止完成，共 %d 个节点", len(refs)))
	return err
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
				// 已经停止，不报错
				return nil
			}

			// Stop 方法已做容错处理，进程不存在不会报错
			if err := a.processManager.Stop(rule.LocalPort); err != nil {
				a.logError(fmt.Sprintf("停止规则 %s 时出现警告", rule.Alias), err)
			}

			rule.Enabled = false
			rule.ProcessID = 0
			rule.RealIP = ""

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

// GetAutoStart 获取开机自启状态（从系统配置读取）
func (a *MyService) GetAutoStart() bool {
	if a.autostartManager != nil {
		return a.autostartManager.IsEnabled()
	}
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

// ExportConfig 导出配置（标准格式，包含版本信息）
// ruleIds 为空时导出全部规则，非空时仅导出选中的规则及其关联项：
//   - 仅当链式代理/故障转移的全部成员节点都被选中时才导出该链式代理/故障转移
//   - 仅导出被导出内容引用到的分组
//   - includeSubscriptions 控制是否导出订阅及订阅分组；不导出时订阅节点转为手动节点
func (a *MyService) ExportConfig(ruleIds []string, includeSubscriptions bool) (string, error) {
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

	exportAll := len(ruleIds) == 0

	// 选中的规则集合
	selectedIDs := make(map[string]bool, len(ruleIds))
	for _, id := range ruleIds {
		selectedIDs[id] = true
	}

	var exportRules []models.ProxyRule
	for _, r := range a.config.Rules {
		if exportAll || selectedIDs[r.ID] {
			exportRules = append(exportRules, r)
		}
	}

	exportedRuleIDs := make(map[string]bool, len(exportRules))
	for _, r := range exportRules {
		exportedRuleIDs[r.ID] = true
	}

	// 故障转移：全部成员节点都被导出时才导出
	var exportLBs []models.LoadBalanceNode
	exportedLBIDs := make(map[string]bool)
	for _, lb := range a.config.LoadBalancers {
		if !exportAll {
			if len(lb.NodeIDs) == 0 {
				continue
			}
			allIncluded := true
			for _, nodeID := range lb.NodeIDs {
				if !exportedRuleIDs[nodeID] {
					allIncluded = false
					break
				}
			}
			if !allIncluded {
				continue
			}
		}
		exportLBs = append(exportLBs, lb)
		exportedLBIDs[lb.ID] = true
	}

	// 链式代理：全部成员（节点或已导出的故障转移）都被导出时才导出
	var exportChains []models.ChainProxy
	for _, chain := range a.config.ChainProxies {
		if !exportAll {
			if len(chain.ChainNodes) == 0 {
				continue
			}
			allIncluded := true
			for _, nodeID := range chain.ChainNodes {
				if !exportedRuleIDs[nodeID] && !exportedLBIDs[nodeID] {
					allIncluded = false
					break
				}
			}
			if !allIncluded {
				continue
			}
		}
		exportChains = append(exportChains, chain)
	}

	// 收集被引用的分组
	referencedGroupIDs := make(map[string]bool)
	for _, r := range exportRules {
		if r.GroupID != "" {
			referencedGroupIDs[r.GroupID] = true
		}
	}
	for _, lb := range exportLBs {
		if lb.GroupID != "" {
			referencedGroupIDs[lb.GroupID] = true
		}
	}
	for _, chain := range exportChains {
		if chain.GroupID != "" {
			referencedGroupIDs[chain.GroupID] = true
		}
	}

	// 分组：仅导出被引用的分组；订阅分组按 includeSubscriptions 决定
	var exportGroups []models.Group
	exportedGroupIDs := make(map[string]bool)
	for _, grp := range a.config.Groups {
		if !referencedGroupIDs[grp.ID] {
			continue
		}
		if !includeSubscriptions && grp.Source == "subscription" {
			continue
		}
		exportGroups = append(exportGroups, grp)
		exportedGroupIDs[grp.ID] = true
	}

	// 订阅：仅在允许导出订阅时导出（且其分组已被导出）
	var exportSubs []models.Subscription
	if includeSubscriptions {
		for _, sub := range a.config.Subscriptions {
			if exportedGroupIDs[sub.GroupID] {
				exportSubs = append(exportSubs, sub)
			}
		}
	}

	// 清理导出数据的运行时状态；分组未导出的节点清除分组引用
	cleanRules := make([]models.ProxyRule, len(exportRules))
	copy(cleanRules, exportRules)
	for i := range cleanRules {
		cleanRules[i].Enabled = false
		cleanRules[i].ProcessID = 0
		cleanRules[i].RealIP = ""
		cleanRules[i].TestStatus = ""
		cleanRules[i].Latency = 0
		cleanRules[i].DownloadSpeed = 0
		cleanRules[i].LastTestTime = ""
		cleanRules[i].HealthStatus = ""
		cleanRules[i].HealthLatency = 0
		cleanRules[i].LastHealthCheck = ""
		cleanRules[i].Traffic = models.TrafficStats{}
		cleanRules[i].LastStartTime = ""
		cleanRules[i].LastStopTime = ""

		if !exportedGroupIDs[cleanRules[i].GroupID] {
			cleanRules[i].GroupID = ""
			cleanRules[i].GroupName = ""
		}
		// 不导出订阅时，订阅节点转为手动节点，保证导入后可独立管理
		if !includeSubscriptions && cleanRules[i].Source == "subscription" {
			cleanRules[i].Source = "manual"
			cleanRules[i].SubscriptionURL = ""
		}
	}

	cleanLBs := make([]models.LoadBalanceNode, len(exportLBs))
	copy(cleanLBs, exportLBs)
	for i := range cleanLBs {
		cleanLBs[i].ResetRuntimeState()
		if !exportedGroupIDs[cleanLBs[i].GroupID] {
			cleanLBs[i].GroupID = ""
			cleanLBs[i].GroupName = ""
		}
	}

	cleanChains := make([]models.ChainProxy, len(exportChains))
	copy(cleanChains, exportChains)
	for i := range cleanChains {
		cleanChains[i].ResetRuntimeState()
		if !exportedGroupIDs[cleanChains[i].GroupID] {
			cleanChains[i].GroupID = ""
			cleanChains[i].GroupName = ""
		}
	}

	exportData := models.ExportData{
		Version:       "1.0",
		ExportTime:    time.Now().Format("2006-01-02 15:04:05"),
		Rules:         cleanRules,
		Groups:        exportGroups,
		Subscriptions: exportSubs,
		LoadBalancers: cleanLBs,
		ChainProxies:  cleanChains,
	}

	data, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化导出数据失败: %v", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("写入导出文件失败: %v", err)
	}

	a.log(fmt.Sprintf("配置已导出到: %s（规则 %d 条，分组 %d 个，故障转移 %d 个，链式代理 %d 个，订阅 %d 个）",
		filePath, len(cleanRules), len(exportGroups), len(cleanLBs), len(cleanChains), len(exportSubs)))
	return filePath, nil
}

// ImportConfig 导入配置（支持标准导出格式和旧格式，含重复检测和校验）
func (a *MyService) ImportConfig() (*models.ImportResult, error) {
	result := &models.ImportResult{Success: true}

	// 选择导入文件
	filePath, err := a.app.Dialog.OpenFile().PromptForSingleSelection()
	if err != nil {
		return nil, fmt.Errorf("选择文件失败: %v", err)
	}
	if filePath == "" {
		return nil, fmt.Errorf("用户取消操作")
	}

	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %v", err)
	}

	// 尝试解析为新版 ExportData 格式
	var exportData models.ExportData
	var importedRules []models.ProxyRule
	var importedGroups []models.Group
	var importedSubs []models.Subscription
	var importedLBs []models.LoadBalanceNode
	var importedChains []models.ChainProxy

	if err := json.Unmarshal(data, &exportData); err == nil && exportData.Version != "" {
		// 新版格式
		importedRules = exportData.Rules
		importedGroups = exportData.Groups
		importedSubs = exportData.Subscriptions
		importedLBs = exportData.LoadBalancers
		importedChains = exportData.ChainProxies
	} else {
		// 尝试解析为旧版 Config 格式（向后兼容）
		var oldConfig models.Config
		if err := json.Unmarshal(data, &oldConfig); err != nil {
			return nil, fmt.Errorf("无法识别的文件格式: %v", err)
		}
		importedRules = oldConfig.Rules
		importedGroups = oldConfig.Groups
		importedSubs = oldConfig.Subscriptions
		importedLBs = oldConfig.LoadBalancers
		importedChains = oldConfig.ChainProxies
		result.Warnings = append(result.Warnings, "检测到旧版格式，已自动兼容")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// === 导入分组 ===
	groupIDMap := make(map[string]string) // 旧ID -> 新ID
	for _, grp := range importedGroups {
		exists := false
		for _, existing := range a.config.Groups {
			if existing.Name == grp.Name && existing.Source == grp.Source {
				groupIDMap[grp.ID] = existing.ID
				exists = true
				break
			}
		}
		if !exists {
			oldID := grp.ID
			grp.ID = fmt.Sprintf("group_%d", time.Now().UnixNano())
			grp.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
			groupIDMap[oldID] = grp.ID
			a.config.Groups = append(a.config.Groups, grp)
			result.GroupsImported++
			time.Sleep(time.Nanosecond) // 确保 ID 唯一
		}
	}

	// === 导入订阅 ===
	subIDMap := make(map[string]string)
	for _, sub := range importedSubs {
		if newGID, ok := groupIDMap[sub.GroupID]; ok {
			sub.GroupID = newGID
		}
		exists := false
		for _, existing := range a.config.Subscriptions {
			if existing.URL == sub.URL {
				subIDMap[sub.ID] = existing.ID
				exists = true
				result.Warnings = append(result.Warnings, fmt.Sprintf("订阅已存在，跳过: %s", sub.Name))
				break
			}
		}
		if !exists {
			oldID := sub.ID
			sub.ID = fmt.Sprintf("sub_%d", time.Now().UnixNano())
			subIDMap[oldID] = sub.ID
			a.config.Subscriptions = append(a.config.Subscriptions, sub)
			result.SubsImported++
			time.Sleep(time.Nanosecond)
		}
	}

	// === 导入规则（含重复检测和校验） ===
	ruleIDMap := make(map[string]string) // 旧规则ID -> 新规则ID（用于修复链式代理/故障转移的成员引用）
	for _, rule := range importedRules {
		// 校验字段合法性
		if err := rule.Validate(); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("规则校验失败 [%s]: %v", rule.Alias, err))
			continue
		}

		// 检查是否与现有规则重复
		isDuplicate := false
		for j := range a.config.Rules {
			if rule.IsDuplicateOf(&a.config.Rules[j]) {
				isDuplicate = true
				// 重复节点映射到现有规则，保证链式代理/故障转移引用有效
				ruleIDMap[rule.ID] = a.config.Rules[j].ID
				result.RulesSkipped++
				result.Warnings = append(result.Warnings, fmt.Sprintf("规则重复，跳过: %s (%s:%d)", rule.Alias, rule.ServerAddr, rule.ServerPort))
				break
			}
		}
		if isDuplicate {
			continue
		}

		// 更新分组ID映射
		if newGID, ok := groupIDMap[rule.GroupID]; ok {
			rule.GroupID = newGID
			for _, grp := range a.config.Groups {
				if grp.ID == newGID {
					rule.GroupName = grp.Name
					break
				}
			}
		} else if rule.GroupID != "" {
			// 分组未随导出文件提供，清除失效引用
			rule.GroupID = ""
			rule.GroupName = ""
		}

		// 重置运行时状态
		oldRuleID := rule.ID
		rule.ID = generateUniqueRuleID(a.config.Rules)
		ruleIDMap[oldRuleID] = rule.ID
		rule.Enabled = false
		rule.ProcessID = 0
		rule.RealIP = ""

		// 分配可用端口（如果端口已被使用）
		if rule.LocalPort <= 0 || !utils.CheckPortAvailable(rule.LocalPort) {
			rule.LocalPort = utils.FindAvailablePort(10800 + len(a.config.Rules))
		}

		a.config.Rules = append(a.config.Rules, rule)
		result.RulesImported++
	}

	// === 导入故障转移 ===
	lbIDMap := make(map[string]string) // 旧故障转移ID -> 新ID（链式代理可能引用故障转移）
	for _, lb := range importedLBs {
		oldLBID := lb.ID
		lb.ID = fmt.Sprintf("lb_%d", time.Now().UnixNano())
		lbIDMap[oldLBID] = lb.ID
		lb.ResetRuntimeState() // 清除导入文件中可能残留的运行时状态
		if newGID, ok := groupIDMap[lb.GroupID]; ok {
			lb.GroupID = newGID
		} else if lb.GroupID != "" {
			lb.GroupID = ""
			lb.GroupName = ""
		}

		// 将成员节点引用映射到导入后的新规则ID，丢弃无效引用
		newNodeIDs := make([]string, 0, len(lb.NodeIDs))
		for _, nodeID := range lb.NodeIDs {
			if newID, ok := ruleIDMap[nodeID]; ok {
				newNodeIDs = append(newNodeIDs, newID)
			} else {
				result.Warnings = append(result.Warnings, fmt.Sprintf("故障转移 %s 的成员节点未包含在导入数据中，已移除引用", lb.Alias))
			}
		}
		if len(newNodeIDs) == 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("故障转移 %s 没有有效的成员节点，跳过导入", lb.Alias))
			continue
		}
		lb.NodeIDs = newNodeIDs

		a.config.LoadBalancers = append(a.config.LoadBalancers, lb)
		result.LBImported++
		time.Sleep(time.Nanosecond)
	}

	// === 导入链式代理 ===
	for _, chain := range importedChains {
		chain.ID = fmt.Sprintf("chain_%d", time.Now().UnixNano())
		chain.ResetRuntimeState() // 清除导入文件中可能残留的运行时状态
		if newGID, ok := groupIDMap[chain.GroupID]; ok {
			chain.GroupID = newGID
		} else if chain.GroupID != "" {
			chain.GroupID = ""
			chain.GroupName = ""
		}

		// 链成员可能是普通节点或故障转移，统一映射到新ID
		newChainNodes := make([]string, 0, len(chain.ChainNodes))
		missing := false
		for _, nodeID := range chain.ChainNodes {
			if newID, ok := ruleIDMap[nodeID]; ok {
				newChainNodes = append(newChainNodes, newID)
			} else if newID, ok := lbIDMap[nodeID]; ok {
				newChainNodes = append(newChainNodes, newID)
			} else {
				missing = true
			}
		}
		// 链式代理成员顺序敏感，缺失任一成员都会改变链路语义，直接跳过
		if missing || len(newChainNodes) < 2 {
			result.Warnings = append(result.Warnings, fmt.Sprintf("链式代理 %s 的成员节点不完整，跳过导入", chain.Alias))
			continue
		}
		chain.ChainNodes = newChainNodes

		a.config.ChainProxies = append(a.config.ChainProxies, chain)
		result.ChainImported++
		time.Sleep(time.Nanosecond)
	}

	// 同步分组管理器缓存
	a.groupManager.LoadGroups(a.config.Groups)

	// 重启订阅自动更新
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].AutoUpdate {
			a.subscriptionManager.RestartAutoUpdate(&a.config.Subscriptions[i])
		}
	}

	// 保存配置
	if err := a.saveConfig(); err != nil {
		return nil, fmt.Errorf("保存配置失败: %v", err)
	}

	a.log(fmt.Sprintf("导入完成: 规则 %d 条（跳过重复 %d），分组 %d 个，订阅 %d 个，故障转移 %d 个，链式代理 %d 个",
		result.RulesImported, result.RulesSkipped, result.GroupsImported, result.SubsImported, result.LBImported, result.ChainImported))

	return result, nil
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

// TestLoadBalancerSpeed 测试故障转移节点的速度（需已启动，通过本地代理端口测试）
func (a *MyService) TestLoadBalancerSpeed(id string) error {
	a.mu.Lock()
	var target *models.LoadBalanceNode
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == id {
			target = &a.config.LoadBalancers[i]
			break
		}
	}
	if target == nil {
		a.mu.Unlock()
		return fmt.Errorf("故障转移节点不存在")
	}
	if !target.Enabled || !a.processManager.IsRunning(target.LocalPort) {
		a.mu.Unlock()
		return fmt.Errorf("请先启动故障转移节点再测速")
	}
	port := target.LocalPort
	alias := target.Alias
	target.TestStatus = "testing"
	a.mu.Unlock()

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		result := a.speedTestManager.TestProxyEndpoint(ctx, id, alias, port)

		a.mu.Lock()
		for i := range a.config.LoadBalancers {
			if a.config.LoadBalancers[i].ID == id {
				lb := &a.config.LoadBalancers[i]
				if result.Success {
					lb.Latency = result.Latency
					lb.DownloadSpeed = result.DownloadSpeed
					lb.TestStatus = "success"
				} else {
					lb.TestStatus = "failed"
				}
				lb.LastTestTime = result.Timestamp
				break
			}
		}
		_ = a.saveConfig()
		a.mu.Unlock()

		a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "speedTestResult", Data: result})
	}()

	return nil
}

// TestChainProxySpeed 测试链式代理的速度（需已启动，通过本地代理端口测试）
func (a *MyService) TestChainProxySpeed(id string) error {
	a.mu.Lock()
	var target *models.ChainProxy
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == id {
			target = &a.config.ChainProxies[i]
			break
		}
	}
	if target == nil {
		a.mu.Unlock()
		return fmt.Errorf("链式代理不存在")
	}
	if !target.Enabled || !a.processManager.IsRunning(target.LocalPort) {
		a.mu.Unlock()
		return fmt.Errorf("请先启动链式代理再测速")
	}
	port := target.LocalPort
	alias := target.Alias
	target.TestStatus = "testing"
	a.mu.Unlock()

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		result := a.speedTestManager.TestProxyEndpoint(ctx, id, alias, port)

		a.mu.Lock()
		for i := range a.config.ChainProxies {
			if a.config.ChainProxies[i].ID == id {
				chain := &a.config.ChainProxies[i]
				if result.Success {
					chain.Latency = result.Latency
					chain.DownloadSpeed = result.DownloadSpeed
					chain.TestStatus = "success"
				} else {
					chain.TestStatus = "failed"
				}
				chain.LastTestTime = result.Timestamp
				break
			}
		}
		_ = a.saveConfig()
		a.mu.Unlock()

		a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
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
// updateMode: 更新方式 direct/system/proxy；updateProxyID: 更新方式为 proxy 时使用的节点 ID
func (a *MyService) AddSubscription(name, url string, autoUpdate bool, updateInterval int, updateMode string, updateProxyID string) error {
	// 创建订阅对象
	sub := &models.Subscription{
		ID:             fmt.Sprintf("sub_%d", time.Now().UnixNano()),
		Name:           name,
		URL:            url,
		Enabled:        true,
		AutoUpdate:     autoUpdate,
		UpdateInterval: updateInterval,
		UpdateMode:     updateMode,
		UpdateProxyID:  updateProxyID,
	}

	// 为订阅创建分组
	group, err := a.groupManager.CreateGroupForSubscription(name, sub.ID)
	if err != nil {
		return fmt.Errorf("创建分组失败: %v", err)
	}
	sub.GroupID = group.ID

	// 添加订阅并获取节点。
	// 注意：此处不能持有 a.mu 锁，订阅更新代理解析器需要读取配置（可能临时启动节点）。
	rules, err := a.subscriptionManager.AddSubscription(sub)
	if err != nil {
		_ = a.groupManager.DeleteGroup(group.ID)
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

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
	// 查找订阅（取副本，避免在持锁状态下进行网络请求导致界面卡死/死锁）
	a.mu.RLock()
	var subCopy models.Subscription
	found := false
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			subCopy = a.config.Subscriptions[i]
			found = true
			break
		}
	}
	a.mu.RUnlock()

	if !found {
		return fmt.Errorf("订阅 %s 不存在", subID)
	}

	// 更新订阅（onUpdate 回调会自行加锁合并节点）
	rules, err := a.subscriptionManager.UpdateSubscription(&subCopy)
	if err != nil {
		return err
	}

	// 将更新后的订阅信息写回配置
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			a.config.Subscriptions[i] = subCopy
			break
		}
	}

	a.log(fmt.Sprintf("订阅更新完成: %s，节点数: %d", subCopy.Name, len(rules)))
	return a.saveConfig()
}

// EditSubscription 编辑订阅（名称/URL/自动更新/更新间隔/更新方式）。
// 不触发订阅内容更新，只修改元数据；改名会同步分组名和该订阅下节点的分组名，
// 改 URL 会同步节点的订阅链接，最后按新配置重设自动更新定时任务。
func (a *MyService) EditSubscription(subID, name, url string, autoUpdate bool, updateInterval int, updateMode, updateProxyID string) error {
	if name == "" {
		return fmt.Errorf("订阅名称不能为空")
	}
	if url == "" {
		return fmt.Errorf("订阅地址不能为空")
	}
	if autoUpdate && updateInterval < 1 {
		return fmt.Errorf("自动更新间隔必须大于等于 1 小时")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// 查找订阅
	idx := -1
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("订阅不存在")
	}

	sub := &a.config.Subscriptions[idx]
	oldName := sub.Name
	oldURL := sub.URL
	groupID := sub.GroupID

	// 更新订阅字段
	sub.Name = name
	sub.URL = url
	sub.AutoUpdate = autoUpdate
	sub.UpdateInterval = updateInterval
	sub.UpdateMode = updateMode
	sub.UpdateProxyID = updateProxyID

	// 改名：同步分组名称
	if name != oldName && groupID != "" {
		// 分组管理器缓存
		for _, g := range a.config.Groups {
			if g.ID == groupID {
				_ = a.groupManager.UpdateGroup(groupID, name, g.Description)
				break
			}
		}
		// 配置中的分组
		for i := range a.config.Groups {
			if a.config.Groups[i].ID == groupID {
				a.config.Groups[i].Name = name
				break
			}
		}
		// 该订阅下所有节点的分组名
		for i := range a.config.Rules {
			if a.config.Rules[i].GroupID == groupID {
				a.config.Rules[i].GroupName = name
			}
		}
	}

	// 改 URL：同步该订阅下节点的订阅链接
	if url != oldURL && groupID != "" {
		for i := range a.config.Rules {
			if a.config.Rules[i].GroupID == groupID && a.config.Rules[i].Source == "subscription" {
				a.config.Rules[i].SubscriptionURL = url
			}
		}
	}

	// 按新配置重设自动更新定时任务（传副本，避免定时协程长期持有 config 切片内部指针）
	subCopy := *sub
	a.subscriptionManager.ReconfigureAutoUpdate(&subCopy)

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("[订阅] 已编辑订阅: %s", name))
	return nil
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

// ImportShareLinks 批量导入分享链接（返回详细结果）
func (a *MyService) ImportShareLinks(text string) (*models.ImportShareResult, error) {
	p := parser.NewShareLinkParser()
	rules, parseErrors := p.ParseMultipleLinks(text)

	result := &models.ImportShareResult{
		FailCount: len(parseErrors),
		Errors:    parseErrors,
	}

	// 记录解析错误到日志
	for _, errMsg := range parseErrors {
		a.log(fmt.Sprintf("[导入] %s", errMsg))
	}

	if len(rules) == 0 && len(parseErrors) > 0 {
		return result, fmt.Errorf("未解析到有效的代理链接")
	}
	if len(rules) == 0 {
		return result, fmt.Errorf("输入内容为空")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range rules {
		// 校验
		if err := rules[i].Validate(); err != nil {
			result.FailCount++
			result.Errors = append(result.Errors, fmt.Sprintf("校验失败 [%s]: %v", rules[i].Alias, err))
			continue
		}

		rules[i].ID = generateUniqueRuleID(a.config.Rules)
		rules[i].Enabled = false
		rules[i].ProcessID = 0
		rules[i].Source = "manual"
		rules[i].LocalPort = utils.FindAvailablePort(10800 + len(a.config.Rules))
		a.config.Rules = append(a.config.Rules, rules[i])
		result.SuccessCount++
	}

	if err := a.saveConfig(); err != nil {
		return result, err
	}

	a.log(fmt.Sprintf("[导入] 批量导入完成，成功 %d 个节点，失败 %d 个", result.SuccessCount, result.FailCount))
	return result, nil
}

// ==================== 系统代理 API (Feature 5) ====================

// EnableSystemProxy 设置系统代理（支持普通节点、链式代理、故障转移）
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

	// 查找故障转移
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == ruleID {
			lb := &a.config.LoadBalancers[i]
			if !lb.Enabled {
				return fmt.Errorf("请先启动故障转移再设置为系统代理")
			}
			if err := a.sysProxyManager.EnableSystemProxy(lb.LocalPort); err != nil {
				return fmt.Errorf("设置系统代理失败: %v", err)
			}
			a.log(fmt.Sprintf("[系统代理] 已设置故障转移 %s (端口:%d) 为系统代理", lb.Alias, lb.LocalPort))
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

// TestSelectedRulesSpeed 测试选中的节点速度（普通节点直测；故障转移/链式代理经本地代理端口测，需已启动）
func (a *MyService) TestSelectedRulesSpeed(ruleIDs []string) error {
	idSet := make(map[string]bool)
	for _, id := range ruleIDs {
		idSet[id] = true
	}

	a.mu.Lock()
	// 普通节点
	rules := make([]*models.ProxyRule, 0)
	for i := range a.config.Rules {
		if idSet[a.config.Rules[i].ID] {
			a.config.Rules[i].TestStatus = "testing"
			rules = append(rules, &a.config.Rules[i])
		}
	}
	// 组合节点（仅已启动的可测）
	proxyTargets := make([]proxyHealthTarget, 0) // 复用 {id, localPort}
	proxyAlias := make(map[string]string)
	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if idSet[lb.ID] && lb.Enabled && a.processManager.IsRunning(lb.LocalPort) {
			lb.TestStatus = "testing"
			proxyTargets = append(proxyTargets, proxyHealthTarget{lb.ID, lb.LocalPort})
			proxyAlias[lb.ID] = lb.Alias
		}
	}
	for i := range a.config.ChainProxies {
		c := &a.config.ChainProxies[i]
		if idSet[c.ID] && c.Enabled && a.processManager.IsRunning(c.LocalPort) {
			c.TestStatus = "testing"
			proxyTargets = append(proxyTargets, proxyHealthTarget{c.ID, c.LocalPort})
			proxyAlias[c.ID] = c.Alias
		}
	}
	a.mu.Unlock()

	if len(rules) == 0 && len(proxyTargets) == 0 {
		return fmt.Errorf("未找到可测速的选中节点（故障转移/链式代理需先启动）")
	}

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	a.log(fmt.Sprintf("开始测速选中的 %d 个节点、%d 个组合代理", len(rules), len(proxyTargets)))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		var wg sync.WaitGroup

		// 普通节点批量测速
		if len(rules) > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results := a.speedTestManager.TestRules(ctx, rules, 3)
				a.mu.Lock()
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
							break
						}
					}
				}
				a.mu.Unlock()
			}()
		}

		// 组合节点并发测速
		for _, tg := range proxyTargets {
			wg.Add(1)
			go func(tg proxyHealthTarget) {
				defer wg.Done()
				result := a.speedTestManager.TestProxyEndpoint(ctx, tg.id, proxyAlias[tg.id], tg.localPort)
				a.mu.Lock()
				a.applySpeedResultToProxyLocked(result)
				a.mu.Unlock()
			}(tg)
		}

		wg.Wait()

		a.mu.Lock()
		_ = a.saveConfig()
		a.mu.Unlock()
		a.log("选中节点测速完成")
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "allSpeedTestComplete"})
	}()

	return nil
}

// applySpeedResultToProxyLocked 将测速结果写回故障转移/链式代理（需已持有锁）
func (a *MyService) applySpeedResultToProxyLocked(result models.SpeedTestResult) {
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == result.RuleID {
			lb := &a.config.LoadBalancers[i]
			if result.Success {
				lb.Latency = result.Latency
				lb.DownloadSpeed = result.DownloadSpeed
				lb.TestStatus = "success"
			} else {
				lb.TestStatus = "failed"
			}
			lb.LastTestTime = result.Timestamp
			return
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == result.RuleID {
			c := &a.config.ChainProxies[i]
			if result.Success {
				c.Latency = result.Latency
				c.DownloadSpeed = result.DownloadSpeed
				c.TestStatus = "success"
			} else {
				c.TestStatus = "failed"
			}
			c.LastTestTime = result.Timestamp
			return
		}
	}
}

// ==================== 故障转移 API (Feature 7) ====================

// GetLoadBalancers 获取所有故障转移节点
func (a *MyService) GetLoadBalancers() []models.LoadBalanceNode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.LoadBalancers
}

// AddLoadBalancer 添加故障转移节点
func (a *MyService) AddLoadBalancer(lb models.LoadBalanceNode) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	lb.ID = fmt.Sprintf("lb_%d", time.Now().UnixNano())
	lb.Enabled = false
	lb.ProcessID = 0

	if len(lb.NodeIDs) == 0 {
		return fmt.Errorf("故障转移节点需要至少一个子节点")
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

	a.log(fmt.Sprintf("添加故障转移节点: %s", lb.Alias))
	return nil
}

// DeleteLoadBalancer 删除故障转移节点
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

	return fmt.Errorf("故障转移节点不存在")
}

// UpdateLoadBalancer 更新故障转移节点
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
				return fmt.Errorf("故障转移节点需要至少一个子节点")
			}

			a.config.LoadBalancers[i] = lb

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("更新故障转移节点: %s", lb.Alias))
			return nil
		}
	}

	return fmt.Errorf("故障转移节点不存在")
}

// startLoadBalancerInternal 启动故障转移节点（内部方法，不加锁）
func (a *MyService) startLoadBalancerInternal(lb *models.LoadBalanceNode) error {
	// 收集子节点
	nodes := a.collectLBNodes(lb)
	if len(nodes) == 0 {
		return fmt.Errorf("未找到有效的子节点")
	}

	// 构建故障转移配置（含 Hysteria2/TUIC 子节点时自动切换 sing-box 内核），附加流量统计 API
	apiPort := process.FindApiPort(lb.LocalPort)
	configJSON, coreType, err := buildLoadBalanceConfigJSON(lb, lb.LocalPort, nodes, apiPort)
	if err != nil {
		return err
	}

	// 创建临时规则用于启动进程（ID 用 LB 的 ID，流量回调据此关联到 LB）
	tempRule := &models.ProxyRule{
		ID:        lb.ID,
		Alias:     lb.Alias,
		LocalType: lb.LocalType,
		LocalPort: lb.LocalPort,
		Protocol:  "vmess", // 占位，不影响实际配置
	}

	// 使用 processManager 启动
	if err := a.processManager.StartWithOptions(tempRule, process.StartOptions{
		ConfigJSON: configJSON,
		CoreType:   coreType,
		ApiPort:    apiPort,
	}); err != nil {
		return err
	}

	lb.Enabled = true
	lb.LastStartTime = time.Now().Format("2006-01-02 15:04:05")
	return nil
}

// StartLoadBalancer 启动故障转移节点
func (a *MyService) StartLoadBalancer(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.ID == id {
			if lb.Enabled {
				return fmt.Errorf("故障转移节点已在运行")
			}

			if err := a.startLoadBalancerInternal(lb); err != nil {
				return err
			}

			return a.saveConfig()
		}
	}

	return fmt.Errorf("故障转移节点不存在")
}

// StopLoadBalancer 停止故障转移节点
func (a *MyService) StopLoadBalancer(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.ID == id {
			if !lb.Enabled {
				return fmt.Errorf("故障转移节点未运行")
			}

			if err := a.processManager.Stop(lb.LocalPort); err != nil {
				lb.Enabled = false
				lb.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
				return err
			}

			lb.Enabled = false
			lb.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
			return a.saveConfig()
		}
	}

	return fmt.Errorf("故障转移节点不存在")
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

// startChainProxyInternal 启动链式代理（内部方法，不加锁）
func (a *MyService) startChainProxyInternal(chain *models.ChainProxy) error {
	// 解析链中的节点（支持故障转移节点）
	chainRules, err := a.resolveChainNodes(chain.ChainNodes)
	if err != nil {
		return err
	}

	// 构建链式代理配置（含 Hysteria2/TUIC 节点时自动切换 sing-box 内核），附加流量统计 API
	apiPort := process.FindApiPort(chain.LocalPort)
	configJSON, coreType, err := buildChainConfigJSON(chain.LocalPort, chainRules, apiPort)
	if err != nil {
		return err
	}

	// 创建临时规则用于启动进程（ID 用 Chain 的 ID，流量回调据此关联到链式代理）
	tempRule := &models.ProxyRule{
		ID:        chain.ID,
		Alias:     chain.Alias,
		LocalType: chain.LocalType,
		LocalPort: chain.LocalPort,
		Protocol:  "vmess", // 占位
	}

	if err := a.processManager.StartWithOptions(tempRule, process.StartOptions{
		ConfigJSON: configJSON,
		CoreType:   coreType,
		ApiPort:    apiPort,
	}); err != nil {
		return err
	}

	chain.Enabled = true
	chain.LastStartTime = time.Now().Format("2006-01-02 15:04:05")
	return nil
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

			if err := a.startChainProxyInternal(chain); err != nil {
				return err
			}

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
				chain.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
				return err
			}

			chain.Enabled = false
			chain.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
			return a.saveConfig()
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// ==================== 健康检查 API ====================

// getRulesSnapshot 获取规则快照（供健康检查后台任务使用）
func (a *MyService) getRulesSnapshot() []models.ProxyRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	rules := make([]models.ProxyRule, len(a.config.Rules))
	copy(rules, a.config.Rules)
	return rules
}

// handleHealthCheckResult 处理健康检查结果（普通节点/故障转移/链式代理）
func (a *MyService) handleHealthCheckResult(result models.HealthCheckResult) {
	a.mu.Lock()
	a.applyHealthResultLocked(result)
	a.mu.Unlock()

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "healthCheckResult", Data: result})
}

// applyHealthResultLocked 将健康检查结果写回对应节点（需已持有锁）
func (a *MyService) applyHealthResultLocked(result models.HealthCheckResult) {
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == result.RuleID {
			a.config.Rules[i].HealthStatus = result.Status
			a.config.Rules[i].HealthLatency = result.Latency
			a.config.Rules[i].LastHealthCheck = result.Timestamp
			return
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == result.RuleID {
			a.config.LoadBalancers[i].HealthStatus = result.Status
			a.config.LoadBalancers[i].HealthLatency = result.Latency
			a.config.LoadBalancers[i].LastHealthCheck = result.Timestamp
			return
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == result.RuleID {
			a.config.ChainProxies[i].HealthStatus = result.Status
			a.config.ChainProxies[i].HealthLatency = result.Latency
			a.config.ChainProxies[i].LastHealthCheck = result.Timestamp
			return
		}
	}
}

// GetHealthCheckConfig 获取健康检查配置
func (a *MyService) GetHealthCheckConfig() models.HealthCheckConfig {
	return a.healthCheckManager.GetConfig()
}

// SetHealthCheckConfig 更新健康检查配置
func (a *MyService) SetHealthCheckConfig(cfg models.HealthCheckConfig) error {
	a.healthCheckManager.Configure(cfg)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.HealthCheck = a.healthCheckManager.GetConfig()
	return a.saveConfig()
}

// markRulesChecking 将指定节点标记为检测中并返回其快照
func (a *MyService) markRulesChecking(ruleIDs []string) []models.ProxyRule {
	idSet := make(map[string]bool, len(ruleIDs))
	for _, id := range ruleIDs {
		idSet[id] = true
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	var rules []models.ProxyRule
	for i := range a.config.Rules {
		if len(ruleIDs) == 0 || idSet[a.config.Rules[i].ID] {
			a.config.Rules[i].HealthStatus = "checking"
			rules = append(rules, a.config.Rules[i])
		}
	}
	return rules
}

// CheckNodeHealth 检测单个节点健康状态
func (a *MyService) CheckNodeHealth(ruleID string) error {
	return a.CheckSelectedNodesHealth([]string{ruleID})
}

// proxyHealthTarget 需要经本地代理端口检测的组合节点（故障转移/链式代理）
type proxyHealthTarget struct {
	id        string
	localPort int
}

// collectProxyHealthTargets 收集需检测的已启动 LB/链式代理（ids 为空表示全部），
// 并将其标记为 checking。需在未持锁状态下调用。
func (a *MyService) collectProxyHealthTargets(ruleIDs []string) []proxyHealthTarget {
	idSet := make(map[string]bool, len(ruleIDs))
	for _, id := range ruleIDs {
		idSet[id] = true
	}
	all := len(ruleIDs) == 0

	a.mu.Lock()
	defer a.mu.Unlock()

	var targets []proxyHealthTarget
	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if (all || idSet[lb.ID]) && lb.Enabled && a.processManager.IsRunning(lb.LocalPort) {
			lb.HealthStatus = "checking"
			targets = append(targets, proxyHealthTarget{lb.ID, lb.LocalPort})
		}
	}
	for i := range a.config.ChainProxies {
		c := &a.config.ChainProxies[i]
		if (all || idSet[c.ID]) && c.Enabled && a.processManager.IsRunning(c.LocalPort) {
			c.HealthStatus = "checking"
			targets = append(targets, proxyHealthTarget{c.ID, c.LocalPort})
		}
	}
	return targets
}

// checkProxyTargets 并发检测组合节点（通过本地代理端口）
func (a *MyService) checkProxyTargets(targets []proxyHealthTarget) {
	if len(targets) == 0 {
		return
	}
	cfg := a.healthCheckManager.GetConfig()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, tg := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(tg proxyHealthTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			result := healthcheck.CheckProxyPort(tg.id, tg.localPort, cfg)
			a.mu.Lock()
			a.applyHealthResultLocked(result)
			a.mu.Unlock()
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "healthCheckResult", Data: result})
		}(tg)
	}
	wg.Wait()
}

// CheckSelectedNodesHealth 检测选中节点健康状态（普通节点直连探测，故障转移/链式代理经本地代理端口探测）
func (a *MyService) CheckSelectedNodesHealth(ruleIDs []string) error {
	rules := a.markRulesChecking(ruleIDs)
	proxyTargets := a.collectProxyHealthTargets(ruleIDs)

	if len(rules) == 0 && len(proxyTargets) == 0 {
		return fmt.Errorf("未找到需要检测的节点（故障转移/链式代理需先启动）")
	}

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	a.log(fmt.Sprintf("[健康检查] 开始检测 %d 个节点、%d 个组合代理", len(rules), len(proxyTargets)))

	go func() {
		var wg sync.WaitGroup
		if len(rules) > 0 {
			wg.Add(1)
			go func() { defer wg.Done(); a.healthCheckManager.CheckRules(context.Background(), rules) }()
		}
		if len(proxyTargets) > 0 {
			wg.Add(1)
			go func() { defer wg.Done(); a.checkProxyTargets(proxyTargets) }()
		}
		wg.Wait()
		a.mu.Lock()
		_ = a.saveConfig()
		a.mu.Unlock()
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "healthCheckComplete"})
	}()
	return nil
}

// CheckAllNodesHealth 检测全部节点健康状态
func (a *MyService) CheckAllNodesHealth() error {
	return a.CheckSelectedNodesHealth(nil)
}

// ==================== 流量统计 ====================

// accumulateTraffic 将流量增量累加到 TrafficStats（跨天自动清零今日统计）
func accumulateTraffic(t *models.TrafficStats, deltaUp, deltaDown int64, today string) {
	if t.TodayDate != today {
		t.TodayDate = today
		t.TodayUp = 0
		t.TodayDown = 0
	}
	t.TodayUp += deltaUp
	t.TodayDown += deltaDown
	t.TotalUp += deltaUp
	t.TotalDown += deltaDown
}

// handleTraffic 处理进程管理器上报的流量增量。
// ruleID 可能是普通节点、故障转移或链式代理的 ID（它们启动时用各自 ID 作为进程标识）。
func (a *MyService) handleTraffic(ruleID string, deltaUp, deltaDown int64, upSpeed, downSpeed float64) {
	today := time.Now().Format("2006-01-02")
	var stats *models.TrafficStats

	a.mu.Lock()
	// 普通节点
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == ruleID {
			stats = &a.config.Rules[i].Traffic
			break
		}
	}
	// 故障转移
	if stats == nil {
		for i := range a.config.LoadBalancers {
			if a.config.LoadBalancers[i].ID == ruleID {
				stats = &a.config.LoadBalancers[i].Traffic
				break
			}
		}
	}
	// 链式代理
	if stats == nil {
		for i := range a.config.ChainProxies {
			if a.config.ChainProxies[i].ID == ruleID {
				stats = &a.config.ChainProxies[i].Traffic
				break
			}
		}
	}

	var snapshot *models.TrafficSnapshot
	if stats != nil {
		accumulateTraffic(stats, deltaUp, deltaDown, today)
		a.trafficDirty = true
		snapshot = &models.TrafficSnapshot{
			RuleID:    ruleID,
			UpSpeed:   upSpeed,
			DownSpeed: downSpeed,
			TodayUp:   stats.TodayUp,
			TodayDown: stats.TodayDown,
			TotalUp:   stats.TotalUp,
			TotalDown: stats.TotalDown,
		}
	}
	a.mu.Unlock()

	// 未匹配到（如订阅临时代理）只推送速度
	if snapshot == nil {
		snapshot = &models.TrafficSnapshot{
			RuleID:    ruleID,
			UpSpeed:   upSpeed,
			DownSpeed: downSpeed,
		}
	}

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "trafficUpdate", Data: snapshot})
}

// ResetRuleTraffic 清零流量统计（ruleID 为空时清零全部节点、故障转移与链式代理）
func (a *MyService) ResetRuleTraffic(ruleID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	empty := models.TrafficStats{TodayDate: today}
	count := 0
	for i := range a.config.Rules {
		if ruleID == "" || a.config.Rules[i].ID == ruleID {
			a.config.Rules[i].Traffic = empty
			count++
		}
	}
	for i := range a.config.LoadBalancers {
		if ruleID == "" || a.config.LoadBalancers[i].ID == ruleID {
			a.config.LoadBalancers[i].Traffic = empty
			count++
		}
	}
	for i := range a.config.ChainProxies {
		if ruleID == "" || a.config.ChainProxies[i].ID == ruleID {
			a.config.ChainProxies[i].Traffic = empty
			count++
		}
	}

	if count == 0 {
		return fmt.Errorf("节点 %s 不存在", ruleID)
	}

	a.log(fmt.Sprintf("[流量统计] 已清零 %d 个节点的流量统计", count))
	return a.saveConfig()
}

// ==================== 订阅更新代理 ====================

// SetSubscriptionUpdateMode 设置订阅的更新方式
// mode: direct（直连）/ system（系统代理）/ proxy（指定节点，proxyID 为节点/链式代理/故障转移的 ID）
func (a *MyService) SetSubscriptionUpdateMode(subID, mode, proxyID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].ID == subID {
			a.config.Subscriptions[i].UpdateMode = mode
			a.config.Subscriptions[i].UpdateProxyID = proxyID
			a.log(fmt.Sprintf("[订阅] %s 更新方式已设置为: %s", a.config.Subscriptions[i].Name, mode))
			return a.saveConfig()
		}
	}

	return fmt.Errorf("订阅不存在")
}

// resolveSubscriptionProxy 根据订阅的更新方式解析代理地址。
// 返回代理 URL（空表示直连）和清理函数（用于关闭临时启动的代理进程）。
// 注意：调用方不能持有 a.mu 锁。
func (a *MyService) resolveSubscriptionProxy(sub *models.Subscription) (string, func(), error) {
	switch sub.UpdateMode {
	case "", "direct":
		return "", nil, nil

	case "system":
		proxyURL := a.sysProxyManager.GetCurrentSystemProxy()
		if proxyURL == "" {
			a.log("[订阅] 未检测到系统代理，改为直连更新")
		}
		return proxyURL, nil, nil

	case "proxy":
		return a.resolveNodeProxy(sub.UpdateProxyID)
	}

	return "", nil, nil
}

// resolveNodeProxy 将节点/链式代理/故障转移解析为可用的本地代理地址。
// 如果目标已在运行则直接复用；否则在临时端口上启动，返回的清理函数负责关闭。
func (a *MyService) resolveNodeProxy(proxyID string) (string, func(), error) {
	a.mu.RLock()

	// 普通节点
	for i := range a.config.Rules {
		rule := a.config.Rules[i]
		if rule.ID != proxyID {
			continue
		}
		a.mu.RUnlock()

		if rule.Enabled && a.processManager.IsRunning(rule.LocalPort) {
			return fmt.Sprintf("socks5://127.0.0.1:%d", rule.LocalPort), nil, nil
		}

		// 临时启动该节点
		tempPort := utils.FindAvailablePort(15800)
		if tempPort == 0 {
			return "", nil, fmt.Errorf("未找到可用的临时端口")
		}
		tempRule := rule
		tempRule.LocalPort = tempPort
		tempRule.LocalType = "mixed"
		tempRule.Alias = fmt.Sprintf("订阅更新临时代理(%s)", rule.Alias)

		if err := a.processManager.Start(&tempRule); err != nil {
			return "", nil, fmt.Errorf("临时启动节点 %s 失败: %v", rule.Alias, err)
		}
		// 等待内核就绪
		time.Sleep(1 * time.Second)

		cleanup := func() {
			_ = a.processManager.Stop(tempPort)
			a.log(fmt.Sprintf("[订阅] 临时代理已关闭 (端口:%d)", tempPort))
		}
		return fmt.Sprintf("socks5://127.0.0.1:%d", tempPort), cleanup, nil
	}

	// 链式代理
	for i := range a.config.ChainProxies {
		chain := a.config.ChainProxies[i]
		if chain.ID != proxyID {
			continue
		}

		if chain.Enabled && a.processManager.IsRunning(chain.LocalPort) {
			a.mu.RUnlock()
			return fmt.Sprintf("socks5://127.0.0.1:%d", chain.LocalPort), nil, nil
		}

		chainRules, err := a.resolveChainNodes(chain.ChainNodes)
		if err != nil {
			a.mu.RUnlock()
			return "", nil, err
		}
		a.mu.RUnlock()

		tempPort := utils.FindAvailablePort(15800)
		configJSON, coreType, err := buildChainConfigJSON(tempPort, chainRules, 0)
		if err != nil {
			return "", nil, err
		}
		return a.startTempProxy(fmt.Sprintf("订阅更新临时代理(%s)", chain.Alias), tempPort, configJSON, coreType)
	}

	// 故障转移
	for i := range a.config.LoadBalancers {
		lb := a.config.LoadBalancers[i]
		if lb.ID != proxyID {
			continue
		}

		if lb.Enabled && a.processManager.IsRunning(lb.LocalPort) {
			a.mu.RUnlock()
			return fmt.Sprintf("socks5://127.0.0.1:%d", lb.LocalPort), nil, nil
		}

		nodes := a.collectLBNodes(&lb)
		a.mu.RUnlock()
		if len(nodes) == 0 {
			return "", nil, fmt.Errorf("故障转移 %s 没有可用的子节点", lb.Alias)
		}

		tempPort := utils.FindAvailablePort(15800)
		configJSON, coreType, err := buildLoadBalanceConfigJSON(&lb, tempPort, nodes, 0)
		if err != nil {
			return "", nil, err
		}
		return a.startTempProxy(fmt.Sprintf("订阅更新临时代理(%s)", lb.Alias), tempPort, configJSON, coreType)
	}

	a.mu.RUnlock()
	return "", nil, fmt.Errorf("指定的代理节点不存在: %s", proxyID)
}

// startTempProxy 使用自定义配置启动临时代理进程
func (a *MyService) startTempProxy(alias string, tempPort int, configJSON, coreType string) (string, func(), error) {
	if tempPort == 0 {
		return "", nil, fmt.Errorf("未找到可用的临时端口")
	}

	tempRule := &models.ProxyRule{
		ID:        fmt.Sprintf("temp_%d", time.Now().UnixNano()),
		Alias:     alias,
		LocalType: "mixed",
		LocalPort: tempPort,
		Protocol:  "vmess", // 占位，不影响实际配置
	}

	if err := a.processManager.StartWithOptions(tempRule, process.StartOptions{
		ConfigJSON: configJSON,
		CoreType:   coreType,
	}); err != nil {
		return "", nil, fmt.Errorf("启动临时代理失败: %v", err)
	}
	time.Sleep(1 * time.Second)

	cleanup := func() {
		_ = a.processManager.Stop(tempPort)
		a.log(fmt.Sprintf("[订阅] 临时代理已关闭 (端口:%d)", tempPort))
	}
	return fmt.Sprintf("socks5://127.0.0.1:%d", tempPort), cleanup, nil
}

// collectLBNodes 收集故障转移的子节点（内部方法，需要已持有读锁）
func (a *MyService) collectLBNodes(lb *models.LoadBalanceNode) []*models.ProxyRule {
	nodes := make([]*models.ProxyRule, 0)
	for _, nodeID := range lb.NodeIDs {
		for j := range a.config.Rules {
			if a.config.Rules[j].ID == nodeID {
				nodes = append(nodes, &a.config.Rules[j])
				break
			}
		}
	}
	return nodes
}

// ==================== 多内核配置构建 ====================

// buildChainConfigJSON 构建链式代理配置。
// 链中包含 Hysteria2/TUIC 节点时使用 sing-box 内核，否则使用 xray。
// apiPort > 0 时附加流量统计 API。
func buildChainConfigJSON(localPort int, chainRules []*models.ProxyRule, apiPort int) (string, string, error) {
	if singbox.RulesNeedSingBox(chainRules) {
		cfg, err := singbox.BuildChainConfig(localPort, chainRules)
		if err != nil {
			return "", "", err
		}
		if apiPort > 0 {
			singbox.AddClashAPI(cfg, apiPort)
		}
		configJSON, err := cfg.ToJSON()
		return configJSON, process.CoreSingBox, err
	}

	cfg, err := xray.BuildChainConfig("mixed", localPort, chainRules)
	if err != nil {
		return "", "", err
	}
	if apiPort > 0 {
		xray.AddStatsAPI(cfg, apiPort)
	}
	configJSON, err := cfg.ToJSON()
	return configJSON, process.CoreXray, err
}

// buildLoadBalanceConfigJSON 构建故障转移配置。
// 子节点包含 Hysteria2/TUIC 时使用 sing-box 内核，否则使用 xray。
// apiPort > 0 时附加流量统计 API。
func buildLoadBalanceConfigJSON(lb *models.LoadBalanceNode, localPort int, nodes []*models.ProxyRule, apiPort int) (string, string, error) {
	if singbox.RulesNeedSingBox(nodes) {
		cfg, err := singbox.BuildLoadBalanceConfig(localPort, nodes)
		if err != nil {
			return "", "", err
		}
		if apiPort > 0 {
			singbox.AddClashAPI(cfg, apiPort)
		}
		configJSON, err := cfg.ToJSON()
		return configJSON, process.CoreSingBox, err
	}

	lbCopy := *lb
	lbCopy.LocalPort = localPort
	cfg, err := xray.BuildLoadBalanceConfig(&lbCopy, nodes)
	if err != nil {
		return "", "", err
	}
	if apiPort > 0 {
		xray.AddStatsAPI(cfg, apiPort)
	}
	configJSON, err := cfg.ToJSON()
	return configJSON, process.CoreXray, err
}

// resolveChainNodes 解析链中的节点（支持故障转移节点）
func (a *MyService) resolveChainNodes(nodeIDs []string) ([]*models.ProxyRule, error) {
	var chainRules []*models.ProxyRule

	for _, nodeID := range nodeIDs {
		// 先查找是否为故障转移节点
		isLB := false
		for _, lb := range a.config.LoadBalancers {
			if lb.ID == nodeID {
				isLB = true
				// 取故障转移中的第一个可用节点
				for _, subNodeID := range lb.NodeIDs {
					for j := range a.config.Rules {
						if a.config.Rules[j].ID == subNodeID {
							chainRules = append(chainRules, &a.config.Rules[j])
							goto nextNode
						}
					}
				}
				return nil, fmt.Errorf("故障转移节点 %s 中没有可用的子节点", lb.Alias)
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
