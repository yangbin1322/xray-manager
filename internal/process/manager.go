package process

import (
	"bufio"
	"fmt"
	"gost-manager/internal/models"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Manager 进程管理器
type Manager struct {
	processes map[int]*ProcessInfo // key: localPort
	mu        sync.RWMutex
	logFunc   func(string) // 日志回调函数
}

// ProcessInfo 进程信息
type ProcessInfo struct {
	Cmd    *exec.Cmd
	Rule   *models.ForwardRule
	Cancel chan struct{}
}

// NewManager 创建进程管理器
func NewManager(logFunc func(string)) *Manager {
	return &Manager{
		processes: make(map[int]*ProcessInfo),
		logFunc:   logFunc,
	}
}

// Start 启动转发规则
func (m *Manager) Start(rule *models.ForwardRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查端口是否已被占用
	if _, exists := m.processes[rule.LocalPort]; exists {
		return fmt.Errorf("端口 %d 已被占用", rule.LocalPort)
	}

	// 构建 gost 命令
	gostCmd := m.buildGostCommand(rule)

	m.log(fmt.Sprintf("[启动] %s - 端口:%d - 命令: %s", rule.Alias, rule.LocalPort, gostCmd))

	// 创建命令
	cmd := exec.Command("gost", gostCmd...)

	// 获取标准输出和标准错误
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("获取标准输出失败: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("获取标准错误失败: %v", err)
	}

	// 启动进程
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 gost 进程失败: %v", err)
	}

	// 创建进程信息
	processInfo := &ProcessInfo{
		Cmd:    cmd,
		Rule:   rule,
		Cancel: make(chan struct{}),
	}

	// 保存进程信息
	m.processes[rule.LocalPort] = processInfo
	rule.ProcessID = cmd.Process.Pid

	// 启动日志读取协程
	go m.readLog(stdout, rule.Alias, "INFO", processInfo.Cancel)
	go m.readLog(stderr, rule.Alias, "ERROR", processInfo.Cancel)

	// 获取真实 IP（异步）
	go m.getRealIP(rule)

	m.log(fmt.Sprintf("[成功] %s 已启动，PID: %d", rule.Alias, cmd.Process.Pid))

	return nil
}

// Stop 停止转发规则
func (m *Manager) Stop(localPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	processInfo, exists := m.processes[localPort]
	if !exists {
		return fmt.Errorf("端口 %d 未找到对应进程", localPort)
	}

	m.log(fmt.Sprintf("[停止] %s - 端口:%d", processInfo.Rule.Alias, localPort))

	// 关闭日志读取协程
	close(processInfo.Cancel)

	// 终止进程
	if err := processInfo.Cmd.Process.Kill(); err != nil {
		return fmt.Errorf("停止进程失败: %v", err)
	}

	// 等待进程结束
	_ = processInfo.Cmd.Wait()

	// 清理进程信息
	delete(m.processes, localPort)
	processInfo.Rule.ProcessID = 0
	processInfo.Rule.RealIP = ""

	m.log(fmt.Sprintf("[成功] %s 已停止", processInfo.Rule.Alias))

	return nil
}

// IsRunning 检查端口是否正在运行
func (m *Manager) IsRunning(localPort int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.processes[localPort]
	return exists
}

// StopAll 停止所有进程
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for localPort, processInfo := range m.processes {
		m.log(fmt.Sprintf("[停止] %s - 端口:%d", processInfo.Rule.Alias, localPort))

		// 关闭日志读取协程
		close(processInfo.Cancel)

		// 终止进程
		if processInfo.Cmd.Process != nil {
			_ = processInfo.Cmd.Process.Kill()
			_ = processInfo.Cmd.Wait()
		}

		processInfo.Rule.ProcessID = 0
		processInfo.Rule.RealIP = ""
	}

	m.processes = make(map[int]*ProcessInfo)
	m.log("[系统] 所有进程已停止")
}

// buildGostCommand 构建 gost 命令参数
func (m *Manager) buildGostCommand(rule *models.ForwardRule) []string {
	// gost 命令格式: gost -L <local> -F <forward>
	// 例如: gost -L socks5://localhost:1080 -F http://proxy.example.com:8080

	localType := rule.LocalType
	if localType == "auto" {
		localType = "socks5" // auto 默认使用 socks5
	}

	args := []string{
		"-L",
		fmt.Sprintf("%s://0.0.0.0:%d", localType, rule.LocalPort),
	}

	// 如果有代理信息，添加转发参数
	if rule.ProxyInfo != "" {
		args = append(args, "-F", rule.ProxyInfo)
	}

	return args
}

// readLog 读取进程日志
func (m *Manager) readLog(reader io.Reader, alias, level string, cancel chan struct{}) {
	scanner := bufio.NewScanner(reader)
	for {
		select {
		case <-cancel:
			return
		default:
			if scanner.Scan() {
				line := scanner.Text()
				m.log(fmt.Sprintf("[%s][%s] %s", alias, level, line))
			} else {
				// 读取完毕或发生错误
				if err := scanner.Err(); err != nil {
					m.log(fmt.Sprintf("[%s][ERROR] 日志读取错误: %v", alias, err))
				}
				return
			}
		}
	}
}

// getRealIP 获取真实 IP
func (m *Manager) getRealIP(rule *models.ForwardRule) {
	// 等待服务启动
	time.Sleep(2 * time.Second)

	// 构建代理 URL
	var proxyURL *url.URL
	var err error
	if rule.LocalType == "socks5" || rule.LocalType == "socks4" || rule.LocalType == "auto" {
		proxyURL, err = url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", rule.LocalPort))
	} else {
		proxyURL, err = url.Parse(fmt.Sprintf("http://127.0.0.1:%d", rule.LocalPort))
	}
	if err != nil {
		m.log(fmt.Sprintf("[错误] %s 构建代理 URL 失败: %v", rule.Alias, err))
		rule.RealIP = "获取失败"
		return
	}

	// 创建 HTTP 客户端，支持代理
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	// IP 查询服务列表
	ipServices := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	for _, service := range ipServices {
		resp, err := client.Get(service)
		if err != nil {
			m.log(fmt.Sprintf("[警告] %s 请求 %s 失败: %v", rule.Alias, service, err))
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() // 循环中直接关闭
		if err != nil {
			m.log(fmt.Sprintf("[警告] %s 读取 %s 响应失败: %v", rule.Alias, service, err))
			continue
		}

		if resp.StatusCode == 200 {
			realIP := strings.TrimSpace(string(body))
			rule.RealIP = realIP
			m.log(fmt.Sprintf("[IP] %s 真实IP: %s", rule.Alias, realIP))
			return
		}
	}

	m.log(fmt.Sprintf("[警告] %s 无法获取真实IP", rule.Alias))
	rule.RealIP = "获取失败"
}

// log 输出日志
func (m *Manager) log(message string) {
	if m.logFunc != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		m.logFunc(fmt.Sprintf("[%s] %s", timestamp, message))
	}
}
