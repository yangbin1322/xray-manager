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
