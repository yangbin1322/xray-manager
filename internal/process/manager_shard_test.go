package process

import (
	"errors"
	"testing"

	"xray-manager/internal/models"
)

// newShardedManager 构造一个启用分片、但不启动流量轮询的管理器
func newShardedManager(t *testing.T) *Manager {
	t.Helper()
	m := &Manager{
		processes:  make(map[int]*ProcessInfo),
		configDir:  t.TempDir(),
		pollerStop: make(chan struct{}),
	}
	m.EnableSharding()
	t.Cleanup(func() {
		if s := m.Shards(); s != nil {
			s.StopAll()
		}
	})
	return m
}

func TestEnableShardingIsIdempotent(t *testing.T) {
	m := newShardedManager(t)
	if !m.ShardingEnabled() {
		t.Fatal("sharding should be enabled")
	}
	first := m.Shards()
	m.EnableSharding()
	if m.Shards() != first {
		t.Error("EnableSharding should not replace an existing shard manager")
	}
}

// 启用分片后，普通节点应由分片承载而不是独立进程
func TestStartRoutesPlainNodeToShard(t *testing.T) {
	requireSingBox(t)
	m := newShardedManager(t)

	port := freePorts(t, 1)[0]
	rule := testNode("plain", port)

	if err := m.Start(rule); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !m.IsRunning(port) {
		t.Error("IsRunning should see the shard-hosted node")
	}
	// 分片模式下节点没有独立进程
	if rule.ProcessID != 0 {
		t.Errorf("ProcessID = %d, want 0 for a shard-hosted node", rule.ProcessID)
	}
	m.mu.RLock()
	perProcess := len(m.processes)
	m.mu.RUnlock()
	if perProcess != 0 {
		t.Errorf("%d dedicated processes were started, want 0", perProcess)
	}
	if got := m.Shards().RunningNodes(); got != 1 {
		t.Errorf("shard hosts %d nodes, want 1", got)
	}
}

// Stop 应当把节点从分片里摘除
func TestStopRemovesNodeFromShard(t *testing.T) {
	requireSingBox(t)
	m := newShardedManager(t)

	ports := freePorts(t, 2)
	a := testNode("a", ports[0])
	b := testNode("b", ports[1])

	if _, err := m.StartNodesInShard([]*models.ProxyRule{a, b}); err != nil {
		t.Fatalf("StartNodesInShard: %v", err)
	}
	if got := m.Shards().RunningNodes(); got != 2 {
		t.Fatalf("shard hosts %d nodes, want 2", got)
	}

	if err := m.Stop(ports[0]); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if m.IsRunning(ports[0]) {
		t.Error("stopped node still reports as running")
	}
	// 同片的另一个节点必须继续服务
	if !m.IsRunning(ports[1]) {
		t.Error("sibling node stopped serving after its peer was removed")
	}
}

// 批量启动只调谐一次，且能报出个别失败的节点
func TestStartNodesInShardReportsFailures(t *testing.T) {
	requireSingBox(t)
	m := newShardedManager(t)

	ports := freePorts(t, 2)
	good := testNode("good", ports[0])
	// REALITY 缺公钥会被 ValidateForXray 拦下（sing-box 对多数字段容错很高，
	// 这是少数能在启动前就判定为非法的情况）
	bad := testNode("bad", ports[1])
	bad.Protocol = "vless"
	bad.Settings.Security = "reality"

	failures, err := m.StartNodesInShard([]*models.ProxyRule{good, bad})
	if err != nil {
		t.Fatalf("StartNodesInShard: %v", err)
	}
	if _, failed := failures["bad"]; !failed {
		t.Error("the invalid node should be reported as a failure")
	}
	if _, failed := failures["good"]; failed {
		t.Error("the valid node should not be reported as a failure")
	}
	if !m.IsRunning(ports[0]) {
		t.Error("the valid node should be running")
	}
	if m.IsRunning(ports[1]) {
		t.Error("the rejected node must not be running")
	}
}

// 批量停止后端口应释放
func TestStopNodesInShard(t *testing.T) {
	requireSingBox(t)
	m := newShardedManager(t)

	ports := freePorts(t, 3)
	var nodes []*models.ProxyRule
	for i, port := range ports {
		nodes = append(nodes, testNode(string(rune('a'+i)), port))
	}
	if _, err := m.StartNodesInShard(nodes); err != nil {
		t.Fatalf("StartNodesInShard: %v", err)
	}

	if err := m.StopNodesInShard([]string{"a", "b"}); err != nil {
		t.Fatalf("StopNodesInShard: %v", err)
	}
	if got := m.Shards().RunningNodes(); got != 1 {
		t.Errorf("shard hosts %d nodes, want 1", got)
	}
	if m.IsRunning(ports[0]) || m.IsRunning(ports[1]) {
		t.Error("stopped nodes still report as running")
	}
	if !m.IsRunning(ports[2]) {
		t.Error("the remaining node should still be running")
	}
}

// StopAll 必须把分片一起停掉
func TestStopAllStopsShards(t *testing.T) {
	requireSingBox(t)
	m := newShardedManager(t)

	ports := freePorts(t, 2)
	nodes := []*models.ProxyRule{testNode("a", ports[0]), testNode("b", ports[1])}
	if _, err := m.StartNodesInShard(nodes); err != nil {
		t.Fatalf("StartNodesInShard: %v", err)
	}

	m.StopAll()

	if got := m.Shards().RunningShards(); got != 0 {
		t.Errorf("RunningShards after StopAll = %d, want 0", got)
	}
	for _, port := range ports {
		if m.IsRunning(port) {
			t.Errorf("port %d still reports as running after StopAll", port)
		}
	}
}

// 分片模式下的容量上限应远高于一节点一进程
func TestShardedCapacityIsHigher(t *testing.T) {
	plain := CheckCapacity(2000)
	sharded := CheckShardedCapacity(2000)

	if plain == nil && sharded == nil {
		t.Skip("machine has enough memory for both modes")
	}
	if plain != nil && sharded == nil {
		return // 正是期望：独立进程放不下，分片放得下
	}
	if plain == nil && sharded != nil {
		t.Error("sharded mode should never be more restrictive than per-process mode")
	}
	var plainErr, shardedErr *CapacityError
	if asCapacityError(plain, &plainErr) && asCapacityError(sharded, &shardedErr) {
		if shardedErr.Allowed <= plainErr.Allowed {
			t.Errorf("sharded allows %d, per-process allows %d; sharded should be higher",
				shardedErr.Allowed, plainErr.Allowed)
		}
	}
}

func asCapacityError(err error, target **CapacityError) bool {
	return err != nil && errors.As(err, target)
}
