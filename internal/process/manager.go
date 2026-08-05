package process

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"xray-manager/internal/assets"
	"xray-manager/internal/models"
	"xray-manager/internal/singbox"
	"xray-manager/internal/utils"
)

// 内核类型。
//
// 已统一为 sing-box：它支持的协议是 Xray 的超集（Hysteria2/TUIC 只有
// sing-box 能跑），单内核也避免了两套配置生成逻辑不同步——REALITY 曾因此
// 在 sing-box 侧被漏写，节点启动后连不通却没有任何配置报错。
const (
	CoreSingBox = "singbox"
)

// Manager 进程管理器
type Manager struct {
	processes map[int]*ProcessInfo // key: localPort
	mu        sync.RWMutex
	logFunc   func(string) // 日志回调函数
	configDir string       // 配置文件目录
	loadRules func()       //前端重新加载规则

	trafficFunc  func(ruleID string, deltaUp, deltaDown int64, upSpeed, downSpeed float64) // 流量统计回调
	onNodeFailed func(localPort int, reason string)                                        // 节点启动后验证不通的回调
	onRealIP     func(localPort int, ip string)                                            // 成功获取真实IP的回调（回填到节点）
	pollerStop   chan struct{}
	stopOnce     sync.Once // 确保 pollerStop 只关闭一次
}

// SetNodeFailedCallback 设置节点启动后验证失败（如无法访问外网）的回调
func (m *Manager) SetNodeFailedCallback(fn func(localPort int, reason string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onNodeFailed = fn
}

// SetRealIPCallback 设置成功获取真实IP的回调
func (m *Manager) SetRealIPCallback(fn func(localPort int, ip string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onRealIP = fn
}

// ProcessInfo 进程信息
type ProcessInfo struct {
	Cmd        *exec.Cmd
	Rule       *models.ProxyRule
	ConfigPath string
	Cancel     chan struct{}
	cancelOnce sync.Once // 确保 Cancel channel 只关闭一次

	CoreType   string // 内核类型: xray / singbox
	CoreBinary string // 内核二进制路径（用于流量查询）
	ApiPort    int    // 流量统计 API 端口，0 表示未启用

	// 流量统计累计值（进程启动以来）
	lastUp           int64
	lastDown         int64
	lastSample       time.Time
	trafficErrLogged bool // 是否已记录过流量查询失败日志（避免刷屏）
}

// StartOptions 使用自定义配置启动的选项
type StartOptions struct {
	ConfigJSON  string
	CoreType    string // 内核类型，空默认 xray
	ApiPort     int    // 流量统计 API 端口，0 表示未启用
	FetchRealIP bool   // 启动后是否获取真实IP并做连通性验证（故障转移/链式代理需要）
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

	m := &Manager{
		processes:  make(map[int]*ProcessInfo),
		logFunc:    logFunc,
		loadRules:  loadRules,
		configDir:  configDir,
		pollerStop: make(chan struct{}),
	}

	// 启动流量统计轮询
	go m.pollTraffic()

	return m
}

// SetTrafficCallback 设置流量统计回调
func (m *Manager) SetTrafficCallback(fn func(ruleID string, deltaUp, deltaDown int64, upSpeed, downSpeed float64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trafficFunc = fn
}

// FindApiPort 为流量统计 API 查找可用端口（基于本地端口偏移，避免与代理端口冲突）
func FindApiPort(localPort int) int {
	start := localPort + 20000
	if start >= 65000 || start <= 0 {
		start = 20000
	}
	return utils.FindAvailablePort(start)
}

// Start 启动代理规则
func (m *Manager) Start(rule *models.ProxyRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查端口是否已被本管理器占用
	if existingProcess, exists := m.processes[rule.LocalPort]; exists {
		return fmt.Errorf("端口 %d 已被占用 (当前规则: %s)", rule.LocalPort, existingProcess.Rule.Alias)
	}

	// 统一使用 sing-box 内核：它支持的协议是 Xray 的超集
	// （Hysteria2/TUIC 只有 sing-box 能跑），单内核也避免了两套配置生成
	// 逻辑不同步导致的问题。
	apiPort := FindApiPort(rule.LocalPort)
	sbConfig, err := singbox.BuildConfig(rule)
	if err != nil {
		return fmt.Errorf("生成 sing-box 配置失败: %v", err)
	}
	if apiPort > 0 {
		singbox.AddClashAPI(sbConfig, apiPort)
	}
	configJSON, err := sbConfig.ToJSON()
	if err != nil {
		return err
	}

	return m.startProcessLocked(rule, StartOptions{
		ConfigJSON:  configJSON,
		CoreType:    CoreSingBox,
		ApiPort:     apiPort,
		FetchRealIP: true,
	})
}

// StartWithConfig 使用自定义配置 JSON 启动进程（不启用流量统计）
func (m *Manager) StartWithConfig(rule *models.ProxyRule, configJSON string) error {
	return m.StartWithOptions(rule, StartOptions{ConfigJSON: configJSON, CoreType: CoreSingBox})
}

// StartWithOptions 使用自定义配置和选项启动进程
func (m *Manager) StartWithOptions(rule *models.ProxyRule, opts StartOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existingProcess, exists := m.processes[rule.LocalPort]; exists {
		return fmt.Errorf("端口 %d 已被占用 (当前规则: %s)", rule.LocalPort, existingProcess.Rule.Alias)
	}

	return m.startProcessLocked(rule, opts)
}

// startProcessLocked 启动内核进程（内部方法，需要已持有锁）
func (m *Manager) startProcessLocked(rule *models.ProxyRule, opts StartOptions) error {
	if opts.CoreType == "" {
		opts.CoreType = CoreSingBox
	}

	// 启动前确保端口未被残留进程占用（残留的 xray/sing-box 直接终止，其他程序则报错）
	if err := utils.EnsurePortFree(rule.LocalPort, m.log); err != nil {
		return err
	}

	// 保存配置文件
	configPath := filepath.Join(m.configDir, fmt.Sprintf("config_%d.json", rule.LocalPort))
	if err := os.WriteFile(configPath, []byte(opts.ConfigJSON), 0644); err != nil {
		return fmt.Errorf("保存配置文件失败: %v", err)
	}

	m.log(fmt.Sprintf("[启动] %s - 端口:%d - 内核:%s", rule.Alias, rule.LocalPort, opts.CoreType))
	m.log(fmt.Sprintf("[配置] 配置文件: %s", configPath))

	// 获取内核二进制
	coreBinary, err := assets.FindSingBoxBinary()
	if err != nil {
		return err
	}

	cmd := exec.Command(coreBinary, "run", "-c", configPath)

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
		return fmt.Errorf("启动 %s 进程失败: %v", opts.CoreType, err)
	}

	// 创建进程信息
	processInfo := &ProcessInfo{
		Cmd:        cmd,
		Rule:       rule,
		ConfigPath: configPath,
		Cancel:     make(chan struct{}),
		CoreType:   opts.CoreType,
		CoreBinary: coreBinary,
		ApiPort:    opts.ApiPort,
	}

	// 保存进程信息
	m.processes[rule.LocalPort] = processInfo
	rule.ProcessID = cmd.Process.Pid
	rule.LastStartTime = time.Now().Format("2006-01-02 15:04:05")

	// 启动日志读取协程
	go m.readLog(stdout, rule.Alias, "INFO", processInfo)
	go m.readLog(stderr, rule.Alias, "ERROR", processInfo)

	// 获取真实 IP + 连通性验证（异步）
	if opts.FetchRealIP {
		go m.getRealIP(rule)
	}

	m.log(fmt.Sprintf("[成功] %s 已启动，PID: %d", rule.Alias, cmd.Process.Pid))

	return nil
}

// Stop 停止指定本地端口上的进程。
//
// 只在锁内做 map 摘除，真正的终止（taskkill / SIGTERM 等待）放到锁外。
// 否则批量停止时，即使调用方开了并发，所有协程也会卡在这把全局锁上排队，
// 退化成串行——每个节点耗时叠加，几百个节点就要等很久。
func (m *Manager) Stop(localPort int) error {
	m.mu.Lock()
	processInfo, exists := m.processes[localPort]
	if exists {
		delete(m.processes, localPort)
		processInfo.Rule.ProcessID = 0
		processInfo.Rule.RealIP = ""
		processInfo.Rule.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
	}
	m.mu.Unlock()

	if !exists {
		// 进程不存在（可能已崩溃或被外部终止），检查端口上是否有残留内核进程
		m.log(fmt.Sprintf("[停止] 端口 %d 无对应进程记录，检查残留进程", localPort))
		utils.WaitPortReleased(localPort, 0, m.log)
		return nil
	}

	// 已从 map 中摘除，此处可安全并发执行
	m.terminateProcess(localPort, processInfo, true)
	return nil
}

// terminateProcess 实际终止进程并清理（不涉及 m.processes，可在锁外并发调用）。
// waitPort 为 true 时会确认端口释放（供单个停止后重启用）；关窗批量停止传 false 跳过等待。
func (m *Manager) terminateProcess(localPort int, processInfo *ProcessInfo, waitPort bool) {
	m.log(fmt.Sprintf("[停止] %s - 端口:%d", processInfo.Rule.Alias, localPort))

	// 关闭日志读取协程 - 使用 sync.Once 确保只关闭一次
	processInfo.cancelOnce.Do(func() {
		close(processInfo.Cancel)
	})

	// 终止进程。
	// Windows 上子进程不支持 os.Interrupt 优雅关闭（返回 "not supported"），
	// 直接强制终止即可；Unix 上先 SIGTERM 优雅退出，超时再 SIGKILL。
	if processInfo.Cmd.Process != nil {
		if runtime.GOOS == "windows" {
			m.killWindows(processInfo)
		} else {
			m.killUnix(processInfo)
		}
	}

	// 删除配置文件
	if err := os.Remove(processInfo.ConfigPath); err != nil && !os.IsNotExist(err) {
		m.log(fmt.Sprintf("[警告] 删除配置文件失败: %v", err))
	}

	processInfo.Rule.ProcessID = 0
	processInfo.Rule.RealIP = ""

	// 确认端口已释放（轻量探测），若仍被残留内核进程占用则强制终止
	if waitPort {
		utils.WaitPortReleased(localPort, 3*time.Second, m.log)
	}

	m.log(fmt.Sprintf("[成功] %s 已停止", processInfo.Rule.Alias))
}

// killWindows Windows 平台强制终止进程树
func (m *Manager) killWindows(processInfo *ProcessInfo) {
	// 优先直接 TerminateProcess（Go 的 Process.Kill）。
	//
	// 原先无条件 fork taskkill /F /T，实测单次约 1.2 秒，而且并发也快不了多少
	// （Windows 的进程创建本身会互相排队）——45 个节点就要二十多秒。
	// 直接 Kill 实测 20 个进程只要 4ms，快约 3000 倍。
	//
	// taskkill /T 的价值在于连带杀掉子进程树，但 xray / sing-box 都不派生子进程，
	// 且我们启动时已给它们建了独立进程组，所以直接 Kill 足够。
	// 仅在 Kill 失败（极少见）时才回退到 taskkill 兜底。
	if err := processInfo.Cmd.Process.Kill(); err != nil {
		m.log(fmt.Sprintf("[警告] Kill() 失败: %v，回退到 taskkill", err))
		killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", processInfo.Cmd.Process.Pid))
		hideConsoleWindow(killCmd)
		if err := killCmd.Run(); err != nil {
			m.log(fmt.Sprintf("[错误] taskkill 也失败: %v (进程可能已结束)", err))
		}
	}

	// 回收进程，避免僵尸
	done := make(chan error, 1)
	go func() { done <- processInfo.Cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		m.log("[警告] 等待进程退出超时")
	}
}

// killUnix Unix 平台优雅关闭，超时后强制终止
func (m *Manager) killUnix(processInfo *ProcessInfo) {
	proc := processInfo.Cmd.Process

	done := make(chan error, 1)
	go func() { done <- processInfo.Cmd.Wait() }()

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		m.log(fmt.Sprintf("[警告] 发送 SIGTERM 失败: %v", err))
	} else {
		select {
		case <-done:
			return
		case <-time.After(3 * time.Second):
			m.log("[警告] 等待进程优雅退出超时，将强制终止")
		}
	}

	// 强制终止
	_ = proc.Signal(syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		m.log("[警告] 等待进程强制终止超时")
	}
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

// StopAll 停止所有进程（并发执行，加速关窗/批量停止）
func (m *Manager) StopAll() {
	// 停止流量轮询，避免关窗时还在 fork statsquery 子进程
	m.stopOnce.Do(func() { close(m.pollerStop) })

	// 锁内摘出所有进程并清空 map，避免并发终止时的 map 竞争
	m.mu.Lock()
	toStop := make(map[int]*ProcessInfo, len(m.processes))
	for port, info := range m.processes {
		toStop[port] = info
		info.Rule.ProcessID = 0
		info.Rule.RealIP = ""
		info.Rule.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
	}
	m.processes = make(map[int]*ProcessInfo)
	m.mu.Unlock()

	if len(toStop) == 0 {
		return
	}

	// 锁外并发终止各进程（终止是慢操作，串行会导致关窗卡顿）。
	// 关窗场景不等待端口释放（应用即将退出，无需为下次启动预留端口）。
	var wg sync.WaitGroup
	for localPort, processInfo := range toStop {
		wg.Add(1)
		go func(port int, info *ProcessInfo) {
			defer wg.Done()
			m.terminateProcess(port, info, false)
		}(localPort, processInfo)
	}
	wg.Wait()

	m.log("[系统] 所有进程已停止")
}

// ==================== 流量统计 ====================

// pollTraffic 后台轮询各进程的流量统计
func (m *Manager) pollTraffic() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.pollerStop:
			return
		case <-ticker.C:
			m.mu.RLock()
			cb := m.trafficFunc
			infos := make([]*ProcessInfo, 0, len(m.processes))
			for _, info := range m.processes {
				if info.ApiPort > 0 {
					infos = append(infos, info)
				}
			}
			m.mu.RUnlock()

			if cb == nil {
				continue
			}

			for _, info := range infos {
				up, down, err := queryTraffic(info)
				if err != nil {
					// 仅首次失败记录日志，避免每 3 秒刷屏
					if !info.trafficErrLogged {
						info.trafficErrLogged = true
						m.log(fmt.Sprintf("[流量统计] %s 查询失败（后续不再重复提示）: %v", info.Rule.Alias, err))
					}
					continue
				}
				info.trafficErrLogged = false

				now := time.Now()
				elapsed := now.Sub(info.lastSample).Seconds()
				if info.lastSample.IsZero() || elapsed <= 0 {
					elapsed = 3
				}

				deltaUp := up - info.lastUp
				deltaDown := down - info.lastDown
				if deltaUp < 0 {
					deltaUp = 0
				}
				if deltaDown < 0 {
					deltaDown = 0
				}

				info.lastUp = up
				info.lastDown = down
				info.lastSample = now

				cb(info.Rule.ID, deltaUp, deltaDown, float64(deltaUp)/elapsed, float64(deltaDown)/elapsed)
			}
		}
	}
}

// queryTraffic 查询进程的累计流量（自进程启动以来，字节）
func queryTraffic(info *ProcessInfo) (up int64, down int64, err error) {
	return querySingBoxTraffic(info.ApiPort)
}

// queryXrayTraffic 通过 xray api statsquery 查询出站流量
func queryXrayTraffic(xrayBinary string, apiPort int) (int64, int64, error) {
	cmd := exec.Command(xrayBinary, "api", "statsquery", fmt.Sprintf("--server=127.0.0.1:%d", apiPort))
	hideConsoleWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}

	var result struct {
		Stat []struct {
			Name  string      `json:"name"`
			Value interface{} `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0, 0, err
	}

	var up, down int64
	for _, stat := range result.Stat {
		// 格式: outbound>>>TAG>>>traffic>>>uplink
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "outbound" {
			continue
		}
		tag := parts[1]
		if tag == "direct" || tag == "block" || tag == "api" {
			continue
		}
		value := coerceInt64(stat.Value)
		switch parts[3] {
		case "uplink":
			up += value
		case "downlink":
			down += value
		}
	}

	return up, down, nil
}

// querySingBoxTraffic 通过 sing-box Clash API 查询累计流量
func querySingBoxTraffic(apiPort int) (int64, int64, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/connections", apiPort))
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	var result struct {
		UploadTotal   int64 `json:"uploadTotal"`
		DownloadTotal int64 `json:"downloadTotal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, err
	}

	return result.UploadTotal, result.DownloadTotal, nil
}

// coerceInt64 将 JSON 数值（可能是字符串或数字）转换为 int64
func coerceInt64(v interface{}) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	case json.Number:
		n, _ := val.Int64()
		return n
	default:
		return 0
	}
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

	// 构建代理 URL（本地入站为混合端口，SOCKS5 始终可用）
	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", rule.LocalPort))
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
		"https://checkip.amazonaws.com",
		"https://api.ipify.org",
		"https://ipinfo.io/ip",
		"https://api.ip.sb/ip",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	var lastErr string
	for _, service := range ipServices {
		// 再次检查进程是否还在运行
		if !m.IsRunning(rule.LocalPort) {
			m.log(fmt.Sprintf("[警告] %s 进程已停止，停止IP获取", rule.Alias))
			m.loadRules()
			return
		}

		resp, err := client.Get(service)
		if err != nil {
			lastErr = err.Error()
			m.log(fmt.Sprintf("[警告] %s 请求 %s 失败: %v", rule.Alias, service, err))
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err.Error()
			m.log(fmt.Sprintf("[警告] %s 读取 %s 响应失败: %v", rule.Alias, service, err))
			continue
		}

		if resp.StatusCode == 200 {
			realIP := strings.TrimSpace(string(body))
			if realIP != "" {
				rule.RealIP = realIP
				rule.LastError = "" // 成功，清除失败原因
				m.log(fmt.Sprintf("[IP] %s 真实IP: %s", rule.Alias, realIP))
				// 回填到对应节点（普通节点直接命中；故障转移/链式代理经回调按端口回填）
				m.mu.RLock()
				ipcb := m.onRealIP
				m.mu.RUnlock()
				if ipcb != nil {
					ipcb(rule.LocalPort, realIP)
				}
				m.loadRules()
				return
			}
		}
		lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	// 全部 IP 服务都失败：节点虽已启动但无法访问外网，视为不通。
	// 记录失败原因，并回调上层停用该节点。
	reason := simplifyProxyError(lastErr)
	m.log(fmt.Sprintf("[失败] %s 启动后无法访问外网（%s），自动停用", rule.Alias, reason))
	rule.RealIP = ""
	rule.LastError = reason

	m.mu.RLock()
	cb := m.onNodeFailed
	m.mu.RUnlock()
	if cb != nil {
		cb(rule.LocalPort, reason)
	} else {
		m.loadRules()
	}
}

// simplifyProxyError 把冗长的网络错误简化为可读原因
func simplifyProxyError(err string) string {
	if err == "" {
		return "无法获取真实IP"
	}
	lower := strings.ToLower(err)
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return "连接超时（节点不可用或本机无网络）"
	case strings.Contains(lower, "refused"):
		return "连接被拒绝（节点端口不可达）"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "dns"):
		return "DNS 解析失败"
	case strings.Contains(lower, "socks") || strings.Contains(lower, "proxy"):
		return "代理连接失败（节点配置可能有误）"
	case strings.Contains(lower, "reset"):
		return "连接被重置"
	default:
		return "无法访问外网：" + err
	}
}

// SyncState 同步进程状态，检查配置中标记为运行的规则其进程是否真的存在
// 在应用启动时调用，修复崩溃后的状态不一致问题
func (m *Manager) SyncState(rules []models.ProxyRule) []models.ProxyRule {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range rules {
		rule := &rules[i]
		if rule.Enabled {
			if rule.ProcessID <= 0 || !processExists(rule.ProcessID) {
				m.log(fmt.Sprintf("[状态同步] 规则 %s (PID:%d) 进程不存在，重置进程状态（保留启用标记以便重启）", rule.Alias, rule.ProcessID))
				rule.ProcessID = 0
				rule.RealIP = ""
				// 保留 Enabled = true，以便启动时自动恢复
			}
		}
	}

	return rules
}

// processExists 检查指定 PID 的进程是否存在
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 发送信号 0 检查进程是否存在（不会真正发送信号）
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// log 输出日志
func (m *Manager) log(message string) {
	if m.logFunc != nil {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		m.logFunc(fmt.Sprintf("[%s] %s", timestamp, message))
	}
}
