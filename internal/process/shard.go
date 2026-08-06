package process

import (
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"xray-manager/internal/assets"
	"xray-manager/internal/models"
	"xray-manager/internal/singbox"
	"xray-manager/internal/utils"
)

// DefaultShardSize 单个分片承载的节点数上限。
//
// 300 是在几方面之间取的平衡：配置体积与 marshal/check 耗时（实测 300 节点
// 136 KB、check 0.27s）、切换时受影响的节点数（同片节点会一起瞬断）、
// 以及进程数（1583 个节点约 6 个进程）。
const DefaultShardSize = 300

// shardStartTimeout 等待新分片进程完成端口监听的上限。
const shardStartTimeout = 30 * time.Second

// Shard 一个承载多个节点的 sing-box 进程。
type Shard struct {
	ID         string
	cmd        *exec.Cmd
	apiPort    int
	configPath string
	configSeq  int
	nodes      []*models.ProxyRule // 当前进程实际承载的节点（已剔除构建/绑定失败的）

	// stale 标记配置已作废（如前置代理变更），下次调谐必须重建，
	// 即便节点集合本身没有变化。
	stale bool
}

// Nodes 返回该分片当前承载的节点副本。
func (s *Shard) Nodes() []*models.ProxyRule {
	out := make([]*models.ProxyRule, len(s.nodes))
	copy(out, s.nodes)
	return out
}

// Running 该分片进程是否在运行。
func (s *Shard) Running() bool {
	return s != nil && s.cmd != nil && s.cmd.Process != nil
}

// ShardAssignment 按 nodeID 哈希把节点固定分配到某个分片。
//
// 固定分配而非「挑一个没满的」：后者在增删节点时会引发其他节点迁移，
// 导致多个分片一起重建、正在使用的连接被无谓地打断。
// 哈希分配下每个节点的归属始终不变，增删只影响它自己所在的那一片。
func ShardAssignment(nodeID string, shardCount int) string {
	if shardCount <= 1 {
		return "shard0"
	}
	sum := crc32.ChecksumIEEE([]byte(nodeID))
	return fmt.Sprintf("shard%d", int(sum%uint32(shardCount)))
}

// ShardCountFor 按节点总数推算需要的分片数（使用默认分片容量）。
func ShardCountFor(nodeCount int) int {
	return shardCountForSize(nodeCount, DefaultShardSize)
}

func shardCountForSize(nodeCount, shardSize int) int {
	if shardSize <= 0 {
		shardSize = DefaultShardSize
	}
	if nodeCount <= 0 {
		return 1
	}
	count := (nodeCount + shardSize - 1) / shardSize
	if count < 1 {
		return 1
	}
	return count
}

// PortUnavailableError 端口无法绑定，节点被排除在分片之外。
type PortUnavailableError struct {
	NodeID string
	Alias  string
	Port   int
}

func (e *PortUnavailableError) Error() string {
	return fmt.Sprintf("节点「%s」的本地端口 %d 无法绑定（可能被其他程序占用，"+
		"或落在系统保留端口范围内），已跳过", e.Alias, e.Port)
}

// filterBindablePorts 剔除端口无法绑定的节点。
//
// 分片里只要有一个端口绑不上，sing-box 就会整体启动失败——同片几百个
// 正常节点跟着一起起不来。sing-box check 只校验配置本身、不检查端口，
// 因此必须在启动前自己探测一遍。
//
// 探测 TCP+UDP：mixed 入站两者都绑，且 Windows 的保留端口范围不显示为
// 「已监听」但同样无法 bind。
func filterBindablePorts(nodes []*models.ProxyRule) (usable []*models.ProxyRule, rejected []error) {
	for _, node := range nodes {
		if utils.CheckPortBindable(node.LocalPort) {
			usable = append(usable, node)
			continue
		}
		rejected = append(rejected, &PortUnavailableError{
			NodeID: node.ID, Alias: node.Alias, Port: node.LocalPort,
		})
	}
	return usable, rejected
}

// ShardManager 以分片方式运行节点：多个节点共享一个 sing-box 进程。
//
// 与一节点一进程相比，内存从「节点数 × 33 MB」降到「分片数 × 33 MB」
// （实测 300 节点单进程 32 MB，对比 300 个独立进程的 9810 MB）。
// 每个节点仍然监听自己原有的 LocalPort，对上层完全透明。
type ShardManager struct {
	mu sync.Mutex

	configDir string
	logFunc   func(string)

	// desired 期望运行的节点集合。Start/Stop/批量操作只修改它，
	// 真正的进程调整统一由 Reconcile 完成——这样批量操作天然合并，
	// 启动 300 个节点只重建一次配置，而不是 300 次。
	desired map[string]*models.ProxyRule

	shards    map[string]*Shard
	nodeShard map[string]string // nodeID -> shardID
	portNode  map[int]string    // 本地端口 -> nodeID，保留端口视角的查询

	apiPortAlloc func(int) int // 为分片分配 Clash API 端口，便于测试注入
	shardSize    int

	// preProxy 全局前置代理。非空时各节点经它出站（配置里共享一份出站，
	// 节点用 detour 指向），因此设置前置代理后节点依然共享进程。
	preProxy *models.ProxyRule
}

// SetPreProxy 设置全局前置代理，nil 表示直连。
//
// 前置代理变更会影响所有节点的出站，需要重建全部分片；
// 这里只记录变更，由调用方随后执行 Reconcile。返回值表示配置是否真的变了。
func (m *ShardManager) SetPreProxy(preProxy *models.ProxyRule) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldID := ""
	if m.preProxy != nil {
		oldID = m.preProxy.ID
	}
	newID := ""
	if preProxy != nil {
		newID = preProxy.ID
	}
	if oldID == newID {
		return false
	}

	m.preProxy = preProxy
	// 前置代理变了，现有分片的出站配置全部作废，下次调谐必须重建。
	// 不能直接清空 nodes——停止分片时要靠它逐个等待端口释放。
	for _, shard := range m.shards {
		shard.stale = true
	}
	return true
}

// NewShardManager 创建分片管理器。configDir 用于存放各分片的配置文件。
func NewShardManager(configDir string, logFunc func(string)) *ShardManager {
	return &ShardManager{
		configDir:    configDir,
		logFunc:      logFunc,
		desired:      make(map[string]*models.ProxyRule),
		shards:       make(map[string]*Shard),
		nodeShard:    make(map[string]string),
		portNode:     make(map[int]string),
		apiPortAlloc: FindApiPort,
		shardSize:    DefaultShardSize,
	}
}

func (m *ShardManager) log(message string) {
	if m.logFunc != nil {
		m.logFunc(message)
	}
}

// SetDesired 批量设置期望运行的节点，替换原有集合。
// 调用后需执行 Reconcile 才会真正生效。
func (m *ShardManager) SetDesired(nodes []*models.ProxyRule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.desired = make(map[string]*models.ProxyRule, len(nodes))
	for _, node := range nodes {
		m.desired[node.ID] = node
	}
}

// AddDesired 把节点加入期望集合（已存在则更新）。
func (m *ShardManager) AddDesired(nodes ...*models.ProxyRule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, node := range nodes {
		m.desired[node.ID] = node
	}
}

// RemoveDesired 把节点移出期望集合。
func (m *ShardManager) RemoveDesired(nodeIDs ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range nodeIDs {
		delete(m.desired, id)
	}
}

// DesiredCount 期望运行的节点数。
func (m *ShardManager) DesiredCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.desired)
}

// planShards 按哈希把期望节点分配到各分片，返回 shardID -> 节点列表。
// 每片内按节点 ID 排序，保证同样的输入产生同样的配置——
// 否则 map 遍历顺序的随机性会让配置反复变化、触发无谓的重启。
func (m *ShardManager) planShards() map[string][]*models.ProxyRule {
	shardCount := shardCountForSize(len(m.desired), m.shardSize)
	plan := make(map[string][]*models.ProxyRule)
	for _, node := range m.desired {
		shardID := ShardAssignment(node.ID, shardCount)
		plan[shardID] = append(plan[shardID], node)
	}
	for shardID := range plan {
		sort.Slice(plan[shardID], func(i, j int) bool {
			return plan[shardID][i].ID < plan[shardID][j].ID
		})
	}
	return plan
}

// ReconcileResult 一次调谐的结果。
type ReconcileResult struct {
	ShardsStarted int
	ShardsStopped int
	ShardsUpdated int
	NodesRunning  int
	Skipped       []error // 因配置非法或端口不可用被排除的节点
}

// Reconcile 让实际运行的分片与期望集合一致。
//
// 只重建内容有变化的分片：节点集合未变的分片保持原进程不动，
// 避免批量操作波及无关节点。
func (m *ShardManager) Reconcile() (ReconcileResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result ReconcileResult
	plan := m.planShards()

	// 停掉不再需要的分片
	for shardID, shard := range m.shards {
		if _, stillNeeded := plan[shardID]; !stillNeeded {
			m.stopShardLocked(shard)
			delete(m.shards, shardID)
			result.ShardsStopped++
		}
	}

	shardIDs := make([]string, 0, len(plan))
	for shardID := range plan {
		shardIDs = append(shardIDs, shardID)
	}
	sort.Strings(shardIDs)

	var firstErr error
	for _, shardID := range shardIDs {
		nodes := plan[shardID]
		existing := m.shards[shardID]

		if existing != nil && existing.Running() && !existing.stale && sameNodeSet(existing.nodes, nodes) {
			result.NodesRunning += len(existing.nodes)
			continue
		}

		started, skipped, err := m.applyShardLocked(shardID, nodes)
		result.Skipped = append(result.Skipped, skipped...)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			m.log(fmt.Sprintf("[分片] %s 启动失败: %v", shardID, err))
			continue
		}
		if existing != nil {
			result.ShardsUpdated++
		} else {
			result.ShardsStarted++
		}
		result.NodesRunning += len(started.nodes)
	}

	m.rebuildIndexLocked()
	return result, firstErr
}

// applyShardLocked 用给定节点集合重建一个分片（需已持有锁）。
//
// 采用「先停旧、再起新」而非双进程并行切换：同一批本地端口不能被两个进程
// 同时监听，新进程必然绑定失败。代价是切换期间该片短暂中断——把分片规模
// 控制在 300 以内、并让批量操作合并为一次重建，就是为了压住这个影响面。
// sing-box 没有配置热重载（实测 Clash API 的 PUT /configs 返回 204 但不生效，
// 官方文档中 reload 仅适用于证书与 rule-set 路径），因此必须重启进程。
func (m *ShardManager) applyShardLocked(shardID string, nodes []*models.ProxyRule) (*Shard, []error, error) {
	old := m.shards[shardID]
	if old != nil {
		// 旧进程占着这些端口，必须先停掉才能探测它们是否真正可用，
		// 否则自己占的端口会被误判为不可绑定
		m.stopShardLocked(old)
	}

	usable, rejected := filterBindablePorts(nodes)
	if len(usable) == 0 {
		delete(m.shards, shardID)
		return nil, rejected, fmt.Errorf("分片 %s 内没有可用节点", shardID)
	}

	seq := 1
	if old != nil {
		seq = old.configSeq + 1
	}
	apiPort := 0
	if m.apiPortAlloc != nil {
		apiPort = m.apiPortAlloc(basePortOf(usable))
	}

	config, skipped, err := singbox.BuildShardConfigWithPreProxy(usable, m.preProxy, apiPort)
	for _, s := range skipped {
		rejected = append(rejected, fmt.Errorf("节点「%s」配置无效，已跳过: %v", s.Alias, s.Err))
	}
	if err != nil {
		delete(m.shards, shardID)
		return nil, rejected, err
	}
	configJSON, err := config.ToJSON()
	if err != nil {
		delete(m.shards, shardID)
		return nil, rejected, err
	}

	// 配置文件带序号，不覆盖旧文件：重建失败时旧配置仍在，便于排查与回退
	configPath := filepath.Join(m.configDir, fmt.Sprintf("%s-%03d.json", shardID, seq))
	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		delete(m.shards, shardID)
		return nil, rejected, fmt.Errorf("创建配置目录失败: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		delete(m.shards, shardID)
		return nil, rejected, fmt.Errorf("写入分片配置失败: %v", err)
	}

	// skipped 里的节点没进配置，实际承载的是二者之差
	included := excludeSkipped(usable, skipped)

	shard := &Shard{
		ID:         shardID,
		apiPort:    apiPort,
		configPath: configPath,
		configSeq:  seq,
		nodes:      included,
	}

	if err := m.startShardProcessLocked(shard); err != nil {
		delete(m.shards, shardID)
		return nil, rejected, err
	}

	m.shards[shardID] = shard
	if old != nil && old.configPath != "" && old.configPath != configPath {
		_ = os.Remove(old.configPath)
	}
	m.log(fmt.Sprintf("[分片] %s 已启动，承载 %d 个节点", shardID, len(included)))
	return shard, rejected, nil
}

// startShardProcessLocked 启动分片进程并等待其完成端口监听。
func (m *ShardManager) startShardProcessLocked(shard *Shard) error {
	binary, err := assets.FindSingBoxBinary()
	if err != nil {
		return err
	}

	cmd := exec.Command(binary, "run", "-c", shard.configPath)
	setPlatformSpecificAttrs(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动分片进程失败: %v", err)
	}
	shard.cmd = cmd

	// 进程刚 fork 出来不等于端口已就绪，要等真正能连上才算切换成功，
	// 否则调用方会在端口还没起来时就把流量打过去。
	//
	// 只探首个节点是不够的：个别节点可能因参数问题始终起不来（sing-box 对
	// 配置容错很高，空 server 之类的问题要到运行时才暴露），死等它会拖住
	// 整批操作。这里只要有任意一个节点就绪就认为进程已经起来了。
	if len(shard.nodes) > 0 {
		if err := waitShardReady(shard.nodes, shardStartTimeout); err != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			shard.cmd = nil
			return fmt.Errorf("分片进程启动后端口未就绪: %v", err)
		}
	}
	return nil
}

// stopShardLocked 停止分片进程并等待端口释放（需已持有锁）。
func (m *ShardManager) stopShardLocked(shard *Shard) {
	if shard == nil || shard.cmd == nil || shard.cmd.Process == nil {
		return
	}
	_ = shard.cmd.Process.Kill()
	_, _ = shard.cmd.Process.Wait()
	shard.cmd = nil

	// 端口未完全释放前就重启新进程会 bind 失败，这里等到确实可用为止
	for _, node := range shard.nodes {
		utils.WaitPortReleased(node.LocalPort, 3*time.Second, nil)
	}
	utils.InvalidatePortPIDCache()
}

// rebuildIndexLocked 重建 nodeID/端口 到分片的反查索引。
func (m *ShardManager) rebuildIndexLocked() {
	m.nodeShard = make(map[string]string)
	m.portNode = make(map[int]string)
	for shardID, shard := range m.shards {
		for _, node := range shard.nodes {
			m.nodeShard[node.ID] = shardID
			m.portNode[node.LocalPort] = node.ID
		}
	}
}

// IsNodeRunning 节点是否正在某个运行中的分片里。
func (m *ShardManager) IsNodeRunning(nodeID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	shardID, ok := m.nodeShard[nodeID]
	if !ok {
		return false
	}
	return m.shards[shardID].Running()
}

// IsPortRunning 指定本地端口是否由某个运行中的分片提供服务。
func (m *ShardManager) IsPortRunning(localPort int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	nodeID, ok := m.portNode[localPort]
	if !ok {
		return false
	}
	shardID := m.nodeShard[nodeID]
	return m.shards[shardID].Running()
}

// NodeIDAt 返回监听指定本地端口的节点 ID。
// 上层多以端口为键操作（Stop/IsRunning），这里提供端口到节点的反查。
func (m *ShardManager) NodeIDAt(localPort int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nodeID, ok := m.portNode[localPort]
	return nodeID, ok
}

// ShardOf 返回承载指定节点的分片 ID。
func (m *ShardManager) ShardOf(nodeID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	shardID, ok := m.nodeShard[nodeID]
	return shardID, ok
}

// RunningShards 当前运行中的分片数。
func (m *ShardManager) RunningShards() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, shard := range m.shards {
		if shard.Running() {
			count++
		}
	}
	return count
}

// RunningNodes 当前实际运行的节点数。
func (m *ShardManager) RunningNodes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, shard := range m.shards {
		if shard.Running() {
			count += len(shard.nodes)
		}
	}
	return count
}

// StopAll 停止全部分片并清空期望集合。
func (m *ShardManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for shardID, shard := range m.shards {
		m.stopShardLocked(shard)
		if shard.configPath != "" {
			_ = os.Remove(shard.configPath)
		}
		delete(m.shards, shardID)
	}
	m.desired = make(map[string]*models.ProxyRule)
	m.rebuildIndexLocked()
}

// ---- 内部辅助 ----

// sameNodeSet 判断两组节点是否等价（ID 与本地端口都一致）。
// 用于跳过内容未变的分片，避免无谓重启。
func sameNodeSet(a, b []*models.ProxyRule) bool {
	if len(a) != len(b) {
		return false
	}
	ports := make(map[string]int, len(a))
	for _, node := range a {
		ports[node.ID] = node.LocalPort
	}
	for _, node := range b {
		port, ok := ports[node.ID]
		if !ok || port != node.LocalPort {
			return false
		}
	}
	return true
}

func excludeSkipped(nodes []*models.ProxyRule, skipped []singbox.SkippedNode) []*models.ProxyRule {
	if len(skipped) == 0 {
		return nodes
	}
	bad := make(map[string]bool, len(skipped))
	for _, s := range skipped {
		bad[s.NodeID] = true
	}
	out := make([]*models.ProxyRule, 0, len(nodes))
	for _, node := range nodes {
		if !bad[node.ID] {
			out = append(out, node)
		}
	}
	return out
}

func basePortOf(nodes []*models.ProxyRule) int {
	if len(nodes) == 0 {
		return 0
	}
	return nodes[0].LocalPort
}

// waitShardReady 等待分片进程就绪：任意一个节点端口能连上即可。
//
// 逐轮扫描全部节点而非死等某一个：个别节点可能因参数问题始终起不来，
// 只盯着它会白等到超时、拖住整批操作。
func waitShardReady(nodes []*models.ProxyRule, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if portDialable(node.LocalPort) {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("等待 %d 个节点的端口就绪超时", len(nodes))
}

// portDialable 端口是否已能建立连接。
//
// 用「能否 dial 通」而不是「端口是否已被占用」来判断：后者在端口被其他进程
// 抢占时也会成立，会把「别人占着」误判成「我们起来了」，调用方随后把流量
// 打到不相干的进程上。
func portDialable(port int) bool {
	conn, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
