package main

import (
	"encoding/json"
	"strings"
	"testing"
	"xray-manager/internal/group"
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

// 删除分组后重命名另一个分组，不能改到无关分组头上。
//
// 回归：groupManager 曾持有 config.Groups 的内部指针，而删除分组时用
// append 就地前移剩余元素，旧指针会串到别的分组上；之后 UpdateGroup 按 ID
// 改名就改到了无关分组身上，界面表现为左侧列表出现两个同名分组。
func TestUpdateGroupAfterDeleteDoesNotRenameOthers(t *testing.T) {
	svc := newTestService(&models.Config{
		Groups: []models.Group{
			{ID: "g1", Name: "免费节点"},
			{ID: "g2", Name: "宝可梦"},
			{ID: "g3", Name: "跨境隔离"},
		},
	})
	svc.groupManager = group.NewManager(nil)
	svc.groupManager.LoadGroups(svc.config.Groups)

	if err := svc.DeleteGroup("g1"); err != nil {
		t.Fatalf("删除分组失败: %v", err)
	}
	// 改 g2 的名字
	if err := svc.UpdateGroup("g2", "宝可梦-新", ""); err != nil {
		t.Fatalf("重命名失败: %v", err)
	}

	byID := map[string]string{}
	for _, g := range svc.config.Groups {
		byID[g.ID] = g.Name
	}
	if byID["g2"] != "宝可梦-新" {
		t.Errorf("g2 应改名为「宝可梦-新」，实际「%s」", byID["g2"])
	}
	if byID["g3"] != "跨境隔离" {
		t.Errorf("g3 的名字被串改了，应为「跨境隔离」，实际「%s」", byID["g3"])
	}

	// 同时检查 groupManager 的缓存：真正持有过期指针的是它，
	// 只断言 config 会漏掉"config 用重建切片规避了、但缓存仍然串位"的情况
	for _, id := range []string{"g2", "g3"} {
		cached, err := svc.groupManager.GetGroup(id)
		if err != nil {
			t.Fatalf("分组 %s 应仍在缓存中: %v", id, err)
		}
		if cached.Name != byID[id] {
			t.Errorf("分组 %s 的缓存名「%s」与配置里的「%s」不一致", id, cached.Name, byID[id])
		}
	}

	// 同样不能出现重名——这正是用户看到的现象
	seen := map[string]string{}
	for id, name := range byID {
		if prev, dup := seen[name]; dup {
			t.Errorf("分组 %s 与 %s 重名「%s」", id, prev, name)
		}
		seen[name] = id
	}
}

// 存量配置里被串改的分组名，启动时应按订阅名回正。
// 指针问题修掉后，已经写进配置的错误名字不会自愈，需要主动修复一次。
func TestRepairSubscriptionGroupNames(t *testing.T) {
	svc := newTestService(&models.Config{
		Groups: []models.Group{
			// g2 的名字被串改成了和 g3 一样（用户看到的两个「免费节点」）
			{ID: "g2", Name: "免费节点", Source: "subscription"},
			{ID: "g3", Name: "免费节点", Source: "subscription"},
			{ID: "g4", Name: "我自己起的名字", Source: "manual"},
		},
		Subscriptions: []models.Subscription{
			{ID: "sub_2", GroupID: "g2", Name: "宝可梦"},
			{ID: "sub_3", GroupID: "g3", Name: "免费节点"},
		},
		Rules: []models.ProxyRule{
			{ID: "r1", GroupID: "g2", GroupName: "免费节点"},
		},
	})
	svc.groupManager = group.NewManager(nil)
	svc.groupManager.LoadGroups(svc.config.Groups)

	svc.repairSubscriptionGroupNamesLocked()

	byID := map[string]string{}
	for _, g := range svc.config.Groups {
		byID[g.ID] = g.Name
	}
	if byID["g2"] != "宝可梦" {
		t.Errorf("g2 应按订阅名回正为「宝可梦」，实际「%s」", byID["g2"])
	}
	if byID["g3"] != "免费节点" {
		t.Errorf("g3 名字本来就对，不应改动，实际「%s」", byID["g3"])
	}
	if byID["g4"] != "我自己起的名字" {
		t.Errorf("手动分组不该被订阅名覆盖，实际「%s」", byID["g4"])
	}
	// 节点上冗余的分组名也要跟着回正
	if svc.config.Rules[0].GroupName != "宝可梦" {
		t.Errorf("节点的 groupName 应同步回正，实际「%s」", svc.config.Rules[0].GroupName)
	}
}

// 多个订阅共用的分组，名字是用户自己起的，不能被某个订阅名覆盖
func TestRepairSkipsSharedSubscriptionGroups(t *testing.T) {
	svc := newTestService(&models.Config{
		Groups: []models.Group{{ID: "g1", Name: "我汇总的分组", Source: "subscription"}},
		Subscriptions: []models.Subscription{
			{ID: "sub_a", GroupID: "g1", Name: "订阅A"},
			{ID: "sub_b", GroupID: "g1", Name: "订阅B"},
		},
	})
	svc.groupManager = group.NewManager(nil)
	svc.groupManager.LoadGroups(svc.config.Groups)

	svc.repairSubscriptionGroupNamesLocked()

	if svc.config.Groups[0].Name != "我汇总的分组" {
		t.Errorf("多订阅共用的分组名不应被覆盖，实际「%s」", svc.config.Groups[0].Name)
	}
}
