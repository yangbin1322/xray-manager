package group

import (
	"fmt"
	"sync"
	"time"
	"xray-manager/internal/models"
)

// Manager 分组管理器
type Manager struct {
	groups  map[string]*models.Group
	logFunc func(string)
	mu      sync.RWMutex
}

// NewManager 创建分组管理器
func NewManager(logFunc func(string)) *Manager {
	return &Manager{
		groups:  make(map[string]*models.Group),
		logFunc: logFunc,
	}
}

// LoadGroups 加载分组。
//
// 存副本而不是 &groups[i]：调用方传进来的是 config.Groups 本身，
// 上层删除分组时用 append 就地前移剩余元素，指向旧下标的指针会串到
// 另一个分组头上——之后按 ID 改名就改到无关分组身上，
// 界面表现为删完分组后左侧列表出现两个同名分组。
func (m *Manager) LoadGroups(groups []models.Group) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.groups = make(map[string]*models.Group, len(groups))
	for i := range groups {
		g := groups[i]
		m.groups[g.ID] = &g
	}

	m.log(fmt.Sprintf("[分组] 加载了 %d 个分组", len(groups)))
}

// CreateGroup 创建分组
func (m *Manager) CreateGroup(name, description, source string) (*models.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 生成唯一 ID
	id := fmt.Sprintf("group_%d", time.Now().UnixNano())

	group := &models.Group{
		ID:          id,
		Name:        name,
		Description: description,
		Source:      source,
		CreatedAt:   time.Now().Format("2006-01-02 15:04:05"),
	}

	m.groups[id] = group
	m.log(fmt.Sprintf("[分组] 创建分组: %s", name))

	return group, nil
}

// GetGroup 获取分组
func (m *Manager) GetGroup(id string) (*models.Group, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	group, exists := m.groups[id]
	if !exists {
		return nil, fmt.Errorf("分组 %s 不存在", id)
	}

	return group, nil
}

// GetAllGroups 获取所有分组
func (m *Manager) GetAllGroups() []models.Group {
	m.mu.RLock()
	defer m.mu.RUnlock()

	groups := make([]models.Group, 0, len(m.groups))
	for _, group := range m.groups {
		groups = append(groups, *group)
	}

	return groups
}

// UpdateGroup 更新分组
func (m *Manager) UpdateGroup(id, name, description string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	group, exists := m.groups[id]
	if !exists {
		return fmt.Errorf("分组 %s 不存在", id)
	}

	group.Name = name
	group.Description = description

	m.log(fmt.Sprintf("[分组] 更新分组: %s", name))
	return nil
}

// DeleteGroup 删除分组
func (m *Manager) DeleteGroup(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.groups[id]; !exists {
		return fmt.Errorf("分组 %s 不存在", id)
	}

	delete(m.groups, id)
	m.log(fmt.Sprintf("[分组] 删除分组: %s", id))

	return nil
}

// CreateGroupForSubscription 为订阅创建分组
func (m *Manager) CreateGroupForSubscription(subName, subID string) (*models.Group, error) {
	return m.CreateGroup(
		subName,
		fmt.Sprintf("订阅 %s 的节点", subName),
		"subscription",
	)
}

// GetRulesByGroup 获取分组下的规则（需要从外部传入规则列表）
func (m *Manager) GetRulesByGroup(groupID string, allRules []models.ProxyRule) []models.ProxyRule {
	var rules []models.ProxyRule
	for _, rule := range allRules {
		if rule.GroupID == groupID {
			rules = append(rules, rule)
		}
	}
	return rules
}

// GetGroupStats 获取分组统计信息
func (m *Manager) GetGroupStats(groupID string, allRules []models.ProxyRule) (total, enabled int) {
	for _, rule := range allRules {
		if rule.GroupID == groupID {
			total++
			if rule.Enabled {
				enabled++
			}
		}
	}
	return
}

// log 输出日志
func (m *Manager) log(message string) {
	if m.logFunc != nil {
		m.logFunc(message)
	}
}
