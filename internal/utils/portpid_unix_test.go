//go:build !windows

package utils

import (
	"reflect"
	"sort"
	"testing"
)

// parsePortPIDsFromLsof 的输入取自真实 lsof -nP -iTCP -sTCP:LISTEN -Fpn 输出：
// 每个进程一条 p 记录，后面跟若干 f/n 记录。
func TestParseLsofOutput(t *testing.T) {
	output := "p1234\n" +
		"f5\n" +
		"n*:10808\n" +
		"f6\n" +
		"n127.0.0.1:10809\n" +
		"p5678\n" +
		"f7\n" +
		"n[::1]:10810\n" +
		"f8\n" +
		"n*:10808\n" // 同一端口被第二个进程监听（IPv4/IPv6 双栈常见）

	got := parseLsofPortPIDs(output)

	want := map[int][]int{
		10808: {1234, 5678},
		10809: {1234},
		10810: {5678},
	}
	for port, wantPIDs := range want {
		gotPIDs := append([]int(nil), got[port]...)
		sort.Ints(gotPIDs)
		if !reflect.DeepEqual(gotPIDs, wantPIDs) {
			t.Errorf("port %d: got PIDs %v, want %v", port, gotPIDs, wantPIDs)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d ports, want %d (%v)", len(got), len(want), got)
	}
}

// 畸形输入不能 panic，也不能把垃圾数据记进表里。
func TestParseLsofOutputMalformed(t *testing.T) {
	cases := []string{
		"",
		"\n\n",
		"n*:10808\n",          // 没有前置 p 记录，应被忽略
		"pabc\nn*:10808\n",    // PID 解析失败，其后的 n 应被忽略
		"p1234\nnnotaport\n",  // 端口解析失败
		"p1234\nn*:0\n",       // 端口 0 无效
		"p-1\nn*:10808\n",     // 负数 PID
		"garbage\np1\nn*:1\n", // 未知前缀行应被跳过
	}
	for _, input := range cases {
		table := parseLsofPortPIDs(input)
		for port, pids := range table {
			if port <= 0 {
				t.Errorf("input %q produced invalid port %d", input, port)
			}
			for _, pid := range pids {
				if pid <= 0 {
					t.Errorf("input %q produced invalid pid %d", input, pid)
				}
			}
		}
	}
	// 只有最后一个用例应解析出内容
	if len(parseLsofPortPIDs("garbage\np1\nn*:1\n")) != 1 {
		t.Error("expected the well-formed record to be parsed")
	}
	if len(parseLsofPortPIDs("n*:10808\n")) != 0 {
		t.Error("n record without a preceding p record must be ignored")
	}
}
