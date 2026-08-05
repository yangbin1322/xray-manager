package process

import (
	"fmt"

	"xray-manager/internal/utils"
)

// EstimatedNodeMemory 单个内核进程的常驻内存估算（字节）。
//
// 实测值：412 个 sing-box 进程平均 32.7 MB/个。xray 略低，取偏保守的一档，
// 宁可提前拦下也不要让用户把机器打崩。
const EstimatedNodeMemory = 33 << 20

// ReservedSystemMemory 预留给系统和其他程序的内存，不参与节点容量计算。
const ReservedSystemMemory = 4 << 30

// CapacityError 批量启动会超出可用内存时返回，供上层原样提示给用户。
type CapacityError struct {
	Requested int    // 本次请求启动的节点数
	Allowed   int    // 按当前可用内存估算可承载的节点数
	Available uint64 // 当前可用物理内存（字节）
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf(
		"一次启动 %d 个节点会超出可用内存：每个节点约需 %d MB，当前可用内存 %.1f GB，"+
			"最多可启动约 %d 个。请减少本次启动的节点数，或先停止部分已启动的节点。",
		e.Requested, EstimatedNodeMemory>>20, float64(e.Available)/(1<<30), e.Allowed)
}

// CheckCapacity 判断再启动 count 个节点是否会超出可用内存。
//
// 每个节点都是一个独立的 xray/sing-box 进程，内存开销随节点数线性增长；
// 上千节点会直接超出物理内存导致进程被系统终止（应用表现为闪退）。
// 取不到内存信息时返回 nil——放行而不是误拦。
func CheckCapacity(count int) error {
	if count <= 0 {
		return nil
	}
	available := utils.AvailableMemory()
	if available == 0 {
		return nil
	}

	usable := int64(available) - ReservedSystemMemory
	if usable < 0 {
		usable = 0
	}
	allowed := int(usable / EstimatedNodeMemory)
	if count <= allowed {
		return nil
	}
	return &CapacityError{Requested: count, Allowed: allowed, Available: available}
}
