package process

import (
	"fmt"

	"xray-manager/internal/utils"
)

// EstimatedNodeMemory 每个节点的常驻内存估算（字节，一节点一进程模式）。
//
// 实测值：412 个 sing-box 进程平均 32.7 MB/个，取偏保守的一档，
// 宁可提前拦下也不要让用户把机器打崩。
const EstimatedNodeMemory = 33 << 20

// EstimatedShardedNodeMemory 分片模式下每个节点的内存估算（字节）。
//
// 分片模式下节点共享进程，开销主要是每个 inbound 的监听与缓冲区。
// 实测 1583 个节点（6 个分片）约 210 MB，即约 0.13 MB/节点；
// 取 0.5 MB 留出余量，覆盖连接活跃时的缓冲区增长。
const EstimatedShardedNodeMemory = 512 << 10

// ReservedSystemMemory 一节点一进程模式下预留给系统和其他程序的内存。
//
// 该模式每个节点要 33 MB，动辄吃掉几 GB，留出 4 GB 缓冲是合理的。
const ReservedSystemMemory = 4 << 30

// ReservedSystemMemorySharded 分片模式下预留给系统和其他程序的内存。
//
// 分片模式每个节点只要约 0.5 MB，几百个节点也就一两百 MB。
// 若沿用 4 GB 的预留，可用内存低于 4 GB 时会算出「最多可启动 0 个」，
// 连启动 5 个节点（约 2.5 MB）都会被拦下——预留必须与开销量级匹配。
const ReservedSystemMemorySharded = 512 << 20

// CapacityError 批量启动会超出可用内存时返回，供上层原样提示给用户。
type CapacityError struct {
	Requested int    // 本次请求启动的节点数
	Allowed   int    // 按当前可用内存估算可承载的节点数
	Available uint64 // 当前可用物理内存（字节）
	PerNode   uint64 // 估算的单节点内存开销（字节）
}

func (e *CapacityError) Error() string {
	perNode := e.PerNode
	if perNode == 0 {
		perNode = EstimatedNodeMemory
	}
	unit := "MB"
	value := float64(perNode) / (1 << 20)
	if value < 1 {
		unit = "KB"
		value = float64(perNode) / (1 << 10)
	}
	return fmt.Sprintf(
		"一次启动 %d 个节点会超出可用内存：每个节点约需 %.0f %s，当前可用内存 %.1f GB，"+
			"最多可启动约 %d 个。请减少本次启动的节点数，或先停止部分已启动的节点。",
		e.Requested, value, unit, float64(e.Available)/(1<<30), e.Allowed)
}

// CheckCapacity 判断再启动 count 个节点是否会超出可用内存（一节点一进程模式）。
func CheckCapacity(count int) error {
	return checkCapacityWith(count, EstimatedNodeMemory, ReservedSystemMemory)
}

// CheckShardedCapacity 判断分片模式下再启动 count 个节点是否会超出可用内存。
//
// 分片模式下节点共享进程，单节点开销远低于独立进程，因此上限高得多：
// 同样的内存，独立进程只能跑几百个，分片模式能跑上万个。
func CheckShardedCapacity(count int) error {
	return checkCapacityWith(count, EstimatedShardedNodeMemory, ReservedSystemMemorySharded)
}

// checkCapacityWith 按给定的单节点内存估算与系统预留判断容量。
//
// 取不到内存信息时返回 nil——放行而不是误拦。
func checkCapacityWith(count int, perNode, reserved uint64) error {
	if count <= 0 {
		return nil
	}
	available := utils.AvailableMemory()
	if available == 0 {
		return nil
	}

	var usable uint64
	if available > reserved {
		usable = available - reserved
	}
	allowed := int(usable / perNode)

	// 本次请求所需内存本就远小于可用内存时直接放行。
	// 否则内存紧张（可用量接近预留值）时会算出「最多 0 个」，
	// 连启动几个只占几 MB 的节点都会被拦下，与实际情况不符。
	if uint64(count)*perNode < available/2 {
		return nil
	}
	if count <= allowed {
		return nil
	}
	return &CapacityError{
		Requested: count, Allowed: allowed, Available: available, PerNode: perNode,
	}
}
