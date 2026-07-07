package subscription

import (
	"context"
	"fmt"
	"sync"
	"time"
	"xray-manager/internal/models"
)

// Manager 订阅管理器
type Manager struct {
	parser      *Parser
	logFunc     func(string)
	onUpdate    func(subID string, rules []models.ProxyRule) error
	updateTasks map[string]*updateTask
	mu          sync.RWMutex

	// resolveProxy 根据订阅的更新方式解析出代理地址（可能临时启动节点）
	// 返回: 代理URL（空表示直连）、清理函数（可为 nil，用于关闭临时代理）、错误
	resolveProxy func(sub *models.Subscription) (string, func(), error)
}

// SetProxyResolver 设置订阅更新代理解析器
func (m *Manager) SetProxyResolver(fn func(sub *models.Subscription) (string, func(), error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveProxy = fn
}

// fetchSubscription 按订阅配置的更新方式获取并解析订阅内容
func (m *Manager) fetchSubscription(sub *models.Subscription) ([]models.ProxyRule, string, error) {
	m.mu.RLock()
	resolver := m.resolveProxy
	m.mu.RUnlock()

	proxyURL := ""
	var cleanup func()
	if resolver != nil {
		var err error
		proxyURL, cleanup, err = resolver(sub)
		if err != nil {
			return nil, "", fmt.Errorf("建立订阅更新代理失败: %v", err)
		}
	}
	if cleanup != nil {
		defer cleanup()
	}

	return m.parser.FetchAndParseWithProxy(sub.URL, proxyURL)
}

// updateTask 更新任务
type updateTask struct {
	subscription *models.Subscription
	ticker       *time.Ticker
	cancel       context.CancelFunc
}

// NewManager 创建订阅管理器
func NewManager(logFunc func(string), onUpdate func(subID string, rules []models.ProxyRule) error) *Manager {
	return &Manager{
		parser:      NewParser(logFunc),
		logFunc:     logFunc,
		onUpdate:    onUpdate,
		updateTasks: make(map[string]*updateTask),
	}
}

// AddSubscription 添加订阅
func (m *Manager) AddSubscription(sub *models.Subscription) ([]models.ProxyRule, error) {
	m.log(fmt.Sprintf("[订阅] 添加订阅: %s", sub.Name))

	// 获取并解析订阅（按配置的更新方式走代理）
	rules, subType, err := m.fetchSubscription(sub)
	if err != nil {
		return nil, err
	}

	// 更新订阅信息
	sub.Type = subType
	sub.NodeCount = len(rules)
	sub.LastUpdate = time.Now().Format("2006-01-02 15:04:05")

	// 计算下次更新时间
	if sub.AutoUpdate && sub.UpdateInterval > 0 {
		nextUpdate := time.Now().Add(time.Duration(sub.UpdateInterval) * time.Hour)
		sub.NextUpdate = nextUpdate.Format("2006-01-02 15:04:05")

		// 启动自动更新任务
		m.startAutoUpdate(sub)
	}

	m.log(fmt.Sprintf("[订阅] 订阅添加成功: %s，节点数量: %d", sub.Name, len(rules)))
	return rules, nil
}

// UpdateSubscription 更新订阅
func (m *Manager) UpdateSubscription(sub *models.Subscription) ([]models.ProxyRule, error) {
	m.log(fmt.Sprintf("[订阅] 更新订阅: %s", sub.Name))

	// 获取并解析订阅（按配置的更新方式走代理）
	rules, subType, err := m.fetchSubscription(sub)
	if err != nil {
		return nil, err
	}

	// 更新订阅信息
	sub.Type = subType
	sub.NodeCount = len(rules)
	sub.LastUpdate = time.Now().Format("2006-01-02 15:04:05")

	// 计算下次更新时间
	if sub.AutoUpdate && sub.UpdateInterval > 0 {
		nextUpdate := time.Now().Add(time.Duration(sub.UpdateInterval) * time.Hour)
		sub.NextUpdate = nextUpdate.Format("2006-01-02 15:04:05")
	}

	// 调用更新回调
	if m.onUpdate != nil {
		if err := m.onUpdate(sub.ID, rules); err != nil {
			return nil, fmt.Errorf("更新回调失败: %v", err)
		}
	}

	m.log(fmt.Sprintf("[订阅] 订阅更新成功: %s，节点数量: %d", sub.Name, len(rules)))
	return rules, nil
}

// RemoveSubscription 移除订阅
func (m *Manager) RemoveSubscription(subID string) {
	m.log(fmt.Sprintf("[订阅] 移除订阅: %s", subID))
	m.stopAutoUpdate(subID)
}

// startAutoUpdate 启动自动更新
func (m *Manager) startAutoUpdate(sub *models.Subscription) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 停止已有的更新任务
	if task, exists := m.updateTasks[sub.ID]; exists {
		task.cancel()
		task.ticker.Stop()
		delete(m.updateTasks, sub.ID)
	}

	if !sub.AutoUpdate || sub.UpdateInterval <= 0 {
		return
	}

	m.log(fmt.Sprintf("[订阅] 启动自动更新任务: %s，间隔: %d 小时", sub.Name, sub.UpdateInterval))

	// 创建定时器
	ticker := time.NewTicker(time.Duration(sub.UpdateInterval) * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())

	task := &updateTask{
		subscription: sub,
		ticker:       ticker,
		cancel:       cancel,
	}

	m.updateTasks[sub.ID] = task

	// 启动更新协程
	go func() {
		for {
			select {
			case <-ticker.C:
				m.log(fmt.Sprintf("[订阅] 开始自动更新: %s", sub.Name))
				_, err := m.UpdateSubscription(sub)
				if err != nil {
					m.log(fmt.Sprintf("[订阅] 自动更新失败: %s - %v", sub.Name, err))
				}
			case <-ctx.Done():
				m.log(fmt.Sprintf("[订阅] 停止自动更新任务: %s", sub.Name))
				return
			}
		}
	}()
}

// stopAutoUpdate 停止自动更新
func (m *Manager) stopAutoUpdate(subID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if task, exists := m.updateTasks[subID]; exists {
		task.cancel()
		task.ticker.Stop()
		delete(m.updateTasks, subID)
		m.log(fmt.Sprintf("[订阅] 已停止自动更新任务: %s", subID))
	}
}

// RestartAutoUpdate 重启自动更新（用于配置加载后恢复任务）
func (m *Manager) RestartAutoUpdate(sub *models.Subscription) {
	if sub.AutoUpdate && sub.Enabled {
		m.startAutoUpdate(sub)
	}
}

// ReconfigureAutoUpdate 按订阅最新配置重设自动更新任务：
// 先停掉已有任务，若启用了自动更新则按新间隔重启，否则保持停止。
// 用于编辑订阅后同步定时任务状态。
func (m *Manager) ReconfigureAutoUpdate(sub *models.Subscription) {
	m.stopAutoUpdate(sub.ID)
	if sub.AutoUpdate && sub.Enabled {
		m.startAutoUpdate(sub)
	}
}

// StopAll 停止所有自动更新任务
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for subID, task := range m.updateTasks {
		task.cancel()
		task.ticker.Stop()
		delete(m.updateTasks, subID)
	}

	m.log("[订阅] 已停止所有自动更新任务")
}

// log 输出日志
func (m *Manager) log(message string) {
	if m.logFunc != nil {
		m.logFunc(message)
	}
}
