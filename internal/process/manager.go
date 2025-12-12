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
	"runtime"
	"strings"
	"sync"
	"syscall"
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
	loadRules func()       //前端重新加载规则
}

// ProcessInfo 进程信息
type ProcessInfo struct {
	Cmd        *exec.Cmd
	Rule       *models.ProxyRule
	ConfigPath string
	Cancel     chan struct{}
	cancelOnce sync.Once // 确保 Cancel channel 只关闭一次
}

// NewManager 创建进程管理器
func NewManager(logFunc func(string), loadRules func()) *Manager {
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
		loadRules: loadRules,
		configDir: configDir,
	}
}

// Start 启动代理规则
func (m *Manager) Start(rule *models.ProxyRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查端口是否已被占用
	if existingProcess, exists := m.processes[rule.LocalPort]; exists {
		return fmt.Errorf("端口 %d 已被占用 (当前规则: %s)", rule.LocalPort, existingProcess.Rule.Alias)
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

	// Windows 平台特殊处理：创建新的进程组并隐藏控制台窗口
	setPlatformSpecificAttrs(cmd)

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
	go m.readLog(stdout, rule.Alias, "INFO", processInfo)
	go m.readLog(stderr, rule.Alias, "ERROR", processInfo)

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

	return m.stopProcessLocked(localPort, processInfo)
}

// stopProcessLocked 停止进程（内部方法，需要已持有锁）
func (m *Manager) stopProcessLocked(localPort int, processInfo *ProcessInfo) error {
	m.log(fmt.Sprintf("[停止] %s - 端口:%d", processInfo.Rule.Alias, localPort))

	// 关闭日志读取协程 - 使用 sync.Once 确保只关闭一次
	processInfo.cancelOnce.Do(func() {
		close(processInfo.Cancel)
	})

	// 终止进程
	if processInfo.Cmd.Process != nil {
		killed := false

		// 尝试优雅关闭（发送中断信号）
		if runtime.GOOS == "windows" {
			// Windows: 尝试发送 CTRL+BREAK 信号
			m.log(fmt.Sprintf("[停止] 尝试发送中断信号到进程 %d", processInfo.Cmd.Process.Pid))
			if err := processInfo.Cmd.Process.Signal(os.Interrupt); err != nil {
				m.log(fmt.Sprintf("[警告] 发送中断信号失败: %v", err))
			} else {
				// 等待进程优雅退出
				done := make(chan error, 1)
				go func() {
					done <- processInfo.Cmd.Wait()
				}()
				select {
				case <-done:
					m.log(fmt.Sprintf("[成功] 进程 %d 已优雅退出", processInfo.Cmd.Process.Pid))
					killed = true
				case <-time.After(3 * time.Second):
					m.log(fmt.Sprintf("[警告] 等待进程优雅退出超时，将强制终止"))
				}
			}
		} else {
			// Linux/Mac: 发送 SIGTERM
			if err := processInfo.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
				m.log(fmt.Sprintf("[警告] 发送 SIGTERM 失败: %v", err))
			} else {
				// 等待进程优雅退出
				done := make(chan error, 1)
				go func() {
					done <- processInfo.Cmd.Wait()
				}()
				select {
				case <-done:
					m.log(fmt.Sprintf("[成功] 进程 %d 已优雅退出", processInfo.Cmd.Process.Pid))
					killed = true
				case <-time.After(3 * time.Second):
					m.log(fmt.Sprintf("[警告] 等待进程优雅退出超时，将强制终止"))
				}
			}
		}

		// 如果优雅关闭失败，尝试强制终止
		if !killed {
			m.log(fmt.Sprintf("[停止] 强制终止进程 %d", processInfo.Cmd.Process.Pid))

			if runtime.GOOS == "windows" {
				// Windows: 使用 taskkill 命令强制终止进程树
				killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", processInfo.Cmd.Process.Pid))
				// 隐藏 taskkill 的控制台窗口
				hideConsoleWindow(killCmd) // 调用平台特定函数

				if err := killCmd.Run(); err != nil {
					m.log(fmt.Sprintf("[警告] taskkill 失败: %v，尝试使用 Kill()", err))
					// 回退到 Kill()
					if err := processInfo.Cmd.Process.Kill(); err != nil {
						m.log(fmt.Sprintf("[错误] Kill() 也失败: %v (进程可能已结束)", err))
					}
				} else {
					m.log(fmt.Sprintf("[成功] 使用 taskkill 终止进程"))
				}
			} else {
				// Linux/Mac: 发送 SIGKILL
				if err := processInfo.Cmd.Process.Signal(syscall.SIGKILL); err != nil {
					m.log(fmt.Sprintf("[警告] 发送 SIGKILL 失败: %v (进程可能已结束)", err))
				} else {
					m.log(fmt.Sprintf("[成功] 使用 SIGKILL 终止进程"))
				}
			}

			// 等待进程结束（带超时）
			done := make(chan error, 1)
			go func() {
				done <- processInfo.Cmd.Wait()
			}()
			select {
			case <-done:
				// 进程已结束
			case <-time.After(2 * time.Second):
				m.log(fmt.Sprintf("[警告] 等待进程强制终止超时"))
			}
		}
	}

	// 删除配置文件
	if err := os.Remove(processInfo.ConfigPath); err != nil && !os.IsNotExist(err) {
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
	processInfo, exists := m.processes[localPort]
	if !exists {
		return false
	}
	// 额外检查进程是否真的还活着
	return m.isProcessAlive(processInfo)
}

// isProcessAlive 检查进程是否真的还在运行
func (m *Manager) isProcessAlive(processInfo *ProcessInfo) bool {
	if processInfo.Cmd.Process == nil {
		return false
	}

	// 尝试发送信号 0 来检查进程是否存在
	if runtime.GOOS == "windows" {
		// Windows: 尝试打开进程句柄
		// 如果进程不存在，FindProcess 会成功但 Signal 会失败
		process, err := os.FindProcess(processInfo.Cmd.Process.Pid)
		if err != nil {
			return false
		}
		// Windows 上 Signal(syscall.Signal(0)) 不可用，改用其他方法
		// 尝试获取进程状态
		err = process.Signal(syscall.Signal(0))
		// 在 Windows 上这个调用总是返回错误，所以我们检查进程状态
		// 更可靠的方法是检查 ProcessState
		if processInfo.Cmd.ProcessState != nil && processInfo.Cmd.ProcessState.Exited() {
			return false
		}
		return true
	} else {
		// Unix 系统: 发送信号 0 不会实际发送信号，只检查权限
		err := processInfo.Cmd.Process.Signal(syscall.Signal(0))
		return err == nil
	}
}

// StopAll 停止所有进程
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 创建副本以避免在迭代时修改 map
	processesToStop := make(map[int]*ProcessInfo)
	for port, info := range m.processes {
		processesToStop[port] = info
	}

	for localPort, processInfo := range processesToStop {
		_ = m.stopProcessLocked(localPort, processInfo)
	}

	m.log("[系统] 所有进程已停止")
}

// readLog 读取进程日志
func (m *Manager) readLog(reader io.Reader, alias, level string, processInfo *ProcessInfo) {
	scanner := bufio.NewScanner(reader)
	// 增加缓冲区大小以处理长日志行
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for {
		select {
		case <-processInfo.Cancel:
			return
		default:
			if scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" { // 忽略空行
					m.log(fmt.Sprintf("[%s][%s] %s", alias, level, line))
				}
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

	// 检查进程是否还在运行
	if !m.IsRunning(rule.LocalPort) {
		m.log(fmt.Sprintf("[警告] %s 进程已停止，跳过IP获取", rule.Alias))
		m.loadRules()
		return
	}

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
		m.loadRules()
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
		// 再次检查进程是否还在运行
		if !m.IsRunning(rule.LocalPort) {
			m.log(fmt.Sprintf("[警告] %s 进程已停止，停止IP获取", rule.Alias))
			m.loadRules()
			return
		}

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
			if realIP != "" {
				rule.RealIP = realIP
				m.log(fmt.Sprintf("[IP] %s 真实IP: %s", rule.Alias, realIP))
				m.loadRules()
				return
			}
		}
	}

	m.log(fmt.Sprintf("[警告] %s 无法获取真实IP", rule.Alias))
	rule.RealIP = "获取失败"
	m.loadRules()
}

// log 输出日志
func (m *Manager) log(message string) {
	if m.logFunc != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		m.logFunc(fmt.Sprintf("[%s] %s", timestamp, message))
	}
}
