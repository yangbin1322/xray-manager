package relay

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// dialThroughRelaySOCKS5 用标准库之外的真实 SOCKS5 客户端实现接入中继，
// 验证握手兼容性（而不是只和自己写的编码器对拨）。
func dialThroughRelaySOCKS5(t *testing.T, relayAddr, session, password, target string) (net.Conn, error) {
	t.Helper()
	dialer, err := proxy.SOCKS5("tcp", relayAddr, &proxy.Auth{User: session, Password: password}, proxy.Direct)
	if err != nil {
		t.Fatalf("构造 SOCKS5 客户端失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return dialer.(proxy.ContextDialer).DialContext(ctx, "tcp", target)
}

// 核心需求的 SOCKS5 版本：不同用户名 → 不同上游会话用户名。
func TestSOCKS5InboundRewritesCredentialsPerSession(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "login__cr.au;sessid.{session}",
		UpstreamPassword: "secret",
	})

	for _, session := range []string{"123", "45"} {
		conn, err := dialThroughRelaySOCKS5(t, r.Addr().String(), session, "anything", echo.Addr().String())
		if err != nil {
			t.Fatalf("会话 %s 经 SOCKS5 接入失败: %v", session, err)
		}

		payload := "socks5-" + session
		if _, err := io.WriteString(conn, payload); err != nil {
			t.Fatalf("写入隧道失败: %v", err)
		}
		buf := make([]byte, len(payload))
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("读取隧道回显失败: %v", err)
		}
		if string(buf) != payload {
			t.Errorf("隧道回显 = %q，期望 %q", buf, payload)
		}
		conn.Close()
	}

	auths := upstream.recordedAuth()
	if len(auths) != 2 {
		t.Fatalf("上游收到 %d 个认证头，期望 2 个", len(auths))
	}
	for i, wantUser := range []string{"login__cr.au;sessid.123", "login__cr.au;sessid.45"} {
		user, pass, _ := parseProxyAuth(auths[i])
		if user != wantUser || pass != "secret" {
			t.Errorf("第 %d 个上游凭证 = %q/%q，期望 %q/%q", i+1, user, pass, wantUser, "secret")
		}
	}
}

// 同一个端口必须同时接受 SOCKS5 和 HTTP 两种客户端。
func TestMixedPortAcceptsBothProtocols(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "u-{session}",
	})

	// SOCKS5 客户端
	socksConn, err := dialThroughRelaySOCKS5(t, r.Addr().String(), "s5", "x", echo.Addr().String())
	if err != nil {
		t.Fatalf("SOCKS5 接入失败: %v", err)
	}
	socksConn.Close()

	// HTTP 客户端（同一端口）
	httpConn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer httpConn.Close()
	writeConnectRequest(t, httpConn, echo.Addr().String(), proxyAuth("http1", "x"))
	if code := readConnectStatus(t, bufio.NewReader(httpConn)); code != 200 {
		t.Fatalf("HTTP CONNECT 返回 %d，期望 200", code)
	}

	auths := upstream.recordedAuth()
	if len(auths) != 2 {
		t.Fatalf("上游收到 %d 个认证头，期望 2 个", len(auths))
	}
	gotUsers := make([]string, 0, 2)
	for _, a := range auths {
		user, _, _ := parseProxyAuth(a)
		gotUsers = append(gotUsers, user)
	}
	for i, want := range []string{"u-s5", "u-http1"} {
		if gotUsers[i] != want {
			t.Errorf("第 %d 个上游用户名 = %q，期望 %q", i+1, gotUsers[i], want)
		}
	}
}

func TestSOCKS5InboundRejectsWrongPassword(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "u-{session}",
		LocalPassword:    "correct",
	})

	_, err := dialThroughRelaySOCKS5(t, r.Addr().String(), "123", "wrong", echo.Addr().String())
	if err == nil {
		t.Fatal("期望密码错误时握手失败")
	}
	if len(upstream.recordedAuth()) != 0 {
		t.Error("密码错误的连接不应到达上游")
	}
}

// 客户端不提供用户名认证方式时无法携带会话，必须拒绝。
func TestSOCKS5InboundRejectsNoAuthClient(t *testing.T) {
	upstream := newFakeUpstream(t)
	r := startRelay(t, Config{UpstreamAddr: upstream.addr(), UsernameTemplate: "u-{session}"})

	conn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer conn.Close()

	// 只声明"无需认证"
	if _, err := conn.Write([]byte{socks5Version, 0x01, socks5AuthNone}); err != nil {
		t.Fatalf("写入握手失败: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("读取握手响应失败: %v", err)
	}
	if reply[1] != 0xFF {
		t.Errorf("方法选择 = 0x%02X，期望 0xFF（无可接受方法）", reply[1])
	}
}

// 住宅网关只支持 TCP CONNECT，BIND/UDP 必须明确拒绝而不是挂起。
func TestSOCKS5InboundRejectsUDPAssociate(t *testing.T) {
	upstream := newFakeUpstream(t)
	r := startRelay(t, Config{UpstreamAddr: upstream.addr(), UsernameTemplate: "u-{session}"})

	conn, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatalf("连接中继失败: %v", err)
	}
	defer conn.Close()

	// 握手 + 认证
	conn.Write([]byte{socks5Version, 0x01, socks5AuthUser})
	io.ReadFull(conn, make([]byte, 2))
	conn.Write(append([]byte{0x01, 0x03}, append([]byte("abc"), append([]byte{0x01}, 'x')...)...))
	authReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, authReply); err != nil {
		t.Fatalf("读取认证响应失败: %v", err)
	}
	if authReply[1] != 0x00 {
		t.Fatalf("认证被拒绝: 0x%02X", authReply[1])
	}

	// UDP ASSOCIATE (0x03)
	conn.Write([]byte{socks5Version, 0x03, 0x00, socks5AddrIPv4, 127, 0, 0, 1, 0x00, 0x50})
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("读取应答失败: %v", err)
	}
	if reply[1] != socks5ReplyCmdNotSupported {
		t.Errorf("应答码 = 0x%02X，期望 0x07（命令不支持）", reply[1])
	}
}

// SOCKS5 入站同样要能经前置加速节点连上游。
func TestSOCKS5InboundViaPreProxy(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	socks := newFakeSOCKS5Server(t)

	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "login;sessid.{session}",
		UpstreamPassword: "pw",
		PreProxyURL:      "socks5://" + socks.addr(),
	})

	conn, err := dialThroughRelaySOCKS5(t, r.Addr().String(), "88", "x", echo.Addr().String())
	if err != nil {
		t.Fatalf("经前置代理的 SOCKS5 接入失败: %v", err)
	}
	defer conn.Close()

	if got := socks.targets(); len(got) != 1 || got[0] != upstream.addr() {
		t.Errorf("前置代理收到的目标 = %v，期望 [%s]", got, upstream.addr())
	}

	payload := "socks5-via-preproxy"
	io.WriteString(conn, payload)
	buf := make([]byte, len(payload))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("读取隧道回显失败: %v", err)
	}
	if string(buf) != payload {
		t.Errorf("隧道回显 = %q，期望 %q", buf, payload)
	}
}

// 域名目标必须原样交给上游解析，不能在本地先解析成 IP。
func TestSOCKS5InboundForwardsDomainToUpstream(t *testing.T) {
	upstream := newFakeUpstream(t)
	r := startRelay(t, Config{UpstreamAddr: upstream.addr(), UsernameTemplate: "u-{session}"})

	// 目标域名不可解析：若中继在本地解析就会失败，交给上游则只是 502
	conn, err := dialThroughRelaySOCKS5(t, r.Addr().String(), "77", "x", "nonexistent.invalid:443")
	if err == nil {
		conn.Close()
	}

	targets := upstream.recordedTargets()
	if len(targets) != 1 {
		t.Fatalf("上游收到 %d 个 CONNECT 目标，期望 1 个", len(targets))
	}
	if !strings.HasPrefix(targets[0], "nonexistent.invalid:") {
		t.Errorf("上游收到的目标 = %q，期望原样的域名", targets[0])
	}
}
