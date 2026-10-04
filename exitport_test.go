package main

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"xray-manager/internal/models"
	"xray-manager/internal/process"
)

// 候选筛选：只有已启动、验证完成、出口 IP 一致的节点才算数。
func TestExitCandidatesFilter(t *testing.T) {
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{
			{ID: "ok", Alias: "命中", LocalPort: 1, Enabled: true, RealIP: "1.2.3.4"},
			{ID: "mapped", Alias: "写法不同", LocalPort: 2, Enabled: true, RealIP: " ::ffff:1.2.3.4 "},
			{ID: "stopped", LocalPort: 3, Enabled: false, RealIP: "1.2.3.4"},
			{ID: "verifying", LocalPort: 4, Enabled: true, Verifying: true, RealIP: "1.2.3.4"},
			{ID: "other", LocalPort: 5, Enabled: true, RealIP: "5.6.7.8"},
			{ID: "noip", LocalPort: 6, Enabled: true},
		},
	})

	got := svc.exitCandidatesLocked("1.2.3.4")
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.RuleID
	}
	if strings.Join(ids, ",") != "ok,mapped" {
		t.Fatalf("候选应为 ok,mapped，实际 %v", ids)
	}
}

// 排序：健康正常的按延迟升序在前，未检测的居中，检测失败的垫底。
func TestExitCandidatesOrder(t *testing.T) {
	ip := "1.2.3.4"
	svc := newTestService(&models.Config{
		Rules: []models.ProxyRule{
			{ID: "failed", LocalPort: 1, Enabled: true, RealIP: ip, HealthStatus: "timeout", HealthLatency: 10},
			{ID: "unknown", LocalPort: 2, Enabled: true, RealIP: ip},
			{ID: "slow", LocalPort: 3, Enabled: true, RealIP: ip, HealthStatus: "high_latency", HealthLatency: 900},
			{ID: "fast", LocalPort: 4, Enabled: true, RealIP: ip, HealthStatus: "online", HealthLatency: 80},
			{ID: "speedtested", LocalPort: 5, Enabled: true, RealIP: ip, HealthStatus: "online", Latency: 200},
		},
	})

	got := svc.exitCandidatesLocked(ip)
	ids := make([]string, len(got))
	for i, m := range got {
		ids[i] = m.RuleID
	}
	want := "fast,speedtested,slow,unknown,failed"
	if strings.Join(ids, ",") != want {
		t.Fatalf("排序应为 %s，实际 %v", want, ids)
	}
}

func TestExitPortValidate(t *testing.T) {
	ep := models.ExitPort{ExitIP: " ::ffff:1.2.3.4 "}
	if err := ep.Validate(); err != nil {
		t.Fatal(err)
	}
	if ep.ExitIP != "1.2.3.4" {
		t.Errorf("出口 IP 应规范化为点分写法，实际 %q", ep.ExitIP)
	}
	if ep.Alias == "" {
		t.Error("应补默认别名")
	}

	for _, bad := range []string{"", "2001:db8::1", "not-ip"} {
		e := models.ExitPort{ExitIP: bad}
		if err := e.Validate(); err == nil {
			t.Errorf("出口 IP %q 应校验失败", bad)
		}
	}
}

// tagServer 假节点：每个连接先回一行标签。
func tagServer(t *testing.T, tag string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte(tag + "\n"))
				_, _ = conn.Read(make([]byte, 1)) // 保持连接直到对端关闭
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func dialTag(t *testing.T, port int) (string, net.Conn) {
	t.Helper()
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		conn.Close()
		return "", nil
	}
	return strings.TrimSpace(line), conn
}

// 端到端：优先走低延迟节点；它的出口 IP 变了之后，现有连接被切断，
// 新连接改走另一个同 IP 节点；全部失效后拒绝连接。
func TestExitPortFailoverOnIPChange(t *testing.T) {
	fastPort := tagServer(t, "fast")
	slowPort := tagServer(t, "slow")
	ip := "1.2.3.4"

	svc := &MyService{
		config: &models.Config{
			Rules: []models.ProxyRule{
				{ID: "fast", Alias: "快", LocalPort: fastPort, Enabled: true, RealIP: ip, HealthStatus: "online", HealthLatency: 50},
				{ID: "slow", Alias: "慢", LocalPort: slowPort, Enabled: true, RealIP: ip, HealthStatus: "online", HealthLatency: 300},
			},
			ExitPorts: []models.ExitPort{{ID: "exit_1", Alias: "出口", ExitIP: ip, LocalPort: freeTestPort(t)}},
		},
		processManager: process.NewManager(func(string) {}, func() {}),
	}
	ep := &svc.config.ExitPorts[0]
	if err := svc.startExitPortInternal(ep); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopExitInstance(ep.ID) })

	tag, conn := dialTag(t, ep.LocalPort)
	if tag != "fast" {
		t.Fatalf("应优先走低延迟节点，实际 %q", tag)
	}
	defer conn.Close()

	// 快节点的出口 IP 漂移（未开启节点级绑定，节点继续运行）
	svc.handleRealIP(fastPort, "9.9.9.9")
	svc.refreshExitMembers()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 16)); err == nil {
		t.Fatal("IP 变更后经该节点的现有连接应被切断")
	}

	tag2, conn2 := dialTag(t, ep.LocalPort)
	if tag2 != "slow" {
		t.Fatalf("新连接应改走仍是该出口的慢节点，实际 %q", tag2)
	}
	conn2.Close()

	// 慢节点也停了：没有候选，必须拒绝而不是乱走
	svc.config.Rules[1].Enabled = false
	svc.refreshExitMembers()
	if tag3, conn3 := dialTag(t, ep.LocalPort); tag3 != "" {
		conn3.Close()
		t.Fatalf("没有候选时应拒绝连接，实际走到了 %q", tag3)
	}

	got := svc.GetExitPorts()[0]
	if got.MemberCount != 0 || got.RejectedConns != 1 {
		t.Errorf("统计不符：候选 %d，拒绝 %d", got.MemberCount, got.RejectedConns)
	}
}

// 先定义出口端口、后启动节点：添加即开始监听，节点探测到该出口 IP 后自动加入，
// 全程无需挑选节点。
func TestExitPortAutoJoinsNodeStartedLater(t *testing.T) {
	ip := "1.2.3.4"
	nodePort := tagServer(t, "late")

	svc := &MyService{
		config: &models.Config{
			// 节点尚未拿到出口 IP（相当于还没启动/还在验证）
			Rules: []models.ProxyRule{{ID: "late", Alias: "后启动", LocalPort: nodePort}},
		},
		processManager: process.NewManager(func(string) {}, func() {}),
	}
	exitPort := freeTestPort(t)
	if err := svc.AddExitPort(models.ExitPort{ExitIP: ip, LocalPort: exitPort}); err != nil {
		t.Fatal(err)
	}
	ep := svc.config.ExitPorts[0]
	t.Cleanup(func() { stopExitInstance(ep.ID) })

	if !ep.Enabled || ep.LastError != "" {
		t.Fatalf("添加后应立即开始监听，实际 enabled=%v err=%q", ep.Enabled, ep.LastError)
	}
	if tag, conn := dialTag(t, exitPort); tag != "" {
		conn.Close()
		t.Fatalf("还没有节点承载该出口时应拒绝连接，实际走到了 %q", tag)
	}

	// 节点启动并探测到出口 IP
	svc.config.Rules[0].Enabled = true
	svc.handleRealIP(nodePort, ip)
	svc.refreshExitMembers()

	tag, conn := dialTag(t, exitPort)
	if tag != "late" {
		t.Fatalf("节点探测到该出口 IP 后应自动加入，实际 %q", tag)
	}
	conn.Close()

	got := svc.GetExitPorts()[0]
	if got.MemberCount != 1 || len(got.MemberAliases) != 1 || got.MemberAliases[0] != "后启动" {
		t.Errorf("候选回显不符：count=%d aliases=%v", got.MemberCount, got.MemberAliases)
	}
}

// 端口被占时添加仍算成功（配置已保存），失败原因记在 LastError，供用户处理后手动启动。
func TestAddExitPortKeepsConfigWhenStartFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	svc := &MyService{config: &models.Config{}}
	// 端口预留只做内存记账：模拟该端口已记在本实例名下，让添加走到真正监听那一步
	svc.portReservations = map[int]bool{busy: true}
	if err := svc.AddExitPort(models.ExitPort{ExitIP: "1.2.3.4", LocalPort: busy}); err != nil {
		t.Fatalf("监听失败不应让添加失败: %v", err)
	}
	ep := svc.config.ExitPorts[0]
	t.Cleanup(func() { stopExitInstance(ep.ID) })
	if ep.Enabled || ep.LastError == "" {
		t.Fatalf("监听失败应保持未启用并记录原因，实际 enabled=%v err=%q", ep.Enabled, ep.LastError)
	}
}

// 备注是用户填写的字段：编辑后要保留，前后空白去掉，超长拒绝。
func TestExitPortRemark(t *testing.T) {
	svc := &MyService{config: &models.Config{
		ExitPorts: []models.ExitPort{{ID: "exit_1", ExitIP: "1.2.3.4", LocalPort: 4001, Remark: "旧备注"}},
	}}

	if err := svc.UpdateExitPort(models.ExitPort{ID: "exit_1", ExitIP: "1.2.3.4", LocalPort: 4001, Remark: "  某平台白名单  "}); err != nil {
		t.Fatal(err)
	}
	if got := svc.config.ExitPorts[0].Remark; got != "某平台白名单" {
		t.Errorf("备注应更新并去掉首尾空白，实际 %q", got)
	}

	long := models.ExitPort{ExitIP: "1.2.3.4", Remark: strings.Repeat("备", 201)}
	if err := long.Validate(); err == nil {
		t.Error("超过 200 个字符的备注应校验失败")
	}
	ok := models.ExitPort{ExitIP: "1.2.3.4", Remark: strings.Repeat("备", 200)}
	if err := ok.Validate(); err != nil {
		t.Errorf("200 个字符（按字符而非字节计）应允许: %v", err)
	}
}
