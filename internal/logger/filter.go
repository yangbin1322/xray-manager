package logger

import (
	"strings"
	"sync"
)

// LogLevel 日志级别
type LogLevel string

const (
	LogLevelAll   LogLevel = "ALL"
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelXray  LogLevel = "XRAY"
)

// LogEntry 日志条目
type LogEntry struct {
	Timestamp string   `json:"timestamp"`
	Level     LogLevel `json:"level"`
	Message   string   `json:"message"`
	Source    string   `json:"source"` // 来源：系统、规则别名等
}

// Filter 日志过滤器
type Filter struct {
	logs       []LogEntry
	maxSize    int
	mu         sync.RWMutex
	onNewLog   func(LogEntry)
}

// NewFilter 创建日志过滤器
func NewFilter(maxSize int, onNewLog func(LogEntry)) *Filter {
	if maxSize <= 0 {
		maxSize = 1000 // 默认最多保存 1000 条日志
	}

	return &Filter{
		logs:     make([]LogEntry, 0, maxSize),
		maxSize:  maxSize,
		onNewLog: onNewLog,
	}
}

// AddLog 添加日志
func (f *Filter) AddLog(message string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry := f.parseLog(message)

	// 添加日志到缓冲区
	f.logs = append(f.logs, entry)

	// 超过最大容量时删除最旧的日志
	if len(f.logs) > f.maxSize {
		f.logs = f.logs[1:]
	}

	// 触发回调
	if f.onNewLog != nil {
		go f.onNewLog(entry)
	}
}

// parseLog 解析日志消息，提取级别和来源
func (f *Filter) parseLog(message string) LogEntry {
	entry := LogEntry{
		Message: message,
		Level:   LogLevelInfo,
		Source:  "系统",
	}

	// 解析日志格式: [时间戳] [来源][级别] 消息
	// 例如: [2024-01-01 12:00:00] [系统] 配置加载成功
	// 例如: [2024-01-01 12:00:00] [节点1][INFO] 连接成功

	if strings.Contains(message, "[") && strings.Contains(message, "]") {
		parts := strings.SplitN(message, "]", 3)

		// 提取时间戳
		if len(parts) > 0 {
			entry.Timestamp = strings.Trim(parts[0], "[]")
		}

		// 提取来源和级别
		if len(parts) > 1 {
			source := strings.Trim(parts[1], "[]")
			entry.Source = source

			// 判断级别
			if strings.Contains(source, "ERROR") || strings.Contains(source, "错误") {
				entry.Level = LogLevelError
			} else if strings.Contains(source, "WARN") || strings.Contains(source, "警告") {
				entry.Level = LogLevelWarn
			} else if strings.Contains(source, "INFO") {
				entry.Level = LogLevelInfo
			}
		}

		// 提取消息
		if len(parts) > 2 {
			entry.Message = strings.TrimSpace(parts[2])
		}
	}

	// 判断是否为 Xray 输出
	if strings.Contains(message, "Xray") || strings.Contains(message, "xray") {
		entry.Level = LogLevelXray
	}

	return entry
}

// GetLogs 获取所有日志
func (f *Filter) GetLogs() []LogEntry {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// 返回副本
	logs := make([]LogEntry, len(f.logs))
	copy(logs, f.logs)
	return logs
}

// SearchLogs 搜索日志
func (f *Filter) SearchLogs(keyword string, level LogLevel) []LogEntry {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var results []LogEntry

	keyword = strings.ToLower(keyword)

	for _, log := range f.logs {
		// 级别过滤
		if level != LogLevelAll && log.Level != level {
			continue
		}

		// 关键字过滤
		if keyword != "" {
			if !strings.Contains(strings.ToLower(log.Message), keyword) &&
				!strings.Contains(strings.ToLower(log.Source), keyword) {
				continue
			}
		}

		results = append(results, log)
	}

	return results
}

// FilterByLevel 按级别过滤日志
func (f *Filter) FilterByLevel(level LogLevel) []LogEntry {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if level == LogLevelAll {
		logs := make([]LogEntry, len(f.logs))
		copy(logs, f.logs)
		return logs
	}

	var results []LogEntry
	for _, log := range f.logs {
		if log.Level == level {
			results = append(results, log)
		}
	}

	return results
}

// FilterBySource 按来源过滤日志
func (f *Filter) FilterBySource(source string) []LogEntry {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var results []LogEntry
	for _, log := range f.logs {
		if strings.Contains(log.Source, source) {
			results = append(results, log)
		}
	}

	return results
}

// Clear 清空日志
func (f *Filter) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.logs = make([]LogEntry, 0, f.maxSize)
}

// GetLatestLogs 获取最新的 N 条日志
func (f *Filter) GetLatestLogs(n int) []LogEntry {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if n <= 0 || n > len(f.logs) {
		n = len(f.logs)
	}

	start := len(f.logs) - n
	logs := make([]LogEntry, n)
	copy(logs, f.logs[start:])
	return logs
}
