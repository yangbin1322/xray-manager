package relay

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

// 端到端：客户端 → 中继(改写用户名) → SOCKS5 加速节点 → 住宅网关 → 目标
// 验证两个不同会话都能连通，且上游收到的是各自改写后的用户名。
func TestEndToEndTwoSessionsViaPreProxy(t *testing.T) {
	upstream := newFakeUpstream(t)
	echo := newEchoServer(t)
	socks := newFakeSOCKS5Server(t)

	r := startRelay(t, Config{
		UpstreamAddr:     upstream.addr(),
		UsernameTemplate: "login__cr.au;sessid.{session}",
		UpstreamPassword: "realpw",
		LocalPassword:    "clientpw",
		PreProxyURL:      "socks5://" + socks.addr(),
	})

	for _, session := range []string{"123", "45"} {
		conn, err := net.Dial("tcp", r.Addr().String())
		if err != nil {
			t.Fatalf("连接中继失败: %v", err)
		}
		fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
			echo.Addr(), echo.Addr(), proxyAuth(session, "clientpw"))
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("会话 %s CONNECT 失败: %v / %v", session, err, resp)
		}
		payload := "e2e-" + session
		io.WriteString(conn, payload)
		buf := make([]byte, len(payload))
		io.ReadFull(reader, buf)
		if string(buf) != payload {
			t.Errorf("会话 %s 回显 = %q, 期望 %q", session, buf, payload)
		}
		conn.Close()
	}

	auths := upstream.recordedAuth()
	for i, want := range []string{"login__cr.au;sessid.123", "login__cr.au;sessid.45"} {
		user, pass, _ := parseProxyAuth(auths[i])
		t.Logf("上游收到凭证 #%d: %s / %s", i+1, user, pass)
		if user != want || pass != "realpw" {
			t.Errorf("凭证 #%d = %s/%s, 期望 %s/realpw", i+1, user, pass, want)
		}
	}
	// 两跳都必须经过加速节点
	if got := socks.targets(); len(got) != 2 {
		t.Errorf("SOCKS5 加速节点被使用 %d 次, 期望 2 次", len(got))
	}
}
