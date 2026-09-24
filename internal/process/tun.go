package process

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"xray-manager/internal/assets"
	"xray-manager/internal/singbox"
)

// tunStartupGrace 启动后观察多久没退出才视为成功。
// 创建 wintun 网卡失败、权限不足等错误都会让 sing-box 在这段时间内直接退出。
const tunStartupGrace = 1500 * time.Millisecond

// TunManager 管理 TUN 模式的 sing-box 进程。
//
// TUN 不占本地端口，与节点进程的生命周期也无关，所以不放进 Manager.processes
// （那里以 LocalPort 为键），单独管理一个进程。
type TunManager struct {
	mu         sync.Mutex
	configPath string
	logFunc    func(string)

	cmd         *exec.Cmd
	exited      chan struct{}
	targetPort  int
	targetAlias string
}

// NewTunManager 创建 TUN 管理器，配置文件与节点配置放在同一目录。
func (m *Manager) NewTunManager() *TunManager {
	return &TunManager{
		configPath: filepath.Join(m.configDir, "tun.json"),
		logFunc:    m.log,
	}
}

// Start 开启 TUN，把全局流量转发到 127.0.0.1:targetPort。已开启时先切换目标。
func (t *TunManager) Start(targetPort int, alias string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stopLocked()

	coreBinary, err := assets.FindSingBoxBinary()
	if err != nil {
		return err
	}
	coreBinary, _ = filepath.Abs(coreBinary)

	// 节点内核与本程序自身（健康检查直连测延迟、拉订阅等）都放行直连
	bypass := []string{coreBinary}
	if exe, err := os.Executable(); err == nil {
		bypass = append(bypass, exe)
	}

	configJSON, err := singbox.BuildTunConfig(targetPort, bypass).ToJSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(t.configPath, []byte(configJSON), 0644); err != nil {
		return fmt.Errorf("保存 TUN 配置失败: %v", err)
	}

	cmd := exec.Command(coreBinary, "run", "-c", t.configPath)
	setPlatformSpecificAttrs(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("获取标准错误失败: %v", err)
	}
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 TUN 进程失败: %v", err)
	}

	// 保留最近几行日志：启动即退出时拿来当错误原因
	var (
		tailMu sync.Mutex
		tail   []string
	)
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			t.logFunc(fmt.Sprintf("[TUN] %s", line))
			tailMu.Lock()
			tail = append(tail, line)
			if len(tail) > 5 {
				tail = tail[1:]
			}
			tailMu.Unlock()
		}
	}()

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		<-logDone
		tailMu.Lock()
		reason := strings.Join(tail, "; ")
		tailMu.Unlock()
		if reason == "" {
			reason = "进程意外退出"
		}
		return fmt.Errorf("TUN 启动失败: %s", reason)
	case <-time.After(tunStartupGrace):
	}

	t.cmd = cmd
	t.exited = exited
	t.targetPort = targetPort
	t.targetAlias = alias
	t.logFunc(fmt.Sprintf("[TUN] 已开启，流量转发到 %s (端口:%d)，PID: %d", alias, targetPort, cmd.Process.Pid))
	return nil
}

// Stop 关闭 TUN。
func (t *TunManager) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
}

func (t *TunManager) stopLocked() {
	if t.cmd == nil {
		return
	}
	proc := t.cmd.Process

	// Unix 上先 SIGTERM，让 sing-box 自己撤掉路由表；Windows 不支持信号，直接终止，
	// 进程退出后 wintun 网卡与其路由随之消失
	if runtime.GOOS != "windows" {
		_ = proc.Signal(syscall.SIGTERM)
		select {
		case <-t.exited:
		case <-time.After(3 * time.Second):
			_ = proc.Kill()
		}
	} else {
		_ = proc.Kill()
	}
	select {
	case <-t.exited:
	case <-time.After(2 * time.Second):
		t.logFunc("[TUN][警告] 等待进程退出超时")
	}

	_ = os.Remove(t.configPath)
	t.logFunc(fmt.Sprintf("[TUN] 已关闭 (原目标: %s)", t.targetAlias))
	t.cmd = nil
	t.exited = nil
	t.targetPort = 0
	t.targetAlias = ""
}

// IsRunning TUN 进程是否在运行。进程意外退出后返回 false。
func (t *TunManager) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd == nil {
		return false
	}
	select {
	case <-t.exited:
		return false
	default:
		return true
	}
}

// Target 返回当前 TUN 转发的目标端口与名称。
func (t *TunManager) Target() (int, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.targetPort, t.targetAlias
}
