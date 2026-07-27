package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"xray-manager/internal/models"
	"xray-manager/internal/relay"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 动态会话代理（SessionRelay）的生命周期管理。
//
// 与其他代理类型不同，中继运行在本进程内而非独立的 xray/sing-box 进程，
// 因此不经过 processManager，改用 runningRelays 维护实例。

// runningRelays 保存已启动的中继实例，键为 SessionRelay.ID。
// 独立于 a.mu：中继的 Stop 会等待连接收尾，不应长时间持有主配置锁。
var (
	relayMu       sync.Mutex
	runningRelays = make(map[string]*relay.Relay)
)

// GetSessionRelays 获取所有动态会话代理（附带实时统计）。
func (a *MyService) GetSessionRelays() []models.SessionRelay {
	a.mu.RLock()
	relays := make([]models.SessionRelay, len(a.config.SessionRelays))
	copy(relays, a.config.SessionRelays)
	a.mu.RUnlock()

	for i := range relays {
		if stats, ok := relayStats(relays[i].ID); ok {
			relays[i].ActiveConns = stats.ActiveConns
			relays[i].TotalConns = stats.TotalConns
			relays[i].SessionCount = stats.SessionCount
			relays[i].Traffic.TotalUp = stats.BytesUp
			relays[i].Traffic.TotalDown = stats.BytesDown
		}
	}
	return relays
}

func relayStats(id string) (relay.Stats, bool) {
	relayMu.Lock()
	defer relayMu.Unlock()
	r, ok := runningRelays[id]
	if !ok {
		return relay.Stats{}, false
	}
	return r.Stats(), true
}

// relayStatsLoop 定期把运行中会话代理的统计推给前端。
//
// 会话代理不经 processManager，没有内核 API 可轮询，也就不会产生
// trafficUpdate 事件——若不主动推送，界面上的会话数/连接数会一直停在 0，
// 只有用户手动刷新才更新一次。
func (a *MyService) relayStatsLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// 上一轮的字节数，用于换算实时速度
	lastBytes := make(map[string]relayByteSample)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			a.broadcastRelayStats(lastBytes, now)
		}
	}
}

type relayByteSample struct {
	up, down int64
	at       time.Time
}

// broadcastRelayStats 推送一轮会话代理统计。
func (a *MyService) broadcastRelayStats(lastBytes map[string]relayByteSample, now time.Time) {
	if a.app == nil {
		return
	}

	relayMu.Lock()
	snapshot := make(map[string]relay.Stats, len(runningRelays))
	for id, instance := range runningRelays {
		snapshot[id] = instance.Stats()
	}
	relayMu.Unlock()

	// 已停止的实例清掉采样，避免重启后按陈旧基线算出巨大速度
	for id := range lastBytes {
		if _, still := snapshot[id]; !still {
			delete(lastBytes, id)
		}
	}

	if len(snapshot) == 0 {
		return
	}

	for id, stats := range snapshot {
		var upSpeed, downSpeed float64
		if prev, ok := lastBytes[id]; ok {
			if seconds := now.Sub(prev.at).Seconds(); seconds > 0 {
				upSpeed = float64(stats.BytesUp-prev.up) / seconds
				downSpeed = float64(stats.BytesDown-prev.down) / seconds
			}
		}
		lastBytes[id] = relayByteSample{up: stats.BytesUp, down: stats.BytesDown, at: now}

		a.app.Event.EmitEvent(&application.CustomEvent{
			Name: "relayStatsUpdate",
			Data: models.SessionRelayStats{
				RelayID:      id,
				ActiveConns:  stats.ActiveConns,
				TotalConns:   stats.TotalConns,
				FailedConns:  stats.FailedConns,
				SessionCount: stats.SessionCount,
				BytesUp:      stats.BytesUp,
				BytesDown:    stats.BytesDown,
				UpSpeed:      upSpeed,
				DownSpeed:    downSpeed,
			},
		})
	}
}

// AddSessionRelay 添加动态会话代理。
func (a *MyService) AddSessionRelay(sr models.SessionRelay) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := sr.Validate(); err != nil {
		return err
	}

	sr.ID = fmt.Sprintf("relay_%d", time.Now().UnixNano())
	sr.Enabled = false
	sr.LastError = ""

	if sr.LocalPort > 0 {
		if err := a.claimPortLocked("sessionRelay", sr.ID, sr.Alias, sr.LocalPort); err != nil {
			return err
		}
		if !a.reservePortLocked(sr.LocalPort) {
			a.releaseRegisteredPortLocked(sr.ID)
			return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", sr.LocalPort)
		}
	} else {
		sr.LocalPort = a.allocateLocalPort()
	}
	if sr.LocalPort == 0 {
		return fmt.Errorf("没有可用的本地端口")
	}

	if sr.PreProxyNodeID != "" && !a.proxyResourceExistsLocked(sr.PreProxyNodeID) {
		return fmt.Errorf("前置加速节点不存在: %s", sr.PreProxyNodeID)
	}

	sr.GroupName = a.groupNameLocked(sr.GroupID)
	a.config.SessionRelays = append(a.config.SessionRelays, sr)

	if err := a.saveConfig(); err != nil {
		return err
	}
	a.log(fmt.Sprintf("添加动态会话代理: %s（端口 %d → %s）", sr.Alias, sr.LocalPort, sr.UpstreamAddr))
	return nil
}

// UpdateSessionRelay 更新动态会话代理。运行中的实例会被停止，需重新启动生效。
func (a *MyService) UpdateSessionRelay(sr models.SessionRelay) error {
	if err := sr.Validate(); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.SessionRelays {
		existing := &a.config.SessionRelays[i]
		if existing.ID != sr.ID {
			continue
		}

		oldPort := existing.LocalPort
		if sr.LocalPort != oldPort {
			if sr.LocalPort <= 0 {
				return fmt.Errorf("本地端口 %d 无效", sr.LocalPort)
			}
			if err := a.claimPortLocked("sessionRelay", sr.ID, sr.Alias, sr.LocalPort); err != nil {
				return err
			}
			if !a.reservePortLocked(sr.LocalPort) {
				_ = a.claimPortLocked("sessionRelay", sr.ID, existing.Alias, oldPort)
				return fmt.Errorf("本地端口 %d 已被系统中的其他进程占用", sr.LocalPort)
			}
			a.releasePortReservationLocked(oldPort)
		}

		if sr.PreProxyNodeID != "" && !a.proxyResourceExistsLocked(sr.PreProxyNodeID) {
			return fmt.Errorf("前置加速节点不存在: %s", sr.PreProxyNodeID)
		}

		// 配置变更需要重建监听，停止后由用户重新启动
		stopRelayInstance(sr.ID)

		sr.Enabled = false
		sr.LastError = ""
		sr.Traffic = existing.Traffic
		sr.LastStartTime = existing.LastStartTime
		sr.LastStopTime = existing.LastStopTime
		sr.GroupName = a.groupNameLocked(sr.GroupID)

		a.config.SessionRelays[i] = sr
		if err := a.saveConfig(); err != nil {
			return err
		}
		a.log(fmt.Sprintf("更新动态会话代理: %s", sr.Alias))
		return nil
	}

	return fmt.Errorf("动态会话代理不存在")
}

// DeleteSessionRelay 删除动态会话代理。
func (a *MyService) DeleteSessionRelay(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i, sr := range a.config.SessionRelays {
		if sr.ID != id {
			continue
		}
		stopRelayInstance(id)
		a.releasePortReservationLocked(sr.LocalPort)
		a.releaseRegisteredPortLocked(id)
		a.config.SessionRelays = append(a.config.SessionRelays[:i], a.config.SessionRelays[i+1:]...)
		a.log(fmt.Sprintf("删除动态会话代理: %s", sr.Alias))
		return a.saveConfig()
	}

	return fmt.Errorf("动态会话代理不存在")
}

// StartSessionRelay 启动动态会话代理。
func (a *MyService) StartSessionRelay(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.ID != id {
			continue
		}
		if sr.Enabled {
			return fmt.Errorf("动态会话代理已在运行")
		}

		if err := a.runWithReleasedPortLocked(sr.LocalPort, func() error {
			return a.startSessionRelayInternal(sr)
		}); err != nil {
			sr.Enabled = false
			sr.LastError = err.Error()
			_ = a.saveConfig()
			a.app.Event.EmitEvent(&application.CustomEvent{Name: "loadRules"})
			return err
		}

		return a.saveConfig()
	}

	return fmt.Errorf("动态会话代理不存在")
}

// startSessionRelayInternal 启动中继实例（内部方法，需已持有 a.mu）。
func (a *MyService) startSessionRelayInternal(sr *models.SessionRelay) error {
	preProxyURL, err := a.resolveRelayPreProxyLocked(sr.PreProxyNodeID)
	if err != nil {
		return err
	}

	alias := sr.Alias
	instance := relay.New(relay.Config{
		ListenAddr:       fmt.Sprintf("127.0.0.1:%d", sr.LocalPort),
		UpstreamAddr:     sr.UpstreamAddr,
		UsernameTemplate: sr.UsernameTemplate,
		UpstreamPassword: sr.UpstreamPassword,
		LocalPassword:    sr.LocalPassword,
		PreProxyURL:      preProxyURL,
		Logf: func(msg string) {
			a.log(fmt.Sprintf("[会话代理 %s] %s", alias, msg))
		},
	})

	if err := instance.Start(); err != nil {
		return err
	}

	relayMu.Lock()
	runningRelays[sr.ID] = instance
	relayMu.Unlock()

	sr.Enabled = true
	sr.LastError = ""
	sr.LastStartTime = time.Now().Format("2006-01-02 15:04:05")

	via := "直连上游"
	if preProxyURL != "" {
		via = fmt.Sprintf("经前置代理 %s", preProxyURL)
	}
	a.log(fmt.Sprintf("动态会话代理已启动: %s（127.0.0.1:%d → %s，%s）", sr.Alias, sr.LocalPort, sr.UpstreamAddr, via))
	return nil
}

// StopSessionRelay 停止动态会话代理。
func (a *MyService) StopSessionRelay(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.ID != id {
			continue
		}
		if !sr.Enabled {
			return fmt.Errorf("动态会话代理未运行")
		}

		stopRelayInstance(id)
		sr.Enabled = false
		sr.LastStopTime = time.Now().Format("2006-01-02 15:04:05")
		a.reservePortLocked(sr.LocalPort)
		a.log(fmt.Sprintf("动态会话代理已停止: %s", sr.Alias))
		return a.saveConfig()
	}

	return fmt.Errorf("动态会话代理不存在")
}

// stopRelayInstance 停止并移除中继实例；实例不存在时静默返回。
func stopRelayInstance(id string) {
	relayMu.Lock()
	instance, ok := runningRelays[id]
	delete(runningRelays, id)
	relayMu.Unlock()

	if ok {
		_ = instance.Stop()
	}
}

// stopAllSessionRelays 停止全部中继实例（应用退出时调用）。
func stopAllSessionRelays() {
	relayMu.Lock()
	instances := make([]*relay.Relay, 0, len(runningRelays))
	for id, instance := range runningRelays {
		instances = append(instances, instance)
		delete(runningRelays, id)
	}
	relayMu.Unlock()

	for _, instance := range instances {
		_ = instance.Stop()
	}
}

// resolveRelayPreProxyLocked 把前置节点 ID 解析为中继可用的代理 URL。
// 前置节点必须已启动——中继只做 TCP 转发，不负责拉起内核进程。
func (a *MyService) resolveRelayPreProxyLocked(nodeID string) (string, error) {
	if nodeID == "" {
		return "", nil
	}

	// 跟随全局前置代理：每次启动时按当前全局设置解析，未设置则视为直连
	if nodeID == models.FollowGlobalPreProxy {
		globalID := a.config.PreProxyNodeID
		if globalID == "" {
			return "", nil
		}
		url, err := a.resolveRelayPreProxyLocked(globalID)
		if err != nil {
			return "", fmt.Errorf("跟随全局前置代理失败：%v", err)
		}
		return url, nil
	}

	for i := range a.config.Rules {
		rule := &a.config.Rules[i]
		if rule.ID != nodeID {
			continue
		}
		if !rule.Enabled || !a.processManager.IsRunning(rule.LocalPort) {
			return "", fmt.Errorf("前置加速节点「%s」未启动，请先启动该节点", rule.Alias)
		}
		return fmt.Sprintf("socks5://127.0.0.1:%d", rule.LocalPort), nil
	}

	for i := range a.config.ChainProxies {
		chain := &a.config.ChainProxies[i]
		if chain.ID != nodeID {
			continue
		}
		if !chain.Enabled || !a.processManager.IsRunning(chain.LocalPort) {
			return "", fmt.Errorf("前置加速链式代理「%s」未启动，请先启动", chain.Alias)
		}
		return fmt.Sprintf("socks5://127.0.0.1:%d", chain.LocalPort), nil
	}

	for i := range a.config.LoadBalancers {
		lb := &a.config.LoadBalancers[i]
		if lb.ID != nodeID {
			continue
		}
		if !lb.Enabled || !a.processManager.IsRunning(lb.LocalPort) {
			return "", fmt.Errorf("前置加速故障转移「%s」未启动，请先启动", lb.Alias)
		}
		return fmt.Sprintf("socks5://127.0.0.1:%d", lb.LocalPort), nil
	}

	return "", fmt.Errorf("前置加速节点不存在: %s", nodeID)
}

// proxyResourceExistsLocked 判断 ID 是否对应一个可作为前置代理的资源。
// 哨兵值「跟随全局」不指向具体资源，但始终有效——即便全局设置当前为空，
// 那只是意味着运行时按直连处理。
func (a *MyService) proxyResourceExistsLocked(id string) bool {
	if id == models.FollowGlobalPreProxy {
		return true
	}
	for i := range a.config.Rules {
		if a.config.Rules[i].ID == id {
			return true
		}
	}
	for i := range a.config.ChainProxies {
		if a.config.ChainProxies[i].ID == id {
			return true
		}
	}
	for i := range a.config.LoadBalancers {
		if a.config.LoadBalancers[i].ID == id {
			return true
		}
	}
	return false
}

// clearRelayPreProxyRefLocked 清除指向已删除资源的前置代理引用，
// 使这些中继降级为直连上游而不是在启动时报错。
func (a *MyService) clearRelayPreProxyRefLocked(deletedID string) {
	for i := range a.config.SessionRelays {
		sr := &a.config.SessionRelays[i]
		if sr.PreProxyNodeID != deletedID {
			continue
		}
		sr.PreProxyNodeID = ""
		a.log(fmt.Sprintf("动态会话代理「%s」的前置加速节点已删除，改为直连上游", sr.Alias))
	}
}

// groupNameLocked 按分组 ID 取分组名，未找到返回空串。
func (a *MyService) groupNameLocked(groupID string) string {
	if groupID == "" {
		return ""
	}
	for _, g := range a.config.Groups {
		if g.ID == groupID {
			return g.Name
		}
	}
	return ""
}
