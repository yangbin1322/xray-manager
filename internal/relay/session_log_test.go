package relay

import (
	"strings"
	"sync"
	"testing"
)

// 会话日志应"每个会话一条"，而不是每个请求一条——否则高频请求会刷屏。
func TestRecordSessionLogsOncePerSession(t *testing.T) {
	var mu sync.Mutex
	var logs []string

	r := New(Config{
		UsernameTemplate: "login__cr.au;sessid.{session}",
		Logf: func(msg string) {
			mu.Lock()
			logs = append(logs, msg)
			mu.Unlock()
		},
	})

	// 会话 a 出现三次，会话 b 出现两次
	for _, s := range []string{"a", "a", "b", "a", "b"} {
		r.recordSession(s)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 2 {
		t.Fatalf("应只为两个不同会话各打一条日志，实际 %d 条: %v", len(logs), logs)
	}
	// 日志里要能看到改写后的上游用户名，方便用户核对模板是否写对
	if !strings.Contains(logs[0], "login__cr.au;sessid.a") {
		t.Errorf("首条日志应包含改写后的上游用户名，实际: %q", logs[0])
	}
	if !strings.Contains(logs[1], "login__cr.au;sessid.b") {
		t.Errorf("第二条日志应包含改写后的上游用户名，实际: %q", logs[1])
	}
	if r.Stats().SessionCount != 2 {
		t.Errorf("会话数应为 2，实际 %d", r.Stats().SessionCount)
	}
}
