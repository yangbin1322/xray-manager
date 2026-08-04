package main

import (
	"encoding/json"
	"strings"
	"testing"
	"xray-manager/internal/models"
)

// newTestService 构造一个只带配置、不依赖 Wails/进程管理器的服务实例，
// 用于测试纯粹的配置层判定逻辑。
func newTestService(cfg *models.Config) *MyService {
	return &MyService{config: cfg}
}

// 一个分组可以关联多个订阅。删订阅、刷新订阅时必须能区分"这个分组是不是我独占的"，
// 否则会误删/误接管同组其他订阅的节点。
func TestGroupExclusiveToSubscription(t *testing.T) {
	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{
			{ID: "sub_a", GroupID: "group_1"},
			{ID: "sub_b", GroupID: "group_1"},
			{ID: "sub_c", GroupID: "group_2"},
		},
	})

	if svc.groupExclusiveToSubscriptionLocked("group_1", "sub_a") {
		t.Fatal("group_1 同时被 sub_b 使用，不应判为 sub_a 独占")
	}
	if !svc.groupExclusiveToSubscriptionLocked("group_2", "sub_c") {
		t.Fatal("group_2 只被 sub_c 使用，应判为独占")
	}
}

// 删订阅时分组只能在彻底空了之后才删掉，否则会连累同组其他订阅
func TestGroupRemovableOnlyWhenEmpty(t *testing.T) {
	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_b", GroupID: "group_1"}},
	})
	if svc.groupRemovableLocked("group_1") {
		t.Fatal("仍有订阅使用该分组时不应删除")
	}

	// 没有订阅但仍有节点（例如手动添加的）：不可删
	svc = newTestService(&models.Config{
		Rules: []models.ProxyRule{{ID: "r1", GroupID: "group_1"}},
	})
	if svc.groupRemovableLocked("group_1") {
		t.Fatal("分组内仍有节点时不应删除")
	}

	svc = newTestService(&models.Config{})
	if !svc.groupRemovableLocked("group_1") {
		t.Fatal("无订阅无节点时应可删除")
	}
}

func TestFindGroupLocked(t *testing.T) {
	svc := newTestService(&models.Config{
		Groups: []models.Group{{ID: "g1", Name: "分组一"}, {ID: "g2", Name: "分组二"}},
	})

	if g := svc.findGroupLocked("g2"); g == nil || g.Name != "分组二" {
		t.Fatalf("应找到 g2，实际 %+v", g)
	}
	if g := svc.findGroupLocked("nope"); g != nil {
		t.Fatalf("不存在的分组应返回 nil，实际 %+v", g)
	}
}

// GetGroups 必须直读 config：groupManager 的缓存只在启动/导入配置时同步一次，
// 用它会让前端读到过期数据。
func TestGetGroupsReadsFromConfig(t *testing.T) {
	svc := newTestService(&models.Config{
		Groups: []models.Group{{ID: "g1", Name: "分组一"}, {ID: "g2", Name: "分组二"}},
	})

	groups := svc.GetGroups()
	if len(groups) != 2 {
		t.Fatalf("应返回 2 个分组，实际 %d", len(groups))
	}
}

// 返回的必须是副本，调用方改动不能污染内部配置
func TestGetGroupsReturnsCopy(t *testing.T) {
	svc := newTestService(&models.Config{
		Groups: []models.Group{{ID: "g1", Name: "原名"}},
	})

	groups := svc.GetGroups()
	groups[0].Name = "被外部改掉了"

	if svc.config.Groups[0].Name != "原名" {
		t.Fatal("GetGroups 返回的切片元素被外部修改后影响了内部配置")
	}
}

// 旧配置里残留的共享端口字段（sharedPortEnabled/sharedPort 等）必须被安全忽略，
// 不能让整份配置解析失败——用户升级后不该丢配置。
func TestLegacySharedPortFieldsAreIgnored(t *testing.T) {
	raw := []byte(`{
		"groups":[{"id":"g1","name":"aa","sharedPortEnabled":true,"sharedPort":19001,
		           "sharedPassword":"pw","sharedPortRunning":true,"sharedNodeCount":8}],
		"rules":[{"id":"r1","alias":"节点","sharedUsername":"hk01","localPort":10800}]
	}`)

	var cfg models.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("含遗留共享端口字段的旧配置应能正常解析: %v", err)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].Name != "aa" {
		t.Fatalf("分组解析结果不对: %+v", cfg.Groups)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0].LocalPort != 10800 {
		t.Fatalf("节点解析结果不对: %+v", cfg.Rules)
	}

	// 重新序列化后不应再写出这些字段
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"sharedPortEnabled", "sharedPort", "sharedUsername"} {
		if strings.Contains(string(out), gone) {
			t.Fatalf("保存配置时不应再写出已移除的字段 %s", gone)
		}
	}
}
