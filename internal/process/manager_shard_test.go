package process

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

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

// 分片节点启动后必须触发真实 IP 获取与连通性验证。
//
// 分片节点不走 startProcessLocked，这一步曾被漏掉：表现为节点能正常代理，
// 但界面上不显示真实 IP，连不通的节点也不会被标记和自动停用。
func TestShardStartTriggersRealIPProbe(t *testing.T) {
	requireSingBox(t)
	m := newShardedManager(t)

	probed := make(chan int, 4)
	m.SetRealIPCallback(func(localPort int, ip string) { probed <- localPort })
	m.SetNodeFailedCallback(func(localPort int, reason string) { probed <- localPort })

	port := freePorts(t, 1)[0]
	rule := testNode("probe", port)
	if _, err := m.StartNodesInShard([]*models.ProxyRule{rule}); err != nil {
		t.Fatalf("StartNodesInShard: %v", err)
	}

	// 节点指向不可达的测试地址，探测必然走到失败回调；
	// 关键是「有没有被探测」，而不是探测结果本身。
	select {
	case got := <-probed:
		if got != port {
			t.Errorf("probe reported port %d, want %d", got, port)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("shard-hosted node was never probed for its real IP")
	}
}

// 首选探测点对所有节点固定不变（HEAD + 响应头、CDN 静态资源，批量也不限流），
// 兜底服务则按端口错开——公共 IP 查询 API 被上千节点同时请求会限流，
// 把可用节点误判为不通。
func TestRotatedIPServicesKeepsPrimaryFirst(t *testing.T) {
	n := len(baseIPServices)
	if n < 3 {
		t.Skip("need at least three services")
	}
	primary := baseIPServices[0]

	firstFallbacks := make(map[string]int)
	for port := 11000; port < 11000+n*3; port++ {
		services := rotatedIPServices(port)
		if len(services) != n {
			t.Fatalf("port %d: got %d services, want %d", port, len(services), n)
		}
		if services[0] != primary {
			t.Errorf("port %d: first service = %s, want the primary %s", port, services[0], primary)
		}
		// 轮转后集合必须不变，只是顺序不同
		seen := make(map[string]bool, n)
		for _, s := range services {
			seen[s] = true
		}
		if len(seen) != n {
			t.Fatalf("port %d: rotation lost or duplicated services: %v", port, services)
		}
		firstFallbacks[services[1]]++
	}

	// 兜底服务应当轮流排在首选项之后，而不是集中在一两个上
	if len(firstFallbacks) != n-1 {
		t.Errorf("only %d of %d fallback services were used as the first fallback",
			len(firstFallbacks), n-1)
	}
}

// 首选探测点必须在 headerIPServices 里登记，否则会退化成 GET 读响应体，
// 而它返回的是 PNG 图片、body 里没有 IP。
func TestPrimaryProbeUsesResponseHeader(t *testing.T) {
	primary := baseIPServices[0]
	header, ok := headerIPServices[primary]
	if !ok {
		t.Fatalf("primary probe %s is not registered as a header-based service", primary)
	}
	if header == "" {
		t.Error("header name must not be empty")
	}
}

func TestRotatedIPServicesHandlesInvalidPort(t *testing.T) {
	for _, port := range []int{0, -1} {
		services := rotatedIPServices(port)
		if len(services) != len(baseIPServices) {
			t.Errorf("port %d: got %d services, want %d", port, len(services), len(baseIPServices))
		}
	}
}

// 端口就绪后应立即返回，不必等满超时——批量探测时每个 worker
// 白等固定时长会直接拖长整批耗时。
func TestWaitLocalPortReadyReturnsEarly(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	start := time.Now()
	waitLocalPortReady(port, 3*time.Second)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s for an already-listening port, expected to return promptly", elapsed)
	}
}

// 端口始终不可用时必须在超时后返回，不能无限等待
func TestWaitLocalPortReadyRespectsTimeout(t *testing.T) {
	start := time.Now()
	waitLocalPortReady(1, 300*time.Millisecond) // 端口 1 几乎不可能被监听
	elapsed := time.Since(start)
	if elapsed < 250*time.Millisecond {
		t.Errorf("returned after %s, should have waited for the timeout", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Errorf("waited %s, far beyond the requested timeout", elapsed)
	}
}

// 探测参数必须在「快」和「准」之间取得平衡。
//
// 曾把重试上限压到 2、单次超时压到 5 秒来提速，结果 1426 个可用节点
// 被误判到只剩 970 个——单个查询服务限流或抖动就足以让可用节点出局。
func TestRealIPProbeSettingsFavorAccuracy(t *testing.T) {
	// 至少要给几次机会，才能扛住单个查询服务的抖动
	if realIPMaxAttempts < 3 {
		t.Errorf("realIPMaxAttempts = %d, too few retries; transient failures will "+
			"misjudge working nodes as dead", realIPMaxAttempts)
	}
	// 重试次数不能超过可用服务数，否则后面的循环是空转
	if realIPMaxAttempts > len(baseIPServices) {
		t.Errorf("realIPMaxAttempts = %d exceeds the %d available services",
			realIPMaxAttempts, len(baseIPServices))
	}
	// 跨境线路握手加请求经常要好几秒，超时太短会误伤慢但可用的节点
	if realIPProbeTimeout < 8*time.Second {
		t.Errorf("realIPProbeTimeout = %s, too short for slow cross-border links",
			realIPProbeTimeout)
	}
	// 建连超时要明显短于整体超时：坏节点卡在建连阶段，靠它快速出局，
	// 而已连上的慢节点仍有完整时间读完响应
	if realIPDialTimeout >= realIPProbeTimeout {
		t.Errorf("realIPDialTimeout (%s) must be well below realIPProbeTimeout (%s), "+
			"otherwise dead nodes cannot fail fast", realIPDialTimeout, realIPProbeTimeout)
	}
}

// 整批节点必须在开工前就标记为「验证中」。
//
// 并发有上限，排队中的节点要等一阵才轮到；若等轮到时才标记，
// 界面上这些节点会先显示成「运行中」，用户会误以为已经可用，
// 而它们可能几秒后就被判定不通并自动停用。
func TestVerifyStartedNodesMarksWholeBatchUpFront(t *testing.T) {
	rules := make([]*models.ProxyRule, 0, realIPConcurrency*2)
	for i := 0; i < cap(rules); i++ {
		// 端口 0 让探测立刻失败，测试不必等真实网络
		rules = append(rules, testNode(fmt.Sprintf("n%d", i), 0))
	}

	m := &Manager{processes: make(map[int]*ProcessInfo), pollerStop: make(chan struct{})}
	m.verifyStartedNodes(rules)

	// verifyStartedNodes 同步标记后才开协程，返回时整批都应已标记
	for _, rule := range rules {
		if !rule.Verifying {
			t.Fatalf("node %s was not marked as verifying before the batch started", rule.ID)
		}
	}
}

// 验证结束后必须清除标记，否则节点会永远停在「验证中」
func TestGetRealIPClearsVerifyingFlag(t *testing.T) {
	m := &Manager{processes: make(map[int]*ProcessInfo), pollerStop: make(chan struct{})}
	// 端口 0 无法连通，探测会走失败分支
	rule := testNode("done", 0)

	m.getRealIP(rule)

	if rule.Verifying {
		t.Error("verifying flag was left set after the probe finished")
	}
}
