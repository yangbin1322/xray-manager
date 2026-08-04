package healthcheck

import (
	"net"
	"testing"
	"time"
	"xray-manager/internal/models"
)

// 用户实际遇到的节点：域名只有 AAAA 记录（机场的 "V6" 线路），
// 本机没有 IPv6 出口时，Windows 的 getaddrinfo 会报 NXDOMAIN，
// 旧实现据此判定 dns_failed——但节点其实能正常启动使用。
func TestIPv6OnlyDomainIsNotDNSFailure(t *testing.T) {
	rule := &models.ProxyRule{
		ID: "r1", Alias: "日本01|V6", Protocol: "trojan",
		ServerAddr: "ptxlv6-01.yunjnet.com", ServerPort: 54261,
		Settings: models.ProxySettings{
			TrojanPassword: "x", Security: "tls",
			TLS: &models.TLSSettings{ServerName: "www.bing.com", AllowInsecure: true},
		},
	}

	res := CheckRule(rule, models.HealthCheckConfig{TimeoutSec: 5})
	t.Logf("检测结果: status=%s error=%s", res.Status, res.Error)

	if res.Status == StatusDNSFailed {
		t.Fatal("仅有 IPv6 地址的节点不应被判为 DNS 失败——节点实际可用")
	}
	// 本机无 IPv6 出口时应给出明确的 ipv6_only 提示；
	// 若本机恰好有 IPv6，则应能正常连通
	switch res.Status {
	case StatusIPv6Only, StatusOnline, StatusHighLatency, StatusTimeout, StatusTLSFailed:
		// 都是合理结果，关键是不能是 dns_failed
	default:
		t.Fatalf("未预期的状态: %s", res.Status)
	}
}

// 复核逻辑本身：纯 AAAA 域名应被识别为 IPv6-only
func TestVerifyDomainDetectsIPv6Only(t *testing.T) {
	// ipv6.google.com 是经典的纯 AAAA 域名
	got := verifyDomain("ipv6.google.com", 5*time.Second)
	t.Logf("ipv6.google.com 复核结果: %v", got)
	if got == domainNotFound {
		t.Fatal("纯 AAAA 域名不应被判为域名不存在")
	}
}

// 真正不存在的域名仍应被认定为不存在
func TestVerifyDomainDetectsNotFound(t *testing.T) {
	got := verifyDomain("no-such-host-xyz-98765.invalid", 5*time.Second)
	if got != domainNotFound {
		t.Fatalf("不存在的域名应判为 domainNotFound，实际 %v", got)
	}
}

// 正常双栈/IPv4 域名不该被误判
func TestVerifyDomainNormalDomain(t *testing.T) {
	got := verifyDomain("www.cloudflare.com", 5*time.Second)
	if got == domainNotFound {
		t.Fatal("正常域名不应被判为不存在")
	}
	_ = net.ParseIP
}
