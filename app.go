package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"xray-manager/internal/config"
	"xray-manager/internal/group"
	"xray-manager/internal/healthcheck"
	"xray-manager/internal/logger"
	"xray-manager/internal/models"
	"xray-manager/internal/parser"
	"xray-manager/internal/portregistry"
	"xray-manager/internal/process"
	"xray-manager/internal/singbox"
	"xray-manager/internal/speedtest"
	"xray-manager/internal/subscription"
	"xray-manager/internal/updater"
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
	healthCheckManager  *healthcheck.Manager
	logFilter           *logger.Filter
	sysProxyManager     *config.SysProxyManager
	config              *models.Config
	mu                  sync.RWMutex

	trafficDirty bool // 流量统计有未保存的变更
	httpServer   *http.Server
	// portReservations 记录本实例已占有的本地端口（惰性记账）。
	//
	// 早期实现给每个未启动节点常驻一个 net.Listener 来"占住"端口，节点上千时
	// 等于开上千个监听套接字，启动阶段就会耗尽句柄并把 a.mu 堵死，界面直接卡住。
	// 现在只记账不监听：真正的占用检测推迟到启动节点时由 EnsurePortFree 完成。
	portReservations map[int]bool
	portRegistry     *portregistry.Registry
	executablePath   string
	configPath       string
	portConflicts    []models.PortConflict

	// 健康检查结果缓冲：上万节点时逐条落库+推事件会压垮主线程和前端
	healthResultMu  sync.Mutex
	healthResultBuf []models.HealthCheckResult
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
	a.configPath = configManager.GetConfigPath()
	a.executablePath, _ = os.Executable()
	a.portRegistry, err = portregistry.New()
	if err != nil {
		a.logError("初始化全局端口注册表失败", err)
		return err
	}

	// 初始化进程管理器
	a.processManager = process.NewManager(func(message string) {
		// 日志回调函数
		a.logFilter.AddLog(message)
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: message})
	}, a.loadRules)

	// 启用分片：普通节点共享 sing-box 进程，内存从「节点数 × 33 MB」
	// 降到「分片数 × 33 MB」（实测 1583 个节点约 210 MB，
	// 一节点一进程则需约 50 GB，远超常见机器的物理内存）。
	if a.processManager != nil {
		a.processManager.EnableSharding()
	}

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
	a.processManager.SetNodeFailedCallback(a.handleNodeFailed)
	a.processManager.SetRealIPCallback(a.handleRealIP)

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
		if a.config.SessionRelays == nil {
			a.config.SessionRelays = []models.SessionRelay{}
		}
		if !a.config.Update.Configured {
			a.config.Update.Configured = true
			a.config.Update.AutoCheck = true
			a.config.Update.AutoDownload = false
		}

		// 迁移旧配置：此前没有前置代理开关，「选了节点」就等于启用。
		// 不补这一步，升级后已配置的前置代理会静默失效。
		if a.config.PreProxyNodeID != "" && !a.config.PreProxyEnabled {
			a.config.PreProxyEnabled = true
		}

		a.groupManager.LoadGroups(a.config.Groups)

		if err := a.syncPortRegistryLocked(false); err != nil {
			a.logError("同步全局端口注册表失败", err)
			return err
		}

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

		// 启动后台健康检查（按配置）
		a.healthCheckManager.Configure(a.config.HealthCheck)

		// 应用测速配置
		a.speedTestManager.Configure(a.config.SpeedTest.URL, a.config.SpeedTest.Headers)

		// 应用订阅拉取配置（空值时解析器内部回退到默认 UA）
		a.subscriptionManager.SetUserAgent(a.config.Subscription.UserAgent)

		// 为所有未启动节点保留本地端口，避免多个客户端实例分配到同一端口。
		a.reserveStoppedPorts()

		// 节点端口若落在系统动态端口范围内，会被其他程序随机借走，
		// 表现为节点时好时坏。这种占用是 Bound 而非 Listen 状态，
		// netstat 查不到，用户几乎无法自行定位，因此在启动时主动提示。
		a.warnDynamicPortConflict()

		// 自动启动放到后台执行。
		//
		// Wails 要等 ServiceStartup 返回才创建窗口，而拉起 N 个内核进程是慢操作
		// （每个都要写配置、fork 进程、探测端口）。节点一多，窗口就要等上几分钟甚至
		// 更久才出现，用户只能看到进程看不到界面。放后台后窗口立即可用，
		// 节点在界面上逐个变为已启动。
		go a.autoStartEnabledNodes()
	}

	// 定期保存流量统计（避免每次流量更新都写盘）
	go a.trafficSaveLoop(ctx)

	// 会话代理不经内核进程，统计需自行定期推送给前端
	go a.relayStatsLoop(ctx)

	a.log("Xray 管理器已启动")

	a.startHTTPAPI()

	// 后台检查更新（不阻塞启动）
	go a.maybeAutoUpdate()

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
	a.stopHTTPAPI()
	a.releaseAllPortReservations()

	// 停止健康检查
	if a.healthCheckManager != nil {
		a.healthCheckManager.Stop()
	}

	// 先保存配置（保留 Enabled 状态，以便下次启动时恢复）
	if err := a.saveConfig(); err != nil {
		a.logError("保存配置失败", err)
	}

	a.log("正在停止所有进程...")
	stopAllSessionRelays()
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

	// 未启动的节点不可能处于「验证中」。
	//
	// 停止节点的入口有十几处（单个停止、批量停止、按分组停止、删除、
	// 订阅更新重建……），逐个补清标记迟早会漏，漏掉的节点会一直显示
	// 黄色呼吸灯。这里在唯一的读取出口统一兜底，保证界面看到的状态自洽。
	rules := make([]models.ProxyRule, len(a.config.Rules))
	copy(rules, a.config.Rules)
	for i := range rules {
		if !rules[i].Enabled {
			rules[i].Verifying = false
		}
	}
	return rules
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

// generateUniqueRuleIDs 批量生成 n 个唯一规则 ID。
// 相比循环调用 generateUniqueRuleID（每次全表扫描，导入上万节点时是 O(n²)），
// 这里只建一次已用 ID 集合，整体是 O(现有节点数 + n)。
func generateUniqueRuleIDs(existingRules []models.ProxyRule, n int) []string {
	used := make(map[string]struct{}, len(existingRules)+n)
	for i := range existingRules {
		used[existingRules[i].ID] = struct{}{}
	}

	ids := make([]string, 0, n)
	base := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("rule_%d", base+int64(i))
		for seq := 0; ; seq++ {
			if _, exists := used[id]; !exists {
				break
			}
			id = fmt.Sprintf("rule_%d_%d", base+int64(i), seq)
		}
		used[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// ruleIDPool 预生成一批唯一规则 ID，循环里逐个取用。
// 池子取空时退回逐个生成（导入数量超出预估的兜底路径）。
type ruleIDPool struct{ ids []string }

func newRuleIDPool(existing []models.ProxyRule, n int) *ruleIDPool {
	return &ruleIDPool{ids: generateUniqueRuleIDs(existing, n)}
}

func (p *ruleIDPool) next(existing []models.ProxyRule) string {
	if len(p.ids) == 0 {
		return generateUniqueRuleID(existing)
	}
	id := p.ids[0]
	p.ids = p.ids[1:]
	return id
}

// portPool 预分配一批本地端口，循环里逐个取用。取空时返回 0（表示未分配端口）。
type portPool struct{ ports []int }

func newPortPool(ports []int) *portPool { return &portPool{ports: ports} }

func (p *portPool) next() int {
	if len(p.ports) == 0 {
		return 0
	}
	port := p.ports[0]
	p.ports = p.ports[1:]
	return port
}

// usedLocalPorts 收集配置中所有节点（普通/故障转移/链式代理）已占用的本地端口。
// 需已持有 a.mu 锁。
func (a *MyService) usedLocalPorts() map[int]bool {
	used := make(map[int]bool)
	for i := range a.config.Rules {
		if p := a.config.Rules[i].LocalPort; p > 0 {
			used[p] = true
		}
	}
	for i := range a.config.LoadBalancers {
		if p := a.config.LoadBalancers[i].LocalPort; p > 0 {
			used[p] = true
		}
	}
	for i := range a.config.ChainProxies {
		if p := a.config.ChainProxies[i].LocalPort; p > 0 {
			used[p] = true
		}
	}
	for i := range a.config.SessionRelays {
		if p := a.config.SessionRelays[i].LocalPort; p > 0 {
			used[p] = true
		}
	}
	return used
}

// reservePortLocked 把端口记为本实例占有。
//
// 只做内存记账，不再实际 net.Listen：上千个常驻监听会耗尽句柄并拖垮启动流程。
// 返回 false 表示该端口已被本机其他进程占用（本实例已持有的端口视为成功）。
func (a *MyService) reservePortLocked(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	if a.portReservations == nil {
		a.portReservations = make(map[int]bool)
	}
	if a.portReservations[port] {
		return true
	}
	// 端口被本机其他进程实际监听时才拒绝。这里是一次性探测，不保留监听。
	if !utils.CheckPortAvailable(port) {
		return false
	}
	a.portReservations[port] = true
	return true
}

func (a *MyService) releasePortReservationLocked(port int) {
	delete(a.portReservations, port)
}

// autoStartEnabledNodes 拉起配置中所有已启用的节点。
//
// 在后台协程中运行，不阻塞窗口创建。整段持锁：启动过程要读写 config 里的节点
// 状态，且期间不应让用户从界面并发改动同一批节点。锁本身很快就能被界面操作抢到
// ——真正慢的是逐个 fork 内核进程，而那已经不在窗口创建的关键路径上了。
func (a *MyService) autoStartEnabledNodes() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.config == nil {
		return
	}

	// 崩溃前启用了多少节点，重启时就会全部自动拉起——而崩溃往往正是节点太多
	// 导致的，于是一开就再崩，陷入"打不开"的循环。这里按可用内存算出还能带动
	// 多少个，超出的保持启用标记不动，等用户自己决定启动哪些。
	budget := a.autoStartBudgetLocked()

	// 分片模式下把普通节点攒成一批，只调谐一次：逐个启动会让同一分片
	// 反复重启，几百个节点就是几百次重建配置 + 重启进程。
	sharded := a.processManager != nil && a.processManager.ShardingEnabled()
	if sharded {
		a.syncShardPreProxyLocked()
	}
	var shardBatch []*models.ProxyRule

	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.Enabled && a.hasPortConflictLocked(rule.ID) {
			a.log(fmt.Sprintf("[端口冲突] 规则 %s 暂不自动启动，等待用户处理", rule.Alias))
			rule.Enabled = false
			continue
		}
		if rule.Enabled {
			if budget <= 0 {
				continue
			}
			budget--
			if sharded {
				a.releasePortReservationLocked(rule.LocalPort)
				shardBatch = append(shardBatch, rule)
				continue
			}
			a.log(fmt.Sprintf("自动启动规则: %s", rule.Alias))
			if err := a.startRuleInternal(rule); err != nil {
				a.logError(fmt.Sprintf("启动规则 %s 失败", rule.Alias), err)
				rule.Enabled = false
			}
		}
	}

	if len(shardBatch) > 0 {
		// 确实有节点要经前置代理出站时才拉起它——没有节点要启动就不该
		// 顺带把前置代理也开了
		if a.preProxyNeededByLocked(shardBatch) {
			a.ensurePreProxyRunningLocked()
			a.syncShardPreProxyLocked()
		}
		a.log(fmt.Sprintf("自动启动 %d 个节点（分片模式）", len(shardBatch)))
		failures, err := a.processManager.StartNodesInShard(shardBatch)
		if err != nil {
			a.logError("批量启动节点失败", err)
			for _, rule := range shardBatch {
				rule.Enabled = false
				a.reservePortLocked(rule.LocalPort)
			}
		} else {
			for _, rule := range shardBatch {
				if failErr, failed := failures[rule.ID]; failed {
					a.logError(fmt.Sprintf("启动规则 %s 失败", rule.Alias), failErr)
					rule.Enabled = false
					rule.Verifying = false
					rule.LastError = failErr.Error()
					a.reservePortLocked(rule.LocalPort)
				}
			}
		}
	}

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.Enabled && a.hasPortConflictLocked(lb.ID) {
			a.log(fmt.Sprintf("[端口冲突] 故障转移 %s 暂不自动启动，等待用户处理", lb.Alias))
			lb.Enabled = false
			continue
		}
		if lb.Enabled {
			if budget <= 0 {
				continue
			}
			budget--
			a.log(fmt.Sprintf("自动启动故障转移: %s", lb.Alias))
			if err := a.startLoadBalancerInternal(lb); err != nil {
				a.logError(fmt.Sprintf("启动故障转移 %s 失败", lb.Alias), err)
				lb.Enabled = false
			}
		}
	}

	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.Enabled && a.hasPortConflictLocked(chain.ID) {
			a.log(fmt.Sprintf("[端口冲突] 链式代理 %s 暂不自动启动，等待用户处理", chain.Alias))
			chain.Enabled = false
			continue
		}
		if chain.Enabled {
			if budget <= 0 {
				continue
			}
			budget--
			a.log(fmt.Sprintf("自动启动链式代理: %s", chain.Alias))
			if err := a.startChainProxyInternal(chain); err != nil {
				a.logError(fmt.Sprintf("启动链式代理 %s 失败", chain.Alias), err)
				chain.Enabled = false
			}
		}
	}

	// 会话代理放在最后：其前置加速节点可能是上面刚启动的普通节点/链式/故障转移。
	// 它们运行在本进程内，不占内核进程的内存预算，因此不计入 budget。
	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.Enabled && a.hasPortConflictLocked(sr.ID) {
			a.log(fmt.Sprintf("[端口冲突] 动态会话代理 %s 暂不自动启动，等待用户处理", sr.Alias))
			sr.Enabled = false
			continue
		}
		if sr.Enabled {
			a.log(fmt.Sprintf("自动启动动态会话代理: %s", sr.Alias))
			sr.Enabled = false // startSessionRelayInternal 成功后置回 true
			if err := a.startSessionRelayInternal(sr); err != nil {
				a.logError(fmt.Sprintf("启动动态会话代理 %s 失败", sr.Alias), err)
				sr.Enabled = false
				sr.LastError = err.Error()
			}
		}
	}

	_ = a.saveConfig()
	a.emitEvent("loadRules", nil)
}

// checkNodeCapacity 按当前运行模式判断能否再启动 count 个节点。
//
// 分片模式下节点共享进程，单节点开销远低于独立进程，容量上限高得多。
func (a *MyService) checkNodeCapacity(count int) error {
	if a.processManager != nil && a.processManager.ShardingEnabled() {
		return process.CheckShardedCapacity(count)
	}
	return process.CheckCapacity(count)
}

// autoStartBudgetLocked 返回本次启动最多可自动拉起的节点数。
//
// 崩溃恢复时配置里可能有上千个 Enabled 节点，全部拉起会再次耗尽内存并闪退。
// 超出预算的节点保持 Enabled 不变，只是这一轮不启动，用户可在界面上按需启动。
// 需已持有 a.mu 锁。
func (a *MyService) autoStartBudgetLocked() int {
	enabled := 0
	for i := range a.config.Rules {
		if a.config.Rules[i].Enabled {
			enabled++
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].Enabled {
			enabled++
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].Enabled {
			enabled++
		}
	}
	for i := range a.config.SessionRelays {
		if a.config.SessionRelays[i].Enabled {
			enabled++
		}
	}

	err := a.checkNodeCapacity(enabled)
	if err == nil {
		return enabled
	}
	capacityErr, ok := err.(*process.CapacityError)
	if !ok {
		return enabled
	}
	a.log(fmt.Sprintf(
		"[容量保护] 配置中有 %d 个节点处于启用状态，按当前可用内存仅自动启动前 %d 个；"+
			"其余节点保持启用标记，可在界面上手动启动。",
		enabled, capacityErr.Allowed))
	return capacityErr.Allowed
}

// reserveStoppedPorts 把所有未启动节点的端口记为本实例占有。
//
// 纯记账，不做可用性探测：启动阶段对上千个端口逐个 net.Listen 会持锁数分钟，
// 界面根本出不来。端口是否真的能用留到启动该节点时由 EnsurePortFree 判定。
func (a *MyService) reserveStoppedPorts() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.portReservations == nil {
		a.portReservations = make(map[int]bool)
	}
	record := func(port int) {
		if port > 0 && port <= 65535 {
			a.portReservations[port] = true
		}
	}
	for _, rule := range a.config.Rules {
		if !rule.Enabled {
			record(rule.LocalPort)
		}
	}
	for _, item := range a.config.LoadBalancers {
		if !item.Enabled {
			record(item.LocalPort)
		}
	}
	for _, item := range a.config.ChainProxies {
		if !item.Enabled {
			record(item.LocalPort)
		}
	}
	for _, item := range a.config.SessionRelays {
		if !item.Enabled {
			record(item.LocalPort)
		}
	}
}

func (a *MyService) releaseAllPortReservations() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.portReservations = nil
}

// runWithReleasedPortLocked 在"端口已交给内核进程"的前提下执行 action。
//
// 端口预留是纯记账，不占用实际监听，因此这里只做归属转移：启动成功后端口由
// 内核进程持有，失败则把记账恢复回来，避免端口被漏记而分配给别的节点。
func (a *MyService) runWithReleasedPortLocked(port int, action func() error) error {
	a.releasePortReservationLocked(port)
	if err := action(); err != nil {
		a.reservePortLocked(port)
		return err
	}
	return nil
}

// allocateLocalPort 分配一个既不与配置中其他节点冲突、系统又可监听的本地端口。
// 相比直接用 utils.FindAvailablePort，额外排除了已分配给未启动节点的端口，
// 避免多个未启动节点拿到同一端口。需已持有 a.mu 锁。
func (a *MyService) allocateLocalPort() int {
	used := a.usedLocalPorts()
	for port := utils.DefaultRecommendPortStart; port < 65535; port++ {
		if used[port] {
			continue
		}
		if a.reservePortLocked(port) && a.reserveTemporaryPortLocked(port) {
			return port
		}
		a.releasePortReservationLocked(port)
	}
	return 0
}

// portShortageMessage 组织"端口没分够"的提示。
//
// 端口本身极少真的耗尽（从 11000 一直扫到 65535），绝大多数情况是被本机上
// 其他 xray-manager 实例注册占用了。提示里点明这一点，用户才知道该去看哪里。
// 需已持有 a.mu 锁。
func (a *MyService) portShortageMessage(want, got int) string {
	msg := fmt.Sprintf("[订阅] %d 个节点中有 %d 个未分配本地端口", want, want-got)
	if foreign := len(a.foreignRegisteredPortsLocked()); foreign > 0 {
		msg += fmt.Sprintf("：本机其他 xray-manager 实例已占用 %d 个端口", foreign)
	}
	return msg + "。可在节点设置中手动指定端口，或关闭其他实例后重新更新订阅。"
}

// foreignRegisteredPortsLocked 返回全局端口注册表中被"其他实例"占用的端口。
//
// 排除本实例自己的条目：那些端口本来就属于我们，分配时不该躲开。
// 注册表不可用时返回 nil——退化为原来的行为，不影响单实例使用。
func (a *MyService) foreignRegisteredPortsLocked() []int {
	if a.portRegistry == nil {
		return nil
	}
	entries, err := a.portRegistry.Entries()
	if err != nil {
		a.log(fmt.Sprintf("[端口] 读取全局端口注册表失败，本次分配不排除其他实例占用的端口: %v", err))
		return nil
	}

	ports := make([]int, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.Port <= 0 {
			continue
		}
		// 同一可执行文件 + 同一配置文件才算"自己"
		if strings.EqualFold(filepath.Clean(e.ExecutablePath), filepath.Clean(a.executablePath)) &&
			strings.EqualFold(filepath.Clean(e.ConfigPath), filepath.Clean(a.configPath)) {
			continue
		}
		ports = append(ports, e.Port)
	}
	return ports
}

// allocateLocalPorts 批量分配 n 个本地端口。
//
// 逐个调用 allocateLocalPort 在导入大量节点时非常慢：每次都要重建已用端口表、
// 从推荐起始端口重新扫描，并且每个端口都往全局端口注册表写一次文件（带文件锁）。
// 这里改为：已用端口表只建一次、端口游标只前进不回退，注册表预留合并成一次批量写入。
// 需已持有 a.mu 锁。返回的切片长度可能小于 n（可用端口耗尽）。
func (a *MyService) allocateLocalPorts(n int) []int {
	if n <= 0 {
		return nil
	}

	used := a.usedLocalPorts()
	// 把全局端口注册表里其他客户端实例占用的端口也算作已用。
	//
	// 否则这里只按本实例配置挑端口，挑中的多半早被别的实例占了，
	// 最后提交注册表时被整批剔除——表现为"可用本地端口不足"，
	// 而实际上高位端口还有几万个空着。
	for _, p := range a.foreignRegisteredPortsLocked() {
		used[p] = true
	}

	// 扫描 → 提交注册表，失败的端口标记为已用后再补扫。
	// 读取注册表和提交之间可能有其他实例抢占（或本实例配置外的占用），
	// 重试几轮能把这类零星缺口补上，而不是直接少分配一批。
	ports := make([]int, 0, n)
	port := utils.DefaultRecommendPortStart
	for round := 0; len(ports) < n && round < 5; round++ {
		batch := make([]int, 0, n-len(ports))
		for len(batch) < n-len(ports) && port < 65535 {
			p := port
			port++
			if used[p] {
				continue
			}
			// 只做本地监听占位，注册表预留留到最后一次性提交
			if !a.reservePortLocked(p) {
				used[p] = true
				continue
			}
			used[p] = true
			batch = append(batch, p)
		}
		if len(batch) == 0 {
			break // 端口段扫完了
		}

		// 批量向全局端口注册表提交预留，被抢占的端口回滚本地占位后剔除
		kept := a.reserveTemporaryPortsLocked(batch)
		keptSet := make(map[int]struct{}, len(kept))
		for _, p := range kept {
			keptSet[p] = struct{}{}
		}
		for _, p := range batch {
			if _, ok := keptSet[p]; !ok {
				a.releasePortReservationLocked(p)
			}
		}
		ports = append(ports, kept...)
	}

	return ports
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

	if rule.LocalPort > 0 {
		if err := a.claimPortLocked("rule", rule.ID, rule.Alias, rule.LocalPort); err != nil {
			return err
		}
		if !a.reservePortLocked(rule.LocalPort) {
			a.releaseRegisteredPortLocked(rule.ID)
			return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", rule.LocalPort)
		}
	} else {
		rule.LocalPort = a.allocateLocalPort()
	}
	if rule.LocalPort == 0 {
		return fmt.Errorf("没有可用的本地端口")
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
			oldPort := a.config.Rules[i].LocalPort
			if updatedRule.LocalPort != oldPort {
				if a.config.Rules[i].Enabled {
					return fmt.Errorf("请先停止规则再修改本地端口")
				}
				if updatedRule.LocalPort <= 0 {
					return fmt.Errorf("本地端口 %d 无效", updatedRule.LocalPort)
				}
				if err := a.claimPortLocked("rule", id, updatedRule.Alias, updatedRule.LocalPort); err != nil {
					return err
				}
				if !a.reservePortLocked(updatedRule.LocalPort) {
					_ = a.claimPortLocked("rule", id, a.config.Rules[i].Alias, oldPort)
					return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", updatedRule.LocalPort)
				}
				a.releasePortReservationLocked(oldPort)
			}
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
// DeleteNodes 批量删除节点（普通节点/故障转移/链式代理/动态会话代理）。
//
// 逐个调用 DeleteRule 会很慢：每个节点都要单独走一次 IPC、在锁内串行停止进程、
// 并各保存一次配置。这里先并发把进程停掉（复用 StopNodes 的并发逻辑），
// 再一次性从配置里摘除并只保存一次。
func (a *MyService) DeleteNodes(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// 先并发停止（StopNodes 内部已做锁外并发，且会更新状态、回收端口）
	if err := a.StopNodes(ids); err != nil {
		a.log(fmt.Sprintf("[批量删除] 停止节点时出错，继续删除: %v", err))
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	removed := 0
	groupIDs := make(map[string]bool)

	keptRules := a.config.Rules[:0]
	for i := range a.config.Rules {
		r := &a.config.Rules[i]
		if !idSet[r.ID] {
			keptRules = append(keptRules, *r)
			continue
		}
		a.releasePortReservationLocked(r.LocalPort)
		if a.config.PreProxyNodeID == r.ID {
			a.config.PreProxyNodeID = ""
			a.log("已清空前置代理（节点已删除）")
		}
		a.clearRelayPreProxyRefLocked(r.ID)
		if r.GroupID != "" {
			groupIDs[r.GroupID] = true
		}
		removed++
	}
	a.config.Rules = keptRules
	// 被删的节点若在前置代理的排除名单里，一并摘掉
	a.dropDeletedFromPreProxyScopeLocked(idSet, nil)

	keptLBs := a.config.LoadBalancers[:0]
	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if !idSet[lb.ID] {
			keptLBs = append(keptLBs, *lb)
			continue
		}
		a.releasePortReservationLocked(lb.LocalPort)
		removed++
	}
	a.config.LoadBalancers = keptLBs

	keptChains := a.config.ChainProxies[:0]
	for i := range a.config.ChainProxies {
		c := &a.config.ChainProxies[i]
		if !idSet[c.ID] {
			keptChains = append(keptChains, *c)
			continue
		}
		a.releasePortReservationLocked(c.LocalPort)
		removed++
	}
	a.config.ChainProxies = keptChains

	keptRelays := a.config.SessionRelays[:0]
	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if !idSet[sr.ID] {
			keptRelays = append(keptRelays, *sr)
			continue
		}
		a.releasePortReservationLocked(sr.LocalPort)
		removed++
	}
	a.config.SessionRelays = keptRelays

	if removed == 0 {
		return nil
	}

	if err := a.saveConfig(); err != nil {
		return err
	}
	a.log(fmt.Sprintf("批量删除完成，共移除 %d 个节点", removed))
	return nil
}

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
			a.releasePortReservationLocked(rule.LocalPort)

			// 删除规则
			a.config.Rules = append(a.config.Rules[:i], a.config.Rules[i+1:]...)

			// 若删除的是前置代理节点，自动清空设置
			if a.config.PreProxyNodeID == id {
				a.config.PreProxyNodeID = ""
				a.log("已清空前置代理（节点已删除）")
			}
			a.dropDeletedFromPreProxyScopeLocked(map[string]bool{id: true}, nil)
			a.clearRelayPreProxyRefLocked(id)

			if err := a.saveConfig(); err != nil {
				return err
			}

			a.log(fmt.Sprintf("删除规则: %s", rule.Alias))
			return nil
		}
	}

	return fmt.Errorf("规则 %s 不存在", id)
}

// nodeRef 批量操作时对一个节点的引用（普通节点/故障转移/链式代理/动态会话代理）
type nodeRef struct {
	id        string
	nodeType  string // rule / lb / chain / relay
	localPort int
	alias     string
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
	// 会话代理排在最后：其前置加速节点可能是同批启动的其他节点
	for i := range a.config.SessionRelays {
		if idSet[a.config.SessionRelays[i].ID] {
			refs = append(refs, nodeRef{a.config.SessionRelays[i].ID, "relay", a.config.SessionRelays[i].LocalPort, a.config.SessionRelays[i].Alias})
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

	// 内存开销随节点数增长，超出物理内存会让进程被系统终止、应用直接闪退。
	// 与其让用户把机器打崩，不如提前拦下并说明还能启动多少个。
	if err := a.checkNodeCapacity(len(refs)); err != nil {
		a.log(err.Error())
		return err
	}

	// 逐个启动（持锁，因为启动需读写 config 中的节点状态）。
	// 主要收益来自：端口检测已优化为轻量探测 + 仅在最后统一保存一次配置，
	// 避免了原先前端逐个调用时每次都 saveConfig 写盘的开销。
	a.mu.Lock()

	// 分片模式下普通节点合并为一次调谐：逐个启动会让同一分片反复重启，
	// 300 个节点原本要重建 300 次配置，合并后只重建一次。
	handledByShard := a.startPlainNodesInShardLocked(refs)

	for _, ref := range refs {
		switch ref.nodeType {
		case "rule":
			if handledByShard[ref.id] {
				continue
			}
			for i := range a.config.Rules {
				r := &a.config.Rules[i]
				if r.ID == ref.id && !r.Enabled {
					if err := a.runWithReleasedPortLocked(r.LocalPort, func() error { return a.startRuleInternal(r) }); err != nil {
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
					if err := a.runWithReleasedPortLocked(lb.LocalPort, func() error { return a.startLoadBalancerInternal(lb) }); err != nil {
						a.logError(fmt.Sprintf("启动故障转移 %s 失败", lb.Alias), err)
					}
					break
				}
			}
		case "chain":
			for i := range a.config.ChainProxies {
				c := &a.config.ChainProxies[i]
				if c.ID == ref.id && !c.Enabled {
					if err := a.runWithReleasedPortLocked(c.LocalPort, func() error { return a.startChainProxyInternal(c) }); err != nil {
						a.logError(fmt.Sprintf("启动链式代理 %s 失败", c.Alias), err)
					}
					break
				}
			}
		case "relay":
			for i := range a.config.SessionRelays {
				sr := &a.config.SessionRelays[i]
				if sr.ID == ref.id && !sr.Enabled {
					if err := a.runWithReleasedPortLocked(sr.LocalPort, func() error { return a.startSessionRelayInternal(sr) }); err != nil {
						a.logError(fmt.Sprintf("启动动态会话代理 %s 失败", sr.Alias), err)
						sr.LastError = err.Error()
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

// startPlainNodesInShardLocked 把普通节点合并成一次分片调谐，返回已处理的节点 ID。
//
// 只处理「直连出站」的普通节点：设置了全局前置代理时，节点要走链式配置
// （经前置节点出站），那是每节点一份定制配置，不能并进分片。
// 未启用分片模式时返回空集合，调用方按原路径逐个启动。
// 需已持有 a.mu 锁。
func (a *MyService) startPlainNodesInShardLocked(refs []nodeRef) map[string]bool {
	handled := make(map[string]bool)
	if a.processManager == nil || !a.processManager.ShardingEnabled() {
		return handled
	}
	// 前置代理在分片配置里是一份共享出站，各节点用 detour 指向它，
	// 因此设置了前置代理也能共享进程
	a.syncShardPreProxyLocked()

	wanted := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if ref.nodeType == "rule" {
			wanted[ref.id] = true
		}
	}
	if len(wanted) == 0 {
		return handled
	}

	var (
		batch   []*models.ProxyRule
		targets []*models.ProxyRule
	)
	for i := range a.config.Rules {
		r := &a.config.Rules[i]
		if !wanted[r.ID] || r.Enabled {
			continue
		}
		a.releasePortReservationLocked(r.LocalPort)
		batch = append(batch, r)
		targets = append(targets, r)
	}
	if len(batch) == 0 {
		return handled
	}

	// 确实有节点要经前置代理出站时，先确保它已启动——
	// 前置是链式/故障转移且没跑起来的话，这批节点全都会连不上
	if a.preProxyNeededByLocked(batch) {
		a.ensurePreProxyRunningLocked()
		a.syncShardPreProxyLocked()
	}

	failures, err := a.processManager.StartNodesInShard(batch)
	if err != nil {
		a.logError("批量启动节点失败", err)
		for _, r := range targets {
			// 整批失败：回退 StartNodesInShard 里的乐观置位
			r.Enabled = false
			r.Verifying = false
			a.reservePortLocked(r.LocalPort)
		}
		return handled
	}

	for _, r := range targets {
		handled[r.ID] = true
		if failErr, failed := failures[r.ID]; failed {
			a.logError(fmt.Sprintf("启动规则 %s 失败", r.Alias), failErr)
			// StartNodesInShard 会先把节点置为已启用（验证协程依赖这个标记），
			// 启动失败的要在这里回退，否则界面上会显示成运行中
			r.Enabled = false
			r.Verifying = false
			r.LastError = failErr.Error()
			a.reservePortLocked(r.LocalPort)
			continue
		}
		r.LastError = ""
	}
	return handled
}

// syncShardPreProxyLocked 把当前的前置代理及其生效范围同步给分片管理器。
//
// 前置代理在分片配置里只有一份出站，受影响的节点通过 detour 共用；
// 它或其生效范围变更时所有分片的出站都要重建，SetPreProxy 会做相应标记。
// 需已持有 a.mu 锁。
func (a *MyService) syncShardPreProxyLocked() {
	if a.processManager == nil {
		return
	}
	shards := a.processManager.Shards()
	if shards == nil {
		return
	}

	pre := a.getPreProxyRuleLocked()
	if pre == nil {
		shards.SetPreProxy(nil, nil, "")
		return
	}

	// 把生效范围快照进闭包：策略会在调谐时（可能已不持锁）被调用，
	// 直接读 a.config 会有并发问题
	groups := make(map[string]bool, len(a.config.PreProxyGroupIDs))
	for _, id := range a.config.PreProxyGroupIDs {
		groups[id] = true
	}
	excluded := make(map[string]bool, len(a.config.PreProxyExcludedIDs))
	for _, id := range a.config.PreProxyExcludedIDs {
		excluded[id] = true
	}
	preID := pre.ID

	policy := func(node *models.ProxyRule) bool {
		if node == nil || node.ID == preID || excluded[node.ID] {
			return false
		}
		if len(groups) == 0 {
			return true // 未限定分组时对全部节点生效
		}
		return groups[node.GroupID]
	}

	shards.SetPreProxy(pre, policy, a.preProxyScopeLocked())
}

// dropDeletedFromPreProxyScopeLocked 把已删除的节点/分组从前置代理的
// 生效范围里摘掉，避免留下永远匹配不到的残留 ID。需已持有 a.mu 锁。
func (a *MyService) dropDeletedFromPreProxyScopeLocked(nodeIDs, groupIDs map[string]bool) {
	if len(nodeIDs) > 0 && len(a.config.PreProxyExcludedIDs) > 0 {
		kept := a.config.PreProxyExcludedIDs[:0]
		for _, id := range a.config.PreProxyExcludedIDs {
			if !nodeIDs[id] {
				kept = append(kept, id)
			}
		}
		a.config.PreProxyExcludedIDs = kept
	}
	if len(groupIDs) > 0 && len(a.config.PreProxyGroupIDs) > 0 {
		kept := a.config.PreProxyGroupIDs[:0]
		for _, id := range a.config.PreProxyGroupIDs {
			if !groupIDs[id] {
				kept = append(kept, id)
			}
		}
		a.config.PreProxyGroupIDs = kept
	}
}

// preProxyScopeLocked 生成生效范围的指纹，用于判断范围是否变化。
// 分组与排除名单排序后拼接，避免顺序变动被误判为内容变更。
func (a *MyService) preProxyScopeLocked() string {
	groups := append([]string(nil), a.config.PreProxyGroupIDs...)
	excluded := append([]string(nil), a.config.PreProxyExcludedIDs...)
	sort.Strings(groups)
	sort.Strings(excluded)
	return strings.Join(groups, ",") + "|" + strings.Join(excluded, ",")
}

// stopPlainNodesInShardLocked 把普通节点合并成一次分片调谐，返回已处理的节点 ID。
// 需已持有 a.mu 锁。
func (a *MyService) stopPlainNodesInShardLocked(refs []nodeRef) map[string]bool {
	handled := make(map[string]bool)
	if a.processManager == nil || !a.processManager.ShardingEnabled() {
		return handled
	}

	var ids []string
	for _, ref := range refs {
		if ref.nodeType != "rule" {
			continue
		}
		if _, ok := a.processManager.Shards().ShardOf(ref.id); !ok {
			continue // 不在分片里（可能走的是链式配置），交给原路径处理
		}
		ids = append(ids, ref.id)
	}
	if len(ids) == 0 {
		return handled
	}

	if err := a.processManager.StopNodesInShard(ids); err != nil {
		a.logError("批量停止节点失败", err)
		return handled
	}
	for _, id := range ids {
		handled[id] = true
	}
	return handled
}

// StopNodes 批量停止节点，并发执行、只保存一次配置。
// stopConcurrency 按批量大小决定停止进程的并发度。
//
// 终止一个进程要 fork taskkill（Windows）或等待进程退出（Unix，最长 5 秒），
// 属于慢 IO，并发放大能显著缩短总耗时。上限 64 是为了避免一次 fork 出
// 几百个 taskkill 子进程把系统压垮。
func stopConcurrency(n int) int {
	switch {
	case n <= 16:
		return 8
	case n <= 100:
		return 32
	default:
		return 64
	}
}

func (a *MyService) StopNodes(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	a.mu.Lock()
	refs := a.collectNodeRefs(ids)
	// 分片模式下普通节点合并为一次调谐，避免同一分片被反复重启
	handledByShard := a.stopPlainNodesInShardLocked(refs)
	a.mu.Unlock()

	// 停止进程是慢操作（Windows 要 fork taskkill，Unix 要等进程退出），
	// 锁外并发执行。并发度随批量大小提升，否则几百个节点会等很久。
	var wg sync.WaitGroup
	sem := make(chan struct{}, stopConcurrency(len(refs)))
	for _, ref := range refs {
		if handledByShard[ref.id] {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ref nodeRef) {
			defer wg.Done()
			defer func() { <-sem }()
			// 会话代理运行在本进程内，没有对应的内核进程
			if ref.nodeType == "relay" {
				stopRelayInstance(ref.id)
				return
			}
			// 进程管理器初始化失败时（如内核目录不可写）为 nil，
			// 此时没有进程需要停，直接跳过而不是崩溃
			if a.processManager != nil {
				_ = a.processManager.Stop(ref.localPort)
			}
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
			// 停止时立刻清掉验证中标记：后台还排着队的探测任务要等一阵才轮到，
			// 不在这里清的话，这些节点会一直显示「验证中」（黄灯呼吸），
			// 直到整批队列跑完
			a.config.Rules[i].Verifying = false
			a.reservePortLocked(a.config.Rules[i].LocalPort)
		}
	}
	for i := range a.config.LoadBalancers {
		if refSet[a.config.LoadBalancers[i].ID] {
			a.config.LoadBalancers[i].Enabled = false
			a.config.LoadBalancers[i].ProcessID = 0
			a.reservePortLocked(a.config.LoadBalancers[i].LocalPort)
		}
	}
	for i := range a.config.ChainProxies {
		if refSet[a.config.ChainProxies[i].ID] {
			a.config.ChainProxies[i].Enabled = false
			a.config.ChainProxies[i].ProcessID = 0
			a.reservePortLocked(a.config.ChainProxies[i].LocalPort)
		}
	}
	for i := range a.config.SessionRelays {
		if refSet[a.config.SessionRelays[i].ID] {
			a.config.SessionRelays[i].Enabled = false
			a.config.SessionRelays[i].LastStopTime = time.Now().Format("2006-01-02 15:04:05")
			a.reservePortLocked(a.config.SessionRelays[i].LocalPort)
		}
	}
	err := a.saveConfig()
	a.mu.Unlock()

	a.emitEvent("loadRules", nil)
	a.log(fmt.Sprintf("批量停止完成，共 %d 个节点", len(refs)))
	return err
}

// emitEvent 向前端发事件。a.app 在单元测试或初始化失败时为 nil，
// 这里统一兜底，避免调用方各自判空或直接崩溃。
func (a *MyService) emitEvent(name string, data any) {
	if a.app == nil {
		return
	}
	a.app.Event.EmitEvent(&application.CustomEvent{Name: name, Data: data})
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

			if err := a.runWithReleasedPortLocked(rule.LocalPort, func() error { return a.startRuleInternal(rule) }); err != nil {
				// 启动失败：记录原因供前端显示，保持未启用
				rule.Enabled = false
				rule.LastError = err.Error()
				_ = a.saveConfig()
				a.app.Event.EmitEvent(&application.CustomEvent{Name: "ruleUpdated", Data: rule})
				return err
			}

			rule.Enabled = true
			rule.LastError = "" // 启动成功，清除旧的失败原因（真实IP 获取后会再确认）

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
			// 后台可能还在探测该节点，停止时一并清掉验证中标记
			rule.Verifying = false
			a.reservePortLocked(rule.LocalPort)

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
	if err := a.syncPortRegistryLocked(false); err != nil {
		return err
	}
	if a.configManager != nil {
		return a.configManager.Save(a.config)
	}
	return nil
}

// warnDynamicPortConflict 检测节点端口与系统动态端口范围是否重叠，重叠时告警。
//
// 只提示不阻断：范围是系统级设置，改动需要管理员权限并重启，
// 应用无权也不该替用户决定；节点在重叠状态下多数时候仍能正常工作。
func (a *MyService) warnDynamicPortConflict() {
	if a.config == nil {
		return
	}
	ports := make([]int, 0, len(a.config.Rules))
	for _, rule := range a.config.Rules {
		if rule.LocalPort > 0 {
			ports = append(ports, rule.LocalPort)
		}
	}
	if conflict := utils.CheckDynamicPortConflict(ports); conflict != nil {
		a.log("[端口警告] " + conflict.Message())
	}
}

func (a *MyService) portEntriesLocked() []portregistry.Entry {
	if a.config == nil || a.portRegistry == nil {
		return nil
	}
	entries := make([]portregistry.Entry, 0, len(a.config.Rules)+len(a.config.LoadBalancers)+len(a.config.ChainProxies)+len(a.config.SessionRelays))
	for _, rule := range a.config.Rules {
		entries = append(entries, a.portEntry("rule", rule.ID, rule.Alias, rule.LocalPort))
	}
	for _, lb := range a.config.LoadBalancers {
		entries = append(entries, a.portEntry("loadBalancer", lb.ID, lb.Alias, lb.LocalPort))
	}
	for _, chain := range a.config.ChainProxies {
		entries = append(entries, a.portEntry("chainProxy", chain.ID, chain.Alias, chain.LocalPort))
	}
	for _, sr := range a.config.SessionRelays {
		entries = append(entries, a.portEntry("sessionRelay", sr.ID, sr.Alias, sr.LocalPort))
	}
	return entries
}

func (a *MyService) portEntry(resourceType, resourceID, alias string, port int) portregistry.Entry {
	return portregistry.Entry{ExecutablePath: a.executablePath, ConfigPath: a.configPath, ResourceID: resourceID, ResourceType: resourceType, Alias: alias, Port: port}
}

func (a *MyService) claimPortLocked(resourceType, resourceID, alias string, port int) error {
	if a.portRegistry == nil {
		return nil
	}
	return a.portRegistry.Claim(a.portEntry(resourceType, resourceID, alias, port))
}

func (a *MyService) reserveTemporaryPortLocked(port int) bool {
	if a.portRegistry == nil {
		return true
	}
	entry := a.portEntry("reservation", fmt.Sprintf("reservation_%d_%d", os.Getpid(), time.Now().UnixNano()), "待添加节点", port)
	return a.portRegistry.ReserveTemporary(entry, 10*time.Minute) == nil
}

// reserveTemporaryPortsLocked 批量向全局端口注册表预留端口，返回预留成功的端口。
// 注册表不可用时视为全部成功（与 reserveTemporaryPortLocked 行为一致）。
func (a *MyService) reserveTemporaryPortsLocked(ports []int) []int {
	if a.portRegistry == nil || len(ports) == 0 {
		return ports
	}

	entries := make([]portregistry.Entry, 0, len(ports))
	now := time.Now().UnixNano()
	for i, port := range ports {
		entries = append(entries, a.portEntry("reservation", fmt.Sprintf("reservation_%d_%d_%d", os.Getpid(), now, i), "待添加节点", port))
	}

	reserved, err := a.portRegistry.ReserveTemporaryBatch(entries, 10*time.Minute)
	if err != nil {
		return nil
	}

	kept := make([]int, 0, len(reserved))
	for _, entry := range reserved {
		kept = append(kept, entry.Port)
	}
	return kept
}

func (a *MyService) releaseRegisteredPortLocked(resourceID string) {
	if a.portRegistry != nil {
		_ = a.portRegistry.Release(a.executablePath, a.configPath, resourceID)
	}
}

func (a *MyService) syncPortRegistryLocked(resolveConflicts bool) error {
	if a.portRegistry == nil || a.config == nil {
		return nil
	}
	conflicts, err := a.portRegistry.ReplaceOwner(a.executablePath, a.configPath, a.portEntriesLocked())
	a.setPortConflictsLocked(conflicts)
	if err != nil || len(conflicts) == 0 || !resolveConflicts {
		return err
	}
	for resourceID, conflict := range conflicts {
		newPort := a.allocateLocalPort()
		if newPort == 0 {
			return fmt.Errorf("%v；且没有可用端口可供重新分配", conflict)
		}
		if !a.setResourcePortLocked(resourceID, newPort) {
			return fmt.Errorf("全局端口注册表包含未知资源 %s", resourceID)
		}
		a.log(fmt.Sprintf("[端口冲突] %v，已自动改用端口 %d", conflict, newPort))
	}
	conflicts, err = a.portRegistry.ReplaceOwner(a.executablePath, a.configPath, a.portEntriesLocked())
	a.setPortConflictsLocked(conflicts)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		for _, conflict := range conflicts {
			return conflict
		}
	}
	return a.configManager.Save(a.config)
}

func (a *MyService) setPortConflictsLocked(conflicts map[string]*portregistry.ConflictError) {
	a.portConflicts = a.portConflicts[:0]
	for _, entry := range a.portEntriesLocked() {
		conflict := conflicts[entry.ResourceID]
		if conflict == nil {
			continue
		}
		a.portConflicts = append(a.portConflicts, models.PortConflict{
			ResourceID: entry.ResourceID, ResourceType: entry.ResourceType, Alias: entry.Alias, Port: entry.Port,
			OwnerExecutablePath: conflict.Owner.ExecutablePath, OwnerConfigPath: conflict.Owner.ConfigPath,
			OwnerResourceType: conflict.Owner.ResourceType, OwnerAlias: conflict.Owner.Alias,
		})
	}
}

func (a *MyService) hasPortConflictLocked(resourceID string) bool {
	for _, conflict := range a.portConflicts {
		if conflict.ResourceID == resourceID {
			return true
		}
	}
	return false
}

// GetPendingPortConflicts 获取启动时尚未处理的全部端口冲突。
func (a *MyService) GetPendingPortConflicts() []models.PortConflict {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]models.PortConflict(nil), a.portConflicts...)
}

// ResolvePortConflicts 仅为选中的冲突资源自动分配新端口。
func (a *MyService) ResolvePortConflicts(resourceIDs []string) ([]models.PortConflict, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	selected := make(map[string]bool, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		selected[resourceID] = true
	}
	for _, conflict := range append([]models.PortConflict(nil), a.portConflicts...) {
		if !selected[conflict.ResourceID] {
			continue
		}
		newPort := a.allocateLocalPort()
		if newPort == 0 {
			return a.portConflicts, fmt.Errorf("节点「%s」没有可用端口可供重新分配", conflict.Alias)
		}
		if !a.setResourcePortLocked(conflict.ResourceID, newPort) {
			return a.portConflicts, fmt.Errorf("节点 %s 不存在", conflict.ResourceID)
		}
		a.log(fmt.Sprintf("[端口冲突] 节点「%s」从端口 %d 自动改为 %d", conflict.Alias, conflict.Port, newPort))
	}
	if err := a.syncPortRegistryLocked(false); err != nil {
		return a.portConflicts, err
	}
	if a.configManager != nil {
		if err := a.configManager.Save(a.config); err != nil {
			return a.portConflicts, err
		}
	}
	if a.app != nil {
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	}
	return append([]models.PortConflict(nil), a.portConflicts...), nil
}

func (a *MyService) setResourcePortLocked(resourceID string, port int) bool {
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == resourceID {
			a.config.Rules[i].LocalPort = port
			return true
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == resourceID {
			a.config.LoadBalancers[i].LocalPort = port
			return true
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == resourceID {
			a.config.ChainProxies[i].LocalPort = port
			return true
		}
	}
	for i := range a.config.SessionRelays {
		if a.config.SessionRelays[i].ID == resourceID {
			a.config.SessionRelays[i].LocalPort = port
			return true
		}
	}
	return false
}

// log 输出日志
func (a *MyService) log(message string) {
	if a.app == nil {
		return
	}
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

	// 动态会话代理：仅在全量导出，或其前置节点已被导出时导出
	var exportRelays []models.SessionRelay
	for _, sr := range a.config.SessionRelays {
		if !exportAll {
			if sr.PreProxyNodeID != "" && !exportedRuleIDs[sr.PreProxyNodeID] && !exportedLBIDs[sr.PreProxyNodeID] {
				continue
			}
			if sr.PreProxyNodeID == "" && !selectedIDs[sr.ID] {
				continue
			}
		}
		exportRelays = append(exportRelays, sr)
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
	for _, sr := range exportRelays {
		if sr.GroupID != "" {
			referencedGroupIDs[sr.GroupID] = true
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

	cleanRelays := make([]models.SessionRelay, len(exportRelays))
	copy(cleanRelays, exportRelays)
	for i := range cleanRelays {
		cleanRelays[i].ResetRuntimeState()
		if !exportedGroupIDs[cleanRelays[i].GroupID] {
			cleanRelays[i].GroupID = ""
			cleanRelays[i].GroupName = ""
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
		SessionRelays: cleanRelays,
	}

	data, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化导出数据失败: %v", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("写入导出文件失败: %v", err)
	}

	a.log(fmt.Sprintf("配置已导出到: %s（规则 %d 条，分组 %d 个，故障转移 %d 个，链式代理 %d 个，会话代理 %d 个，订阅 %d 个）",
		filePath, len(cleanRules), len(exportGroups), len(cleanLBs), len(cleanChains), len(cleanRelays), len(exportSubs)))
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
	var importedRelays []models.SessionRelay

	if err := json.Unmarshal(data, &exportData); err == nil && exportData.Version != "" {
		// 新版格式
		importedRules = exportData.Rules
		importedGroups = exportData.Groups
		importedSubs = exportData.Subscriptions
		importedLBs = exportData.LoadBalancers
		importedChains = exportData.ChainProxies
		importedRelays = exportData.SessionRelays
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
		importedRelays = oldConfig.SessionRelays
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
	//
	// 逐个分配 ID 和端口在导入几十个节点时就会明显卡顿：generateUniqueRuleID
	// 每次全表扫描，allocateLocalPort 每次重扫端口段并往全局注册表写一次文件。
	// 这里按导入规模一次性预分配，循环里只取用。
	importRuleIDs := newRuleIDPool(a.config.Rules, len(importedRules))
	importPorts := newPortPool(a.allocateLocalPorts(len(importedRules)))
	usedPorts := a.usedLocalPorts()

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
		rule.ID = importRuleIDs.next(a.config.Rules)
		ruleIDMap[oldRuleID] = rule.ID
		rule.Enabled = false
		rule.ProcessID = 0
		rule.RealIP = ""

		// 分配可用端口：端口无效、已被其他节点占用、或系统不可用时重新分配。
		// usedPorts 复用同一份快照并随分配更新，避免每个节点都重建整表（O(n²)）。
		if rule.LocalPort <= 0 || usedPorts[rule.LocalPort] || !utils.CheckPortAvailable(rule.LocalPort) {
			rule.LocalPort = importPorts.next()
		}
		if rule.LocalPort > 0 {
			usedPorts[rule.LocalPort] = true
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

	// === 导入动态会话代理 ===
	for _, sr := range importedRelays {
		if err := sr.Validate(); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("动态会话代理校验失败 [%s]: %v", sr.Alias, err))
			continue
		}

		sr.ID = fmt.Sprintf("relay_%d", time.Now().UnixNano())
		sr.ResetRuntimeState()
		if newGID, ok := groupIDMap[sr.GroupID]; ok {
			sr.GroupID = newGID
			sr.GroupName = a.groupNameLocked(newGID)
		} else if sr.GroupID != "" {
			sr.GroupID = ""
			sr.GroupName = ""
		}

		// 前置节点引用映射到导入后的新 ID，无法映射时降级为直连
		if sr.PreProxyNodeID != "" {
			if newID, ok := ruleIDMap[sr.PreProxyNodeID]; ok {
				sr.PreProxyNodeID = newID
			} else if newID, ok := lbIDMap[sr.PreProxyNodeID]; ok {
				sr.PreProxyNodeID = newID
			} else {
				sr.PreProxyNodeID = ""
				result.Warnings = append(result.Warnings, fmt.Sprintf("动态会话代理 %s 的前置加速节点未包含在导入数据中，已改为直连上游", sr.Alias))
			}
		}

		if sr.LocalPort <= 0 || a.usedLocalPorts()[sr.LocalPort] || !utils.CheckPortAvailable(sr.LocalPort) {
			sr.LocalPort = a.allocateLocalPort()
		}

		a.config.SessionRelays = append(a.config.SessionRelays, sr)
		result.RelayImported++
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

	a.log(fmt.Sprintf("导入完成: 规则 %d 条（跳过重复 %d），分组 %d 个，订阅 %d 个，故障转移 %d 个，链式代理 %d 个，会话代理 %d 个",
		result.RulesImported, result.RulesSkipped, result.GroupsImported, result.SubsImported, result.LBImported, result.ChainImported, result.RelayImported))

	return result, nil
}

// logError 输出错误日志
func (a *MyService) logError(message string, err error) {
	text := fmt.Sprintf("[错误] %s: %v", message, err)
	// 同时写入日志过滤器，保证错误在日志面板可搜索、可按级别筛选
	if a.logFilter != nil {
		a.logFilter.AddLog(text)
	}
	if a.app == nil {
		return
	}
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "log", Data: text})
}

// 触发重新加载规则事件
func (a *MyService) loadRules() {
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
	return
}

// ==================== 端口检测相关 API ====================

// CheckPortAvailable 检查端口是否可用
func (a *MyService) CheckPortAvailable(port int) bool {
	a.mu.RLock()
	reservedBySelf := a.portReservations[port]
	a.mu.RUnlock()
	if reservedBySelf {
		return true
	}
	return utils.CheckPortAvailable(port)
}

// RecommendPort 推荐可用端口（默认从 11000 起）
func (a *MyService) RecommendPort() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	used := a.usedLocalPorts()
	for port := utils.DefaultRecommendPortStart; port < 65535; port++ {
		if !used[port] && a.reservePortLocked(port) && a.reserveTemporaryPortLocked(port) {
			return port
		}
		a.releasePortReservationLocked(port)
	}
	return 0
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
// groupID: 目标分组；为空表示按订阅名新建分组（保持旧行为）。
// 多个订阅可以指定同一个 groupID，从而把节点汇入同一分组统一管理。
func (a *MyService) AddSubscription(name, url string, autoUpdate bool, updateInterval int, updateMode string, updateProxyID string, groupID string) error {
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

	// 解析目标分组：指定了就复用已有分组，否则按订阅名新建
	var group *models.Group
	createdGroup := false
	if groupID != "" {
		existing, err := a.groupManager.GetGroup(groupID)
		if err != nil {
			return fmt.Errorf("指定的分组不存在: %v", err)
		}
		group = existing
	} else {
		created, err := a.groupManager.CreateGroupForSubscription(name, sub.ID)
		if err != nil {
			return fmt.Errorf("创建分组失败: %v", err)
		}
		group = created
		createdGroup = true
	}
	sub.GroupID = group.ID

	// 添加订阅并获取节点。
	// 注意：此处不能持有 a.mu 锁，订阅更新代理解析器需要读取配置（可能临时启动节点）。
	rules, err := a.subscriptionManager.AddSubscription(sub)
	if err != nil {
		if createdGroup {
			_ = a.groupManager.DeleteGroup(group.ID)
		}
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// 添加节点到配置。ID 和本地端口都批量分配：逐个分配在订阅有上万节点时是 O(n²)，
	// 且每个端口都会写一次全局端口注册表文件，实测会卡住界面很久。
	ids := generateUniqueRuleIDs(a.config.Rules, len(rules))
	ports := a.allocateLocalPorts(len(rules))
	if len(ports) < len(rules) {
		a.log(a.portShortageMessage(len(rules), len(ports)))
	}

	if cap(a.config.Rules)-len(a.config.Rules) < len(rules) {
		grown := make([]models.ProxyRule, len(a.config.Rules), len(a.config.Rules)+len(rules))
		copy(grown, a.config.Rules)
		a.config.Rules = grown
	}
	for i := range rules {
		rules[i].ID = ids[i]
		rules[i].Enabled = false
		rules[i].ProcessID = 0
		rules[i].GroupID = group.ID
		rules[i].GroupName = group.Name
		rules[i].SubscriptionID = sub.ID
		rules[i].SubscriptionURL = url
		rules[i].Source = "subscription"
		if i < len(ports) {
			rules[i].LocalPort = ports[i]
		}

		a.config.Rules = append(a.config.Rules, rules[i])
	}

	// 保存订阅；分组只在新建时追加，复用已有分组不能重复写入
	a.config.Subscriptions = append(a.config.Subscriptions, *sub)
	if createdGroup {
		a.config.Groups = append(a.config.Groups, *group)
	}

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

// EditSubscription 编辑订阅（名称/URL/自动更新/更新间隔/更新方式/所属分组）。
// 不触发订阅内容更新，只修改元数据；改 URL 会同步节点的订阅链接，
// 改分组会把该订阅的节点整体迁移过去，最后按新配置重设自动更新定时任务。
//
// groupID 为空表示保持当前分组不变。
func (a *MyService) EditSubscription(subID, name, url string, autoUpdate bool, updateInterval int, updateMode, updateProxyID, groupID string) error {
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
	oldGroupID := sub.GroupID

	// 目标分组：为空表示不变
	newGroupID := oldGroupID
	if groupID != "" && groupID != oldGroupID {
		if a.findGroupLocked(groupID) == nil {
			return fmt.Errorf("指定的分组不存在")
		}
		newGroupID = groupID
	}

	// 更新订阅字段
	sub.Name = name
	sub.URL = url
	sub.AutoUpdate = autoUpdate
	sub.UpdateInterval = updateInterval
	sub.UpdateMode = updateMode
	sub.UpdateProxyID = updateProxyID
	sub.GroupID = newGroupID

	// 改名：只有当分组是这个订阅独占时才跟着改名。
	// 一个分组可以汇集多个订阅，此时分组名是用户自己起的，不该被某个订阅的改名覆盖。
	if name != oldName && oldGroupID != "" && newGroupID == oldGroupID {
		if a.groupExclusiveToSubscriptionLocked(oldGroupID, subID) {
			for i := range a.config.Groups {
				if a.config.Groups[i].ID == oldGroupID {
					_ = a.groupManager.UpdateGroup(oldGroupID, name, a.config.Groups[i].Description)
					a.config.Groups[i].Name = name
					break
				}
			}
			for i := range a.config.Rules {
				if a.config.Rules[i].GroupID == oldGroupID {
					a.config.Rules[i].GroupName = name
				}
			}
		}
	}

	// 改分组：把该订阅的节点整体迁过去。
	// 只迁移属于本订阅的节点（按 SubscriptionID 匹配），同组其他订阅的节点不受影响。
	if newGroupID != oldGroupID {
		newGroup := a.findGroupLocked(newGroupID)
		moved := 0
		for i := range a.config.Rules {
			if a.config.Rules[i].SubscriptionID != subID {
				continue
			}
			a.config.Rules[i].GroupID = newGroupID
			if newGroup != nil {
				a.config.Rules[i].GroupName = newGroup.Name
			}
			moved++
		}
		a.log(fmt.Sprintf("[订阅] 「%s」已迁移到分组「%s」，%d 个节点随之移动", name, newGroup.Name, moved))
	}

	// 改 URL：同步该订阅下节点的订阅链接
	if url != oldURL {
		for i := range a.config.Rules {
			if a.config.Rules[i].SubscriptionID == subID {
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

	// 删除该订阅的所有节点。
	// 按 SubscriptionID 而不是 GroupID 匹配：一个分组可能汇集了多个订阅，
	// 按分组删会连带删掉同组其他订阅的节点。
	// 历史数据没有 SubscriptionID，回退到"分组内的订阅节点且分组为该订阅独占"的判断。
	exclusive := a.groupExclusiveToSubscriptionLocked(groupID, subID)
	belongsToSub := func(rule *models.ProxyRule) bool {
		if rule.SubscriptionID != "" {
			return rule.SubscriptionID == subID
		}
		return exclusive && rule.GroupID == groupID && rule.Source == "subscription"
	}

	newRules := make([]models.ProxyRule, 0, len(a.config.Rules))
	removed := 0
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if !belongsToSub(rule) {
			newRules = append(newRules, *rule)
			continue
		}
		if rule.Enabled {
			_ = a.processManager.Stop(rule.LocalPort)
		}
		a.releasePortReservationLocked(rule.LocalPort)
		removed++
	}
	a.config.Rules = newRules

	// 删除订阅
	a.config.Subscriptions = append(
		a.config.Subscriptions[:subIndex],
		a.config.Subscriptions[subIndex+1:]...,
	)

	// 分组只在没有其他订阅使用、且不再有节点时才删除。
	// 多个订阅共用一个分组时，删掉其中一个订阅不该连累分组本身。
	if groupID != "" && a.groupRemovableLocked(groupID) {
		_ = a.groupManager.DeleteGroup(groupID)
		newGroups := make([]models.Group, 0, len(a.config.Groups))
		for _, group := range a.config.Groups {
			if group.ID != groupID {
				newGroups = append(newGroups, group)
			}
		}
		a.config.Groups = newGroups
	}

	a.subscriptionManager.RemoveSubscription(subID)
	a.log(fmt.Sprintf("[订阅] 已删除订阅，移除 %d 个节点", removed))

	return a.saveConfig()
}

// groupExclusiveToSubscriptionLocked 判断分组是否只被该订阅使用。需已持有 a.mu。
func (a *MyService) groupExclusiveToSubscriptionLocked(groupID, subID string) bool {
	if groupID == "" {
		return false
	}
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].GroupID == groupID && a.config.Subscriptions[i].ID != subID {
			return false
		}
	}
	return true
}

// groupRemovableLocked 判断分组是否可以随订阅一起删除：
// 没有其他订阅指向它，且组内已无残留节点（手动添加的节点也算）。需已持有 a.mu。
func (a *MyService) groupRemovableLocked(groupID string) bool {
	for i := range a.config.Subscriptions {
		if a.config.Subscriptions[i].GroupID == groupID {
			return false
		}
	}
	for i := range a.config.Rules {
		if a.config.Rules[i].GroupID == groupID {
			return false
		}
	}
	return true
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

	// 获取该订阅的现有节点。存下标而不是指针：下面 append 新节点会让底层数组重新分配，
	// 指向旧数组的指针写入就会丢失。
	//
	// 只认属于本订阅的节点：一个分组可以汇集多个订阅，若按 GroupID 匹配，
	// 本次更新会把同组其他订阅的节点当成"自己的旧节点"接管甚至删掉。
	// 历史数据没有 SubscriptionID，回退到分组匹配（此时分组必为该订阅独占）。
	exclusive := a.groupExclusiveToSubscriptionLocked(targetSub.GroupID, subID)
	// 同一个标识可能对应多条历史记录（此前重复添加攒下来的），用切片存全部下标：
	// 匹配时消费一条，剩下的会落进 oldRules 被当作"已失效"清理掉，
	// 这样一次更新就能把历史重复项收敛回一条。
	oldRules := make(map[string][]int, len(a.config.Rules))
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		own := rule.SubscriptionID == subID ||
			(rule.SubscriptionID == "" && exclusive && rule.GroupID == targetSub.GroupID && rule.Source == "subscription")
		if !own {
			continue
		}
		key := rule.SubscriptionIdentity()
		oldRules[key] = append(oldRules[key], i)
	}

	group := a.findGroupLocked(targetSub.GroupID)

	// 先挑出真正的新增节点，再批量分配 ID 和端口，
	// 避免逐个分配时的全表扫描和逐端口写注册表文件（订阅上万节点时会卡很久）。
	added := make([]int, 0, len(newRules))
	// 订阅内部也可能出现完全相同的两条，去重避免把重复项写进配置
	seen := make(map[string]struct{}, len(newRules))
	for i := range newRules {
		key := newRules[i].SubscriptionIdentity()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		if idxs, exists := oldRules[key]; exists && len(idxs) > 0 {
			// 节点已存在，更新配置但保留状态；只消费一条，多余的重复项留待清理
			idx := idxs[0]
			a.config.Rules[idx].Alias = newRules[i].Alias
			a.config.Rules[idx].Settings = newRules[i].Settings
			// 顺带补齐历史节点缺失的订阅归属，后续更新就能精确匹配
			a.config.Rules[idx].SubscriptionID = subID
			if len(idxs) == 1 {
				delete(oldRules, key)
			} else {
				oldRules[key] = idxs[1:]
			}
		} else {
			added = append(added, i)
		}
	}

	ids := generateUniqueRuleIDs(a.config.Rules, len(added))
	ports := a.allocateLocalPorts(len(added))
	if len(ports) < len(added) {
		a.log(a.portShortageMessage(len(added), len(ports)))
	}

	groupName := ""
	if group != nil {
		groupName = group.Name
	}
	for n, i := range added {
		newRules[i].ID = ids[n]
		newRules[i].Enabled = false
		newRules[i].GroupID = targetSub.GroupID
		newRules[i].GroupName = groupName
		newRules[i].SubscriptionID = subID
		newRules[i].SubscriptionURL = targetSub.URL
		newRules[i].Source = "subscription"
		newRules[i].LocalPort = 0
		if n < len(ports) {
			newRules[i].LocalPort = ports[n]
		}

		a.config.Rules = append(a.config.Rules, newRules[i])
	}

	// 删除订阅里已不存在的节点，以及历史遗留的重复项。
	// 一次过滤代替逐个查找+切片删除（原来是 O(n²)）。
	if len(oldRules) > 0 {
		staleIDs := make(map[string]struct{}, len(oldRules))
		for _, idxs := range oldRules {
			for _, idx := range idxs {
				stale := &a.config.Rules[idx]
				if stale.Enabled {
					_ = a.processManager.Stop(stale.LocalPort)
				}
				a.releasePortReservationLocked(stale.LocalPort)
				staleIDs[stale.ID] = struct{}{}
			}
		}

		kept := a.config.Rules[:0]
		for i := range a.config.Rules {
			if _, drop := staleIDs[a.config.Rules[i].ID]; drop {
				continue
			}
			kept = append(kept, a.config.Rules[i])
		}
		a.config.Rules = kept
		a.log(fmt.Sprintf("[订阅] 清理 %d 个已失效或重复的节点", len(staleIDs)))
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
// findGroupLocked 按 ID 查找配置中的分组，找不到返回 nil。需已持有 a.mu。
func (a *MyService) findGroupLocked(groupID string) *models.Group {
	for i := range a.config.Groups {
		if a.config.Groups[i].ID == groupID {
			return &a.config.Groups[i]
		}
	}
	return nil
}

// GetGroups 返回所有分组。
//
// 直接读 a.config.Groups 而不是 groupManager 的缓存：config 是唯一的真相来源，
// 而 groupManager 只在启动/导入配置时同步一次，用它的缓存容易读到过期数据。
func (a *MyService) GetGroups() []models.Group {
	a.mu.RLock()
	defer a.mu.RUnlock()
	groups := make([]models.Group, len(a.config.Groups))
	copy(groups, a.config.Groups)
	return groups
}

// UpdateGroup 更新分组名称和描述，并同步组内节点显示的分组名。
func (a *MyService) UpdateGroup(groupID, name, description string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("分组名称不能为空")
	}
	if err := a.groupManager.UpdateGroup(groupID, name, description); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.config.Groups {
		if a.config.Groups[i].ID == groupID {
			a.config.Groups[i].Name = name
			a.config.Groups[i].Description = description
			break
		}
	}
	for i := range a.config.Rules {
		if a.config.Rules[i].GroupID == groupID {
			a.config.Rules[i].GroupName = name
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].GroupID == groupID {
			a.config.LoadBalancers[i].GroupName = name
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].GroupID == groupID {
			a.config.ChainProxies[i].GroupName = name
		}
	}
	return a.saveConfig()
}

// DeleteGroup 删除分组
// DeleteGroup 删除分组（级联）：停止并删除分组内所有节点，再删除分组本身。
// 调用方（前端）应先向用户确认。
func (a *MyService) DeleteGroup(groupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	stopped, removed := 0, 0

	// 先停止并删除该分组下的故障转移与链式代理（它们可能引用分组内的普通节点）
	remainingLBs := make([]models.LoadBalanceNode, 0, len(a.config.LoadBalancers))
	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.GroupID == groupID {
			if lb.Enabled {
				if err := a.processManager.Stop(lb.LocalPort); err != nil {
					a.logError(fmt.Sprintf("停止故障转移 %s 失败", lb.Alias), err)
				} else {
					stopped++
				}
			}
			a.releasePortReservationLocked(lb.LocalPort)
			removed++
			continue
		}
		remainingLBs = append(remainingLBs, *lb)
	}
	a.config.LoadBalancers = remainingLBs

	remainingChains := make([]models.ChainProxy, 0, len(a.config.ChainProxies))
	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.GroupID == groupID {
			if chain.Enabled {
				if err := a.processManager.Stop(chain.LocalPort); err != nil {
					a.logError(fmt.Sprintf("停止链式代理 %s 失败", chain.Alias), err)
				} else {
					stopped++
				}
			}
			a.releasePortReservationLocked(chain.LocalPort)
			removed++
			continue
		}
		remainingChains = append(remainingChains, *chain)
	}
	a.config.ChainProxies = remainingChains

	// 动态会话代理运行在本进程内，停止方式与外部内核进程不同
	remainingRelays := make([]models.SessionRelay, 0, len(a.config.SessionRelays))
	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.GroupID == groupID {
			if sr.Enabled {
				stopRelayInstance(sr.ID)
				stopped++
			}
			a.releasePortReservationLocked(sr.LocalPort)
			a.releaseRegisteredPortLocked(sr.ID)
			removed++
			continue
		}
		remainingRelays = append(remainingRelays, *sr)
	}
	a.config.SessionRelays = remainingRelays

	// 再停止并删除该分组下的普通节点
	remaining := make([]models.ProxyRule, 0, len(a.config.Rules))
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.GroupID == groupID {
			if rule.Enabled {
				if err := a.processManager.Stop(rule.LocalPort); err != nil {
					a.logError(fmt.Sprintf("停止规则 %s 失败", rule.Alias), err)
				} else {
					stopped++
				}
			}
			a.releasePortReservationLocked(rule.LocalPort)
			removed++
			continue // 不加入 remaining，即删除
		}
		remaining = append(remaining, *rule)
	}
	a.config.Rules = remaining

	// 删除分组
	if err := a.groupManager.DeleteGroup(groupID); err != nil {
		return err
	}
	for i := range a.config.Groups {
		if a.config.Groups[i].ID == groupID {
			a.config.Groups = append(a.config.Groups[:i], a.config.Groups[i+1:]...)
			break
		}
	}
	// 分组已删除，从前置代理的生效范围里摘掉，避免留下匹配不到的残留 ID
	a.dropDeletedFromPreProxyScopeLocked(nil, map[string]bool{groupID: true})
	a.syncShardPreProxyLocked()

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("已删除分组，停止 %d 个运行中节点，删除 %d 个节点", stopped, removed))
	return nil
}

// StartAllRulesInGroup 启动分组中的所有节点（普通节点 + 故障转移 + 链式代理）
func (a *MyService) StartAllRulesInGroup(groupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	count := 0
	// 普通节点
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.GroupID == groupID && !rule.Enabled {
			if err := a.runWithReleasedPortLocked(rule.LocalPort, func() error { return a.startRuleInternal(rule) }); err != nil {
				a.logError(fmt.Sprintf("启动规则 %s 失败", rule.Alias), err)
				continue
			}
			rule.Enabled = true
			count++
		}
	}
	// 故障转移
	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.GroupID == groupID && !lb.Enabled {
			if err := a.runWithReleasedPortLocked(lb.LocalPort, func() error { return a.startLoadBalancerInternal(lb) }); err != nil {
				a.logError(fmt.Sprintf("启动故障转移 %s 失败", lb.Alias), err)
				continue
			}
			count++
		}
	}
	// 链式代理
	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.GroupID == groupID && !chain.Enabled {
			if err := a.runWithReleasedPortLocked(chain.LocalPort, func() error { return a.startChainProxyInternal(chain) }); err != nil {
				a.logError(fmt.Sprintf("启动链式代理 %s 失败", chain.Alias), err)
				continue
			}
			count++
		}
	}
	// 动态会话代理放最后：其前置加速节点可能是上面刚启动的节点
	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.GroupID == groupID && !sr.Enabled {
			if err := a.runWithReleasedPortLocked(sr.LocalPort, func() error { return a.startSessionRelayInternal(sr) }); err != nil {
				a.logError(fmt.Sprintf("启动动态会话代理 %s 失败", sr.Alias), err)
				sr.LastError = err.Error()
				continue
			}
			count++
		}
	}

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("已启动分组中的 %d 个节点", count))
	return nil
}

// StopAllRulesInGroup 停止分组中的所有节点（普通节点 + 故障转移 + 链式代理）
func (a *MyService) StopAllRulesInGroup(groupID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	count := 0
	// 普通节点
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.GroupID == groupID && rule.Enabled {
			if err := a.processManager.Stop(rule.LocalPort); err != nil {
				a.logError(fmt.Sprintf("停止规则 %s 失败", rule.Alias), err)
				continue
			}
			rule.Enabled = false
			a.reservePortLocked(rule.LocalPort)
			count++
		}
	}
	// 故障转移
	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.GroupID == groupID && lb.Enabled {
			if err := a.processManager.Stop(lb.LocalPort); err != nil {
				a.logError(fmt.Sprintf("停止故障转移 %s 失败", lb.Alias), err)
			}
			lb.Enabled = false
			lb.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
			a.reservePortLocked(lb.LocalPort)
			count++
		}
	}
	// 链式代理
	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.GroupID == groupID && chain.Enabled {
			if err := a.processManager.Stop(chain.LocalPort); err != nil {
				a.logError(fmt.Sprintf("停止链式代理 %s 失败", chain.Alias), err)
			}
			chain.Enabled = false
			chain.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
			a.reservePortLocked(chain.LocalPort)
			count++
		}
	}
	// 动态会话代理
	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.GroupID == groupID && sr.Enabled {
			stopRelayInstance(sr.ID)
			sr.Enabled = false
			sr.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
			a.reservePortLocked(sr.LocalPort)
			count++
		}
	}

	if err := a.saveConfig(); err != nil {
		return err
	}

	a.log(fmt.Sprintf("已停止分组中的 %d 个节点", count))
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

// ExportShareLinks 将指定普通节点导出为每行一个的标准分享链接。
func (a *MyService) ExportShareLinks(ruleIDs []string) (string, error) {
	if len(ruleIDs) == 0 {
		return "", fmt.Errorf("请选择要复制的普通节点")
	}
	idSet := make(map[string]bool, len(ruleIDs))
	for _, id := range ruleIDs {
		idSet[id] = true
	}
	a.mu.RLock()
	rules := make([]models.ProxyRule, 0, len(ruleIDs))
	for _, rule := range a.config.Rules {
		if idSet[rule.ID] {
			rules = append(rules, rule)
		}
	}
	a.mu.RUnlock()
	if len(rules) == 0 {
		return "", fmt.Errorf("未找到可复制的普通节点")
	}

	encoder := parser.NewShareLinkParser()
	links := make([]string, 0, len(rules))
	for _, rule := range rules {
		link, err := encoder.EncodeLink(rule)
		if err != nil {
			return "", fmt.Errorf("导出节点 %s 失败: %v", rule.Alias, err)
		}
		links = append(links, link)
	}
	return strings.Join(links, "\n"), nil
}

// ImportShareLinks 批量导入分享链接（返回详细结果）。
// groupID：导入到的现有分组 ID（空=不分组）；
// newGroupName：非空时新建一个手动分组并将导入节点归入（优先于 groupID）。
func (a *MyService) ImportShareLinks(text, groupID, newGroupName string) (*models.ImportShareResult, error) {
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

	// 若指定新建分组，则先创建
	newGroupName = strings.TrimSpace(newGroupName)
	if newGroupName != "" {
		group, err := a.groupManager.CreateGroup(newGroupName, "批量导入", "manual")
		if err != nil {
			return result, fmt.Errorf("创建分组失败: %v", err)
		}
		a.config.Groups = append(a.config.Groups, *group)
		groupID = group.ID
	}

	// 解析目标分组名称（用于回填 GroupName）
	var groupName string
	if groupID != "" {
		for _, g := range a.config.Groups {
			if g.ID == groupID {
				groupName = g.Name
				break
			}
		}
		if groupName == "" {
			// 指定的分组不存在，视为不分组
			groupID = ""
		}
	}

	// 先过一遍校验，挑出真正要导入的节点
	valid := make([]int, 0, len(rules))
	for i := range rules {
		if err := rules[i].Validate(); err != nil {
			result.FailCount++
			result.Errors = append(result.Errors, fmt.Sprintf("校验失败 [%s]: %v", rules[i].Alias, err))
			continue
		}
		valid = append(valid, i)
	}

	// ID 和端口都批量分配。
	// 逐个分配时，generateUniqueRuleID 每次全表扫描（O(n²)），
	// allocateLocalPort 每次都重建已用端口表、重新扫端口段，并且要抢文件锁
	// 往全局端口注册表写一次——实测 50 个节点就要 773ms，注册表条目多时更久，
	// 而这一切都在持有全局锁的状态下进行，界面表现为"卡住"。
	ids := generateUniqueRuleIDs(a.config.Rules, len(valid))
	ports := a.allocateLocalPorts(len(valid))
	if len(ports) < len(valid) {
		a.log(a.portShortageMessage(len(valid), len(ports)))
	}

	for n, i := range valid {
		rules[i].ID = ids[n]
		rules[i].Enabled = false
		rules[i].ProcessID = 0
		rules[i].Source = "manual"
		rules[i].GroupID = groupID
		rules[i].GroupName = groupName
		rules[i].LocalPort = 0
		if n < len(ports) {
			rules[i].LocalPort = ports[n]
		}
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
	if lb.LocalPort > 0 {
		if err := a.claimPortLocked("loadBalancer", lb.ID, lb.Alias, lb.LocalPort); err != nil {
			return err
		}
		if !a.reservePortLocked(lb.LocalPort) {
			a.releaseRegisteredPortLocked(lb.ID)
			return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", lb.LocalPort)
		}
	} else {
		lb.LocalPort = a.allocateLocalPort()
	}
	if lb.LocalPort == 0 {
		return fmt.Errorf("没有可用的本地端口")
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
			a.releasePortReservationLocked(lb.LocalPort)
			a.config.LoadBalancers = append(a.config.LoadBalancers[:i], a.config.LoadBalancers[i+1:]...)
			a.clearRelayPreProxyRefLocked(id)
			return a.saveConfig()
		}
	}

	return fmt.Errorf("故障转移节点不存在")
}

// UpdateLoadBalancer 更新故障转移节点
func (a *MyService) UpdateLoadBalancer(lb models.LoadBalanceNode) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(lb.NodeIDs) == 0 {
		return fmt.Errorf("故障转移节点需要至少一个子节点")
	}

	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == lb.ID {
			wasEnabled := a.config.LoadBalancers[i].Enabled
			oldPort := a.config.LoadBalancers[i].LocalPort
			if lb.LocalPort != oldPort {
				if lb.LocalPort <= 0 {
					return fmt.Errorf("本地端口 %d 无效", lb.LocalPort)
				}
				if err := a.claimPortLocked("loadBalancer", lb.ID, lb.Alias, lb.LocalPort); err != nil {
					return err
				}
				if !a.reservePortLocked(lb.LocalPort) {
					_ = a.claimPortLocked("loadBalancer", lb.ID, a.config.LoadBalancers[i].Alias, oldPort)
					return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", lb.LocalPort)
				}
				a.releasePortReservationLocked(oldPort)
			}

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

	// 构建故障转移配置（含 Hysteria2/TUIC 子节点时自动切换 sing-box 内核），附加流量统计 API。
	// 该故障转移自身就是前置代理时不套自己——否则会生成指向本节点尚未监听端口的出站。
	var preProxy *models.ProxyRule
	if a.config.PreProxyNodeID != lb.ID {
		preProxy = a.getPreProxyRuleLocked()
	}
	apiPort := process.FindApiPort(lb.LocalPort)
	configJSON, coreType, err := buildLoadBalanceConfigJSON(lb, lb.LocalPort, nodes, apiPort, preProxy)
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
		ConfigJSON:  configJSON,
		CoreType:    coreType,
		ApiPort:     apiPort,
		FetchRealIP: true,
	}); err != nil {
		return err
	}

	lb.Enabled = true
	lb.LastError = ""
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

			if err := a.runWithReleasedPortLocked(lb.LocalPort, func() error { return a.startLoadBalancerInternal(lb) }); err != nil {
				// 启动失败：记录原因供前端显示，保持未启用
				lb.Enabled = false
				lb.LastError = err.Error()
				_ = a.saveConfig()
				a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
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
			a.reservePortLocked(lb.LocalPort)
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
	if chain.LocalPort > 0 {
		if err := a.claimPortLocked("chainProxy", chain.ID, chain.Alias, chain.LocalPort); err != nil {
			return err
		}
		if !a.reservePortLocked(chain.LocalPort) {
			a.releaseRegisteredPortLocked(chain.ID)
			return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", chain.LocalPort)
		}
	} else {
		chain.LocalPort = a.allocateLocalPort()
	}
	if chain.LocalPort == 0 {
		return fmt.Errorf("没有可用的本地端口")
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
			a.releasePortReservationLocked(chain.LocalPort)
			a.config.ChainProxies = append(a.config.ChainProxies[:i], a.config.ChainProxies[i+1:]...)
			a.clearRelayPreProxyRefLocked(id)
			return a.saveConfig()
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// UpdateChainProxy 更新链式代理
func (a *MyService) UpdateChainProxy(chain models.ChainProxy) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(chain.ChainNodes) < 2 {
		return fmt.Errorf("链式代理需要至少2个节点")
	}

	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == chain.ID {
			wasEnabled := a.config.ChainProxies[i].Enabled
			oldPort := a.config.ChainProxies[i].LocalPort
			if chain.LocalPort != oldPort {
				if chain.LocalPort <= 0 {
					return fmt.Errorf("本地端口 %d 无效", chain.LocalPort)
				}
				if err := a.claimPortLocked("chainProxy", chain.ID, chain.Alias, chain.LocalPort); err != nil {
					return err
				}
				if !a.reservePortLocked(chain.LocalPort) {
					_ = a.claimPortLocked("chainProxy", chain.ID, a.config.ChainProxies[i].Alias, oldPort)
					return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", chain.LocalPort)
				}
				a.releasePortReservationLocked(oldPort)
			}

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

	// 全局前置代理：插到链最前端（已在链中则不重复添加）。
	// 该链自身就是前置代理时不能再套自己——那会生成一个指向本链尚未监听的
	// 端口的出站，整条链直接不通。
	if a.config.PreProxyNodeID != chain.ID {
		chainRules = a.prependPreProxyLocked(chainRules)
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
		ConfigJSON:  configJSON,
		CoreType:    coreType,
		ApiPort:     apiPort,
		FetchRealIP: true,
	}); err != nil {
		return err
	}

	chain.Enabled = true
	chain.LastError = ""
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

			if err := a.runWithReleasedPortLocked(chain.LocalPort, func() error { return a.startChainProxyInternal(chain) }); err != nil {
				// 启动失败：记录原因供前端显示，保持未启用
				chain.Enabled = false
				chain.LastError = err.Error()
				_ = a.saveConfig()
				a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
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
			a.reservePortLocked(chain.LocalPort)
			return a.saveConfig()
		}
	}

	return fmt.Errorf("链式代理不存在")
}

// ==================== 全局前置代理 ====================

// getPreProxyRuleLocked 返回当前配置的全局前置代理节点（调用方需已持有锁）。
// 未配置、节点不存在时返回 nil。
func (a *MyService) getPreProxyRuleLocked() *models.ProxyRule {
	id := a.config.PreProxyNodeID
	if id == "" || !a.preProxyEnabledLocked() {
		return nil
	}
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == id {
			return &a.config.Rules[i]
		}
	}
	// 链式代理/故障转移也可以做前置：它们对外提供本地混合端口，
	// 用一个指向该端口的 socks 出站即可接入，无需把整条链复制进配置。
	// 代价是必须先启动它们——本地端口没起来时这一跳不通。
	if composite := a.compositePreProxyLocked(id); composite != nil {
		return composite
	}
	return nil
}

// preProxyEnabledLocked 前置代理是否启用。
// 兼容旧配置：没有 PreProxyEnabled 字段的老配置，选了节点即视为启用。
func (a *MyService) preProxyEnabledLocked() bool {
	return a.config.PreProxyEnabled
}

// preProxyNeededByLocked 这批节点里是否有需要经前置代理出站的。
// 用于避免「没有节点用得上前置代理时仍把它拉起来」。需已持有 a.mu 锁。
func (a *MyService) preProxyNeededByLocked(rules []*models.ProxyRule) bool {
	for _, rule := range rules {
		if a.preProxyAppliesToLocked(rule) {
			return true
		}
	}
	return false
}

// ensurePreProxyRunningLocked 确保前置代理已启动。
//
// 前置代理若是链式代理/故障转移，它要先跑起来、占住本地端口，其他节点才能
// 经它出站。忘记先启动会让所有受影响的节点报「代理连接失败」，而问题其实
// 不在这些节点身上——这种错法很难自己排查，所以这里代为拉起。
// 已在运行、未启用前置代理、或前置是普通节点（随分片一起启动）时都无需处理。
// 需已持有 a.mu 锁。
func (a *MyService) ensurePreProxyRunningLocked() {
	id := a.config.PreProxyNodeID
	if id == "" || !a.preProxyEnabledLocked() {
		return
	}

	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.ID != id {
			continue
		}
		if chain.Enabled {
			return // 已经在跑
		}
		a.log(fmt.Sprintf("[前置代理] 链式代理 %s 尚未启动，正在自动启动", chain.Alias))
		// 必须经 runWithReleasedPortLocked：未启动节点的端口被本实例记账占着，
		// 不先释放的话进程管理器会认为端口已被占用而拒绝启动
		err := a.runWithReleasedPortLocked(chain.LocalPort, func() error {
			return a.startChainProxyInternal(chain)
		})
		if err != nil {
			a.logError(fmt.Sprintf("自动启动前置代理 %s 失败", chain.Alias), err)
			return
		}
		chain.Enabled = true
		_ = a.saveConfig()
		a.emitEvent("loadRules", nil)
		return
	}

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.ID != id {
			continue
		}
		if lb.Enabled {
			return
		}
		a.log(fmt.Sprintf("[前置代理] 故障转移 %s 尚未启动，正在自动启动", lb.Alias))
		// 同上：先释放端口记账，否则会被判为端口已占用
		err := a.runWithReleasedPortLocked(lb.LocalPort, func() error {
			return a.startLoadBalancerInternal(lb)
		})
		if err != nil {
			a.logError(fmt.Sprintf("自动启动前置代理 %s 失败", lb.Alias), err)
			return
		}
		lb.Enabled = true
		_ = a.saveConfig()
		a.emitEvent("loadRules", nil)
		return
	}
}

// compositePreProxyLocked 把链式代理/故障转移包装成指向其本地端口的 socks 节点。
// 找不到或未启动时返回 nil——未启动的复合代理没有可用端口，接上去只会全链路不通。
func (a *MyService) compositePreProxyLocked(id string) *models.ProxyRule {
	makeLocal := func(alias string, port int, enabled bool) *models.ProxyRule {
		if !enabled || port <= 0 {
			return nil
		}
		return &models.ProxyRule{
			ID:         id,
			Alias:      alias,
			Protocol:   "socks",
			ServerAddr: "127.0.0.1",
			ServerPort: port,
		}
	}
	for i := range a.config.ChainProxies {
		if c := &a.config.ChainProxies[i]; c.ID == id {
			return makeLocal(c.Alias, c.LocalPort, c.Enabled)
		}
	}
	for i := range a.config.LoadBalancers {
		if lb := &a.config.LoadBalancers[i]; lb.ID == id {
			return makeLocal(lb.Alias, lb.LocalPort, lb.Enabled)
		}
	}
	return nil
}

// preProxyAppliesToLocked 判断某个节点是否应经前置代理出站（调用方需已持有锁）。
//
// 规则依次为：
//  1. 前置代理节点自身直连——否则 detour 指向自己会成环
//  2. 在例外名单里的节点直连
//  3. 未配置生效范围时对全部节点生效（兼容旧配置，此前前置代理是全局的）
//  4. 配置了生效范围时，只有落在这些分组里的节点才走前置代理
func (a *MyService) preProxyAppliesToLocked(rule *models.ProxyRule) bool {
	if rule == nil || a.config.PreProxyNodeID == "" || !a.preProxyEnabledLocked() {
		return false
	}
	if rule.ID == a.config.PreProxyNodeID {
		return false
	}
	for _, id := range a.config.PreProxyExcludedIDs {
		if id == rule.ID {
			return false
		}
	}
	if len(a.config.PreProxyGroupIDs) == 0 {
		return true
	}
	for _, groupID := range a.config.PreProxyGroupIDs {
		if groupID == rule.GroupID {
			return true
		}
	}
	return false
}

// prependPreProxyLocked 将全局前置代理插入链首（已在链中则不重复）。
func (a *MyService) prependPreProxyLocked(chainRules []*models.ProxyRule) []*models.ProxyRule {
	pre := a.getPreProxyRuleLocked()
	if pre == nil {
		return chainRules
	}
	for _, r := range chainRules {
		if r != nil && r.ID == pre.ID {
			return chainRules
		}
	}
	return append([]*models.ProxyRule{pre}, chainRules...)
}

// startRuleInternal 启动普通节点（内部方法，不加锁）。
// 若配置了全局前置代理且目标不是前置节点本身，则按 前置→落地 两跳链启动。
func (a *MyService) startRuleInternal(rule *models.ProxyRule) error {
	// 进程管理器以 LocalPort 为键，端口为 0 会写坏 config_0.json 并污染 processes[0]，
	// 提前拦下比事后排查容易得多
	if rule.LocalPort <= 0 {
		return fmt.Errorf("节点「%s」没有分配本地端口，请先在节点设置中指定端口", rule.Alias)
	}
	// 提前拦下配置不完整的节点，避免内核加载失败后进程刚起来就退出，
	// 用户只看到"启动成功"却连不上
	if err := rule.ValidateForXray(); err != nil {
		return fmt.Errorf("节点「%s」%v", rule.Alias, err)
	}

	pre := a.getPreProxyRuleLocked()

	// 分片模式下前置代理是配置里的一份共享出站，节点用 detour 指向它，
	// 因此设置了前置代理也不必退化成一节点一进程。
	if a.processManager != nil && a.processManager.ShardingEnabled() {
		// 前置是链式/故障转移时先拉起它，否则本节点连不上
		if a.preProxyAppliesToLocked(rule) {
			a.ensurePreProxyRunningLocked()
		}
		a.syncShardPreProxyLocked()
		return a.processManager.Start(rule)
	}

	// 不在前置代理生效范围内（未启用、节点自身是前置、被排除、或不属于目标分组）
	// 时直连启动
	if pre == nil || !a.preProxyAppliesToLocked(rule) {
		return a.processManager.Start(rule)
	}

	apiPort := process.FindApiPort(rule.LocalPort)
	configJSON, coreType, err := buildChainConfigJSON(rule.LocalPort, []*models.ProxyRule{pre, rule}, apiPort)
	if err != nil {
		return err
	}
	return a.processManager.StartWithOptions(rule, process.StartOptions{
		ConfigJSON:  configJSON,
		CoreType:    coreType,
		ApiPort:     apiPort,
		FetchRealIP: true,
	})
}

// GetPreProxy 获取全局前置代理配置
func (a *MyService) GetPreProxy() models.PreProxyConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()

	cfg := models.PreProxyConfig{
		NodeID:      a.config.PreProxyNodeID,
		Enabled:     a.config.PreProxyEnabled,
		GroupIDs:    append([]string(nil), a.config.PreProxyGroupIDs...),
		ExcludedIDs: append([]string(nil), a.config.PreProxyExcludedIDs...),
	}
	if cfg.NodeID == "" {
		return cfg
	}
	for _, r := range a.config.Rules {
		if r.ID == cfg.NodeID {
			cfg.Alias, cfg.Type = r.Alias, "rule"
			return cfg
		}
	}
	for _, c := range a.config.ChainProxies {
		if c.ID == cfg.NodeID {
			cfg.Alias, cfg.Type = c.Alias, "chain"
			return cfg
		}
	}
	for _, lb := range a.config.LoadBalancers {
		if lb.ID == cfg.NodeID {
			cfg.Alias, cfg.Type = lb.Alias, "lb"
			return cfg
		}
	}
	// 节点已不存在：仍返回 ID，别名留空，便于前端提示失效
	return cfg
}

// SetPreProxy 设置前置代理节点，不改动生效范围。nodeID 为空表示清除。
// 已启动的节点不会自动重启，需重新启动后生效。
func (a *MyService) SetPreProxy(nodeID string) error {
	a.mu.RLock()
	cfg := models.PreProxyConfig{
		NodeID: nodeID,
		// 旧接口没有开关概念：选了节点即启用，清空即停用
		Enabled:     nodeID != "",
		GroupIDs:    append([]string(nil), a.config.PreProxyGroupIDs...),
		ExcludedIDs: append([]string(nil), a.config.PreProxyExcludedIDs...),
	}
	a.mu.RUnlock()
	return a.SetPreProxyConfig(cfg)
}

// SetPreProxyConfig 设置前置代理及其生效范围。
//
// GroupIDs 为空表示对全部节点生效；非空时只有这些分组内的节点走前置代理。
// ExcludedIDs 里的节点即使落在范围内也直连，用于个别必须从本机 IP 出去的节点。
// 已启动的节点不会自动重启，需重新启动后生效。
func (a *MyService) SetPreProxyConfig(cfg models.PreProxyConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if cfg.NodeID == "" {
		if a.config.PreProxyNodeID == "" &&
			len(a.config.PreProxyGroupIDs) == 0 && len(a.config.PreProxyExcludedIDs) == 0 {
			return nil
		}
		a.config.PreProxyNodeID = ""
		a.config.PreProxyEnabled = false
		a.config.PreProxyGroupIDs = nil
		a.config.PreProxyExcludedIDs = nil
		a.syncShardPreProxyLocked()
		a.log("已清除前置代理（重新启动节点后生效）")
		return a.saveConfig()
	}

	// 普通节点、链式代理、故障转移都可以做前置代理
	alias, found := a.preProxyCandidateAliasLocked(cfg.NodeID)
	if !found {
		return fmt.Errorf("前置代理节点不存在: %s", cfg.NodeID)
	}

	// 校验分组存在，避免因分组被删而留下永远匹配不到的范围
	for _, groupID := range cfg.GroupIDs {
		if !a.groupExistsLocked(groupID) {
			return fmt.Errorf("分组不存在: %s", groupID)
		}
	}

	a.config.PreProxyNodeID = cfg.NodeID
	a.config.PreProxyEnabled = cfg.Enabled
	a.config.PreProxyGroupIDs = append([]string(nil), cfg.GroupIDs...)
	a.config.PreProxyExcludedIDs = append([]string(nil), cfg.ExcludedIDs...)
	a.syncShardPreProxyLocked()

	if !cfg.Enabled {
		// 保留节点与范围配置，下次启用不必重新配一遍
		a.log(fmt.Sprintf("前置代理已停用（仍保留所选节点 %s 与生效范围）", alias))
		return a.saveConfig()
	}

	scope := "全部节点"
	if len(cfg.GroupIDs) > 0 {
		scope = fmt.Sprintf("%d 个分组", len(cfg.GroupIDs))
	}
	if len(cfg.ExcludedIDs) > 0 {
		scope += fmt.Sprintf("（排除 %d 个节点）", len(cfg.ExcludedIDs))
	}
	a.log(fmt.Sprintf("前置代理已设置为: %s，生效范围 %s（重新启动节点后生效）", alias, scope))
	return a.saveConfig()
}

// preProxyCandidateAliasLocked 在普通节点、链式代理、故障转移里查找候选前置节点。
func (a *MyService) preProxyCandidateAliasLocked(id string) (string, bool) {
	for _, r := range a.config.Rules {
		if r.ID == id {
			return r.Alias, true
		}
	}
	for _, c := range a.config.ChainProxies {
		if c.ID == id {
			return c.Alias, true
		}
	}
	for _, lb := range a.config.LoadBalancers {
		if lb.ID == id {
			return lb.Alias, true
		}
	}
	return "", false
}

// groupExistsLocked 判断分组是否存在（需已持有锁）。
func (a *MyService) groupExistsLocked(groupID string) bool {
	for i := range a.config.Groups {
		if a.config.Groups[i].ID == groupID {
			return true
		}
	}
	return false
}

// ==================== 应用更新 ====================

// GetAppVersion 返回当前应用版本
func (a *MyService) GetAppVersion() string {
	return updater.NormalizeVersion(AppVersion)
}

// GetUpdateConfig 获取更新设置
func (a *MyService) GetUpdateConfig() models.UpdateConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cfg := a.config.Update
	return cfg
}

// SetUpdateConfig 保存更新设置
func (a *MyService) SetUpdateConfig(cfg models.UpdateConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg.Configured = true
	a.config.Update = cfg
	a.log(fmt.Sprintf("更新设置已保存: autoCheck=%v autoDownload=%v", cfg.AutoCheck, cfg.AutoDownload))
	return a.saveConfig()
}

// CheckForUpdate 检查 GitHub Releases 是否有新版本
func (a *MyService) CheckForUpdate() (models.UpdateInfo, error) {
	info, err := updater.CheckLatest(AppVersion)
	if err != nil {
		return models.UpdateInfo{CurrentVersion: updater.NormalizeVersion(AppVersion)}, err
	}
	result := models.UpdateInfo{
		CurrentVersion: info.CurrentVersion,
		LatestVersion:  info.LatestVersion,
		HasUpdate:      info.HasUpdate,
		ReleaseName:    info.ReleaseName,
		ReleaseNotes:   info.ReleaseNotes,
		ReleaseURL:     info.ReleaseURL,
		AssetName:      info.AssetName,
		AssetURL:       info.AssetURL,
		AssetSize:      info.AssetSize,
		PublishedAt:    info.PublishedAt,
		CheckedAt:      info.CheckedAt,
	}
	if !result.HasUpdate {
		result.Message = "当前已是最新版本"
	} else if result.AssetURL == "" {
		result.Message = "发现新版本，但未找到当前平台对应的安装包"
	} else {
		result.Message = fmt.Sprintf("发现新版本 v%s", result.LatestVersion)
	}
	a.log(fmt.Sprintf("[更新] 检查完成: 当前=%s 最新=%s hasUpdate=%v asset=%s",
		result.CurrentVersion, result.LatestVersion, result.HasUpdate, result.AssetName))
	return result, nil
}

// DownloadAndInstallUpdate 下载并应用最新版本（可执行文件将在退出后替换并重启）
func (a *MyService) DownloadAndInstallUpdate() (string, error) {
	info, err := updater.CheckLatest(AppVersion)
	if err != nil {
		return "", err
	}
	if !info.HasUpdate {
		return "当前已是最新版本", nil
	}
	if info.AssetURL == "" {
		// 打不开具体资源时，打开 Release 页面
		if a.app != nil && info.ReleaseURL != "" {
			_ = a.app.Browser.OpenURL(info.ReleaseURL)
		}
		return "", fmt.Errorf("未找到当前平台的更新包，已打开 Releases 页面")
	}

	dest := filepath.Join(os.TempDir(), "xray-manager-update", info.AssetName)
	a.log(fmt.Sprintf("[更新] 开始下载 %s -> %s", info.AssetName, dest))
	if err := updater.Download(info.AssetURL, dest); err != nil {
		return "", err
	}
	a.log(fmt.Sprintf("[更新] 下载完成: %s", dest))

	needQuit, err := updater.ApplyDownloadedUpdate(dest)
	if err != nil {
		return "", err
	}
	if needQuit {
		a.log("[更新] 已安排替换程序，即将退出以完成更新")
		// 异步退出，让前端先收到返回值
		go func() {
			time.Sleep(800 * time.Millisecond)
			if a.app != nil {
				a.app.Quit()
			} else {
				os.Exit(0)
			}
		}()
		return "更新包已下载，程序将退出并自动完成安装", nil
	}
	return "更新包已下载并打开，请按提示完成安装", nil
}

// OpenReleasePage 打开最新 Release 页面
func (a *MyService) OpenReleasePage() error {
	info, err := updater.CheckLatest(AppVersion)
	url := fmt.Sprintf("https://github.com/%s/%s/releases", githubOwner, githubRepo)
	if err == nil && info.ReleaseURL != "" {
		url = info.ReleaseURL
	}
	if a.app == nil {
		return fmt.Errorf("应用未初始化")
	}
	return a.app.Browser.OpenURL(url)
}

// maybeAutoUpdate 启动后按配置检查/自动更新（后台执行）
func (a *MyService) maybeAutoUpdate() {
	a.mu.RLock()
	cfg := a.config.Update
	a.mu.RUnlock()
	if !cfg.AutoCheck {
		return
	}
	info, err := a.CheckForUpdate()
	if err != nil {
		a.log(fmt.Sprintf("[更新] 自动检查失败: %v", err))
		return
	}
	if !info.HasUpdate {
		return
	}
	if a.app != nil {
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "updateAvailable", Data: info})
	}
	if !cfg.AutoDownload {
		return
	}
	msg, err := a.DownloadAndInstallUpdate()
	if err != nil {
		a.log(fmt.Sprintf("[更新] 自动更新失败: %v", err))
		if a.app != nil {
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "updateError", Data: err.Error()})
		}
		return
	}
	if a.app != nil {
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "updateProgress", Data: msg})
	}
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

// handleHealthCheckResult 处理健康检查结果（普通节点/故障转移/链式代理）。
//
// 上万节点时逐条加锁 + 逐条推事件会把主线程和前端一起压垮，
// 因此结果先进缓冲区，由 healthResultFlusher 定期合并落库并只推一个批量事件。
func (a *MyService) handleHealthCheckResult(result models.HealthCheckResult) {
	a.healthResultMu.Lock()
	a.healthResultBuf = append(a.healthResultBuf, result)
	n := len(a.healthResultBuf)
	a.healthResultMu.Unlock()

	// 攒够一批就立刻刷，避免检测很快时缓冲区无限涨
	if n >= healthResultBatchSize {
		a.flushHealthResults()
	}
}

// healthResultBatchSize 达到此条数立即刷新一批结果
const healthResultBatchSize = 200

// flushHealthResults 将缓冲区里的健康检查结果一次性写回配置并推送给前端。
func (a *MyService) flushHealthResults() {
	a.healthResultMu.Lock()
	batch := a.healthResultBuf
	a.healthResultBuf = nil
	a.healthResultMu.Unlock()

	if len(batch) == 0 {
		return
	}

	a.mu.Lock()
	a.applyHealthResultsLocked(batch)
	a.mu.Unlock()

	a.app.Event.EmitEvent(&application.CustomEvent{Name: "healthCheckResults", Data: batch})
}

// applyHealthResultsLocked 批量写回健康检查结果（需已持有锁）。
// 先按 ID 建索引再一次遍历，避免逐条全表扫描（上万节点时是 O(n²)）。
func (a *MyService) applyHealthResultsLocked(results []models.HealthCheckResult) {
	byID := make(map[string]*models.HealthCheckResult, len(results))
	for i := range results {
		byID[results[i].RuleID] = &results[i]
	}

	for i := range a.config.Rules {
		if r, ok := byID[a.config.Rules[i].ID]; ok {
			a.config.Rules[i].HealthStatus = r.Status
			a.config.Rules[i].HealthLatency = r.Latency
			a.config.Rules[i].LastHealthCheck = r.Timestamp
		}
	}
	for i := range a.config.LoadBalancers {
		if r, ok := byID[a.config.LoadBalancers[i].ID]; ok {
			a.config.LoadBalancers[i].HealthStatus = r.Status
			a.config.LoadBalancers[i].HealthLatency = r.Latency
			a.config.LoadBalancers[i].LastHealthCheck = r.Timestamp
		}
	}
	for i := range a.config.ChainProxies {
		if r, ok := byID[a.config.ChainProxies[i].ID]; ok {
			a.config.ChainProxies[i].HealthStatus = r.Status
			a.config.ChainProxies[i].HealthLatency = r.Latency
			a.config.ChainProxies[i].LastHealthCheck = r.Timestamp
		}
	}
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

// GetSpeedTestConfig 获取测速配置。为空的字段用默认值填充，便于前端展示当前生效值。
func (a *MyService) GetSpeedTestConfig() models.SpeedTestConfig {
	a.mu.RLock()
	cfg := a.config.SpeedTest
	a.mu.RUnlock()

	if cfg.URL == "" {
		cfg.URL = speedtest.DefaultSpeedTestURL()
	}
	if len(cfg.Headers) == 0 {
		cfg.Headers = speedtest.DefaultSpeedTestHeaders()
	}
	return cfg
}

// GetDefaultSpeedTestConfig 获取默认测速配置（供前端"恢复默认"使用）
func (a *MyService) GetDefaultSpeedTestConfig() models.SpeedTestConfig {
	return models.SpeedTestConfig{
		URL:     speedtest.DefaultSpeedTestURL(),
		Headers: speedtest.DefaultSpeedTestHeaders(),
	}
}

// SetSpeedTestConfig 更新测速配置并立即生效
func (a *MyService) SetSpeedTestConfig(cfg models.SpeedTestConfig) error {
	a.mu.Lock()
	a.config.SpeedTest = cfg
	err := a.saveConfig()
	a.mu.Unlock()

	// 应用到测速器（空值时测速器内部回退到默认）
	a.speedTestManager.Configure(cfg.URL, cfg.Headers)
	a.log("[测速] 测速配置已更新")
	return err
}

// GetSubscriptionConfig 获取订阅拉取配置。为空的字段填充默认值，便于前端展示当前生效值。
func (a *MyService) GetSubscriptionConfig() models.SubscriptionConfig {
	a.mu.RLock()
	cfg := a.config.Subscription
	a.mu.RUnlock()

	if strings.TrimSpace(cfg.UserAgent) == "" {
		cfg.UserAgent = subscription.DefaultUserAgent
	}
	return cfg
}

// GetDefaultSubscriptionConfig 获取默认订阅配置（供前端"恢复默认"使用）
func (a *MyService) GetDefaultSubscriptionConfig() models.SubscriptionConfig {
	return models.SubscriptionConfig{UserAgent: subscription.DefaultUserAgent}
}

// SetSubscriptionConfig 更新订阅拉取配置并立即生效
func (a *MyService) SetSubscriptionConfig(cfg models.SubscriptionConfig) error {
	cfg.UserAgent = strings.TrimSpace(cfg.UserAgent)

	a.mu.Lock()
	a.config.Subscription = cfg
	err := a.saveConfig()
	a.mu.Unlock()

	// 空值时解析器内部回退到默认 UA
	a.subscriptionManager.SetUserAgent(cfg.UserAgent)
	if cfg.UserAgent == "" {
		a.log("[订阅] User-Agent 已恢复默认")
	} else {
		a.log(fmt.Sprintf("[订阅] User-Agent 已更新: %s", cfg.UserAgent))
	}
	return err
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
			// 与普通节点走同一条缓冲通道，统一批量落库和推送
			a.handleHealthCheckResult(healthcheck.CheckProxyPort(tg.id, tg.localPort, cfg))
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
		// 收尾：把最后不足一批的结果刷出去，再保存
		a.flushHealthResults()
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

// handleRealIP 处理成功获取真实IP的回调：按 localPort 回填到对应节点
// （普通节点/故障转移/链式代理），并清除失败原因。
func (a *MyService) handleRealIP(localPort int, ip string) {
	a.mu.Lock()
	for i := range a.config.Rules {
		if a.config.Rules[i].LocalPort == localPort {
			a.config.Rules[i].RealIP = ip
			a.config.Rules[i].LastError = ""
			a.mu.Unlock()
			// 与故障转移/链式分支一致地通知前端刷新，
			// 否则真实 IP 已经拿到却不会显示在界面上
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
			return
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].LocalPort == localPort {
			a.config.LoadBalancers[i].RealIP = ip
			a.config.LoadBalancers[i].LastError = ""
			a.mu.Unlock()
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
			return
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].LocalPort == localPort {
			a.config.ChainProxies[i].RealIP = ip
			a.config.ChainProxies[i].LastError = ""
			a.mu.Unlock()
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
			return
		}
	}
	a.mu.Unlock()
}

// handleNodeFailed 处理进程管理器上报的"节点启动后不通"事件：
// 停止该节点的进程、标记为未启用、记录失败原因，并通知前端刷新。
// localPort 定位节点（可能是普通节点/故障转移/链式代理）。
func (a *MyService) handleNodeFailed(localPort int, reason string) {
	// 先停止进程（不持 a.mu，避免与 processManager 内部锁交叉）
	_ = a.processManager.Stop(localPort)

	a.mu.Lock()
	var alias string
	matched := false
	for i := range a.config.Rules {
		if a.config.Rules[i].LocalPort == localPort {
			a.config.Rules[i].Enabled = false
			a.config.Rules[i].ProcessID = 0
			a.config.Rules[i].RealIP = ""
			a.config.Rules[i].Verifying = false
			a.config.Rules[i].LastError = reason
			alias = a.config.Rules[i].Alias
			matched = true
			break
		}
	}
	if !matched {
		for i := range a.config.LoadBalancers {
			if a.config.LoadBalancers[i].LocalPort == localPort {
				a.config.LoadBalancers[i].Enabled = false
				a.config.LoadBalancers[i].ProcessID = 0
				a.config.LoadBalancers[i].RealIP = ""
				a.config.LoadBalancers[i].LastError = reason
				alias = a.config.LoadBalancers[i].Alias
				matched = true
				break
			}
		}
	}
	if !matched {
		for i := range a.config.ChainProxies {
			if a.config.ChainProxies[i].LocalPort == localPort {
				a.config.ChainProxies[i].Enabled = false
				a.config.ChainProxies[i].ProcessID = 0
				a.config.ChainProxies[i].RealIP = ""
				a.config.ChainProxies[i].LastError = reason
				alias = a.config.ChainProxies[i].Alias
				matched = true
				break
			}
		}
	}
	if matched {
		a.reservePortLocked(localPort)
	}
	_ = a.saveConfig()
	a.mu.Unlock()

	if matched {
		a.log(fmt.Sprintf("[系统] 节点 %s 启动后不通（%s），已自动停用", alias, reason))
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "nodeFailed", Data: map[string]string{"alias": alias, "reason": reason}})
	}
	a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
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
		configJSON, coreType, err := buildLoadBalanceConfigJSON(&lb, tempPort, nodes, 0, nil)
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
// apiPort > 0 时附加流量统计 API。
func buildChainConfigJSON(localPort int, chainRules []*models.ProxyRule, apiPort int) (string, string, error) {
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

// buildLoadBalanceConfigJSON 构建故障转移配置。
// apiPort > 0 时附加流量统计 API。
// preProxy 非空时，子节点经前置代理出站。
func buildLoadBalanceConfigJSON(lb *models.LoadBalanceNode, localPort int, nodes []*models.ProxyRule, apiPort int, preProxy *models.ProxyRule) (string, string, error) {
	cfg, err := singbox.BuildLoadBalanceConfig(localPort, nodes, preProxy)
	if err != nil {
		return "", "", err
	}
	if apiPort > 0 {
		singbox.AddClashAPI(cfg, apiPort)
	}
	configJSON, err := cfg.ToJSON()
	return configJSON, process.CoreSingBox, err
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
