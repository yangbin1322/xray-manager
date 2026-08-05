package process

import (
	"fmt"
	"net"
	"testing"
	"time"

	"xray-manager/internal/assets"
	"xray-manager/internal/models"
	"xray-manager/internal/utils"
)

// requireSingBox 跳过没有内核二进制的环境（如未放置 embed 文件的 CI）
func requireSingBox(t *testing.T) {
	t.Helper()
	if _, err := assets.FindSingBoxBinary(); err != nil {
		t.Skipf("sing-box binary unavailable: %v", err)
	}
}

// freePorts 取 count 个 TCP+UDP 都空闲的端口。
// mixed 入站同时绑 TCP 和 UDP，只探 TCP 会挑中实际绑不上的端口。
func freePorts(t *testing.T, count int) []int {
	t.Helper()
	var ports []int
	for port := 39000; port < 65000 && len(ports) < count; port++ {
		if utils.CheckPortBindable(port) {
			ports = append(ports, port)
		}
	}
	if len(ports) < count {
		t.Skipf("could not find %d free ports", count)
	}
	return ports
}

// dialable 端口是否已被监听
func dialable(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// 多个节点应当由同一个进程承载，且每个节点的端口都真正在监听
func TestReconcileRunsNodesInOneProcess(t *testing.T) {
	requireSingBox(t)

	ports := freePorts(t, 3)
	m := NewShardManager(t.TempDir(), nil)
	t.Cleanup(m.StopAll)

	nodes := []*models.ProxyRule{
		testNode("a", ports[0]),
		testNode("b", ports[1]),
		testNode("c", ports[2]),
	}
	m.SetDesired(nodes)

	result, err := m.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.ShardsStarted != 1 {
		t.Errorf("started %d shards, want 1", result.ShardsStarted)
	}
	if result.NodesRunning != 3 {
		t.Errorf("running %d nodes, want 3", result.NodesRunning)
	}
	// 三个节点共享一个进程，这正是本次改造的目的
	if got := m.RunningShards(); got != 1 {
		t.Errorf("RunningShards = %d, want 1 process for 3 nodes", got)
	}

	for _, port := range ports {
		if !dialable(port) {
			t.Errorf("port %d is not listening after reconcile", port)
		}
		if !m.IsPortRunning(port) {
			t.Errorf("IsPortRunning(%d) = false, want true", port)
		}
	}
	for _, node := range nodes {
		if !m.IsNodeRunning(node.ID) {
			t.Errorf("IsNodeRunning(%s) = false, want true", node.ID)
		}
	}
}

// 节点集合未变时重复 Reconcile 不应重启分片——否则批量操作会反复打断连接
func TestReconcileIsIdempotent(t *testing.T) {
	requireSingBox(t)

	ports := freePorts(t, 2)
	m := NewShardManager(t.TempDir(), nil)
	t.Cleanup(m.StopAll)

	m.SetDesired([]*models.ProxyRule{testNode("a", ports[0]), testNode("b", ports[1])})
	if _, err := m.Reconcile(); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	result, err := m.Reconcile()
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if result.ShardsStarted != 0 || result.ShardsUpdated != 0 {
		t.Errorf("a no-op reconcile restarted shards: started=%d updated=%d",
			result.ShardsStarted, result.ShardsUpdated)
	}
	if result.NodesRunning != 2 {
		t.Errorf("running %d nodes, want 2", result.NodesRunning)
	}
}

// 增删节点后端口应随之开启/关闭
func TestReconcileAppliesDesiredChanges(t *testing.T) {
	requireSingBox(t)

	ports := freePorts(t, 3)
	m := NewShardManager(t.TempDir(), nil)
	t.Cleanup(m.StopAll)

	m.SetDesired([]*models.ProxyRule{testNode("a", ports[0]), testNode("b", ports[1])})
	if _, err := m.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// 加一个节点
	m.AddDesired(testNode("c", ports[2]))
	if _, err := m.Reconcile(); err != nil {
		t.Fatalf("Reconcile after add: %v", err)
	}
	if !dialable(ports[2]) {
		t.Errorf("port %d should be listening after the node was added", ports[2])
	}
	if got := m.RunningNodes(); got != 3 {
		t.Errorf("RunningNodes = %d, want 3", got)
	}

	// 删一个节点
	m.RemoveDesired("a")
	if _, err := m.Reconcile(); err != nil {
		t.Fatalf("Reconcile after remove: %v", err)
	}
	if got := m.RunningNodes(); got != 2 {
		t.Errorf("RunningNodes = %d, want 2", got)
	}
	if m.IsNodeRunning("a") {
		t.Error("removed node still reports as running")
	}
	// 剩下的节点必须继续服务
	if !dialable(ports[1]) || !dialable(ports[2]) {
		t.Error("remaining nodes stopped serving after a sibling was removed")
	}
}

// 端口绑不上的节点必须被跳过，同片其余节点照常启动。
// 这是第一阶段实测发现的问题：一个端口冲突会让整片起不来。
func TestReconcileSkipsUnbindablePortAndKeepsRest(t *testing.T) {
	requireSingBox(t)

	ports := freePorts(t, 2)
	// 占住一个端口，模拟被其他程序抢先
	blocker, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", ports[1]))
	if err != nil {
		t.Skipf("could not occupy port %d: %v", ports[1], err)
	}
	defer blocker.Close()

	m := NewShardManager(t.TempDir(), nil)
	t.Cleanup(m.StopAll)

	m.SetDesired([]*models.ProxyRule{
		testNode("good", ports[0]),
		testNode("blocked", ports[1]),
	})

	result, err := m.Reconcile()
	if err != nil {
		t.Fatalf("one unbindable port must not fail the whole shard: %v", err)
	}
	if result.NodesRunning != 1 {
		t.Errorf("running %d nodes, want 1 (the good one)", result.NodesRunning)
	}
	if len(result.Skipped) != 1 {
		t.Fatalf("Skipped = %v, want one entry", result.Skipped)
	}
	if !dialable(ports[0]) {
		t.Error("the good node should be serving despite its sibling's port conflict")
	}
	if m.IsNodeRunning("blocked") {
		t.Error("the blocked node must not report as running")
	}
}

// 配置非法的节点被跳过，不影响同片其他节点
func TestReconcileSkipsInvalidNode(t *testing.T) {
	requireSingBox(t)

	ports := freePorts(t, 2)
	bad := testNode("bad", ports[1])
	bad.Protocol = "no-such-protocol"

	m := NewShardManager(t.TempDir(), nil)
	t.Cleanup(m.StopAll)
	m.SetDesired([]*models.ProxyRule{testNode("good", ports[0]), bad})

	result, err := m.Reconcile()
	if err != nil {
		t.Fatalf("an invalid node must not fail the shard: %v", err)
	}
	if result.NodesRunning != 1 {
		t.Errorf("running %d nodes, want 1", result.NodesRunning)
	}
	if len(result.Skipped) == 0 {
		t.Error("the invalid node should be reported in Skipped")
	}
	if !dialable(ports[0]) {
		t.Error("the valid node should be serving")
	}
}

// 超过单片容量时应拆成多个进程
func TestReconcileSplitsIntoMultipleShards(t *testing.T) {
	requireSingBox(t)

	const nodeCount = 12
	ports := freePorts(t, nodeCount)

	m := NewShardManager(t.TempDir(), nil)
	m.shardSize = 5 // 缩小分片便于测试
	t.Cleanup(m.StopAll)

	var nodes []*models.ProxyRule
	for i := 0; i < nodeCount; i++ {
		nodes = append(nodes, testNode(fmt.Sprintf("node%d", i), ports[i]))
	}
	m.SetDesired(nodes)

	if _, err := m.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := m.RunningNodes(); got != nodeCount {
		t.Errorf("RunningNodes = %d, want %d", got, nodeCount)
	}
	for _, port := range ports {
		if !dialable(port) {
			t.Errorf("port %d is not listening", port)
		}
	}
}

// StopAll 之后所有端口都应释放
func TestStopAllReleasesPorts(t *testing.T) {
	requireSingBox(t)

	ports := freePorts(t, 2)
	m := NewShardManager(t.TempDir(), nil)

	m.SetDesired([]*models.ProxyRule{testNode("a", ports[0]), testNode("b", ports[1])})
	if _, err := m.Reconcile(); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	m.StopAll()

	if got := m.RunningShards(); got != 0 {
		t.Errorf("RunningShards after StopAll = %d, want 0", got)
	}
	if got := m.DesiredCount(); got != 0 {
		t.Errorf("DesiredCount after StopAll = %d, want 0", got)
	}
	for _, port := range ports {
		if dialable(port) {
			t.Errorf("port %d is still listening after StopAll", port)
		}
	}
}
