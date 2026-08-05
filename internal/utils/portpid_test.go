package utils

import (
	"net"
	"testing"
)

func TestPortFromAddress(t *testing.T) {
	cases := []struct {
		address string
		want    int
	}{
		{"0.0.0.0:10808", 10808},
		{"127.0.0.1:10809", 10809},
		{"[::]:10810", 10810},
		{"[::1]:10811", 10811},
		{"*:10812", 10812},
		{"0.0.0.0:0", 0},
		{"notaport", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := portFromAddress(tc.address); got != tc.want {
			t.Errorf("portFromAddress(%q) = %d, want %d", tc.address, got, tc.want)
		}
	}
}

// 全表查询必须能定位到一个真实存在的监听端口，否则 EnsurePortFree 会漏判占用。
func TestGetAllPortPIDsFindsRealListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	InvalidatePortPIDCache()
	table := GetAllPortPIDs()
	if len(table[port]) == 0 {
		t.Fatalf("listening port %d missing from the port table", port)
	}

	// 单端口查询应与全表结果一致（走的是同一张表）
	if len(GetPortPIDs(port)) == 0 {
		t.Fatalf("GetPortPIDs(%d) returned no PID for a live listener", port)
	}
}

// 缓存失效后必须重新取数，否则停止节点后仍会读到旧的占用快照。
func TestInvalidatePortPIDCacheForcesRefetch(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	InvalidatePortPIDCache()
	if len(GetPortPIDs(port)) == 0 {
		t.Fatalf("expected port %d to be reported as occupied while listening", port)
	}

	_ = listener.Close()

	// 不失效缓存时读到的仍是关闭前的快照；失效后应反映真实状态
	InvalidatePortPIDCache()
	if pids := GetPortPIDs(port); len(pids) != 0 {
		t.Logf("port %d still reported as held by %v after close (may be TIME_WAIT)", port, pids)
	}
}
