package process

import "testing"

func TestIsIPv4(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"1.2.3.4", true},
		{" 1.2.3.4 ", true},      // 探测服务的响应常带换行/空格
		{"::ffff:1.2.3.4", true}, // IPv4-mapped 本质仍是 IPv4
		{"2001:db8::1", false},
		{"240e:390:7ec:8d50::1", false},
		{"", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		if got := isIPv4(c.in); got != c.want {
			t.Errorf("isIPv4(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// 兜底探测点要以只有 A 记录的域名为主：双栈探测点会给双栈节点随机返回 IPv6，
// 让"出口 IP 有没有变"的判断失去意义。这条断言把意图固定下来，
// 将来若有人把这些域名换成双栈的，测试会立刻报出来。
func TestFallbackIPServicesPreferIPv4Hosts(t *testing.T) {
	ipv4Only := map[string]bool{
		"https://ipv4.icanhazip.com":    true,
		"https://api-ipv4.ip.sb/ip":     true,
		"https://checkip.amazonaws.com": true,
		"https://api.ipify.org":         true,
	}

	fallbacks := baseIPServices[1:] // 第 0 个是走响应头的首选项，另行处理
	got := 0
	for _, s := range fallbacks {
		if ipv4Only[s] {
			got++
		}
	}
	// 前 realIPMaxAttempts 次尝试里要有足够多的 IPv4-only 端点，
	// 否则双栈节点大概率还是先撞上双栈服务
	if got < 4 {
		t.Errorf("兜底探测点中仅有 %d 个是 IPv4-only，至少需要 4 个；当前列表 %v", got, fallbacks)
	}
}

// 探测服务数量不能少于最大尝试次数，否则重试轮不满就提前放弃，
// 可用节点会被误判为不通（与 TestRealIPProbeSettingsFavorAccuracy 同源的约束）
func TestIPServiceCountCoversMaxAttempts(t *testing.T) {
	if len(baseIPServices) < realIPMaxAttempts {
		t.Fatalf("探测服务只有 %d 个，少于最大尝试次数 %d", len(baseIPServices), realIPMaxAttempts)
	}
}
