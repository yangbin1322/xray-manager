package process

import (
	"os/exec"
	"sync"
	"testing"
	"time"
	"xray-manager/internal/models"
)

// 起一批睡眠进程当作"内核进程"，验证批量停止确实并行。
//
// 修复前 Stop 在锁内完成整个终止流程（fork taskkill / 等待进程退出），
// 调用方即使开了并发也会全部堵在这把锁上，退化为串行。
func TestStopIsConcurrent(t *testing.T) {
	const n = 12

	m := &Manager{
		processes:  make(map[int]*ProcessInfo),
		logFunc:    func(string) {},
		configDir:  t.TempDir(),
		pollerStop: make(chan struct{}),
	}

	// 造 n 个真实子进程
	for i := 0; i < n; i++ {
		cmd := sleepCmd()
		if err := cmd.Start(); err != nil {
			t.Skipf("无法启动测试子进程: %v", err)
		}
		port := 40000 + i
		m.processes[port] = &ProcessInfo{
			Cmd:        cmd,
			Rule:       &models.ProxyRule{ID: "r", Alias: "test", LocalPort: port},
			ConfigPath: t.TempDir() + "/none.json",
			Cancel:     make(chan struct{}),
		}
	}

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			_ = m.Stop(port)
		}(40000 + i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("并发停止 %d 个进程耗时: %v", n, elapsed.Round(time.Millisecond))

	if len(m.processes) != 0 {
		t.Fatalf("进程表应被清空，实际还剩 %d 个", len(m.processes))
	}

	// 串行的话每个至少要走一遍终止流程；并发下总耗时应远小于 n 倍单个耗时。
	// 这里用一个宽松上限，只要没有明显串行化即可。
	if elapsed > 15*time.Second {
		t.Fatalf("停止 %d 个进程耗时 %v，疑似串行执行", n, elapsed)
	}
}

// 停止不存在的端口不应 panic，也不应长时间阻塞
func TestStopMissingPortIsSafe(t *testing.T) {
	m := &Manager{
		processes:  make(map[int]*ProcessInfo),
		logFunc:    func(string) {},
		configDir:  t.TempDir(),
		pollerStop: make(chan struct{}),
	}
	start := time.Now()
	if err := m.Stop(45999); err != nil {
		t.Fatalf("停止不存在的端口不应返回错误: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("停止不存在的端口耗时过长: %v", d)
	}
}

func sleepCmd() *exec.Cmd {
	// 跨平台的"睡一会"子进程
	if isWindows() {
		return exec.Command("cmd", "/c", "ping", "127.0.0.1", "-n", "30")
	}
	return exec.Command("sleep", "30")
}

// 批量停止必须在合理时间内完成。
//
// 修复前 Windows 上无条件 fork taskkill /F /T，单次约 1.2 秒且并发也排队，
// 45 个节点要二十多秒（用户实测日志：22:32:02 发起，22:32:24 才结束）。
// 改为直接 Process.Kill 后，20 个进程约 0.5 秒。
func TestBatchStopCompletesQuickly(t *testing.T) {
	const n = 20
	m := &Manager{
		processes: make(map[int]*ProcessInfo),
		logFunc:   func(string) {},
		configDir: t.TempDir(),
	}

	var ports []int
	for i := 0; i < n; i++ {
		cmd := sleepCmd()
		if err := cmd.Start(); err != nil {
			t.Skipf("无法启动测试子进程: %v", err)
		}
		p := 46500 + i
		m.processes[p] = &ProcessInfo{
			Cmd:        cmd,
			Rule:       &models.ProxyRule{Alias: "t", LocalPort: p},
			ConfigPath: t.TempDir() + "/none.json",
			Cancel:     make(chan struct{}),
		}
		ports = append(ports, p)
	}

	start := time.Now()
	var wg sync.WaitGroup
	for _, p := range ports {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			_ = m.Stop(port)
		}(p)
	}
	wg.Wait()
	elapsed := time.Since(start)
	t.Logf("批量停止 %d 个进程耗时: %v", n, elapsed.Round(time.Millisecond))

	if len(m.processes) != 0 {
		t.Fatalf("进程表应被清空，实际剩 %d 个", len(m.processes))
	}
	// 留足余量：只要没退回到"每个都 fork taskkill"的量级即可
	if elapsed > 8*time.Second {
		t.Fatalf("批量停止 %d 个进程耗时 %v，疑似又回到逐个 fork 外部命令", n, elapsed)
	}
}
