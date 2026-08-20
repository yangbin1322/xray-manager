package main

import (
	"testing"
	"xray-manager/internal/models"
)

// vlessNode 构造一个走 CDN 的 vless 节点：多个节点共用同一个 IP:端口，
// 靠 ws path / SNI 区分——这正是机场常见的形态。
func vlessNode(alias, path, sni string) models.ProxyRule {
	return models.ProxyRule{
		Alias: alias, Protocol: "vless",
		ServerAddr: "108.162.198.30", ServerPort: 443,
		Source: "subscription",
		Settings: models.ProxySettings{
			VLessUserID: "adf8d585-7c01-40be-84f6-d4d2a31caa49",
			Network:     "ws", Security: "tls",
			WS:  &models.WSSettings{Path: path},
			TLS: &models.TLSSettings{ServerName: sni},
		},
	}
}

// 共用同一 IP:端口的不同节点必须有不同的标识。
// 修复前的 key 只有 serverAddr:serverPort，导致它们互相覆盖、每次更新重复添加。
func TestSubscriptionIdentityDistinguishesNodesBehindSameCDN(t *testing.T) {
	us1 := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1-us1.aiopen.sbs")
	us2 := vlessNode("🇺🇸美国2", "/kbjc/us2", "us2-us2.aiopen.sbs")
	jp1 := vlessNode("🇯🇵日本", "/kbjc/jp1", "jp1-jp1.aiopen.sbs")

	ids := map[string]string{}
	for _, n := range []models.ProxyRule{us1, us2, jp1} {
		key := n.SubscriptionIdentity()
		if prev, dup := ids[key]; dup {
			t.Fatalf("「%s」与「%s」标识相同，更新时会互相覆盖导致重复添加", n.Alias, prev)
		}
		ids[key] = n.Alias
	}
}

// 同一个节点在两次订阅拉取之间必须得到稳定的标识，否则每次更新都会被当成新节点
func TestSubscriptionIdentityIsStableAcrossFetches(t *testing.T) {
	first := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1-us1.aiopen.sbs")
	second := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1-us1.aiopen.sbs")

	if first.SubscriptionIdentity() != second.SubscriptionIdentity() {
		t.Fatal("同一节点两次拉取的标识应一致，否则会被反复当作新节点添加")
	}
}

// 节点参数变了（例如换了 UUID 或路径）应视为不同节点
func TestSubscriptionIdentityChangesWithCredentials(t *testing.T) {
	base := vlessNode("节点", "/path", "sni.example.com")

	changed := base
	changed.Settings.VLessUserID = "11111111-1111-1111-1111-111111111111"
	if base.SubscriptionIdentity() == changed.SubscriptionIdentity() {
		t.Fatal("UUID 不同应产生不同标识")
	}

	changed = base
	changed.Settings.WS = &models.WSSettings{Path: "/other"}
	if base.SubscriptionIdentity() == changed.SubscriptionIdentity() {
		t.Fatal("ws path 不同应产生不同标识")
	}
}

// 其他协议也要能正确区分
func TestSubscriptionIdentityCoversOtherProtocols(t *testing.T) {
	mk := func(proto string, mut func(*models.ProxySettings)) models.ProxyRule {
		r := models.ProxyRule{Alias: "n", Protocol: proto, ServerAddr: "a.com", ServerPort: 443}
		mut(&r.Settings)
		return r
	}

	cases := []struct {
		name string
		a, b models.ProxyRule
	}{
		{"trojan", mk("trojan", func(s *models.ProxySettings) { s.TrojanPassword = "p1" }),
			mk("trojan", func(s *models.ProxySettings) { s.TrojanPassword = "p2" })},
		{"shadowsocks", mk("shadowsocks", func(s *models.ProxySettings) { s.SSPassword = "p1" }),
			mk("shadowsocks", func(s *models.ProxySettings) { s.SSPassword = "p2" })},
		{"vmess", mk("vmess", func(s *models.ProxySettings) { s.VMessUserID = "u1" }),
			mk("vmess", func(s *models.ProxySettings) { s.VMessUserID = "u2" })},
		{"hysteria2", mk("hysteria2", func(s *models.ProxySettings) { s.Hy2Password = "p1" }),
			mk("hysteria2", func(s *models.ProxySettings) { s.Hy2Password = "p2" })},
	}
	for _, c := range cases {
		if c.a.SubscriptionIdentity() == c.b.SubscriptionIdentity() {
			t.Fatalf("%s: 凭证不同的节点应有不同标识", c.name)
		}
	}
}

// 核心回归：反复更新同一份订阅，节点数必须稳定，不能每次都翻倍增长。
// 这正是用户遇到的问题——26 次更新后 4 个节点变成了 104 条记录。
func TestRepeatedSubscriptionUpdateDoesNotAccumulate(t *testing.T) {
	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1", URL: "https://example.com/sub"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
	})

	// 订阅内容固定：4 个节点全部挂在同一个 CDN IP 上
	fetch := func() []models.ProxyRule {
		return []models.ProxyRule{
			vlessNode("🇺🇸美国1", "/kbjc/us1", "us1.aiopen.sbs"),
			vlessNode("🇺🇸美国2", "/kbjc/us2", "us2.aiopen.sbs"),
			vlessNode("🇯🇵日本", "/kbjc/jp1", "jp1.aiopen.sbs"),
			vlessNode("🇯🇵日本2", "/kbjc/jp2", "jp2.aiopen.sbs"),
		}
	}

	for round := 1; round <= 5; round++ {
		if err := svc.handleSubscriptionUpdate("sub_1", fetch()); err != nil {
			t.Fatalf("第 %d 次更新失败: %v", round, err)
		}
		if got := len(svc.config.Rules); got != 4 {
			t.Fatalf("第 %d 次更新后应为 4 个节点，实际 %d 个（重复累积了）", round, got)
		}
	}
}

// 已经攒下重复项的历史配置，更新一次后应自动收敛回正确数量
func TestSubscriptionUpdateCleansUpExistingDuplicates(t *testing.T) {
	// 模拟历史状态：同一个节点存了 5 份
	var existing []models.ProxyRule
	for i := 0; i < 5; i++ {
		n := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1.aiopen.sbs")
		n.ID = "rule_dup_" + string(rune('a'+i))
		n.GroupID = "g1"
		n.SubscriptionID = "sub_1"
		existing = append(existing, n)
	}

	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
		Rules:         existing,
	})

	if err := svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{
		vlessNode("🇺🇸美国1", "/kbjc/us1", "us1.aiopen.sbs"),
	}); err != nil {
		t.Fatal(err)
	}

	if got := len(svc.config.Rules); got != 1 {
		t.Fatalf("更新后应收敛为 1 个节点，实际 %d 个", got)
	}
}

// 订阅本身含重复条目时也不能写进配置
func TestSubscriptionUpdateDedupesWithinFeed(t *testing.T) {
	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
	})

	dup := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1.aiopen.sbs")
	if err := svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{dup, dup, dup}); err != nil {
		t.Fatal(err)
	}

	if got := len(svc.config.Rules); got != 1 {
		t.Fatalf("订阅内的重复条目应被去重，期望 1 个，实际 %d 个", got)
	}
}

// 订阅里真的新增/移除节点时，必须如实反映
func TestSubscriptionUpdateAddsAndRemovesNodes(t *testing.T) {
	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
	})

	svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{
		vlessNode("A", "/a", "a.com"),
		vlessNode("B", "/b", "b.com"),
	})
	if len(svc.config.Rules) != 2 {
		t.Fatalf("初始应有 2 个节点，实际 %d", len(svc.config.Rules))
	}

	// B 下线、C 上线
	svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{
		vlessNode("A", "/a", "a.com"),
		vlessNode("C", "/c", "c.com"),
	})
	if len(svc.config.Rules) != 2 {
		t.Fatalf("更新后仍应为 2 个节点，实际 %d", len(svc.config.Rules))
	}
	aliases := map[string]bool{}
	for _, r := range svc.config.Rules {
		aliases[r.Alias] = true
	}
	if !aliases["A"] || !aliases["C"] || aliases["B"] {
		t.Fatalf("应保留 A、新增 C、移除 B，实际 %v", aliases)
	}
}

// 机场改名（附上剩余流量/到期日、重排序号）后，节点必须被认出是同一个：
// ID、本地端口、备注、出口 IP 绑定都要留住，而不是删掉重加。
// 这是"订阅更新后备注不变、本地端口不变"的核心保证。
func TestSubscriptionUpdatePreservesRemarkAndPortOnRename(t *testing.T) {
	old := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1.aiopen.sbs")
	old.ID = "rule_keep"
	old.LocalPort = 11001
	old.GroupID = "g1"
	old.SubscriptionID = "sub_1"
	old.Remark = "公司专线"
	old.BindExitIP = true
	old.BoundExitIP = "1.2.3.4"

	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
		Rules:         []models.ProxyRule{old},
	})

	// 同一个节点，只有别名变了
	renamed := vlessNode("🇺🇸美国1 | 剩余 80GB", "/kbjc/us1", "us1.aiopen.sbs")
	if err := svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{renamed}); err != nil {
		t.Fatal(err)
	}

	if got := len(svc.config.Rules); got != 1 {
		t.Fatalf("改名不应产生新节点，期望 1 个，实际 %d 个", got)
	}
	got := svc.config.Rules[0]
	if got.ID != "rule_keep" {
		t.Errorf("节点 ID 应保持不变，期望 rule_keep，实际 %s", got.ID)
	}
	if got.LocalPort != 11001 {
		t.Errorf("本地端口应保持不变，期望 11001，实际 %d", got.LocalPort)
	}
	if got.Remark != "公司专线" {
		t.Errorf("备注应保持不变，期望「公司专线」，实际「%s」", got.Remark)
	}
	if !got.BindExitIP || got.BoundExitIP != "1.2.3.4" {
		t.Errorf("出口 IP 绑定应保持不变，实际 bind=%v ip=%s", got.BindExitIP, got.BoundExitIP)
	}
	if got.Alias != "🇺🇸美国1 | 剩余 80GB" {
		t.Errorf("别名应同步为订阅的新名，实际「%s」", got.Alias)
	}
}

// 无法区分时不能猜：两个旧节点连 path/SNI/凭证都相同、只靠别名区分，
// 双双改名后宁可退回删除+新增，也不能把 A 的备注挪到 B 上。
func TestSubscriptionUpdateDoesNotCarryOverAmbiguousRenames(t *testing.T) {
	// 两个节点各方面都一样，只有别名不同
	a := vlessNode("节点A", "/same", "same.com")
	a.ID = "rule_a"
	a.GroupID = "g1"
	a.SubscriptionID = "sub_1"
	a.Remark = "A 的备注"
	b := vlessNode("节点B", "/same", "same.com")
	b.ID = "rule_b"
	b.GroupID = "g1"
	b.SubscriptionID = "sub_1"
	b.Remark = "B 的备注"

	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
		Rules:         []models.ProxyRule{a, b},
	})

	// 两个都改名，此时无从判断谁是谁
	if err := svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{
		vlessNode("节点A-新", "/same", "same.com"),
		vlessNode("节点B-新", "/same", "same.com"),
	}); err != nil {
		t.Fatal(err)
	}

	// 关键断言：不能出现张冠李戴——带着旧备注却顶着另一个节点的新名字
	for _, r := range svc.config.Rules {
		if r.Alias == "节点A-新" && r.Remark == "B 的备注" {
			t.Fatal("把 B 的备注错配到了 A 上")
		}
		if r.Alias == "节点B-新" && r.Remark == "A 的备注" {
			t.Fatal("把 A 的备注错配到了 B 上")
		}
	}
}

// 改名与真正的新增/下线混在一次更新里，也要各归各位
func TestSubscriptionUpdateHandlesRenameAlongsideAddRemove(t *testing.T) {
	keep := vlessNode("保留", "/keep", "keep.com")
	keep.ID = "rule_keep"
	keep.LocalPort = 11001
	keep.GroupID = "g1"
	keep.SubscriptionID = "sub_1"
	keep.Remark = "要留住"

	gone := vlessNode("下线", "/gone", "gone.com")
	gone.ID = "rule_gone"
	gone.GroupID = "g1"
	gone.SubscriptionID = "sub_1"

	svc := newTestService(&models.Config{
		Subscriptions: []models.Subscription{{ID: "sub_1", GroupID: "g1"}},
		Groups:        []models.Group{{ID: "g1", Name: "机场"}},
		Rules:         []models.ProxyRule{keep, gone},
	})

	// 保留的改了名、下线的消失、另有一个全新节点
	if err := svc.handleSubscriptionUpdate("sub_1", []models.ProxyRule{
		vlessNode("保留 | 剩余 10GB", "/keep", "keep.com"),
		vlessNode("新增", "/new", "new.com"),
	}); err != nil {
		t.Fatal(err)
	}

	if got := len(svc.config.Rules); got != 2 {
		t.Fatalf("期望 2 个节点（改名保留 + 新增），实际 %d 个", got)
	}
	var renamed *models.ProxyRule
	aliases := map[string]bool{}
	for i := range svc.config.Rules {
		r := &svc.config.Rules[i]
		aliases[r.Alias] = true
		if r.ID == "rule_keep" {
			renamed = r
		}
	}
	if renamed == nil {
		t.Fatal("改名的节点应被认领回原记录，而不是删掉重建")
	}
	if renamed.LocalPort != 11001 || renamed.Remark != "要留住" {
		t.Errorf("改名节点的端口/备注应保留，实际 port=%d remark=%s", renamed.LocalPort, renamed.Remark)
	}
	if !aliases["新增"] {
		t.Error("全新节点应被加入")
	}
	if aliases["下线"] {
		t.Error("下线节点应被移除")
	}
}

// 无别名标识仍要能区分同 CDN 下的不同节点，否则二次匹配会把它们互相认错
func TestSubscriptionIdentityWithoutAliasDistinguishesCDNNodes(t *testing.T) {
	us1 := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1-us1.aiopen.sbs")
	us2 := vlessNode("🇺🇸美国2", "/kbjc/us2", "us2-us2.aiopen.sbs")
	jp1 := vlessNode("🇯🇵日本", "/kbjc/jp1", "jp1-jp1.aiopen.sbs")

	ids := map[string]string{}
	for _, n := range []models.ProxyRule{us1, us2, jp1} {
		key := n.SubscriptionIdentityWithoutAlias()
		if prev, dup := ids[key]; dup {
			t.Fatalf("「%s」与「%s」去别名标识相同，二次匹配时会互相认错", n.Alias, prev)
		}
		ids[key] = n.Alias
	}
}

// 用户自己填的字段不能进身份标识：一改备注就认不出节点，
// 下次更新会把它当成新节点重建，备注和端口反而丢得更彻底
func TestSubscriptionIdentityIgnoresUserFields(t *testing.T) {
	base := vlessNode("🇺🇸美国1", "/kbjc/us1", "us1.aiopen.sbs")
	changed := base
	changed.Remark = "我的备注"
	changed.BindExitIP = true
	changed.BoundExitIP = "1.2.3.4"

	if base.SubscriptionIdentity() != changed.SubscriptionIdentity() {
		t.Error("备注/出口 IP 绑定不应影响 SubscriptionIdentity")
	}
	if base.SubscriptionIdentityWithoutAlias() != changed.SubscriptionIdentityWithoutAlias() {
		t.Error("备注/出口 IP 绑定不应影响 SubscriptionIdentityWithoutAlias")
	}
}
