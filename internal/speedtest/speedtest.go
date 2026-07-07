package speedtest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
	"xray-manager/internal/models"
)

// Tester 测速器
type Tester struct {
	logFunc func(string)
	mu      sync.RWMutex
}

// NewTester 创建测速器
func NewTester(logFunc func(string)) *Tester {
	return &Tester{
		logFunc: logFunc,
	}
}

// TestLatency 测试 TCP 延迟
func (t *Tester) TestLatency(ctx context.Context, serverAddr string, serverPort int, timeout time.Duration) (int, error) {
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	// 使用带超时的拨号器
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", serverAddr, serverPort))
	if err != nil {
		return 0, fmt.Errorf("连接失败: %v", err)
	}
	defer conn.Close()

	latency := time.Since(start).Milliseconds()
	return int(latency), nil
}

// 默认测速参数
const (
	// defaultMeasureWindow 测速采样时长：在此时间窗口内下载多少算多少，
	// 这样慢速网络不会超时失败，快速网络样本也足够，测速时间可预期。
	defaultMeasureWindow = 8 * time.Second
	// 默认测速源：请求一个足够大的文件（95MB），让快速线路在采样窗口内也下不完。
	// 慢速线路则在采样窗口到点后主动停止，用已下载的量计算速度。
	// cloudflare的测速文件限制最大为99999999bytes
	defaultTestURL = "https://speed.cloudflare.com/__down?bytes=99999999"
)

// TestDownloadSpeed 测试下载速度（通过代理，限时采样）
// timeout 为整体上限；实际下载采样时长取 defaultMeasureWindow 与 timeout 中较小者。
func (t *Tester) TestDownloadSpeed(ctx context.Context, proxyType string, proxyPort int, testURL string, timeout time.Duration) (float64, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// 采样窗口：不超过整体超时，也不超过默认窗口
	measureWindow := defaultMeasureWindow
	if timeout < measureWindow {
		measureWindow = timeout
	}

	// 整体超时略大于采样窗口，为建立连接（尤其慢速网络首字节）留出余量
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构建代理 URL（本地入站为混合端口，SOCKS5 始终可用）
	var proxyURL *url.URL
	var err error
	if proxyType == "http" {
		proxyURL, err = url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	} else {
		proxyURL, err = url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", proxyPort))
	}
	if err != nil {
		return 0, fmt.Errorf("构建代理 URL 失败: %v", err)
	}

	// 创建 HTTP 客户端
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}

	if testURL == "" {
		testURL = defaultTestURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return 0, fmt.Errorf("创建请求失败: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("HTTP 状态码: %d", resp.StatusCode)
	}

	// 限时采样：从首字节到达开始计时，读取 measureWindow 时长后主动停止
	deadline := time.NewTimer(measureWindow)
	defer deadline.Stop()

	// 到达采样时限时关闭响应体，令 Read 立即返回，从而停止下载
	stopped := make(chan struct{})
	go func() {
		select {
		case <-deadline.C:
			resp.Body.Close()
		case <-stopped:
		}
	}()
	defer close(stopped)

	start := time.Now()
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := resp.Body.Read(buf)
		written += int64(n)
		if readErr != nil {
			// io.EOF 表示文件下载完毕（快线路下小文件），采样窗口到点主动关闭也会走到这里
			break
		}
	}

	duration := time.Since(start).Seconds()
	if written == 0 {
		return 0, fmt.Errorf("未下载到任何数据（可能连接失败或超时）")
	}
	if duration <= 0 {
		return 0, fmt.Errorf("测速时间过短")
	}

	// 计算速度 (MB/s)
	speedMBps := float64(written) / duration / 1024 / 1024

	return speedMBps, nil
}

// TestRule 测试单个规则（包括延迟和下载速度）
func (t *Tester) TestRule(ctx context.Context, rule *models.ProxyRule) models.SpeedTestResult {
	result := models.SpeedTestResult{
		RuleID:    rule.ID,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	}

	t.log(fmt.Sprintf("[测速] 开始测试: %s", rule.Alias))

	// 测试延迟
	latency, err := t.TestLatency(ctx, rule.ServerAddr, rule.ServerPort, 5*time.Second)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("延迟测试失败: %v", err)
		t.log(fmt.Sprintf("[测速] %s - 延迟测试失败: %v", rule.Alias, err))
		return result
	}
	result.Latency = latency
	t.log(fmt.Sprintf("[测速] %s - 延迟: %d ms", rule.Alias, latency))

	// 如果规则未启动，只测试延迟
	if !rule.Enabled {
		result.Success = true
		result.DownloadSpeed = 0
		t.log(fmt.Sprintf("[测速] %s - 未启动，跳过速度测试", rule.Alias))
		return result
	}

	// 测试下载速度（限时采样，20 秒整体上限：为慢速网络建连留余量 + 12 秒采样窗口）
	speed, err := t.TestDownloadSpeed(ctx, rule.LocalType, rule.LocalPort, "", 20*time.Second)
	if err != nil {
		// 延迟测试成功但速度测试失败，仍然算部分成功
		result.Success = true
		result.DownloadSpeed = 0
		result.Error = fmt.Sprintf("速度测试失败: %v", err)
		t.log(fmt.Sprintf("[测速] %s - 速度测试失败: %v", rule.Alias, err))
		return result
	}

	result.DownloadSpeed = speed
	result.Success = true
	t.log(fmt.Sprintf("[测速] %s - 下载速度: %.2f MB/s", rule.Alias, speed))

	return result
}

// TestRules 批量测试规则
func (t *Tester) TestRules(ctx context.Context, rules []*models.ProxyRule, concurrent int) []models.SpeedTestResult {
	if concurrent <= 0 {
		concurrent = 3 // 默认并发数
	}

	results := make([]models.SpeedTestResult, len(rules))
	var wg sync.WaitGroup

	// 使用信号量控制并发数
	semaphore := make(chan struct{}, concurrent)

	for i, rule := range rules {
		wg.Add(1)
		go func(index int, r *models.ProxyRule) {
			defer wg.Done()

			// 获取信号量
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = models.SpeedTestResult{
					RuleID:    r.ID,
					Success:   false,
					Error:     "测速被取消",
					Timestamp: time.Now().Format("2006-01-02 15:04:05"),
				}
				return
			}

			// 执行测速
			results[index] = t.TestRule(ctx, r)
		}(i, rule)
	}

	wg.Wait()
	return results
}

// QuickLatencyTest 快速延迟测试（只测试延迟，不测速度）
func (t *Tester) QuickLatencyTest(ctx context.Context, rule *models.ProxyRule) (int, error) {
	return t.TestLatency(ctx, rule.ServerAddr, rule.ServerPort, 5*time.Second)
}

// log 输出日志
func (t *Tester) log(message string) {
	if t.logFunc != nil {
		t.logFunc(message)
	}
}
