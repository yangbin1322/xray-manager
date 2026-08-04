package healthcheck

import "testing"

// 并发上限直接决定大批量检测的耗时：检测是纯网络等待，
// 并发 8 时每个不可达节点会占满槽位直到超时。
// 实测 200 个超时节点：并发 8 需 50 秒，并发 256 只需 2 秒。
func TestConcurrencyScalesWithNodeCount(t *testing.T) {
	cases := []struct {
		nodes   int
		wantMin int
	}{
		{1, 8},
		{16, 8},
		{100, 64},
		{1000, 256},
		{10000, 256},
	}
	for _, c := range cases {
		got := concurrencyFor(c.nodes)
		if got != c.wantMin {
			t.Errorf("%d 个节点应使用并发 %d，实际 %d", c.nodes, c.wantMin, got)
		}
	}
}

// 上限不能无限增长，否则会耗尽文件描述符（macOS 默认 ulimit 常见 256~1024）
func TestConcurrencyIsCapped(t *testing.T) {
	if got := concurrencyFor(1000000); got > 256 {
		t.Fatalf("并发上限不应超过 256，实际 %d", got)
	}
}
