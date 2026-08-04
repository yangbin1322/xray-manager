// Package healthcheck 节点健康检查。
// 无需启动节点即可直接对服务器进行连通性检测：
// DNS 解析 → TCP 连接（测延迟）→ TLS/Reality 握手（按协议配置）。
// Hysteria2/TUIC 等 QUIC(UDP) 协议使用 UDP 探测。
package healthcheck

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
	"xray-manager/internal/models"
)

// 健康状态常量
const (
	StatusOnline        = "online"
	StatusHighLatency   = "high_latency"
	StatusTimeout       = "timeout"
	StatusDNSFailed     = "dns_failed"
	StatusTLSFailed     = "tls_failed"
	StatusRealityFailed = "reality_failed"
	// StatusIPv6Only 节点只有 IPv6 地址，而本机没有 IPv6 出口。
	// 这不是节点故障——内核走自己的链路时通常可用，只是本机无法直接探测。
	StatusIPv6Only = "ipv6_only"
)

// Manager 健康检查管理器
type Manager struct {
	logFunc  func(string)
	getRules func() []models.ProxyRule             // 获取所有规则的快照
	onResult func(result models.HealthCheckResult) // 单个节点检查完成回调
	onDone   func()                                // 一轮批量检查完成回调

	mu     sync.Mutex
	cfg    models.HealthCheckConfig
	cancel context.CancelFunc // 后台定时任务取消函数
}

// NewManager 创建健康检查管理器
func NewManager(logFunc func(string), getRules func() []models.ProxyRule,
	onResult func(models.HealthCheckResult), onDone func()) *Manager {
	return &Manager{
		logFunc:  logFunc,
		getRules: getRules,
		onResult: onResult,
		onDone:   onDone,
	}
}

// normalizeConfig 填充默认值
func normalizeConfig(cfg models.HealthCheckConfig) models.HealthCheckConfig {
	if cfg.IntervalSec <= 0 {
		cfg.IntervalSec = 60
	}
	if cfg.IntervalSec < 10 {
		cfg.IntervalSec = 10
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 5
	}
	if cfg.LatencyThreshold <= 0 {
		cfg.LatencyThreshold = 500
	}
	return cfg
}

// Configure 更新配置并按需重启后台检测任务
func (m *Manager) Configure(cfg models.HealthCheckConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = normalizeConfig(cfg)

	// 停止旧任务
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	if !m.cfg.Enabled {
		m.log("[健康检查] 后台自动检测已关闭")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	interval := time.Duration(m.cfg.IntervalSec) * time.Second

	m.log(fmt.Sprintf("[健康检查] 后台自动检测已启动，周期: %d 秒", m.cfg.IntervalSec))

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.CheckRules(ctx, m.getRules())
			}
		}
	}()
}

// GetConfig 获取当前配置
func (m *Manager) GetConfig() models.HealthCheckConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return normalizeConfig(m.cfg)
}

// Stop 停止后台检测
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// concurrencyFor 按节点数量决定并发上限。
//
// 检测是纯网络等待（DNS/TCP/TLS），几乎不吃 CPU，瓶颈在于超时时长：
// 并发 8 时每个不可达节点要占满一个槽位直到超时，实测 200 个超时节点耗时 50 秒，
// 折算 1 万节点要 42 分钟；提到 256 后同样的量只需 2 秒（约 100 秒/万）。
// 上限 256 是为了不耗尽文件描述符（macOS 默认 ulimit 常见为 256~1024）。
func concurrencyFor(n int) int {
	switch {
	case n <= 16:
		return 8 // 少量节点没必要铺开，避免瞬时打满网络
	case n <= 200:
		return 64
	default:
		return 256
	}
}

// CheckRules 并发检查一组节点（阻塞直到全部完成）
func (m *Manager) CheckRules(ctx context.Context, rules []models.ProxyRule) {
	cfg := m.GetConfig()

	sem := make(chan struct{}, concurrencyFor(len(rules)))
	var wg sync.WaitGroup

	for i := range rules {
		rule := rules[i]
		select {
		case <-ctx.Done():
			return
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			result := CheckRule(&rule, cfg)
			if m.onResult != nil {
				m.onResult(result)
			}
		}()
	}

	wg.Wait()
	if m.onDone != nil {
		m.onDone()
	}
}

// 域名复核结果
type domainVerdict int

const (
	domainUnknown  domainVerdict = iota // 复核不了（公共 DNS 也不通），按可用处理，继续往下试
	domainNotFound                      // 确认不存在：公共 DNS 也说没有任何记录
	domainIPv6Only                      // 只有 AAAA 记录，没有 A 记录
)

// publicDNSServers 复核用的公共 DNS。
// 国内外各留一个，避免单点不可达导致复核失效。
var publicDNSServers = []string{"223.5.5.5:53", "1.1.1.1:53"}

// verifyDomain 用公共 DNS 复核域名，判断"系统解析器说不存在"是否属实。
//
// 存在的意义：本机没有 IPv6 出口时，Windows 的 getaddrinfo 会把纯 AAAA 域名
// 也报成 NXDOMAIN；国内网络的 DNS 污染同样会返回 NXDOMAIN。这两种情况都不
// 代表节点不可用，直接判失败会产生大量误报。
func verifyDomain(host string, timeout time.Duration) domainVerdict {
	for _, server := range publicDNSServers {
		resolver := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: timeout}
				return d.DialContext(ctx, "udp", server)
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		v4, err4 := resolver.LookupIP(ctx, "ip4", host)
		cancel()
		if err4 == nil && len(v4) > 0 {
			return domainUnknown // 有 A 记录，是本机解析的问题
		}

		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		v6, err6 := resolver.LookupIP(ctx, "ip6", host)
		cancel()
		if err6 == nil && len(v6) > 0 {
			return domainIPv6Only
		}

		// 两种记录都明确不存在，才认定域名无效；
		// 若是网络错误（查不通），换下一个 DNS 继续试
		var dnsErr *net.DNSError
		if errors.As(err4, &dnsErr) && dnsErr.IsNotFound &&
			errors.As(err6, &dnsErr) && dnsErr.IsNotFound {
			return domainNotFound
		}
	}
	// 所有公共 DNS 都没能给出确定答案，不轻易判死
	return domainUnknown
}

// CheckRule 检查单个节点（纯函数，可独立调用）
func CheckRule(rule *models.ProxyRule, cfg models.HealthCheckConfig) models.HealthCheckResult {
	cfg = normalizeConfig(cfg)
	timeout := time.Duration(cfg.TimeoutSec) * time.Second

	result := models.HealthCheckResult{
		RuleID:    rule.ID,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	}

	// 1. 解析域名。
	//
	// 这里刻意不自己挑一个 IP 去连，而是把所有地址都交给 Dialer 逐个尝试
	// （net.Dialer 对域名会自动做 Happy Eyeballs：并行尝试 IPv4/IPv6 并取先成功的）。
	//
	// 之前的实现取 ips[0] 单个地址，有两个问题，都会造成"检测报失败但节点能启动"：
	//   - LookupIP("ip") 同时返回 A 和 AAAA 且顺序不定。若首位是 IPv6 而本机没有
	//     IPv6 出口，连接必然失败；而启动节点时是内核自己解析并逐个重试，所以能通。
	//   - 多 IP 的域名（CDN 很常见）只试第一个，那个 IP 恰好不通就误判整个节点。
	addr := rule.ServerAddr
	isIP := net.ParseIP(addr) != nil
	if !isIP {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", addr)
		cancel()

		// 系统解析器报"域名不存在"时不能直接下结论：
		//   - 本机没有 IPv6 出口时，Windows 的 getaddrinfo 会把纯 AAAA 域名
		//     也报成 NXDOMAIN（机场常见的 "V6" 节点就是这种）；
		//   - 国内网络里 DNS 污染同样会返回 NXDOMAIN。
		// 这两种情况节点其实都能用——内核走自己的 DNS 和链路。
		// 因此再用公共 DNS 复核一次，确认无误才判失败。
		if err != nil && len(ips) == 0 {
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
				switch verifyDomain(addr, timeout) {
				case domainIPv6Only:
					// 域名只有 AAAA 记录，而本机无 IPv6 出口：
					// 检测不了不等于节点不可用，如实说明而不是判死
					result.Status = StatusIPv6Only
					result.Error = "该节点仅有 IPv6 地址，本机无 IPv6 出口，无法直接检测（节点本身可能可用）"
					return result
				case domainNotFound:
					result.Status = StatusDNSFailed
					result.Error = fmt.Sprintf("域名不存在: %s", addr)
					return result
				default:
					// 公共 DNS 能解析出 IPv4，说明是本机 DNS 被污染或配置异常，
					// 继续往下走，交给拨号器再试
				}
			}
		}
	}

	target := net.JoinHostPort(addr, fmt.Sprintf("%d", rule.ServerPort))

	// 2. QUIC(UDP) 协议使用 UDP 探测
	if rule.Protocol == "hysteria2" || rule.Protocol == "tuic" {
		return checkUDP(target, timeout, result)
	}

	// 3. TCP 连接（测延迟）。
	// 用 Dialer 而非固定单个 IP：域名有多个地址时会依次尝试，
	// 与 Xray 启动节点时的行为一致。
	start := time.Now()
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		// 区分"域名压根解析不出来"和"能解析但连不上"，便于用户判断问题在哪
		var dnsErr *net.DNSError
		if !isIP && errors.As(err, &dnsErr) {
			result.Status = StatusDNSFailed
			result.Error = fmt.Sprintf("DNS 解析失败: %v", err)
			return result
		}
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("TCP 连接失败: %v", err)
		return result
	}
	latency := int(time.Since(start).Milliseconds())
	result.Latency = latency

	// 4. TLS / Reality 握手检测
	security := rule.Settings.Security
	if security == "tls" || security == "reality" {
		serverName := rule.ServerAddr
		insecure := security == "reality" // Reality 借用目标站证书，跳过校验只验证握手可达
		var alpn []string
		if rule.Settings.TLS != nil {
			if rule.Settings.TLS.ServerName != "" {
				serverName = rule.Settings.TLS.ServerName
			}
			if rule.Settings.TLS.AllowInsecure {
				insecure = true
			}
			alpn = rule.Settings.TLS.ALPN
		}

		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: insecure,
			NextProtos:         alpn,
		})
		_ = tlsConn.SetDeadline(time.Now().Add(timeout))
		err := tlsConn.Handshake()
		tlsConn.Close()

		if err != nil {
			if security == "reality" {
				result.Status = StatusRealityFailed
				result.Error = fmt.Sprintf("Reality 握手失败: %v", err)
			} else {
				result.Status = StatusTLSFailed
				result.Error = fmt.Sprintf("TLS 握手失败: %v", err)
			}
			return result
		}
	} else {
		conn.Close()
	}

	// 5. 延迟分级
	if latency > cfg.LatencyThreshold {
		result.Status = StatusHighLatency
	} else {
		result.Status = StatusOnline
	}
	return result
}

// CheckProxyPort 通过本地代理端口检测健康状态（用于故障转移/链式代理这类组合节点）。
// 先确认代理端口在监听，再经 SOCKS5 代理做一次 HTTP 探测测整条链路延迟。
// id 用于回填结果，cfg 提供超时与延迟阈值。
func CheckProxyPort(id string, proxyPort int, cfg models.HealthCheckConfig) models.HealthCheckResult {
	cfg = normalizeConfig(cfg)
	timeout := time.Duration(cfg.TimeoutSec) * time.Second

	result := models.HealthCheckResult{
		RuleID:    id,
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	}

	// 1. 确认本地代理端口在监听
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), timeout)
	if err != nil {
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("本地代理端口不可达（可能未启动）: %v", err)
		return result
	}
	conn.Close()

	// 2. 经代理做 HTTP 探测，测整条链路往返延迟
	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", proxyPort))
	if err != nil {
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("构建代理地址失败: %v", err)
		return result
	}
	client := &http.Client{
		Timeout:   timeout + 3*time.Second, // 链路探测比本地连接留更多余量
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	start := time.Now()
	req, _ := http.NewRequest("GET", "https://www.gstatic.com/generate_204", nil)
	resp, err := client.Do(req)
	if err != nil {
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("经代理连通性测试失败: %v", err)
		return result
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	latency := int(time.Since(start).Milliseconds())
	result.Latency = latency
	if latency > cfg.LatencyThreshold {
		result.Status = StatusHighLatency
	} else {
		result.Status = StatusOnline
	}
	return result
}

// checkUDP 对 QUIC(UDP) 协议做尽力探测：
// 发送探测包后等待读取。收到 ICMP 端口不可达会表现为读取错误 → 端口未开放；
// 读取超时说明数据包未被拒绝，视为可达（无法精确测量延迟）。
func checkUDP(target string, timeout time.Duration, result models.HealthCheckResult) models.HealthCheckResult {
	conn, err := net.DialTimeout("udp", target, timeout)
	if err != nil {
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("UDP 连接失败: %v", err)
		return result
	}
	defer conn.Close()

	start := time.Now()
	if _, err := conn.Write([]byte{0x00}); err != nil {
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("UDP 发送失败: %v", err)
		return result
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// 未收到拒绝，视为在线（QUIC 服务不响应非法包属正常行为）
			result.Status = StatusOnline
			result.Latency = 0
			return result
		}
		// 连接被重置（ICMP 端口不可达）
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("UDP 端口不可达: %v", err)
		return result
	}

	// 收到了响应（部分服务器会回应）
	result.Latency = int(time.Since(start).Milliseconds())
	result.Status = StatusOnline
	return result
}

// log 输出日志
func (m *Manager) log(message string) {
	if m.logFunc != nil {
		m.logFunc(message)
	}
}
