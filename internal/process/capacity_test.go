package process

import (
	"strings"
	"testing"
)

func TestCheckCapacityAllowsSmallBatches(t *testing.T) {
	// 少量节点在任何正常机器上都应放行
	if err := CheckCapacity(1); err != nil {
		t.Errorf("starting 1 node should always be allowed, got %v", err)
	}
	if err := CheckCapacity(0); err != nil {
		t.Errorf("starting 0 nodes must be a no-op, got %v", err)
	}
	if err := CheckCapacity(-5); err != nil {
		t.Errorf("negative count must be a no-op, got %v", err)
	}
}

// 1400 个节点约需 45 GB，远超常见机器的可用内存，必须被拦下。
func TestCheckCapacityRejectsHugeBatches(t *testing.T) {
	err := CheckCapacity(100000)
	if err == nil {
		t.Fatal("starting 100000 nodes must be rejected")
	}
	capacityErr, ok := err.(*CapacityError)
	if !ok {
		t.Fatalf("expected *CapacityError, got %T", err)
	}
	if capacityErr.Requested != 100000 {
		t.Errorf("Requested = %d, want 100000", capacityErr.Requested)
	}
	if capacityErr.Allowed >= 100000 {
		t.Errorf("Allowed = %d, should be far below the request", capacityErr.Allowed)
	}
}

// 错误信息要能直接展示给用户：说明原因、当前余量和还能启动多少个。
func TestCapacityErrorMessageIsActionable(t *testing.T) {
	err := &CapacityError{Requested: 1400, Allowed: 700, Available: 24 << 30}
	message := err.Error()
	for _, want := range []string{"1400", "700", "24.0 GB", "33 MB"} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q should mention %q", message, want)
		}
	}
}

// 内存偏紧时不能把小批量也拦下。
//
// 曾有的 bug：分片模式沿用了 4 GB 的系统预留，可用内存 3.2 GB 时
// 「可用 - 预留」为负、被钳到 0，于是算出「最多可启动 0 个」，
// 连启动 5 个节点（约 2.5 MB）都会被拒绝。
func TestShardedCapacityAllowsSmallBatchesUnderMemoryPressure(t *testing.T) {
	for _, count := range []int{1, 5, 20, 100} {
		if err := CheckShardedCapacity(count); err != nil {
			t.Errorf("starting %d sharded nodes was rejected: %v", count, err)
		}
	}
}

// 分片模式的预留必须远小于一节点一进程模式，否则小批量会被误拦
func TestShardedReserveIsSmallerThanPerProcess(t *testing.T) {
	if ReservedSystemMemorySharded >= ReservedSystemMemory {
		t.Errorf("sharded reserve (%d) should be well below the per-process reserve (%d)",
			ReservedSystemMemorySharded, ReservedSystemMemory)
	}
}

// 真正超出内存的请求仍要被拦下，且给出的上限要合理
func TestShardedCapacityStillRejectsImpossibleBatches(t *testing.T) {
	const huge = 10_000_000 // 约 4.8 TB，任何机器都放不下
	err := CheckShardedCapacity(huge)
	if err == nil {
		t.Fatal("an impossibly large batch must be rejected")
	}
	var capacityErr *CapacityError
	if !asCapacityError(err, &capacityErr) {
		t.Fatalf("expected *CapacityError, got %T", err)
	}
	if capacityErr.Allowed >= huge {
		t.Errorf("Allowed = %d, should be far below the request", capacityErr.Allowed)
	}
	if capacityErr.Allowed < 0 {
		t.Errorf("Allowed = %d, must never be negative", capacityErr.Allowed)
	}
}
