package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"xray-manager/internal/models"
	"xray-manager/internal/portregistry"
)

// 端到端测量批量导入耗时（含 ID 生成、端口分配、注册表写入）
func TestImportShareLinksPerf(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "a.exe")
	cfgp := filepath.Join(dir, "a.json")
	os.WriteFile(exe, []byte("x"), 0600)
	os.WriteFile(cfgp, []byte("{}"), 0600)

	svc := &MyService{
		config:         &models.Config{},
		portRegistry:   portregistry.NewAt(filepath.Join(dir, "reg.json")),
		executablePath: exe,
		configPath:     cfgp,
		configManager:  nil, // saveConfig 会跳过写盘
	}

	// 造 50 条各不相同的 trojan 链接
	var sb strings.Builder
	const n = 50
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "trojan://pw%d@host%d.example.com:443?security=tls&sni=host%d.example.com#节点%d\n", i, i, i, i)
	}

	start := time.Now()
	res, err := svc.ImportShareLinks(sb.String(), "", "")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}

	t.Logf("导入 %d 个节点耗时 %v（成功 %d，失败 %d）",
		n, elapsed.Round(time.Millisecond), res.SuccessCount, res.FailCount)

	if res.SuccessCount != n {
		t.Fatalf("应成功导入 %d 个，实际 %d 个", n, res.SuccessCount)
	}
	// 每个节点都要有唯一 ID 和端口
	ids := map[string]bool{}
	ports := map[int]bool{}
	for _, r := range svc.config.Rules {
		if ids[r.ID] {
			t.Fatalf("ID 重复: %s", r.ID)
		}
		ids[r.ID] = true
		if r.LocalPort > 0 {
			if ports[r.LocalPort] {
				t.Fatalf("端口重复: %d", r.LocalPort)
			}
			ports[r.LocalPort] = true
		}
	}
	if len(ports) != n {
		t.Fatalf("应为 %d 个节点各分配端口，实际 %d 个", n, len(ports))
	}
	if elapsed > 3*time.Second {
		t.Fatalf("导入 %d 个节点耗时 %v，疑似退回逐个分配", n, elapsed)
	}
	for _, p := range svc.config.Rules {
		svc.releasePortReservationLocked(p.LocalPort)
	}
}

// Hysteria v1 与 v2 是两套协议，内置 sing-box 只实现了 v2，
// 报错要说清原因，而不是笼统的"不支持的链接格式"
func TestHysteriaV1GivesClearError(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "a.exe")
	cfgp := filepath.Join(dir, "a.json")
	os.WriteFile(exe, []byte("x"), 0600)
	os.WriteFile(cfgp, []byte("{}"), 0600)

	svc := &MyService{
		config:         &models.Config{},
		portRegistry:   portregistry.NewAt(filepath.Join(dir, "reg.json")),
		executablePath: exe,
		configPath:     cfgp,
	}

	// 一条 v1、一条 v2：v2 应导入成功，v1 应给出明确原因
	text := "hysteria://5.83.129.90:54177?alpn=h3&auth_str=x&insecure=1#V1节点\n" +
		"hysteria2://pw@example.com:443?insecure=1&sni=example.com#V2节点\n"

	res, err := svc.ImportShareLinks(text, "", "")
	if err != nil {
		t.Fatalf("导入不应整体失败: %v", err)
	}
	if res.SuccessCount != 1 {
		t.Fatalf("hysteria2 应导入成功，实际成功 %d 个", res.SuccessCount)
	}
	if res.FailCount != 1 {
		t.Fatalf("hysteria v1 应失败，实际失败 %d 个", res.FailCount)
	}
	joined := strings.Join(res.Errors, " ")
	if !strings.Contains(joined, "Hysteria v1") {
		t.Fatalf("错误信息应说明是 Hysteria v1 不受支持，实际: %s", joined)
	}
	for _, p := range svc.config.Rules {
		svc.releasePortReservationLocked(p.LocalPort)
	}
}
