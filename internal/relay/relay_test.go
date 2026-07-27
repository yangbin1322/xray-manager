package relay

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream 模拟住宅代理网关：记录收到的 Proxy-Authorization，
// 对 CONNECT 建立到目标的隧道，对普通请求返回固定响应。
type fakeUpstream struct {
	listener net.Listener

	mu          sync.Mutex
	authSeen    []string
	targetsSeen []string
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	up := &fakeUpstream{listener: listener}
	go up.serve()
	t.Cleanup(func() { listener.Close() })
	return up
}

func (u *fakeUpstream) addr() string { return u.listener.Addr().String() }

func (u *fakeUpstream) recordedAuth() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.authSeen...)
}

// recordedTargets 返回上游收到的 CONNECT 目标（用于验证域名未被本地解析）
func (u *fakeUpstream) recordedTargets() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.targetsSeen...)
}

func (u *fakeUpstream) serve() {
	for {
		conn, err := u.listener.Accept()
		if err != nil {
			return
		}
		go u.handle(conn)
	}
}

func (u *fakeUpstream) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	u.mu.Lock()
	u.authSeen = append(u.authSeen, req.Header.Get("Proxy-Authorization"))
	if req.Method == http.MethodConnect {
		u.targetsSeen = append(u.targetsSeen, req.Host)
	}
	u.mu.Unlock()

	if req.Method != http.MethodConnect {
		body := "plain-ok"
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		return
	}

	target, err := net.Dial("tcp", req.Host)
	if err != nil {
		io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer target.Close()
	io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(target, reader) }()
	go func() { defer wg.Done(); io.Copy(conn, target) }()
	wg.Wait()
}

// echoServer 隧道另一端：把收到的内容原样回写。
func newEchoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	t.Cleanup(func() { listener.Close() })
	return listener
}

func startRelay(t *testing.T, cfg Config) *Relay {
	t.Helper()
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:0"
	}
	r := New(cfg)
	if err := r.Start(); err != nil {
		t.Fatalf("启动中继失败: %v", err)
	}
	t.Cleanup(func() { r.Stop() })
	return r
}

func proxyAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// writeConnectRequest 以 HTTP 代理协议向中继发起 CONNECT。
func writeConnectRequest(t *testing.T, conn net.Conn, target, auth string) {
	t.Helper()
	_, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", target, target, auth)
	if err != nil {
		t.Fatalf("发送 CONNECT 失败: %v", err)
	}
}

// readConnectStatus 读取 CONNECT 响应状态码。
func readConnectStatus(t *testing.T, reader *bufio.Reader) int {
	t.Helper()
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("读取 CONNECT 响应失败: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestUpstreamUsernameFromTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		session  string
		want     string
	}{
		{"套用模板", "login__cr.au;sessid.{session}", "123", "login__cr.au;sessid.123"},
		{"模板为空时透传", "", "login__cr.uk;sessid.7", "login__cr.uk;sessid.7"},
		{"占位符出现多次", "{session}-x-{session}", "9", "9-x-9"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(Config{UsernameTemplate: tc.template})
			if got := r.upstreamUsername(tc.session); got != tc.want {
				t.Errorf("upstreamUsername(%q) = %q, 期望 %q", tc.session, got, tc.want)
			}
		})
	}
}

// 核心需求：不同客户端用户名必须被改写成不同的上游会话用户名。
func TestConnectRewritesCredentialsPerSession(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "login__cr.au;sessid.{session}",
		UpstreamPassword: "secret",
	})

	for _, session := range []string{"123", "45"} {
		conn, err := net.Dial("tcp", r.Addr().String())
		if err != nil {
			t.Fatalf("连接中继失败: %v", err)
		}

		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
			echo.Addr(), echo.Addr(), proxyAuth(session, "anything"))

		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatalf("读取 CONNECT 响应失败: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("会话 %s 的 CONNECT 返回 %d，期望 200", session, resp.StatusCode)
		}

		// 隧道应真正连通到 echo 服务
		payload := "hello-" + session
		io.WriteString(conn, payload)
		buf := make([]byte, len(payload))
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(reader, buf); err != nil {
			t.Fatalf("读取隧道回显失败: %v", err)
		}
		if string(buf) != payload {
			t.Errorf("隧道回显 = %q，期望 %q", buf, payload)
		}
		conn.Close()
	}

	got := upstream.recordedAuth()
	want := []string{
		proxyAuth("login__cr.au;sessid.123", "secret"),
		proxyAuth("login__cr.au;sessid.45", "secret"),
	}
	if len(got) != len(want) {
		t.Fatalf("上游收到 %d 个认证头，期望 %d 个", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			user, _, _ := parseProxyAuth(got[i])
			wantUser, _, _ := parseProxyAuth(want[i])
			t.Errorf("第 %d 个上游用户名 = %q，期望 %q", i+1, user, wantUser)
		}
	}
}

func TestPlainHTTPRewritesCredentials(t *testing.T) {
	upstream := newFakeUpstream(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "login__cr.us;sessid.{session}",
		UpstreamPassword: "pw",
	})

	conn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\nProxy-Authorization: %s\r\nConnection: close\r\n\r\n",
		proxyAuth("77", "x"))

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "plain-ok" {
		t.Errorf("响应体 = %q，期望 %q", body, "plain-ok")
	}

	auth := upstream.recordedAuth()
	if len(auth) != 1 {
		t.Fatalf("上游收到 %d 个认证头，期望 1 个", len(auth))
	}
	user, pass, _ := parseProxyAuth(auth[0])
	if user != "login__cr.us;sessid.77" || pass != "pw" {
		t.Errorf("上游凭证 = %q/%q，期望 %q/%q", user, pass, "login__cr.us;sessid.77", "pw")
	}
}

func TestMissingProxyAuthIsRejected(t *testing.T) {
	upstream := newFakeUpstream(t)
	r := startRelay(t, Config{UpstreamAddr: upstream.addr(), UsernameTemplate: "u-{session}"})

	conn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer conn.Close()

	io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("状态码 = %d，期望 407", resp.StatusCode)
	}
	if len(upstream.recordedAuth()) != 0 {
		t.Error("未认证的请求不应到达上游")
	}
}

func TestWrongLocalPasswordIsRejected(t *testing.T) {
	upstream := newFakeUpstream(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "u-{session}",
		LocalPassword:    "correct",
	})

	conn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: %s\r\n\r\n",
		proxyAuth("123", "wrong"))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("状态码 = %d，期望 407", resp.StatusCode)
	}
	if len(upstream.recordedAuth()) != 0 {
		t.Error("密码错误的请求不应到达上游")
	}
}

// 前置加速代理：中继必须经它去连上游，而不是直连。
func TestDialsUpstreamViaSOCKS5PreProxy(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	socks := newFakeSOCKS5Server(t)

	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "login;sessid.{session}",
		UpstreamPassword: "pw",
		PreProxyURL:      "socks5://" + socks.addr(),
	})

	conn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
		echo.Addr(), echo.Addr(), proxyAuth("55", "x"))

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("读取 CONNECT 响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT 返回 %d，期望 200", resp.StatusCode)
	}

	if got := socks.targets(); len(got) != 1 || got[0] != upstream.addr() {
		t.Errorf("SOCKS5 前置代理收到的目标 = %v，期望 [%s]", got, upstream.addr())
	}

	payload := "via-preproxy"
	io.WriteString(conn, payload)
	buf := make([]byte, len(payload))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("读取隧道回显失败: %v", err)
	}
	if string(buf) != payload {
		t.Errorf("经前置代理的隧道回显 = %q，期望 %q", buf, payload)
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"缺少监听地址", Config{UpstreamAddr: "gw:823"}, "监听地址"},
		{"缺少上游地址", Config{ListenAddr: "127.0.0.1:0"}, "上游网关地址"},
		{"上游缺少端口", Config{ListenAddr: "127.0.0.1:0", UpstreamAddr: "gw.example.com"}, "host:port"},
		{"模板缺少占位符", Config{ListenAddr: "127.0.0.1:0", UpstreamAddr: "gw:823", UsernameTemplate: "login__cr.au"}, "占位符"},
		{"前置代理协议不支持", Config{ListenAddr: "127.0.0.1:0", UpstreamAddr: "gw:823", PreProxyURL: "ftp://127.0.0.1:1080"}, "socks5/http"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("期望校验失败，实际通过")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息 = %q，期望包含 %q", err, tc.want)
			}
		})
	}
}

func TestStatsTracksSessionsAndConns(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "u-{session}",
	})

	// 同一会话连两次，不同会话连一次 → 2 个不同会话，3 条连接
	for _, session := range []string{"a", "a", "b"} {
		conn, err := net.Dial("tcp", r.Addr().String())
		if err != nil {
			t.Fatalf("连接中继失败: %v", err)
		}
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
			echo.Addr(), echo.Addr(), proxyAuth(session, "x"))
		http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
		conn.Close()
	}

	stats := r.Stats()
	if stats.TotalConns != 3 {
		t.Errorf("TotalConns = %d，期望 3", stats.TotalConns)
	}
	if stats.SessionCount != 2 {
		t.Errorf("SessionCount = %d，期望 2", stats.SessionCount)
	}
}
