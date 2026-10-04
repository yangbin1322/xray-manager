package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"xray-manager/internal/exitport"
	"xray-manager/internal/models"
	"xray-manager/internal/process"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 出口端口（ExitPort）的生命周期管理。
//
// 一个固定本地端口对应一个出口 IP：已启动且探测到的真实出口 IP 等于它的节点
// 自动成为候选，每条新连接优先走延迟最低的候选，挂了换下一个。
// 与会话代理一样运行在本进程内，不经 processManager。
//
// 锁的约定：转发器处理连接的 goroutine 只读实例自己的候选快照，绝不碰 a.mu——
// 停止转发器时会等这些 goroutine 收尾，而停止路径往往持有 a.mu，
// 连接 goroutine 若去抢 a.mu 就会互相等死。候选快照由 exitPortLoop 在
// a.mu 读锁下计算后写入。

// exitMember 出口端口的一个候选节点。
type exitMember struct {
	RuleID string
	Alias  string
	Port   int
}

// exitInstance 运行中的出口端口实例。
type exitInstance struct {
	fw     *exitport.Forwarder
	exitIP string

	mu      sync.Mutex
	members []exitMember // 已按优先级排序

	// lastIDs 上一轮的成员 ID，只由 exitPortLoop 读写，用于输出加入/退出日志
	lastIDs map[string]string // RuleID → Alias
}

// maxMemberAliases 回显给前端的候选节点别名数上限
const maxMemberAliases = 5

// memberAliases 取前几个候选节点的别名，供界面悬停提示。
func memberAliases(members []exitMember) []string {
	n := len(members)
	if n > maxMemberAliases {
		n = maxMemberAliases
	}
	aliases := make([]string, n)
	for i := 0; i < n; i++ {
		aliases[i] = members[i].Alias
	}
	return aliases
}

func memberIDSet(members []exitMember) map[string]string {
	set := make(map[string]string, len(members))
	for _, m := range members {
		set[m.RuleID] = m.Alias
	}
	return set
}

func (e *exitInstance) candidatePorts() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	ports := make([]int, len(e.members))
	for i, m := range e.members {
		ports[i] = m.Port
	}
	return ports
}

func (e *exitInstance) snapshotMembers() []exitMember {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]exitMember(nil), e.members...)
}

func (e *exitInstance) setMembers(members []exitMember) {
	e.mu.Lock()
	e.members = members
	e.mu.Unlock()
}

var (
	exitMu       sync.Mutex
	runningExits = make(map[string]*exitInstance)

	// exitDirty 节点出口 IP / 启停状态变化时发信号，让 exitPortLoop 立刻重算候选，
	// 而不是等下一个周期——IP 变了的节点要尽快从候选里摘掉并切断它的连接。
	exitDirty = make(chan struct{}, 1)

	// exitReprobing 防止周期复查重叠执行
	exitReprobing int32
)

// exitReprobeInterval 复查成员节点出口 IP 的周期。
//
// 节点出口 IP 只在启动时探测一次，而机场换落地 IP 不会通知任何人；
// 出口端口的承诺是「这个端口出去一定是这个 IP」，必须定期复查成员。
// 只查出口端口的成员，数量有限，不会压垮探测服务。
const exitReprobeInterval = 3 * time.Minute

// markExitPortsDirty 通知出口端口重算候选（非阻塞）。
func markExitPortsDirty() {
	select {
	case exitDirty <- struct{}{}:
	default:
	}
}

// exitCandidatesLocked 计算某出口 IP 当前的候选节点，按优先级排序（需已持有 a.mu）。
//
// 条件：已启动、不在验证中（验证中的 RealIP 可能是上次运行留下的旧值）、
// 真实出口 IP 与目标一致。
// 排序：健康检测正常的在前，未检测的居中，检测失败的垫底（不直接排除：
// 健康检测偶有误报，且它们出口 IP 相同，作为最后兜底不会出错）；同级按延迟升序。
func (a *MyService) exitCandidatesLocked(exitIP string) []exitMember {
	type ranked struct {
		member  exitMember
		rank    int
		latency int
	}
	var list []ranked
	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if !rule.Enabled || rule.Verifying || rule.LocalPort <= 0 {
			continue
		}
		if rule.RealIP == "" || !sameExitIP(rule.RealIP, exitIP) {
			continue
		}
		list = append(list, ranked{
			member:  exitMember{RuleID: rule.ID, Alias: rule.Alias, Port: rule.LocalPort},
			rank:    exitHealthRank(rule.HealthStatus),
			latency: exitLatency(rule),
		})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].rank != list[j].rank {
			return list[i].rank < list[j].rank
		}
		return list[i].latency < list[j].latency
	})
	members := make([]exitMember, len(list))
	for i := range list {
		members[i] = list[i].member
	}
	return members
}

func exitHealthRank(status string) int {
	switch status {
	case "online", "high_latency":
		return 0
	case "", "checking":
		return 1
	default:
		return 2
	}
}

func exitLatency(rule *models.ProxyRule) int {
	if rule.HealthLatency > 0 {
		return rule.HealthLatency
	}
	if rule.Latency > 0 {
		return rule.Latency
	}
	return math.MaxInt32
}

// exitPortLoop 维护运行中出口端口的候选快照、推送统计、定期复查成员出口 IP。
func (a *MyService) exitPortLoop(ctx context.Context) {
	refresh := time.NewTicker(time.Second)
	defer refresh.Stop()
	stats := time.NewTicker(2 * time.Second)
	defer stats.Stop()
	reprobe := time.NewTicker(exitReprobeInterval)
	defer reprobe.Stop()

	lastBytes := make(map[string]relayByteSample)

	for {
		select {
		case <-ctx.Done():
			return
		case <-exitDirty:
			a.refreshExitMembers()
		case <-refresh.C:
			a.refreshExitMembers()
		case now := <-stats.C:
			a.broadcastExitStats(lastBytes, now)
		case <-reprobe.C:
			if atomic.CompareAndSwapInt32(&exitReprobing, 0, 1) {
				go func() {
					defer atomic.StoreInt32(&exitReprobing, 0)
					a.reprobeExitMembers()
				}()
			}
		}
	}
}

func snapshotExitInstances() map[string]*exitInstance {
	exitMu.Lock()
	defer exitMu.Unlock()
	snapshot := make(map[string]*exitInstance, len(runningExits))
	for id, inst := range runningExits {
		snapshot[id] = inst
	}
	return snapshot
}

// refreshExitMembers 重算所有运行中出口端口的候选，并切断经已失效成员的连接。
func (a *MyService) refreshExitMembers() {
	instances := snapshotExitInstances()
	if len(instances) == 0 {
		return
	}

	computed := make(map[string][]exitMember, len(instances))
	aliases := make(map[string]string, len(instances))
	a.mu.RLock()
	for id, inst := range instances {
		computed[id] = a.exitCandidatesLocked(inst.exitIP)
	}
	for i := range a.config.ExitPorts {
		aliases[a.config.ExitPorts[i].ID] = a.config.ExitPorts[i].Alias
	}
	a.mu.RUnlock()

	for id, inst := range instances {
		members := computed[id]
		inst.setMembers(members)
		a.logExitMembershipChange(aliases[id], inst, members)

		valid := make(map[int]bool, len(members))
		for _, m := range members {
			valid[m.Port] = true
		}
		for _, port := range inst.fw.ActiveUpstreams() {
			if valid[port] {
				continue
			}
			if n := inst.fw.DropUpstream(port); n > 0 {
				a.log(fmt.Sprintf("[出口端口 %s] 端口 %d 的节点已不再承载出口 %s（IP 变更或已停止），断开其 %d 条连接",
					aliases[id], port, inst.exitIP, n))
			}
		}
	}
}

// exitMembershipLogLimit 单轮加入/退出超过这个数就合并成一条汇总，
// 批量启动上千节点时逐条写会把日志面板刷爆。
const exitMembershipLogLimit = 10

// logExitMembershipChange 对比上一轮成员，输出节点自动加入/退出的日志。
// 用户不再手动挑选成员，这条日志是他们确认「节点确实被纳入了」的唯一途径。
func (a *MyService) logExitMembershipChange(alias string, inst *exitInstance, members []exitMember) {
	current := memberIDSet(members)
	prev := inst.lastIDs
	inst.lastIDs = current

	var joined, left []string
	for id, name := range current {
		if _, ok := prev[id]; !ok {
			joined = append(joined, name)
		}
	}
	for id, name := range prev {
		if _, ok := current[id]; !ok {
			left = append(left, name)
		}
	}
	if len(joined) == 0 && len(left) == 0 {
		return
	}
	sort.Strings(joined)
	sort.Strings(left)

	prefix := fmt.Sprintf("[出口端口 %s]", alias)
	if len(joined)+len(left) > exitMembershipLogLimit {
		a.log(fmt.Sprintf("%s 加入 %d 个、退出 %d 个节点，当前 %d 个节点承载出口 %s",
			prefix, len(joined), len(left), len(current), inst.exitIP))
		return
	}
	for _, name := range joined {
		a.log(fmt.Sprintf("%s 节点「%s」出口为 %s，已自动加入（当前 %d 个）", prefix, name, inst.exitIP, len(current)))
	}
	for _, name := range left {
		a.log(fmt.Sprintf("%s 节点「%s」已退出（IP 变更或已停止，当前 %d 个）", prefix, name, len(current)))
	}
}

// reprobeExitMembers 复查所有出口端口成员的真实出口 IP。
//
// 探到的 IP 变了就交给 handleRealIP：它会更新节点的 RealIP（绑定了出口 IP
// 的节点直接停用），随后候选重算把它摘掉并切断连接。
// 探测失败不处理：一次偶发失败不该影响正在承载流量的节点，真不通会由
// 转发器拨号失败与健康检测体现出来。
func (a *MyService) reprobeExitMembers() {
	type target struct {
		port   int
		exitIP string
	}
	var targets []target
	seen := make(map[int]bool)
	for _, inst := range snapshotExitInstances() {
		for _, m := range inst.snapshotMembers() {
			if !seen[m.Port] {
				seen[m.Port] = true
				targets = append(targets, target{port: m.Port, exitIP: inst.exitIP})
			}
		}
	}

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t target) {
			defer wg.Done()
			defer func() { <-sem }()
			ip, err := process.ProbeExitIP(t.port)
			if err != nil || sameExitIP(ip, t.exitIP) {
				return
			}
			a.log(fmt.Sprintf("[出口端口] 复查发现端口 %d 的节点出口 IP 已从 %s 变为 %s", t.port, t.exitIP, ip))
			a.handleRealIP(t.port, ip)
		}(t)
	}
	wg.Wait()
}

// broadcastExitStats 推送一轮出口端口统计。
func (a *MyService) broadcastExitStats(lastBytes map[string]relayByteSample, now time.Time) {
	instances := snapshotExitInstances()
	for id := range lastBytes {
		if _, still := instances[id]; !still {
			delete(lastBytes, id)
		}
	}
	if a.app == nil || len(instances) == 0 {
		return
	}

	for id, inst := range instances {
		stats := inst.fw.Stats()
		members := inst.snapshotMembers()

		var upSpeed, downSpeed float64
		if prev, ok := lastBytes[id]; ok {
			if seconds := now.Sub(prev.at).Seconds(); seconds > 0 {
				upSpeed = float64(stats.BytesUp-prev.up) / seconds
				downSpeed = float64(stats.BytesDown-prev.down) / seconds
			}
		}
		lastBytes[id] = relayByteSample{up: stats.BytesUp, down: stats.BytesDown, at: now}

		snap := models.ExitPortStats{
			ExitPortID:    id,
			MemberCount:   len(members),
			MemberAliases: memberAliases(members),
			ActiveConns:   stats.ActiveConns,
			TotalConns:    stats.TotalConns,
			RejectedConns: stats.RejectedConns,
			BytesUp:       stats.BytesUp,
			BytesDown:     stats.BytesDown,
			UpSpeed:       upSpeed,
			DownSpeed:     downSpeed,
		}
		if len(members) > 0 {
			snap.ActiveNodeID = members[0].RuleID
			snap.ActiveNodeAlias = members[0].Alias
		}
		a.app.Event.EmitEvent(&application.CustomEvent{Name: "exitPortStatsUpdate", Data: snap})
	}
}

// GetExitPorts 获取所有出口端口（附带实时统计与当前候选）。
func (a *MyService) GetExitPorts() []models.ExitPort {
	a.mu.RLock()
	items := make([]models.ExitPort, len(a.config.ExitPorts))
	copy(items, a.config.ExitPorts)
	a.mu.RUnlock()

	instances := snapshotExitInstances()
	for i := range items {
		inst, ok := instances[items[i].ID]
		if !ok {
			continue
		}
		stats := inst.fw.Stats()
		members := inst.snapshotMembers()
		items[i].MemberCount = len(members)
		items[i].MemberAliases = memberAliases(members)
		if len(members) > 0 {
			items[i].ActiveNodeID = members[0].RuleID
			items[i].ActiveNodeAlias = members[0].Alias
		}
		items[i].ActiveConns = stats.ActiveConns
		items[i].RejectedConns = stats.RejectedConns
		items[i].Traffic.TotalUp = stats.BytesUp
		items[i].Traffic.TotalDown = stats.BytesDown
	}
	return items
}

// AddExitPort 添加出口端口并立即开始监听。
//
// 用户只定义「出口 IP + 端口」，之后出口为该 IP 的节点启动后自动加入；
// 若还要再手动启动一次，没启动的出口端口不会接纳任何节点，用户会以为功能没生效。
// 启动失败（如端口被占）不算添加失败：配置已保存，原因记在 LastError，处理后可手动启动。
func (a *MyService) AddExitPort(ep models.ExitPort) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	item, err := a.addExitPortLocked(ep)
	if err != nil {
		return err
	}
	if err := a.runWithReleasedPortLocked(item.LocalPort, func() error { return a.startExitPortInternal(item) }); err != nil {
		item.LastError = err.Error()
		a.logError(fmt.Sprintf("启动出口端口 %s 失败", item.Alias), err)
	}
	_ = a.saveConfig()
	a.emitEvent("loadRules", nil)
	return nil
}

func (a *MyService) addExitPortLocked(ep models.ExitPort) (*models.ExitPort, error) {
	if err := ep.Validate(); err != nil {
		return nil, err
	}

	ep.ID = fmt.Sprintf("exit_%d", time.Now().UnixNano())
	ep.ResetRuntimeState()

	if ep.LocalPort > 0 {
		if err := a.claimPortLocked("exitPort", ep.ID, ep.Alias, ep.LocalPort); err != nil {
			return nil, err
		}
		if !a.reservePortLocked(ep.LocalPort) {
			a.releaseRegisteredPortLocked(ep.ID)
			return nil, fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", ep.LocalPort)
		}
	} else {
		ep.LocalPort = a.allocateLocalPort()
	}
	if ep.LocalPort == 0 {
		return nil, fmt.Errorf("没有可用的本地端口")
	}

	ep.GroupName = a.groupNameLocked(ep.GroupID)
	a.config.ExitPorts = append(a.config.ExitPorts, ep)

	if err := a.saveConfig(); err != nil {
		return nil, err
	}
	a.log(fmt.Sprintf("添加出口端口: %s（端口 %d → 出口 %s）", ep.Alias, ep.LocalPort, ep.ExitIP))
	return &a.config.ExitPorts[len(a.config.ExitPorts)-1], nil
}

// UpdateExitPort 更新出口端口。运行中的实例会按新配置自动重启。
func (a *MyService) UpdateExitPort(ep models.ExitPort) error {
	if err := ep.Validate(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.ExitPorts {
		existing := &a.config.ExitPorts[i]
		if existing.ID != ep.ID {
			continue
		}

		oldPort := existing.LocalPort
		wasRunning := existing.Enabled
		if ep.LocalPort != oldPort {
			if ep.LocalPort <= 0 {
				return fmt.Errorf("本地端口 %d 无效", ep.LocalPort)
			}
			if err := a.claimPortLocked("exitPort", ep.ID, ep.Alias, ep.LocalPort); err != nil {
				return err
			}
			if !a.reservePortLocked(ep.LocalPort) {
				_ = a.claimPortLocked("exitPort", ep.ID, existing.Alias, oldPort)
				return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", ep.LocalPort)
			}
		}

		stopExitInstance(ep.ID)
		if ep.LocalPort != oldPort {
			a.releasePortReservationLocked(oldPort)
		} else if wasRunning {
			a.reservePortLocked(oldPort)
		}

		updated := models.ExitPort{
			ID:            ep.ID,
			Alias:         ep.Alias,
			ExitIP:        ep.ExitIP,
			LocalPort:     ep.LocalPort,
			GroupID:       ep.GroupID,
			GroupName:     a.groupNameLocked(ep.GroupID),
			Remark:        ep.Remark,
			Traffic:       existing.Traffic,
			LastStartTime: existing.LastStartTime,
			LastStopTime:  existing.LastStopTime,
		}
		a.config.ExitPorts[i] = updated
		item := &a.config.ExitPorts[i]

		// 进程内转发，重启只是重新监听，代价极小：保持用户「编辑后仍在运行」的预期
		if wasRunning {
			if err := a.runWithReleasedPortLocked(item.LocalPort, func() error { return a.startExitPortInternal(item) }); err != nil {
				item.LastError = err.Error()
			}
		}

		if err := a.saveConfig(); err != nil {
			return err
		}
		a.log(fmt.Sprintf("更新出口端口: %s", item.Alias))
		return nil
	}

	return fmt.Errorf("出口端口不存在")
}

// DeleteExitPort 删除出口端口。
func (a *MyService) DeleteExitPort(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, ep := range a.config.ExitPorts {
		if ep.ID != id {
			continue
		}
		stopExitInstance(id)
		a.releasePortReservationLocked(ep.LocalPort)
		a.releaseRegisteredPortLocked(id)
		a.config.ExitPorts = append(a.config.ExitPorts[:i], a.config.ExitPorts[i+1:]...)
		a.log(fmt.Sprintf("删除出口端口: %s", ep.Alias))
		return a.saveConfig()
	}

	return fmt.Errorf("出口端口不存在")
}

// StartExitPort 启动出口端口。
func (a *MyService) StartExitPort(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.ExitPorts {
		ep := &a.config.ExitPorts[i]
		if ep.ID != id {
			continue
		}
		if ep.Enabled {
			return fmt.Errorf("出口端口已在运行")
		}

		if err := a.runWithReleasedPortLocked(ep.LocalPort, func() error {
			return a.startExitPortInternal(ep)
		}); err != nil {
			ep.Enabled = false
			ep.LastError = err.Error()
			_ = a.saveConfig()
			a.emitEvent("loadRules", nil)
			return err
		}

		return a.saveConfig()
	}

	return fmt.Errorf("出口端口不存在")
}

// startExitPortInternal 启动出口端口实例（内部方法，需已持有 a.mu）。
func (a *MyService) startExitPortInternal(ep *models.ExitPort) error {
	inst := &exitInstance{exitIP: ep.ExitIP}
	// 首批候选在锁内直接算好：不等 exitPortLoop 下一轮，启动即可用
	inst.members = a.exitCandidatesLocked(ep.ExitIP)
	// 启动时已有的成员由下方启动日志汇总，不再逐个报「已加入」
	inst.lastIDs = memberIDSet(inst.members)

	alias := ep.Alias
	inst.fw = exitport.New(exitport.Config{
		ListenAddr: fmt.Sprintf("127.0.0.1:%d", ep.LocalPort),
		Candidates: inst.candidatePorts,
		Logf:       a.throttledExitLogf(alias),
	})
	if err := inst.fw.Start(); err != nil {
		return err
	}

	exitMu.Lock()
	runningExits[ep.ID] = inst
	exitMu.Unlock()

	ep.Enabled = true
	ep.LastError = ""
	ep.LastStartTime = time.Now().Format("2006-01-02 15:04:05")

	status := fmt.Sprintf("当前 %d 个节点承载该出口", len(inst.members))
	if len(inst.members) == 0 {
		status = "当前没有节点承载该出口，连接将被拒绝，直到有节点探测到该 IP"
	}
	a.log(fmt.Sprintf("出口端口已启动: %s（127.0.0.1:%d → 出口 %s，%s）", ep.Alias, ep.LocalPort, ep.ExitIP, status))
	return nil
}

// throttledExitLogf 转发器日志限流：没有候选时每条连接都会被拒绝，
// 客户端重试起来一秒几十次，逐条写日志会把日志面板刷爆。
func (a *MyService) throttledExitLogf(alias string) func(string) {
	var mu sync.Mutex
	var last time.Time
	var suppressed int
	return func(msg string) {
		mu.Lock()
		now := time.Now()
		if now.Sub(last) < 10*time.Second {
			suppressed++
			mu.Unlock()
			return
		}
		extra := ""
		if suppressed > 0 {
			extra = fmt.Sprintf("（期间另有 %d 条同类日志已省略）", suppressed)
		}
		last, suppressed = now, 0
		mu.Unlock()
		a.log(fmt.Sprintf("[出口端口 %s] %s%s", alias, msg, extra))
	}
}

// StopExitPort 停止出口端口。
func (a *MyService) StopExitPort(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.ExitPorts {
		ep := &a.config.ExitPorts[i]
		if ep.ID != id {
			continue
		}
		if !ep.Enabled {
			return fmt.Errorf("出口端口未运行")
		}

		stopExitInstance(id)
		ep.Enabled = false
		ep.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
		a.reservePortLocked(ep.LocalPort)
		a.log(fmt.Sprintf("出口端口已停止: %s", ep.Alias))
		return a.saveConfig()
	}

	return fmt.Errorf("出口端口不存在")
}

// stopExitInstance 停止并移除出口端口实例；实例不存在时静默返回。
func stopExitInstance(id string) {
	exitMu.Lock()
	inst, ok := runningExits[id]
	delete(runningExits, id)
	exitMu.Unlock()

	if ok {
		_ = inst.fw.Stop()
	}
}

// stopAllExitPorts 停止全部出口端口实例（应用退出时调用）。
func stopAllExitPorts() {
	exitMu.Lock()
	instances := make([]*exitInstance, 0, len(runningExits))
	for id, inst := range runningExits {
		instances = append(instances, inst)
		delete(runningExits, id)
	}
	exitMu.Unlock()

	for _, inst := range instances {
		_ = inst.fw.Stop()
	}
}
