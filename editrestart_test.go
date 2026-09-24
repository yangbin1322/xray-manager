package main

import (
	"testing"
	"xray-manager/internal/models"
)

func TestRuleConnChanged(t *testing.T) {
	base := models.ProxyRule{
		Alias: "A", LocalPort: 10808, Protocol: "vmess",
		ServerAddr: "1.1.1.1", ServerPort: 443,
		Settings: models.ProxySettings{VMessUserID: "u1"},
	}
	tests := []struct {
		name   string
		modify func(r *models.ProxyRule)
		want   bool
	}{
		{"只改别名", func(r *models.ProxyRule) { r.Alias = "B" }, false},
		{"只改备注", func(r *models.ProxyRule) { r.Remark = "x" }, false},
		{"改服务器地址", func(r *models.ProxyRule) { r.ServerAddr = "2.2.2.2" }, true},
		{"改本地端口", func(r *models.ProxyRule) { r.LocalPort = 10809 }, true},
		{"改协议参数", func(r *models.ProxyRule) { r.Settings.VMessUserID = "u2" }, true},
		{"改前置代理绕过", func(r *models.ProxyRule) { r.BypassPreProxy = true }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated := base
			tt.modify(&updated)
			if got := ruleConnChanged(&base, &updated); got != tt.want {
				t.Fatalf("ruleConnChanged = %v, want %v", got, tt.want)
			}
		})
	}
}

// 编辑已停止的节点不应被拉起
func TestUpdateRule_StoppedNodeNotStarted(t *testing.T) {
	a := &MyService{config: &models.Config{Rules: []models.ProxyRule{
		{ID: "r1", Alias: "A", LocalPort: 10808, Protocol: "vmess", ServerAddr: "1.1.1.1", ServerPort: 443},
	}}, configPath: t.TempDir() + "/config.json"}
	updated := a.config.Rules[0]
	updated.ServerAddr = "2.2.2.2"
	if err := a.UpdateRule("r1", updated); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if a.config.Rules[0].Enabled {
		t.Fatal("编辑已停止的节点不应自动启动")
	}
}
