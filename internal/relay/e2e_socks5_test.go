package relay

import (
	"io"
	"net"
	"testing"
	"time"
)

// 端到端：SOCKS5 客户端 → 中继(改写用户名) → 加速节点 → 住宅网关 → 目标
func TestEndToEndSOCKS5ClientViaPreProxy(t *testing.T) {
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
		conn, err := dialThroughRelaySOCKS5(t, r.Addr().String(), session, "clientpw", echo.Addr().String())
		if err != nil {
			t.Fatalf("会话 %s SOCKS5 接入失败: %v", session, err)
		}
		payload := "s5e2e-" + session
		io.WriteString(conn, payload)
		buf := make([]byte, len(payload))
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("会话 %s 隧道读取失败: %v", session, err)
		}
		if string(buf) != payload {
			t.Errorf("会话 %s 回显 = %q", session, buf)
		}
		conn.(net.Conn).Close()
	}

	for i, want := range []string{"login__cr.au;sessid.123", "login__cr.au;sessid.45"} {
		user, pass, _ := parseProxyAuth(upstream.recordedAuth()[i])
		t.Logf("SOCKS5 会话 #%d → 上游凭证: %s / %s", i+1, user, pass)
		if user != want || pass != "realpw" {
			t.Errorf("凭证 #%d = %s/%s, 期望 %s/realpw", i+1, user, pass, want)
		}
	}
	if got := len(socks.targets()); got != 2 {
		t.Errorf("加速节点被使用 %d 次, 期望 2 次", got)
	}
	t.Logf("两条会话均经加速节点 %s 到达上游", socks.addr())
}
