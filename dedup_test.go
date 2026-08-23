package main

import (
	"encoding/json"
	"testing"

	"xray-manager/internal/models"
)

func dedupTestRule(id, alias, path string) models.ProxyRule {
	return models.ProxyRule{
		ID: id, Alias: alias, Protocol: "vless",
		ServerAddr: "108.162.198.30", ServerPort: 443,
		Settings: models.ProxySettings{
			VLessUserID: "adf8d585-7c01-40be-84f6-d4d2a31caa49",
			Network:     "ws", Security: "tls",
			WS:  &models.WSSettings{Path: path},
			TLS: &models.TLSSettings{ServerName: "a.example.com"},
		},
	}
}

// GetRules 要给每个节点带上判重键，前端的「选中重复节点」靠它分组
func TestGetRulesPopulatesDedupKey(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{
			dedupTestRule("r1", "香港01", "/a"),
			// 运行中的节点同样要有键，不能只给未启动的填
			func() models.ProxyRule { r := dedupTestRule("r2", "香港02", "/b"); r.Enabled = true; return r }(),
		},
	})

	got := svc.GetRules()
	for i := range got {
		if got[i].DedupKey == "" {
			t.Fatalf("节点 %s 没有判重键", got[i].ID)
		}
		if want := got[i].SubscriptionIdentityWithoutAlias(); got[i].DedupKey != want {
			t.Errorf("节点 %s 的判重键不等于去别名标识", got[i].ID)
		}
	}
}

// 判重键是每次读取现算的派生值，绝不能写回配置。
// 一旦泄漏进 a.config.Rules 就会被落盘，配置文件里每个节点凭空多一个字段。
func TestGetRulesDoesNotPersistDedupKey(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{dedupTestRule("r1", "香港01", "/a")},
	})

	_ = svc.GetRules()

	if svc.config.Rules[0].DedupKey != "" {
		t.Fatalf("判重键泄漏进了配置：%q", svc.config.Rules[0].DedupKey)
	}
	// 序列化一遍再确认：omitempty 应让它完全不出现
	raw, err := json.Marshal(svc.config.Rules[0])
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if _, exists := back["dedupKey"]; exists {
		t.Error("配置序列化后仍带 dedupKey 字段")
	}
}

// 别名不同但其余相同必须判为同一个节点——机场爱在名字里塞剩余流量和编号，
// 按别名判重等于判不出来
func TestDedupKeyIgnoresAlias(t *testing.T) {
	a := dedupTestRule("r1", "香港01", "/same")
	b := dedupTestRule("r2", "香港01 | 剩余 80GB", "/same")

	if a.SubscriptionIdentityWithoutAlias() != b.SubscriptionIdentityWithoutAlias() {
		t.Error("仅别名不同的两个节点应判为重复")
	}
}

// 同 CDN 同凭证下靠 ws path / SNI 区分的节点不能被误判为重复
func TestDedupKeyDistinguishesTransport(t *testing.T) {
	a := dedupTestRule("r1", "美国1", "/us1")
	b := dedupTestRule("r2", "美国2", "/us2")
	if a.SubscriptionIdentityWithoutAlias() == b.SubscriptionIdentityWithoutAlias() {
		t.Error("ws path 不同的节点不该判为重复")
	}

	c := dedupTestRule("r3", "美国3", "/us1")
	c.Settings.TLS = &models.TLSSettings{ServerName: "other.example.com"}
	if a.SubscriptionIdentityWithoutAlias() == c.SubscriptionIdentityWithoutAlias() {
		t.Error("SNI 不同的节点不该判为重复")
	}
}
