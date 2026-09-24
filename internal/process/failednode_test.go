package process

import (
	"testing"
	"xray-manager/internal/models"
)

func newShardedTestManager(t *testing.T) *Manager {
	t.Helper()
	m := &Manager{
		processes: make(map[int]*ProcessInfo),
		logFunc:   func(string) {},
		configDir: t.TempDir(),
		shards:    NewShardManager(t.TempDir(), nil),
	}
	t.Cleanup(m.shards.StopAll)
	return m
}

// 批量验证期间停用不通节点，不能立即重启分片——那会切断同片其他节点正在进行的
// 验证，让它们也被判为不通，连锁把整片好节点全部停掉。应等整批验证结束后重建一次。
func TestStopFailedNodeDefersShardRebuildDuringVerify(t *testing.T) {
	requireSingBox(t)
	ports := freePorts(t, 3)
	m := newShardedTestManager(t)

	nodes := []*models.ProxyRule{testNode("a", ports[0]), testNode("b", ports[1]), testNode("c", ports[2])}
	m.shards.SetDesired(nodes)
	if _, err := m.shards.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	gen := m.portGeneration(ports[0])
	if gen == 0 {
		t.Fatal("分片节点应有启动代次")
	}

	done := m.beginVerify()
	if err := m.StopFailedNode(ports[1]); err != nil {
		t.Fatalf("StopFailedNode: %v", err)
	}
	if got := m.portGeneration(ports[0]); got != gen {
		t.Fatalf("验证期间分片不应重启: gen %d -> %d", gen, got)
	}
	if !dialable(ports[0]) || !dialable(ports[2]) {
		t.Fatal("验证期间同片其他节点应持续可用")
	}

	done() // 整批验证结束，统一重建一次
	if got := m.portGeneration(ports[0]); got == gen {
		t.Fatal("验证结束后应重建分片以移除不通节点")
	}
	if m.shards.IsPortRunning(ports[1]) {
		t.Fatal("不通节点应已从分片移除")
	}
	if !dialable(ports[0]) || !dialable(ports[2]) {
		t.Fatal("重建后其他节点应可用")
	}
}

// 没有验证在进行时（如出口 IP 变化的单独停用）照常立即停用
func TestStopFailedNodeImmediateWhenIdle(t *testing.T) {
	requireSingBox(t)
	ports := freePorts(t, 2)
	m := newShardedTestManager(t)

	m.shards.SetDesired([]*models.ProxyRule{testNode("a", ports[0]), testNode("b", ports[1])})
	if _, err := m.shards.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if err := m.StopFailedNode(ports[1]); err != nil {
		t.Fatalf("StopFailedNode: %v", err)
	}
	if m.shards.IsPortRunning(ports[1]) {
		t.Fatal("空闲时应立即移除不通节点")
	}
	if m.pendingReconcile.Load() {
		t.Fatal("不应留下待办的重建")
	}
}
