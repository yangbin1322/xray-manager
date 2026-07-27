// Package relay 实现凭证改写型 HTTP 代理中继。
//
// 住宅代理服务商（DataImpulse 等）把会话/地区信息编码在代理用户名里，
// 例如 login__cr.au;sessid.123。想要多个出口 IP，传统做法是为每个会话
// 启动一个固定用户名的代理实例，占用几十个本地端口。
//
// 本中继只监听一个端口：客户端在代理凭证里传入会话标识（如 au-123），
// 中继按模板把它拼成上游真实用户名再连上游网关，于是"客户端用户名 →
// 出口 IP"的映射完全动态，无需重启任何进程。
//
// 监听端口是混合端口，HTTP 代理和 SOCKS5 客户端都能接入（SOCKS5 侧会话
// 标识取自 RFC 1929 认证的用户名）；上游侧统一以 HTTP CONNECT 连接。
//
// 上游连接可经一个前置代理（socks5/http）建立，用于加速直连很慢或
// 不通的境外网关。
package relay

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SessionPlaceholder 用户名模板中代表客户端会话标识的占位符。
const SessionPlaceholder = "{session}"

// Config 中继运行参数。
type Config struct {
	// ListenAddr 中继监听地址，如 "127.0.0.1:18080"。
	ListenAddr string

	// UpstreamAddr 上游住宅代理网关地址，如 "gw.dataimpulse.com:823"。
	UpstreamAddr string

	// UsernameTemplate 上游用户名模板，其中 {session} 会被替换为客户端
	// 传入的会话标识。例如 "login__cr.au;sessid.{session}"。
	// 模板为空时直接使用客户端用户名（完全透传模式）。
	UsernameTemplate string

	// UpstreamPassword 上游固定密码。
	UpstreamPassword string

	// LocalUsername/LocalPassword 非空时校验客户端凭证中的密码字段，
	// 避免本机中继被局域网内其他程序滥用。LocalUsername 恒为空——
	// 用户名承载会话标识，只校验密码。
	LocalPassword string

	// PreProxyURL 前置加速代理，形如 socks5://127.0.0.1:1080 或
	// http://127.0.0.1:8080。为空表示直连上游。
	PreProxyURL string

	// DialTimeout 建立上游连接的超时，0 使用默认值。
	DialTimeout time.Duration

	// Logf 日志回调，可为 nil。
	Logf func(string)
}

const (
	defaultDialTimeout = 20 * time.Second
	// 上游代理协商（发送 CONNECT 到收到响应）的超时。
	handshakeTimeout = 30 * time.Second
)

// Stats 中继运行时统计。
type Stats struct {
	ActiveConns  int64 `json:"activeConns"`  // 当前活跃连接数
	TotalConns   int64 `json:"totalConns"`   // 累计接受的连接数
	FailedConns  int64 `json:"failedConns"`  // 建立上游失败的连接数
	SessionCount int   `json:"sessionCount"` // 已出现过的不同会话标识数
	BytesUp      int64 `json:"bytesUp"`      // 上行字节数
	BytesDown    int64 `json:"bytesDown"`    // 下行字节数
}

// Relay 一个运行中的中继实例。
type Relay struct {
	cfg      Config
	listener net.Listener

	// 关闭协调
	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup

	activeConns int64
	totalConns  int64
	failedConns int64
	bytesUp     int64
	bytesDown   int64

	sessionMu sync.Mutex
	sessions  map[string]struct{}
}

// New 创建中继（尚未监听）。
func New(cfg Config) *Relay {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = defaultDialTimeout
	}
	return &Relay{
		cfg:      cfg,
		done:     make(chan struct{}),
		sessions: make(map[string]struct{}),
	}
}

// Validate 校验配置。
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return errors.New("监听地址不能为空")
	}
	if c.UpstreamAddr == "" {
		return errors.New("上游网关地址不能为空")
	}
	if _, _, err := net.SplitHostPort(c.UpstreamAddr); err != nil {
		return fmt.Errorf("上游网关地址需为 host:port 格式: %v", err)
	}
	if c.UsernameTemplate != "" && !strings.Contains(c.UsernameTemplate, SessionPlaceholder) {
		return fmt.Errorf("用户名模板必须包含 %s 占位符", SessionPlaceholder)
	}
	if c.PreProxyURL != "" {
		u, err := url.Parse(c.PreProxyURL)
		if err != nil {
			return fmt.Errorf("前置代理地址无效: %v", err)
		}
		switch u.Scheme {
		case "socks5", "socks5h", "http":
		default:
			return fmt.Errorf("前置代理仅支持 socks5/http，当前为 %q", u.Scheme)
		}
		if u.Host == "" {
			return errors.New("前置代理地址缺少主机名")
		}
	}
	return nil
}

// Start 开始监听并在后台接受连接。
func (r *Relay) Start() error {
	if err := r.cfg.Validate(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", r.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %v", r.cfg.ListenAddr, err)
	}
	r.listener = listener

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.acceptLoop()
	}()
	return nil
}

// Addr 返回实际监听地址（端口为 0 时可取到系统分配的端口）。
func (r *Relay) Addr() net.Addr {
	if r.listener == nil {
		return nil
	}
	return r.listener.Addr()
}

// Stop 停止中继并等待所有连接处理结束。
func (r *Relay) Stop() error {
	var err error
	r.closeOnce.Do(func() {
		close(r.done)
		if r.listener != nil {
			err = r.listener.Close()
		}
	})
	r.wg.Wait()
	return err
}

// Stats 返回当前统计快照。
func (r *Relay) Stats() Stats {
	r.sessionMu.Lock()
	sessionCount := len(r.sessions)
	r.sessionMu.Unlock()
	return Stats{
		ActiveConns:  atomic.LoadInt64(&r.activeConns),
		TotalConns:   atomic.LoadInt64(&r.totalConns),
		FailedConns:  atomic.LoadInt64(&r.failedConns),
		SessionCount: sessionCount,
		BytesUp:      atomic.LoadInt64(&r.bytesUp),
		BytesDown:    atomic.LoadInt64(&r.bytesDown),
	}
}

func (r *Relay) logf(format string, args ...interface{}) {
	if r.cfg.Logf != nil {
		r.cfg.Logf(fmt.Sprintf(format, args...))
	}
}

func (r *Relay) acceptLoop() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			select {
			case <-r.done:
				return
			default:
			}
			// 临时错误（如 fd 耗尽）退避后重试，其余直接退出
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			r.logf("接受连接失败: %v", err)
			return
		}

		atomic.AddInt64(&r.totalConns, 1)
		atomic.AddInt64(&r.activeConns, 1)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer atomic.AddInt64(&r.activeConns, -1)
			defer conn.Close()
			r.handleConn(conn)
		}()
	}
}

// handleConn 处理一个客户端连接。监听端口是混合端口：先看首字节判断
// 客户端说的是 SOCKS5 还是 HTTP 代理协议，再分流给各自的处理器。
// 无论哪种入站，会话标识都取自客户端用户名，上游侧统一是 HTTP CONNECT。
func (r *Relay) handleConn(clientConn net.Conn) {
	reader := bufio.NewReader(clientConn)

	// SOCKS5 首字节是版本号 0x05；HTTP 请求首字节必为方法名的大写字母
	firstByte, err := reader.Peek(1)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			r.logf("读取客户端首字节失败: %v", err)
		}
		return
	}
	if firstByte[0] == socks5Version {
		r.handleSOCKS5Inbound(clientConn, reader)
		return
	}

	r.handleHTTPInbound(clientConn, reader)
}

// handleHTTPInbound 处理 HTTP 代理入站：读首个请求，取出会话标识，
// 建立到上游的隧道，之后 CONNECT 走裸转发、普通请求走 HTTP 转发。
func (r *Relay) handleHTTPInbound(clientConn net.Conn, reader *bufio.Reader) {
	req, err := http.ReadRequest(reader)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			r.logf("解析客户端请求失败: %v", err)
		}
		return
	}

	session, password, ok := parseProxyAuth(req.Header.Get("Proxy-Authorization"))
	if !ok {
		writeSimpleResponse(clientConn, http.StatusProxyAuthRequired,
			"Proxy-Authenticate: Basic realm=\"xray-manager relay\"\r\n",
			"需要在代理用户名中提供会话标识")
		return
	}
	if r.cfg.LocalPassword != "" && password != r.cfg.LocalPassword {
		writeSimpleResponse(clientConn, http.StatusProxyAuthRequired, "", "代理密码错误")
		return
	}
	if session == "" {
		writeSimpleResponse(clientConn, http.StatusProxyAuthRequired, "", "代理用户名不能为空")
		return
	}
	r.recordSession(session)

	upstreamUser := r.upstreamUsername(session)
	upstreamAuth := basicAuthHeader(upstreamUser, r.cfg.UpstreamPassword)

	upstreamConn, err := r.dialUpstream()
	if err != nil {
		atomic.AddInt64(&r.failedConns, 1)
		r.logf("连接上游 %s 失败 (会话 %s): %v", r.cfg.UpstreamAddr, session, err)
		writeSimpleResponse(clientConn, http.StatusBadGateway, "", "连接上游代理失败")
		return
	}
	defer upstreamConn.Close()

	if req.Method == http.MethodConnect {
		r.handleConnect(clientConn, reader, upstreamConn, req, upstreamAuth, session)
		return
	}
	r.handlePlainHTTP(clientConn, reader, upstreamConn, req, upstreamAuth, session)
}

// handleConnect 把客户端的 CONNECT 请求原样转给上游（换成上游凭证），
// 上游返回 2xx 后双向裸转发。
func (r *Relay) handleConnect(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn, req *http.Request, upstreamAuth, session string) {
	upstreamReader, err := openUpstreamTunnel(upstreamConn, req.Host, upstreamAuth)
	if err != nil {
		atomic.AddInt64(&r.failedConns, 1)
		r.logf("上游隧道建立失败 %s (会话 %s): %v", req.Host, session, err)
		writeSimpleResponse(clientConn, http.StatusBadGateway, "", "连接上游代理失败")
		return
	}

	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}

	// 上游握手时可能已把隧道数据读进 bufio 缓冲，先补发再进入裸转发
	if n := upstreamReader.Buffered(); n > 0 {
		buffered, _ := upstreamReader.Peek(n)
		if _, err := clientConn.Write(buffered); err != nil {
			return
		}
		atomic.AddInt64(&r.bytesDown, int64(n))
		_, _ = upstreamReader.Discard(n)
	}

	r.pipe(clientConn, clientReader, upstreamConn)
}

// handlePlainHTTP 处理非 CONNECT 的代理请求（明文 HTTP）。
// 同一连接上的后续请求复用同一条上游连接，因此会话标识保持不变。
func (r *Relay) handlePlainHTTP(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn, firstReq *http.Request, upstreamAuth, session string) {
	upstreamReader := bufio.NewReader(upstreamConn)
	req := firstReq

	for {
		req.Header.Set("Proxy-Authorization", upstreamAuth)
		// 保持代理链路复用，避免上游在每个请求后断开导致会话重建
		req.Header.Set("Proxy-Connection", "Keep-Alive")

		_ = upstreamConn.SetDeadline(time.Now().Add(handshakeTimeout))
		if err := req.WriteProxy(upstreamConn); err != nil {
			r.logf("转发请求到上游失败 (会话 %s): %v", session, err)
			writeSimpleResponse(clientConn, http.StatusBadGateway, "", "上游代理写入失败")
			return
		}

		resp, err := http.ReadResponse(upstreamReader, req)
		if err != nil {
			r.logf("读取上游响应失败 (会话 %s): %v", session, err)
			writeSimpleResponse(clientConn, http.StatusBadGateway, "", "上游代理无响应")
			return
		}
		_ = upstreamConn.SetDeadline(time.Time{})

		if err := resp.Write(clientConn); err != nil {
			resp.Body.Close()
			return
		}
		resp.Body.Close()

		if resp.Close || req.Close {
			return
		}

		next, err := http.ReadRequest(clientReader)
		if err != nil {
			return
		}
		req = next
	}
}

// pipe 在客户端与上游之间双向复制数据，任一方向结束即收尾。
//
// 只等第一个方向结束，而不是等两个都结束：客户端用完隧道后常常留着连接
// 复用，此时另一个方向会一直阻塞在 io.Copy 上。等两个方向会让连接和它
// 占用的上游会话迟迟不释放——住宅代理按并发连接数限流，这类泄漏会直接
// 表现为后续连接超时。
func (r *Relay) pipe(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn) {
	// 中继停止时强制断开，避免长连接阻塞 Stop
	finished := make(chan struct{})
	go func() {
		select {
		case <-r.done:
		case <-finished:
		}
		// 无论哪种情况都关掉两端：让仍阻塞在 io.Copy 的一侧立即返回
		clientConn.Close()
		upstreamConn.Close()
	}()

	done := make(chan struct{}, 2)

	go func() {
		n, _ := io.Copy(upstreamConn, clientReader)
		atomic.AddInt64(&r.bytesUp, n)
		closeWrite(upstreamConn)
		done <- struct{}{}
	}()

	go func() {
		n, _ := io.Copy(clientConn, upstreamConn)
		atomic.AddInt64(&r.bytesDown, n)
		closeWrite(clientConn)
		done <- struct{}{}
	}()

	// 只等第一个方向结束，随后由上面的 goroutine 关闭两端收尾
	<-done
	close(finished)
}

// closeWrite 半关闭写方向，让对端读到 EOF；不支持时退化为直接关闭。
func closeWrite(conn net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = conn.Close()
}

// upstreamUsername 按模板拼接上游用户名。模板为空时透传客户端用户名。
func (r *Relay) upstreamUsername(session string) string {
	if r.cfg.UsernameTemplate == "" {
		return session
	}
	return strings.ReplaceAll(r.cfg.UsernameTemplate, SessionPlaceholder, session)
}

// recordSession 记录出现过的会话标识，并在首次出现时打一条日志。
// 只在首次记录，避免每个请求都刷屏——但用户至少能看到"会话确实被识别了"。
func (r *Relay) recordSession(session string) {
	r.sessionMu.Lock()
	_, seen := r.sessions[session]
	// 会话标识由客户端提供，做个上限防止长时间运行后无限增长
	if !seen && len(r.sessions) < 10000 {
		r.sessions[session] = struct{}{}
	}
	total := len(r.sessions)
	r.sessionMu.Unlock()

	if !seen {
		r.logf("新会话 %q → 上游用户名 %q（当前共 %d 个会话）",
			session, r.upstreamUsername(session), total)
	}
}

// dialUpstream 建立到上游网关的 TCP 连接，必要时经前置代理。
func (r *Relay) dialUpstream() (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.DialTimeout)
	defer cancel()

	if r.cfg.PreProxyURL == "" {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", r.cfg.UpstreamAddr)
	}

	u, err := url.Parse(r.cfg.PreProxyURL)
	if err != nil {
		return nil, fmt.Errorf("前置代理地址无效: %v", err)
	}
	switch u.Scheme {
	case "socks5", "socks5h":
		return dialViaSOCKS5(ctx, u, r.cfg.UpstreamAddr)
	case "http":
		return dialViaHTTPProxy(ctx, u, r.cfg.UpstreamAddr)
	default:
		return nil, fmt.Errorf("前置代理仅支持 socks5/http，当前为 %q", u.Scheme)
	}
}

// parseProxyAuth 解析 Proxy-Authorization 头，返回用户名（会话标识）和密码。
func parseProxyAuth(header string) (session, password string, ok bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}
	return user, pass, true
}

func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// writeSimpleResponse 向客户端返回一个短响应；extraHeaders 需自带 CRLF 结尾。
func writeSimpleResponse(conn net.Conn, statusCode int, extraHeaders, body string) {
	statusText := http.StatusText(statusCode)
	if statusText == "" {
		statusText = "Error"
	}
	_, _ = fmt.Fprintf(conn,
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n%s\r\n%s",
		statusCode, statusText, len(body), extraHeaders, body)
}
