package main

import (
	"strings"
	"testing"

	"xray-manager/internal/models"
	"xray-manager/internal/process"
)

// newRelayTestService 构造一个带节点的服务实例。processManager 是真实的，
// 但测试里的节点都没有真进程，因此 IsRunning 恒为 false——这正好用于验证
// "前置节点未启动"的分支。
func newRelayTestService(cfg *models.Config) *MyService {
	return &MyService{
		config:         cfg,
		processManager: process.NewManager(func(string) {}, func() {}),
	}
}

func TestResolveRelayPreProxyDirect(t *testing.T) {
	svc := newRelayTestService(&models.Config{})

	url, err := svc.resolveRelayPreProxyLocked("")
	if err != nil {
		t.Fatalf("直连不应报错: %v", err)
	}
	if url != "" {
		t.Errorf("直连应返回空 URL，实际 %q", url)
	}
}

// 跟随全局：全局未设置时等同于直连，而不是报错。
func TestResolveRelayPreProxyFollowGlobalUnset(t *testing.T) {
	svc := newRelayTestService(&models.Config{PreProxyNodeID: ""})

	url, err := svc.resolveRelayPreProxyLocked(models.FollowGlobalPreProxy)
	if err != nil {
		t.Fatalf("全局未设置时不应报错: %v", err)
	}
	if url != "" {
		t.Errorf("应返回空 URL（直连），实际 %q", url)
	}
}

// 跟随全局：全局指向的节点未启动时要报错，且错误信息要点明是"跟随全局"，
// 否则用户会困惑于自己明明没选那个节点。
func TestResolveRelayPreProxyFollowGlobalNodeStopped(t *testing.T) {
	svc := newRelayTestService(&models.Config{
		PreProxyNodeID: "rule_1",
		Rules: []models.ProxyRule{
			{ID: "rule_1", Alias: "香港中转", LocalPort: 10801, Enabled: false},
		},
	})

	_, err := svc.resolveRelayPreProxyLocked(models.FollowGlobalPreProxy)
	if err == nil {
		t.Fatal("前置节点未启动时应报错")
	}
	if !strings.Contains(err.Error(), "跟随全局前置代理") {
		t.Errorf("错误信息应说明是跟随全局导致的，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "香港中转") {
		t.Errorf("错误信息应包含节点别名，实际: %v", err)
	}
}

// 跟随全局：全局指向一个已不存在的节点时要报错而不是静默直连。
func TestResolveRelayPreProxyFollowGlobalMissingNode(t *testing.T) {
	svc := newRelayTestService(&models.Config{PreProxyNodeID: "rule_gone"})

	_, err := svc.resolveRelayPreProxyLocked(models.FollowGlobalPreProxy)
	if err == nil {
		t.Fatal("全局指向的节点不存在时应报错")
	}
	if !strings.Contains(err.Error(), "跟随全局前置代理") {
		t.Errorf("错误信息应说明是跟随全局导致的，实际: %v", err)
	}
}

// 显式指定的节点未启动时报错，且不应提到"跟随全局"。
func TestResolveRelayPreProxyExplicitNodeStopped(t *testing.T) {
	svc := newRelayTestService(&models.Config{
		Rules: []models.ProxyRule{
			{ID: "rule_1", Alias: "日本中转", LocalPort: 10802, Enabled: false},
		},
	})

	_, err := svc.resolveRelayPreProxyLocked("rule_1")
	if err == nil {
		t.Fatal("前置节点未启动时应报错")
	}
	if strings.Contains(err.Error(), "跟随全局") {
		t.Errorf("显式指定时不应提到跟随全局，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "日本中转") {
		t.Errorf("错误信息应包含节点别名，实际: %v", err)
	}
}

// 哨兵值必须被校验逻辑接受，否则新建/编辑会话代理时会被判为"节点不存在"。
func TestProxyResourceExistsAcceptsFollowGlobalSentinel(t *testing.T) {
	svc := newRelayTestService(&models.Config{})

	if !svc.proxyResourceExistsLocked(models.FollowGlobalPreProxy) {
		t.Error("哨兵值应被视为有效引用")
	}
	if svc.proxyResourceExistsLocked("rule_不存在") {
		t.Error("不存在的节点 ID 不应通过校验")
	}
}

// 删除某个节点时，跟随全局的会话代理不该被误清空——它并不直接引用该节点。
func TestClearRelayPreProxyRefKeepsFollowGlobal(t *testing.T) {
	svc := newRelayTestService(&models.Config{
		SessionRelays: []models.SessionRelay{
			{ID: "relay_1", Alias: "跟随全局的", PreProxyNodeID: models.FollowGlobalPreProxy},
			{ID: "relay_2", Alias: "显式指定的", PreProxyNodeID: "rule_1"},
		},
	})

	svc.clearRelayPreProxyRefLocked("rule_1")

	if got := svc.config.SessionRelays[0].PreProxyNodeID; got != models.FollowGlobalPreProxy {
		t.Errorf("跟随全局的会话代理不应被清空，实际 %q", got)
	}
	if got := svc.config.SessionRelays[1].PreProxyNodeID; got != "" {
		t.Errorf("显式引用已删除节点的应被清空，实际 %q", got)
	}
}
