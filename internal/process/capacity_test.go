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
