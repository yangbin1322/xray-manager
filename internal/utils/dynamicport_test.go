package utils

import (
	"strings"
	"testing"
)

func TestDynamicPortRangeContains(t *testing.T) {
	r := DynamicPortRange{Start: 10000, Count: 55000} // 10000-64999

	if r.End() != 64999 {
		t.Errorf("End() = %d, want 64999", r.End())
	}
	for _, port := range []int{10000, 11001, 35515, 64999} {
		if !r.Contains(port) {
			t.Errorf("port %d should be inside %d-%d", port, r.Start, r.End())
		}
	}
	for _, port := range []int{9999, 65000} {
		if r.Contains(port) {
			t.Errorf("port %d should be outside %d-%d", port, r.Start, r.End())
		}
	}
}

// 范围为空时不该把任意端口都算作命中
func TestDynamicPortRangeEmptyContainsNothing(t *testing.T) {
	var empty DynamicPortRange
	if empty.Contains(11001) {
		t.Error("an empty range must not contain any port")
	}
}

// 冲突提示必须给出可执行的修复命令，否则用户无从下手
func TestDynamicPortConflictMessage(t *testing.T) {
	c := DynamicPortConflict{
		Range:    DynamicPortRange{Start: 10000, Count: 55000},
		Affected: 418,
		Total:    418,
	}
	msg := c.Message()

	for _, want := range []string{"10000-64999", "418/418", "netsh int ipv4 set dynamicport tcp start=49152"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should mention %q, got:\n%s", want, msg)
		}
	}
}
