package utils

import "fmt"

// DynamicPortRange 系统的动态（临时）端口范围。
//
// 系统为对外连接分配源端口时就从这个范围里取。落在范围内的本地监听端口
// 随时可能被其他程序抢先占用，且占用形态是 Bound 而非 Listen——
// netstat 与「查看端口占用」类工具都看不到，排查时极具迷惑性。
type DynamicPortRange struct {
	Start int
	Count int
}

// End 范围内的最后一个端口。
func (r DynamicPortRange) End() int {
	return r.Start + r.Count - 1
}

// Contains 判断端口是否落在动态范围内。
func (r DynamicPortRange) Contains(port int) bool {
	return r.Count > 0 && port >= r.Start && port <= r.End()
}

// WindowsDefaultDynamicPortStart Windows 默认的动态端口起始值。
// 低于此值说明范围被人为调低过（常见于国产网络工具的「优化」）。
const WindowsDefaultDynamicPortStart = 49152

// DynamicPortConflict 节点端口与动态端口范围重叠的检测结果。
type DynamicPortConflict struct {
	Range DynamicPortRange
	// Affected 落在动态范围内的节点端口数量
	Affected int
	// Total 参与检查的节点端口总数
	Total int
}

// Message 面向用户的说明，含修复命令。
func (c DynamicPortConflict) Message() string {
	return fmt.Sprintf(
		"检测到系统动态端口范围为 %d-%d，与 %d/%d 个节点的本地端口重叠。"+
			"这些端口可能被其他程序（如网盘、音乐客户端）随机借用为临时端口，"+
			"表现为节点时好时坏、且在 netstat 里看不到占用者。"+
			"建议以管理员身份执行以下命令后重启电脑：\n"+
			"  netsh int ipv4 set dynamicport tcp start=%d num=16384\n"+
			"  netsh int ipv4 set dynamicport udp start=%d num=16384",
		c.Range.Start, c.Range.End(), c.Affected, c.Total,
		WindowsDefaultDynamicPortStart, WindowsDefaultDynamicPortStart)
}

// CheckDynamicPortConflict 检查节点端口是否与系统动态端口范围重叠。
//
// 返回 nil 表示没有问题：范围取不到、范围正常（起始值不低于默认值），
// 或没有任何节点端口落在范围内。
func CheckDynamicPortConflict(ports []int) *DynamicPortConflict {
	r, err := SystemDynamicPortRange()
	if err != nil || r.Count <= 0 {
		return nil
	}
	// 起始值不低于默认值时，范围本身是合理的：此时仍与节点端口重叠
	// 说明是节点端口选得太高，属于用户自选，不该反复告警。
	if r.Start >= WindowsDefaultDynamicPortStart {
		return nil
	}

	affected := 0
	for _, port := range ports {
		if r.Contains(port) {
			affected++
		}
	}
	if affected == 0 {
		return nil
	}
	return &DynamicPortConflict{Range: r, Affected: affected, Total: len(ports)}
}
