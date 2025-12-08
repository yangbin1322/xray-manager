package speedtest

import (
	"context"
	"fmt"
	"io"
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

// TestDownloadSpeed 测试下载速度（通过代理）
func (t *Tester) TestDownloadSpeed(ctx context.Context, proxyType string, proxyPort int, testURL string, timeout time.Duration) (float64, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构建代理 URL
	var proxyURL *url.URL
	var err error
	if proxyType == "socks5" || proxyType == "socks" {
		proxyURL, err = url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", proxyPort))
	} else {
		proxyURL, err = url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
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

	// 如果没有指定测试 URL，使用默认的
	if testURL == "" {
		// 使用 1MB 测试文件
		testURL = "http://cachefly.cachefly.net/1mb.test"
	}

	start := time.Now()
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

	// 读取响应体
	written, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return 0, fmt.Errorf("读取响应失败: %v", err)
	}

	duration := time.Since(start).Seconds()
	if duration == 0 {
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

	// 测试下载速度
	speed, err := t.TestDownloadSpeed(ctx, rule.LocalType, rule.LocalPort, "", 30*time.Second)
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
