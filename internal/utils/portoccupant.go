package utils

import (
	"os"
	"path/filepath"
	"strings"
)

// PortOccupant 描述占用某个本地端口的进程。
type PortOccupant struct {
	PID  int    `json:"pid"`
	Name string `json:"name"` // 进程名（取不到时为空）

	// Killable 表示是否允许从界面上终止它。
	//
	// 终止别人的进程是不可逆操作，宁可少给一个按钮也不能杀错：
	// 自身进程、以及取不到进程名（多半是权限不足看不到的系统进程）一律不给。
	Killable bool `json:"killable"`

	// Reason 不可终止时的原因，直接展示给用户。
	Reason string `json:"reason,omitempty"`

	// Self 该进程是否是本客户端自己（含内核子进程）。
	Self bool `json:"self"`
}

// selfProcessNames 属于本客户端的进程名，终止它们没有误杀风险。
// 内核进程由本客户端 fork，端口没释放干净时正是要清理的对象。
var selfProcessNames = map[string]bool{
	"sing-box":     true,
	"sing-box.exe": true,
	"xray":         true,
	"xray.exe":     true,
}

// InspectPortOccupants 返回占用该端口的进程列表，并标注哪些可以安全终止。
//
// 端口空闲时返回空列表。这个函数只做只读探测，不终止任何进程。
func InspectPortOccupants(port int) []PortOccupant {
	if CheckPortAvailable(port) {
		return nil
	}

	// 占用快照可能是停止流程刚留下的旧数据，重新取一次
	InvalidatePortPIDCache()

	selfPID := os.Getpid()
	selfExe, _ := os.Executable()
	selfExeName := strings.ToLower(filepath.Base(selfExe))

	pids := GetPortPIDs(port)
	occupants := make([]PortOccupant, 0, len(pids))
	for _, pid := range pids {
		name := GetProcessName(pid)
		lower := strings.ToLower(name)

		occ := PortOccupant{PID: pid, Name: name}
		switch {
		case pid == selfPID:
			occ.Reason = "这是本客户端自身的进程"
		case pid <= 0:
			occ.Reason = "无效的进程 ID"
		case name == "":
			// 拿不到进程名通常意味着权限不足（系统进程、其他用户的进程），
			// 这种情况下更不该贸然终止
			occ.Reason = "无法获取进程信息，可能是系统进程或需要管理员权限"
		case selfProcessNames[lower]:
			occ.Self = true
			occ.Killable = true
		case lower == selfExeName:
			// 另一个 xray-manager 实例：可以终止，但要让用户明确知道
			occ.Killable = true
		default:
			occ.Killable = true
		}
		occupants = append(occupants, occ)
	}
	return occupants
}

// KillPortOccupants 终止占用该端口的进程，返回实际终止的进程与错误。
//
// 只终止 InspectPortOccupants 判定为可终止的 PID，且调用方必须显式传入
// 要终止的 PID 列表——不接受"终止占用这个端口的一切"这种模糊指令，
// 避免探测与终止之间端口易主导致误杀。
func KillPortOccupants(port int, pids []int, logFunc func(string)) (killed []PortOccupant, err error) {
	if len(pids) == 0 {
		return nil, nil
	}
	want := make(map[int]bool, len(pids))
	for _, pid := range pids {
		want[pid] = true
	}

	for _, occ := range InspectPortOccupants(port) {
		if !want[occ.PID] || !occ.Killable {
			continue
		}
		if kerr := KillPID(occ.PID); kerr != nil {
			if logFunc != nil {
				logFunc("[端口清理] 终止进程 " + occ.Name + " 失败: " + kerr.Error())
			}
			err = kerr
			continue
		}
		if logFunc != nil {
			logFunc("[端口清理] 已终止占用端口的进程 " + occ.Name)
		}
		killed = append(killed, occ)
	}
	return killed, err
}
