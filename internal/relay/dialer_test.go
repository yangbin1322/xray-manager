package relay

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeSOCKS5Server 最小 SOCKS5 服务端（仅无认证 + CONNECT），
// 记录客户端请求的目标地址，用于验证中继确实走了前置代理。
type fakeSOCKS5Server struct {
	listener net.Listener

	mu           sync.Mutex
	dialedTarget []string
}

func newFakeSOCKS5Server(t *testing.T) *fakeSOCKS5Server {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	s := &fakeSOCKS5Server{listener: listener}
	go s.serve()
	t.Cleanup(func() { listener.Close() })
	return s
}

func (s *fakeSOCKS5Server) addr() string { return s.listener.Addr().String() }

func (s *fakeSOCKS5Server) targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dialedTarget...)
}

func (s *fakeSOCKS5Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSOCKS5Server) handle(conn net.Conn) {
	defer conn.Close()

	// 方法协商
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	conn.Write([]byte{socks5Version, socks5AuthNone})

	// CONNECT 请求
	reqHead := make([]byte, 4)
	if _, err := io.ReadFull(conn, reqHead); err != nil {
		return
	}
	var host string
	switch reqHead[3] {
	case socks5AddrIPv4:
		buf := make([]byte, 4)
		io.ReadFull(conn, buf)
		host = net.IP(buf).String()
	case socks5AddrIPv6:
		buf := make([]byte, 16)
		io.ReadFull(conn, buf)
		host = net.IP(buf).String()
	case socks5AddrDomain:
		length := make([]byte, 1)
		io.ReadFull(conn, length)
		buf := make([]byte, length[0])
		io.ReadFull(conn, buf)
		host = string(buf)
	default:
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", binary.BigEndian.Uint16(portBuf)))

	s.mu.Lock()
	s.dialedTarget = append(s.dialedTarget, target)
	s.mu.Unlock()

	remote, err := net.Dial("tcp", target)
	if err != nil {
		conn.Write([]byte{socks5Version, 0x01, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	conn.Write([]byte{socks5Version, 0x00, 0x00, socks5AddrIPv4, 0, 0, 0, 0, 0, 0})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(remote, conn) }()
	go func() { defer wg.Done(); io.Copy(conn, remote) }()
	wg.Wait()
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 URL %q 失败: %v", raw, err)
	}
	return u
}

func TestDialViaSOCKS5ReachesTarget(t *testing.T) {
	echo := newEchoServer(t)
	socks := newFakeSOCKS5Server(t)

	conn, err := dialViaSOCKS5(t.Context(), mustParseURL(t, "socks5://"+socks.addr()), echo.Addr().String())
	if err != nil {
		t.Fatalf("经 SOCKS5 拨号失败: %v", err)
	}
	defer conn.Close()

	payload := "socks5-echo"
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(buf) != payload {
		t.Errorf("回显 = %q，期望 %q", buf, payload)
	}
	if got := socks.targets(); len(got) != 1 || got[0] != echo.Addr().String() {
		t.Errorf("SOCKS5 收到的目标 = %v，期望 [%s]", got, echo.Addr())
	}
}

func TestNormalizePreProxyURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"127.0.0.1:1080", "socks5://127.0.0.1:1080"},
		{"socks5://127.0.0.1:1080", "socks5://127.0.0.1:1080"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"  127.0.0.1:1080  ", "socks5://127.0.0.1:1080"},
	}
	for _, tc := range tests {
		if got := normalizePreProxyURL(tc.in); got != tc.want {
			t.Errorf("normalizePreProxyURL(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestDialViaSOCKS5RejectsBadTarget(t *testing.T) {
	socks := newFakeSOCKS5Server(t)
	_, err := dialViaSOCKS5(t.Context(), mustParseURL(t, "socks5://"+socks.addr()), "no-port-here")
	if err == nil {
		t.Fatal("期望目标地址校验失败")
	}
	if !strings.Contains(err.Error(), "目标地址无效") {
		t.Errorf("错误信息 = %q，期望包含 %q", err, "目标地址无效")
	}
}
