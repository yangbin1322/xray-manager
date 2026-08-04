package healthcheck

import (
	"fmt"
	"net"
	"testing"
	"xray-manager/internal/models"
)

// 起一个本地 TCP 服务，用来验证"能连通就不该报 DNS 失败"
func listenLocal(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// localhost 有多条记录（127.0.0.1 和 ::1），且部分机器没有 IPv6 出口。
// 旧实现取 ips[0]，若首位是 ::1 就会连不上并误报；
// 新实现交给 Dialer 逐个尝试，必须成功。
func TestLocalhostWithMixedAddressFamilies(t *testing.T) {
	port := listenLocal(t)
	rule := &models.ProxyRule{
		ID: "r1", Alias: "本地", Protocol: "vmess",
		ServerAddr: "localhost", ServerPort: port,
	}

	res := CheckRule(rule, models.HealthCheckConfig{TimeoutSec: 3})
	if res.Status == StatusDNSFailed {
		t.Fatalf("localhost 可解析且可连通，不应报 DNS 失败: %s", res.Error)
	}
	if res.Status != StatusOnline && res.Status != StatusHighLatency {
		t.Fatalf("应判定为在线，实际 %s (%s)", res.Status, res.Error)
	}
}

// 真正不存在的域名才应该判 DNS 失败
func TestNonexistentDomainIsDNSFailed(t *testing.T) {
	rule := &models.ProxyRule{
		ID: "r1", Alias: "不存在", Protocol: "vmess",
		ServerAddr: "this-domain-definitely-does-not-exist-12345.invalid",
		ServerPort: 443,
	}
	res := CheckRule(rule, models.HealthCheckConfig{TimeoutSec: 3})
	if res.Status != StatusDNSFailed {
		t.Fatalf("不存在的域名应报 DNS 失败，实际 %s (%s)", res.Status, res.Error)
	}
}

// 域名能解析但端口不通，应报连接超时而不是 DNS 失败——
// 两者的排查方向完全不同，混为一谈会误导用户
func TestResolvableButUnreachableIsTimeout(t *testing.T) {
	rule := &models.ProxyRule{
		ID: "r1", Alias: "端口不通", Protocol: "vmess",
		ServerAddr: "localhost",
		ServerPort: 1, // 几乎不可能有服务在监听
	}
	res := CheckRule(rule, models.HealthCheckConfig{TimeoutSec: 2})
	if res.Status == StatusDNSFailed {
		t.Fatalf("域名可解析，不应报 DNS 失败: %s", res.Error)
	}
	if res.Status != StatusTimeout {
		t.Fatalf("应报连接超时，实际 %s (%s)", res.Status, res.Error)
	}
}

// 直接给 IP 的节点不该走 DNS 分支
func TestPlainIPSkipsDNS(t *testing.T) {
	port := listenLocal(t)
	rule := &models.ProxyRule{
		ID: "r1", Alias: "IP直连", Protocol: "vmess",
		ServerAddr: "127.0.0.1", ServerPort: port,
	}
	res := CheckRule(rule, models.HealthCheckConfig{TimeoutSec: 3})
	if res.Status != StatusOnline && res.Status != StatusHighLatency {
		t.Fatalf("IP 直连应成功，实际 %s (%s)", res.Status, res.Error)
	}
	_ = fmt.Sprint
}
