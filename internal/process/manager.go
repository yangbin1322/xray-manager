package process

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"xray-manager/internal/assets"
	"xray-manager/internal/models"
	"xray-manager/internal/xray"
)

// Manager 进程管理器
type Manager struct {
	processes map[int]*ProcessInfo // key: localPort
	mu        sync.RWMutex
	logFunc   func(string) // 日志回调函数
	configDir string       // 配置文件目录
}

// ProcessInfo 进程信息
type ProcessInfo struct {
	Cmd        *exec.Cmd
	Rule       *models.ProxyRule
	ConfigPath string
	Cancel     chan struct{}
}

// NewManager 创建进程管理器
func NewManager(logFunc func(string)) *Manager {
	// 获取可执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		logFunc(fmt.Sprintf("[错误] 获取可执行文件路径失败: %v", err))
		return nil
	}
	exeDir := filepath.Dir(exePath)
	configDir := filepath.Join(exeDir, "xray-configs")

	// 创建配置文件目录
	if err := os.MkdirAll(configDir, 0755); err != nil {
		logFunc(fmt.Sprintf("[错误] 创建配置目录失败: %v", err))
		return nil
	}

	return &Manager{
		processes: make(map[int]*ProcessInfo),
		logFunc:   logFunc,
		configDir: configDir,
	}
}

// Start 启动代理规则
func (m *Manager) Start(rule *models.ProxyRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查端口是否已被占用
	if _, exists := m.processes[rule.LocalPort]; exists {
		return fmt.Errorf("端口 %d 已被占用", rule.LocalPort)
	}

	// 生成 Xray 配置
	xrayConfig, err := xray.BuildConfig(rule)
	if err != nil {
		return fmt.Errorf("生成 Xray 配置失败: %v", err)
	}

	// 将配置转换为 JSON
	configJSON, err := xrayConfig.ToJSON()
	if err != nil {
		return fmt.Errorf("转换配置为 JSON 失败: %v", err)
	}

	// 保存配置文件
	configPath := filepath.Join(m.configDir, fmt.Sprintf("config_%d.json", rule.LocalPort))
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		return fmt.Errorf("保存配置文件失败: %v", err)
	}

	m.log(fmt.Sprintf("[启动] %s - 端口:%d - 协议:%s", rule.Alias, rule.LocalPort, rule.Protocol))
	m.log(fmt.Sprintf("[配置] 配置文件: %s", configPath))

	// 提取 xray 二进制文件
	xrayBinary, err := assets.ExtractXrayBinary()
	if err != nil {
		return fmt.Errorf("提取 xray 二进制文件失败: %v", err)
	}

	// 创建命令 - 使用提取的 xray 二进制文件
	cmd := exec.Command(xrayBinary, "run", "-c", configPath)

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
		return fmt.Errorf("启动 xray 进程失败: %v (请确保 xray 命令可用)", err)
	}

	// 创建进程信息
	processInfo := &ProcessInfo{
		Cmd:        cmd,
		Rule:       rule,
		ConfigPath: configPath,
		Cancel:     make(chan struct{}),
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

// Stop 停止代理规则
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

	// 删除配置文件
	if err := os.Remove(processInfo.ConfigPath); err != nil {
		m.log(fmt.Sprintf("[警告] 删除配置文件失败: %v", err))
	}

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

		// 删除配置文件
		if err := os.Remove(processInfo.ConfigPath); err != nil {
			m.log(fmt.Sprintf("[警告] 删除配置文件失败: %v", err))
		}

		processInfo.Rule.ProcessID = 0
		processInfo.Rule.RealIP = ""
	}

	m.processes = make(map[int]*ProcessInfo)
	m.log("[系统] 所有进程已停止")
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
func (m *Manager) getRealIP(rule *models.ProxyRule) {
	// 等待服务启动
	time.Sleep(2 * time.Second)

	// 构建代理 URL
	var proxyURL *url.URL
	var err error
	if rule.LocalType == "socks5" || rule.LocalType == "socks" {
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
		resp.Body.Close()
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
