package process

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"xray-manager/internal/models"
)

// findFreePort 取一个 TCP+UDP 都空闲的端口
func findFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func asPortError(err error, target **PortUnavailableError) bool {
	return errors.As(err, target)
}

func testNode(id string, port int) *models.ProxyRule {
	return &models.ProxyRule{
		ID:         id,
		Alias:      id,
		Protocol:   "trojan",
		ServerAddr: "1.2.3.4",
		ServerPort: 443,
		LocalPort:  port,
		LocalType:  "mixed",
		Settings:   models.ProxySettings{TrojanPassword: "pw"},
	}
}

// 同一个节点必须始终落在同一个分片：否则增删节点会引发无关节点迁移，
// 导致多个分片一起重建、正在使用的连接被无谓打断。
func TestShardAssignmentIsStable(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("node%d", i)
		first := ShardAssignment(id, 6)
		for j := 0; j < 5; j++ {
			if got := ShardAssignment(id, 6); got != first {
				t.Fatalf("node %s moved between shards: %s -> %s", id, first, got)
			}
		}
	}
}

// 分片数变化时归属可以变，但同一分片数下必须稳定，且分布不能过于集中
func TestShardAssignmentDistributes(t *testing.T) {
	const shardCount = 6
	counts := make(map[string]int)
	for i := 0; i < 600; i++ {
		counts[ShardAssignment(fmt.Sprintf("node%d", i), shardCount)]++
	}
	if len(counts) != shardCount {
		t.Errorf("used %d shards, want %d", len(counts), shardCount)
	}
	// 理想每片 100 个，放宽到 [40, 200] 只为排除严重倾斜
	for shardID, count := range counts {
		if count < 40 || count > 200 {
			t.Errorf("shard %s got %d nodes, distribution too skewed", shardID, count)
		}
	}
}

func TestShardAssignmentSingleShard(t *testing.T) {
	if got := ShardAssignment("anything", 1); got != "shard0" {
		t.Errorf("single-shard assignment = %s, want shard0", got)
	}
	if got := ShardAssignment("anything", 0); got != "shard0" {
		t.Errorf("zero shard count should fall back to shard0, got %s", got)
	}
}

func TestShardCountFor(t *testing.T) {
	cases := []struct{ nodes, want int }{
		{0, 1},
		{1, 1},
		{300, 1},
		{301, 2},
		{600, 2},
		{1583, 6}, // 用户的实际节点规模
	}
	for _, tc := range cases {
		if got := ShardCountFor(tc.nodes); got != tc.want {
			t.Errorf("ShardCountFor(%d) = %d, want %d", tc.nodes, got, tc.want)
		}
	}
}

// 期望集合的增删应当即时反映，但不触发进程操作
func TestDesiredStateMutations(t *testing.T) {
	m := NewShardManager(t.TempDir(), nil)

	m.AddDesired(testNode("a", 11000), testNode("b", 11001))
	if got := m.DesiredCount(); got != 2 {
		t.Errorf("DesiredCount = %d, want 2", got)
	}

	// 重复添加同一 ID 是更新而非新增
	m.AddDesired(testNode("a", 11002))
	if got := m.DesiredCount(); got != 2 {
		t.Errorf("re-adding an existing node changed the count to %d", got)
	}

	m.RemoveDesired("a")
	if got := m.DesiredCount(); got != 1 {
		t.Errorf("DesiredCount after removal = %d, want 1", got)
	}

	m.SetDesired([]*models.ProxyRule{testNode("x", 11010), testNode("y", 11011), testNode("z", 11012)})
	if got := m.DesiredCount(); got != 3 {
		t.Errorf("SetDesired should replace the set, got %d", got)
	}
}

// 分片计划必须稳定：同样的期望集合要产生同样的分组与顺序，
// 否则 map 遍历顺序的随机性会让配置反复变化、触发无谓重启。
func TestPlanShardsIsDeterministic(t *testing.T) {
	m := NewShardManager(t.TempDir(), nil)
	nodes := make([]*models.ProxyRule, 0, 700)
	for i := 0; i < 700; i++ {
		nodes = append(nodes, testNode(fmt.Sprintf("node%d", i), 11000+i))
	}
	m.SetDesired(nodes)

	first := m.planShards()
	for attempt := 0; attempt < 5; attempt++ {
		next := m.planShards()
		if len(next) != len(first) {
			t.Fatalf("shard count changed between runs: %d vs %d", len(next), len(first))
		}
		for shardID, want := range first {
			got := next[shardID]
			if len(got) != len(want) {
				t.Fatalf("shard %s size changed: %d vs %d", shardID, len(got), len(want))
			}
			for i := range want {
				if got[i].ID != want[i].ID {
					t.Fatalf("shard %s order changed at %d: %s vs %s", shardID, i, got[i].ID, want[i].ID)
				}
			}
		}
	}

	// 700 个节点应当分成 3 片，且总数守恒
	if len(first) != ShardCountFor(700) {
		t.Errorf("planned %d shards, want %d", len(first), ShardCountFor(700))
	}
	total := 0
	for _, nodes := range first {
		total += len(nodes)
	}
	if total != 700 {
		t.Errorf("planned %d nodes in total, want 700", total)
	}
}

func TestSameNodeSet(t *testing.T) {
	a := []*models.ProxyRule{testNode("x", 1), testNode("y", 2)}
	if !sameNodeSet(a, []*models.ProxyRule{testNode("y", 2), testNode("x", 1)}) {
		t.Error("same nodes in a different order should compare equal")
	}
	if sameNodeSet(a, []*models.ProxyRule{testNode("x", 1)}) {
		t.Error("different sizes must not compare equal")
	}
	if sameNodeSet(a, []*models.ProxyRule{testNode("x", 1), testNode("y", 99)}) {
		t.Error("a changed local port must not compare equal")
	}
	if sameNodeSet(a, []*models.ProxyRule{testNode("x", 1), testNode("z", 2)}) {
		t.Error("a different node ID must not compare equal")
	}
}

// 端口无法绑定的节点必须被剔除，且给出可读原因
func TestFilterBindablePortsRejectsBadPorts(t *testing.T) {
	good := testNode("good", 0) // 端口 0 稍后填真实可用端口
	free := findFreePort(t)
	good.LocalPort = free

	bad := testNode("bad", 0) // 端口 0 无法绑定
	usable, rejected := filterBindablePorts([]*models.ProxyRule{good, bad})

	if len(usable) != 1 || usable[0].ID != "good" {
		t.Fatalf("usable = %v, want only the good node", usable)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %v, want one entry", rejected)
	}
	var portErr *PortUnavailableError
	if !asPortError(rejected[0], &portErr) {
		t.Fatalf("rejection should be a *PortUnavailableError, got %T", rejected[0])
	}
	if portErr.NodeID != "bad" {
		t.Errorf("rejected node = %s, want bad", portErr.NodeID)
	}
}

// 整片节点都被剔除时，日志必须带上每个节点的具体原因。
//
// 曾经只打「分片内没有可用节点」这句汇总结论，看不出是哪个节点、
// 哪个端口、为什么，导致端口被别的程序占用时无从排查。
func TestFormatSkipReasonsListsCauses(t *testing.T) {
	if got := formatSkipReasons(nil); got != "" {
		t.Errorf("no skips should produce no suffix, got %q", got)
	}

	reasons := formatSkipReasons([]error{
		&PortUnavailableError{NodeID: "n1", Alias: "东京-01", Port: 11201},
	})
	if !strings.Contains(reasons, "东京-01") || !strings.Contains(reasons, "11201") {
		t.Errorf("reason must name the node and its port, got %q", reasons)
	}
}

// 原因过多时只列前几条，避免几百个节点把日志刷爆
func TestFormatSkipReasonsTruncates(t *testing.T) {
	var skipped []error
	for i := 0; i < maxLoggedSkipReasons+3; i++ {
		skipped = append(skipped, &PortUnavailableError{
			NodeID: fmt.Sprintf("n%d", i), Alias: fmt.Sprintf("节点%d", i), Port: 11000 + i,
		})
	}
	got := formatSkipReasons(skipped)

	if strings.Contains(got, fmt.Sprintf("节点%d", maxLoggedSkipReasons+2)) {
		t.Errorf("reasons beyond the cap must be omitted, got %q", got)
	}
	if !strings.Contains(got, fmt.Sprintf("共 %d 个节点被跳过", len(skipped))) {
		t.Errorf("truncated output must report the total, got %q", got)
	}
}
