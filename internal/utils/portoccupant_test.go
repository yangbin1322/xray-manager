package utils

import (
	"fmt"
	"net"
	"os"
	"testing"
)

// 端口空闲时不该报出任何占用者，也不该去 fork netstat
func TestInspectPortOccupantsEmptyWhenFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("无法分配临时端口:", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // 立刻释放，端口应为空闲

	if got := InspectPortOccupants(port); len(got) != 0 {
		t.Errorf("空闲端口不应有占用者，实际 %+v", got)
	}
}

// 本进程占着的端口，必须标记为「自身进程」且不可终止——
// 给用户一个「结束本客户端」的按钮等于让他自杀
func TestInspectPortOccupantsRefusesToKillSelf(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("无法分配临时端口:", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	occupants := InspectPortOccupants(port)
	if len(occupants) == 0 {
		t.Skip("当前平台/权限下查不到端口占用进程，跳过")
	}

	selfPID := os.Getpid()
	for _, occ := range occupants {
		if occ.PID != selfPID {
			continue
		}
		if occ.Killable {
			t.Error("自身进程绝不能标记为可终止")
		}
		if occ.Reason == "" {
			t.Error("不可终止时必须给出原因，否则界面无从解释")
		}
		return
	}
	t.Skip("未在占用列表中找到本进程（平台差异），跳过")
}

// 不传 PID 时必须原地返回，不能理解成「结束占用这个端口的一切」
func TestKillPortOccupantsRequiresExplicitPIDs(t *testing.T) {
	killed, err := KillPortOccupants(12345, nil, nil)
	if err != nil {
		t.Errorf("空 PID 列表不该报错，实际 %v", err)
	}
	if len(killed) != 0 {
		t.Errorf("空 PID 列表不该终止任何进程，实际 %+v", killed)
	}
}

// 只终止显式点名、且判定为可终止的 PID。
// 拿本进程做实验：它一定是不可终止的，即使点名也必须被拒绝——
// 真终止了这个测试进程就直接挂了，所以这条断言本身就是安全网。
func TestKillPortOccupantsSkipsUnkillable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("无法分配临时端口:", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	selfPID := os.Getpid()
	killed, _ := KillPortOccupants(port, []int{selfPID}, nil)
	for _, occ := range killed {
		if occ.PID == selfPID {
			t.Fatal("终止了本进程——安全判定失效")
		}
	}
}

// 自身内核进程（sing-box/xray）属于本客户端 fork 出来的，可以安全终止
func TestSelfProcessNamesAreKillable(t *testing.T) {
	for _, name := range []string{"sing-box", "sing-box.exe", "xray", "xray.exe"} {
		if !selfProcessNames[name] {
			t.Errorf("%s 应被认作本客户端的内核进程", name)
		}
	}
	for _, name := range []string{"chrome.exe", "explorer.exe", ""} {
		if selfProcessNames[name] {
			t.Errorf("%s 不该被认作本客户端的内核进程", fmt.Sprintf("%q", name))
		}
	}
}
