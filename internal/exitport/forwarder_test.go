package exitport

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startTagServer 启动一个假上游：每个连接先回一行自己的标签，再回显后续数据。
func startTagServer(t *testing.T, tag string) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				_, _ = conn.Write([]byte(tag + "\n"))
				buf := make([]byte, 1024)
				for {
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					_, _ = conn.Write(buf[:n])
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

// freePort 拿一个当前无人监听的端口（模拟已退出的节点）。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func startForwarder(t *testing.T, candidates func() []int) *Forwarder {
	t.Helper()
	f := New(Config{ListenAddr: "127.0.0.1:0", Candidates: candidates, DialTimeout: 500 * time.Millisecond})
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Stop() })
	return f
}

// readTag 连上转发器，读回上游标签；连接被拒时返回空串。
func readTag(t *testing.T, f *Forwarder) (string, net.Conn) {
	t.Helper()
	conn, err := net.Dial("tcp", f.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		return "", nil
	}
	return strings.TrimSpace(line), conn
}

func TestForwarderPrefersFirstCandidate(t *testing.T) {
	a, stopA := startTagServer(t, "A")
	defer stopA()
	b, stopB := startTagServer(t, "B")
	defer stopB()

	f := startForwarder(t, func() []int { return []int{a, b} })
	tag, conn := readTag(t, f)
	if conn != nil {
		defer conn.Close()
	}
	if tag != "A" {
		t.Fatalf("应走优先级最高的上游 A，实际 %q", tag)
	}
}

func TestForwarderFailsOverToNextCandidate(t *testing.T) {
	dead := freePort(t)
	b, stopB := startTagServer(t, "B")
	defer stopB()

	f := startForwarder(t, func() []int { return []int{dead, b} })
	tag, conn := readTag(t, f)
	if conn != nil {
		defer conn.Close()
	}
	if tag != "B" {
		t.Fatalf("首选连不上时应切到 B，实际 %q", tag)
	}

	// 失败的上游进入冷却，下一条连接应直接选 B 而不再先撞 dead
	f.mu.Lock()
	_, cooling := f.failedAt[dead]
	f.mu.Unlock()
	if !cooling {
		t.Fatal("连不上的上游应进入冷却")
	}
}

func TestForwarderRejectsWhenNoCandidate(t *testing.T) {
	f := startForwarder(t, func() []int { return nil })
	tag, conn := readTag(t, f)
	if conn != nil {
		conn.Close()
	}
	if tag != "" {
		t.Fatalf("没有候选时必须拒绝连接，实际走到了 %q", tag)
	}
	if f.Stats().RejectedConns != 1 {
		t.Fatalf("拒绝计数应为 1，实际 %d", f.Stats().RejectedConns)
	}
}

func TestForwarderRejectsWhenAllCandidatesDead(t *testing.T) {
	f := startForwarder(t, func() []int { return []int{freePort(t), freePort(t)} })
	tag, conn := readTag(t, f)
	if conn != nil {
		conn.Close()
	}
	if tag != "" {
		t.Fatalf("候选全部不可用时必须拒绝连接，实际走到了 %q", tag)
	}
}

func TestForwarderDropUpstreamClosesActiveConns(t *testing.T) {
	a, stopA := startTagServer(t, "A")
	defer stopA()

	f := startForwarder(t, func() []int { return []int{a} })
	tag, conn := readTag(t, f)
	if conn == nil || tag != "A" {
		t.Fatalf("建立连接失败: %q", tag)
	}
	defer conn.Close()

	// 等 track 登记完成
	deadline := time.Now().Add(2 * time.Second)
	for len(f.ActiveUpstreams()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := f.DropUpstream(a); n != 1 {
		t.Fatalf("应断开 1 条连接，实际 %d", n)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 16)); err == nil {
		t.Fatal("DropUpstream 后客户端连接应被关闭")
	}
}

func TestForwarderPipesData(t *testing.T) {
	a, stopA := startTagServer(t, "A")
	defer stopA()

	f := startForwarder(t, func() []int { return []int{a} })
	conn, err := net.Dial("tcp", f.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("数据应原样往返，实际 %q err=%v", line, err)
	}
}
