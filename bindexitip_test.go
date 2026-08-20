package main

import (
	"strings"
	"testing"

	"xray-manager/internal/models"
	"xray-manager/internal/process"
)

// 出口 IP 比较要能容忍写法差异：探测服务返回的可能带空格或用
// IPv4-mapped 形式，字面量比较会把没变的 IP 误判成"变了"、误停好节点。
func TestSameExitIPIgnoresFormatting(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
		why  string
	}{
		{"1.2.3.4", "1.2.3.4", true, "完全相同"},
		{" 1.2.3.4 ", "1.2.3.4", true, "前后空格"},
		{"::ffff:1.2.3.4", "1.2.3.4", true, "IPv4-mapped 与点分写法等价"},
		{"1.2.3.4", "1.2.3.5", false, "确实是不同的 IP"},
		{"2001:db8::1", "1.2.3.4", false, "不同家族"},
		{"", "1.2.3.4", false, "空值不应视为相等"},
		{"garbage", "garbage", true, "都解析不出时退回字符串比较"},
		{"garbage", "1.2.3.4", false, "解析不出且不相等"},
	}
	for _, c := range cases {
		if got := sameExitIP(c.a, c.b); got != c.same {
			t.Errorf("sameExitIP(%q, %q) = %v，期望 %v（%s）", c.a, c.b, got, c.same, c.why)
		}
	}
}

func TestIsIPv4Addr(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"1.2.3.4", true},
		{" 1.2.3.4 ", true},
		{"::ffff:1.2.3.4", true}, // IPv4-mapped 本质仍是 IPv4
		{"2001:db8::1", false},
		{"", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		if got := isIPv4Addr(c.in); got != c.want {
			t.Errorf("isIPv4Addr(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// 绑定了出口 IP 的节点，探到的 IP 与绑定值不一致时必须被自动停用，
// 且失败原因里要带上实际探到的 IP——handleNodeFailed 会清空 RealIP，
// 不写进原因文案用户就无从知道漂到了哪个 IP。
func TestBoundNodeIsDisabledWhenExitIPChanges(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{{
			ID: "rule_1", Alias: "绑定节点", LocalPort: 11001,
			Enabled: true, BindExitIP: true, BoundExitIP: "1.2.3.4",
		}},
	})
	svc.processManager = process.NewManager(func(string) {}, func() {})

	svc.handleRealIP(11001, "5.6.7.8")

	got := svc.config.Rules[0]
	if got.Enabled {
		t.Error("出口 IP 变化后节点应被自动停用")
	}
	if !strings.Contains(got.LastError, "5.6.7.8") {
		t.Errorf("失败原因应包含实际探到的 IP，实际「%s」", got.LastError)
	}
	if !strings.Contains(got.LastError, "1.2.3.4") {
		t.Errorf("失败原因应包含绑定的 IP，实际「%s」", got.LastError)
	}
	if got.BoundExitIP != "1.2.3.4" {
		t.Errorf("绑定值不应被改动，实际「%s」", got.BoundExitIP)
	}
}

// 出口 IP 没变时不应误停节点
func TestBoundNodeStaysUpWhenExitIPUnchanged(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{{
			ID: "rule_1", Alias: "绑定节点", LocalPort: 11001,
			Enabled: true, BindExitIP: true, BoundExitIP: "1.2.3.4",
		}},
	})

	svc.handleRealIP(11001, "1.2.3.4")

	got := svc.config.Rules[0]
	if !got.Enabled {
		t.Error("出口 IP 未变化，节点不应被停用")
	}
	if got.RealIP != "1.2.3.4" {
		t.Errorf("真实 IP 应被回填，实际「%s」", got.RealIP)
	}
}

// 开了绑定但还没有基准值时，首次探到的 IP 应被记为基准
func TestBoundNodeLearnsFirstExitIP(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{{
			ID: "rule_1", Alias: "绑定节点", LocalPort: 11001,
			Enabled: true, BindExitIP: true,
		}},
	})

	svc.handleRealIP(11001, "1.2.3.4")

	got := svc.config.Rules[0]
	if got.BoundExitIP != "1.2.3.4" {
		t.Errorf("首次探测到的 IP 应被记为绑定基准，实际「%s」", got.BoundExitIP)
	}
	if !got.Enabled {
		t.Error("首次学习不应停用节点")
	}
}

// 没开绑定的节点行为必须与以前完全一致：IP 变了也不停用
func TestUnboundNodeIgnoresExitIPChange(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{{
			ID: "rule_1", Alias: "普通节点", LocalPort: 11001,
			Enabled: true, RealIP: "1.2.3.4",
		}},
	})

	svc.handleRealIP(11001, "5.6.7.8")

	got := svc.config.Rules[0]
	if !got.Enabled {
		t.Error("未开启绑定的节点不应因 IP 变化被停用")
	}
	if got.RealIP != "5.6.7.8" {
		t.Errorf("真实 IP 应正常刷新，实际「%s」", got.RealIP)
	}
}
