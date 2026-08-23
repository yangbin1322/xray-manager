package main

import (
	"fmt"
	"testing"

	"xray-manager/internal/models"
)

// 批量生成的订阅 ID 必须两两不同。
//
// 朴素写法 fmt.Sprintf("sub_%d", time.Now().UnixNano()) 在紧凑循环里会拿到同一个
// 时间戳（Windows 时钟粒度是毫秒级），一批订阅共用一个 ID 后会在订阅更新时
// 互相删除对方的节点。这里不去断言"朴素写法会撞车"——那依赖时钟粒度，
// 换台机器就 flaky；只钉住新写法的正确性。
func TestGenerateUniqueSubscriptionIDsAreDistinct(t *testing.T) {
	ids := generateUniqueSubscriptionIDs(nil, 50)
	if len(ids) != 50 {
		t.Fatalf("应生成 50 个 ID，实际 %d 个", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("ID 重复: %s", id)
		}
		seen[id] = true
	}
}

// 新生成的 ID 不能与已有订阅撞车
func TestGenerateUniqueSubscriptionIDsAvoidExisting(t *testing.T) {
	first := generateUniqueSubscriptionIDs(nil, 20)
	existing := make([]models.Subscription, 0, len(first))
	for _, id := range first {
		existing = append(existing, models.Subscription{ID: id})
	}

	second := generateUniqueSubscriptionIDs(existing, 20)
	all := map[string]bool{}
	for _, id := range first {
		all[id] = true
	}
	for _, id := range second {
		if all[id] {
			t.Fatalf("新 ID %s 与已有订阅撞车", id)
		}
		all[id] = true
	}
	if len(all) != 40 {
		t.Fatalf("两批合计应有 40 个不同 ID，实际 %d 个", len(all))
	}
}

func TestParseSubscriptionURLs(t *testing.T) {
	text := "https://a.example.com/sub\r\n" +
		"\n" +
		"# 这是注释\n" +
		"  https://b.example.com/sub  \n" +
		"https://a.example.com/sub\n" + // 重复
		"not-a-url\n"

	urls, errs := parseSubscriptionURLs(text)

	want := []string{"https://a.example.com/sub", "https://b.example.com/sub"}
	if len(urls) != len(want) {
		t.Fatalf("应解析出 %d 条链接，实际 %d 条: %v", len(want), len(urls), urls)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Errorf("第 %d 条应为 %s，实际 %s", i+1, want[i], urls[i])
		}
	}
	if len(errs) != 1 {
		t.Fatalf("应有 1 条无法识别的行，实际 %d 条: %v", len(errs), errs)
	}
}

func TestNextSubscriptionSeq(t *testing.T) {
	subs := []models.Subscription{
		{Name: "机场合集-1"},
		{Name: "机场合集-3"}, // 中间那条被删过
		{Name: "别的分组-9"},
		{Name: "机场合集"}, // 没有序号后缀，不参与
	}
	if got := nextSubscriptionSeq(subs, "机场合集"); got != 4 {
		t.Errorf("应从最大序号 3 之后接着编号，期望 4，实际 %d", got)
	}
	if got := nextSubscriptionSeq(nil, "新分组"); got != 1 {
		t.Errorf("空分组应从 1 开始，实际 %d", got)
	}
}

// 分组名里带正则元字符时不能误匹配或 panic
func TestNextSubscriptionSeqHandlesRegexMetaChars(t *testing.T) {
	subs := []models.Subscription{
		{Name: "机场(备用)-2"},
		{Name: "机场X备用Y-7"}, // 若没做转义，( ) 会被当成分组而误匹配
	}
	if got := nextSubscriptionSeq(subs, "机场(备用)"); got != 3 {
		t.Errorf("应只认字面量匹配的那条，期望 3，实际 %d", got)
	}
}

// 批量导入的多条订阅必须各有各的 ID：更新其中一条时，
// 其他订阅的节点必须原封不动。ID 撞车会在这里炸掉。
func TestBatchImportedSubscriptionsDoNotClobberEachOther(t *testing.T) {
	ids := generateUniqueSubscriptionIDs(nil, 2)
	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{
			{ID: ids[0], GroupID: "g1", Name: "机场合集-1"},
			{ID: ids[1], GroupID: "g1", Name: "机场合集-2"},
		},
		Groups: []models.Group{{ID: "g1", Name: "机场合集"}},
	})

	// 两条订阅各挂 2 个节点
	for i, subID := range ids {
		for j := 0; j < 2; j++ {
			n := vlessNode(fmt.Sprintf("节点%d-%d", i+1, j+1), fmt.Sprintf("/s%d/%d", i+1, j+1), "a.com")
			n.ID = fmt.Sprintf("rule_%d_%d", i, j)
			n.GroupID = "g1"
			n.SubscriptionID = subID
			svc.config.Rules = append(svc.config.Rules, n)
		}
	}

	// 只更新第一条订阅，内容与原来一致
	if err := svc.handleSubscriptionUpdate(ids[0], []models.ProxyRule{
		vlessNode("节点1-1", "/s1/1", "a.com"),
		vlessNode("节点1-2", "/s1/2", "a.com"),
	}); err != nil {
		t.Fatal(err)
	}

	if got := len(svc.config.Rules); got != 4 {
		t.Fatalf("更新第一条订阅不该动到第二条的节点，期望 4 个，实际 %d 个", got)
	}
	remaining := 0
	for i := range svc.config.Rules {
		if svc.config.Rules[i].SubscriptionID == ids[1] {
			remaining++
		}
	}
	if remaining != 2 {
		t.Errorf("第二条订阅的节点应仍为 2 个，实际 %d 个", remaining)
	}
}

// 两条订阅的节点写进配置后不能串味：各自的订阅 ID / 分组归属必须正确
func TestAttachSubscriptionRulesLockedKeepsSubscriptionsSeparate(t *testing.T) {
	svc := newTestService(&models.Config{})
	group := &models.Group{ID: "g1", Name: "机场合集"}

	subA := &models.Subscription{ID: "sub_a", Name: "机场合集-1", URL: "https://a.example.com"}
	subB := &models.Subscription{ID: "sub_b", Name: "机场合集-2", URL: "https://b.example.com"}

	svc.attachSubscriptionRulesLocked(subA, group,
		[]models.ProxyRule{vlessNode("A1", "/a1", "a.com"), vlessNode("A2", "/a2", "a.com")},
		subA.URL, []string{"r1", "r2"}, []int{11001, 11002})
	svc.attachSubscriptionRulesLocked(subB, group,
		[]models.ProxyRule{vlessNode("B1", "/b1", "b.com")},
		subB.URL, []string{"r3"}, []int{11003})

	if len(svc.config.Rules) != 3 {
		t.Fatalf("应写入 3 个节点，实际 %d 个", len(svc.config.Rules))
	}
	if len(svc.config.Subscriptions) != 2 {
		t.Fatalf("应追加 2 条订阅，实际 %d 条", len(svc.config.Subscriptions))
	}

	bySub := map[string]int{}
	for i := range svc.config.Rules {
		r := &svc.config.Rules[i]
		bySub[r.SubscriptionID]++
		if r.GroupID != "g1" || r.GroupName != "机场合集" {
			t.Errorf("节点 %s 的分组归属不对: %s/%s", r.ID, r.GroupID, r.GroupName)
		}
		if r.Source != "subscription" {
			t.Errorf("节点 %s 的来源应为 subscription，实际 %s", r.ID, r.Source)
		}
		if r.Enabled {
			t.Errorf("新导入的节点不该是启用状态: %s", r.ID)
		}
	}
	if bySub["sub_a"] != 2 || bySub["sub_b"] != 1 {
		t.Errorf("节点与订阅的归属串了: %v", bySub)
	}
	// 各自的订阅链接要跟着自己那条订阅
	for i := range svc.config.Rules {
		r := &svc.config.Rules[i]
		want := "https://a.example.com"
		if r.SubscriptionID == "sub_b" {
			want = "https://b.example.com"
		}
		if r.SubscriptionURL != want {
			t.Errorf("节点 %s 的订阅链接应为 %s，实际 %s", r.ID, want, r.SubscriptionURL)
		}
	}
}

// 端口不够时多出来的节点端口为 0，不能 panic 或错位
func TestAttachSubscriptionRulesLockedToleratesPortShortage(t *testing.T) {
	svc := newTestService(&models.Config{})
	group := &models.Group{ID: "g1", Name: "分组"}
	sub := &models.Subscription{ID: "sub_a", Name: "分组-1"}

	svc.attachSubscriptionRulesLocked(sub, group,
		[]models.ProxyRule{vlessNode("A", "/a", "a.com"), vlessNode("B", "/b", "b.com")},
		"https://a.example.com", []string{"r1", "r2"}, []int{11001}) // 只给 1 个端口

	if len(svc.config.Rules) != 2 {
		t.Fatalf("端口不足也应写入全部节点，实际 %d 个", len(svc.config.Rules))
	}
	if svc.config.Rules[0].LocalPort != 11001 {
		t.Errorf("第一个节点应拿到端口 11001，实际 %d", svc.config.Rules[0].LocalPort)
	}
	if svc.config.Rules[1].LocalPort != 0 {
		t.Errorf("端口不足时第二个节点应为 0，实际 %d", svc.config.Rules[1].LocalPort)
	}
}

// 参数校验：两个分组参数都空时必须报错，不能悄悄各建一个分组
func TestImportSubscriptionsRequiresGroup(t *testing.T) {
	svc := newTestService(&models.Config{})
	_, err := svc.ImportSubscriptions("https://a.example.com/sub", "", "", false, 6, "direct", "")
	if err == nil {
		t.Fatal("未指定分组时应报错")
	}
}

// 一条有效链接都没有时直接报错，且不该建分组
func TestImportSubscriptionsRejectsEmptyInput(t *testing.T) {
	svc := newTestService(&models.Config{})
	res, err := svc.ImportSubscriptions("# 全是注释\n\nnot-a-url\n", "", "新分组", false, 6, "direct", "")
	if err == nil {
		t.Fatal("没有有效链接时应报错")
	}
	if res.FailCount != 1 {
		t.Errorf("应记录 1 条无法识别的行，实际 %d", res.FailCount)
	}
	if len(svc.config.Groups) != 0 {
		t.Errorf("没有有效链接时不该建分组，实际建了 %d 个", len(svc.config.Groups))
	}
}
