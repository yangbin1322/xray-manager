package xray

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"xray-manager/internal/models"
)

// 伪装 SNI 的 Trojan 节点必须在生成的配置里带 allowInsecure，
// 否则 Xray 会拿伪装域名去校验证书，握手必失败。
func TestTrojanConfigEmitsAllowInsecure(t *testing.T) {
	rule := &models.ProxyRule{
		ID: "rule_trojan", Alias: "美国04", Protocol: "trojan",
		ServerAddr: "pokemonus-02.yunjnet.com", ServerPort: 56017,
		LocalPort: 10800,
		Settings: models.ProxySettings{
			TrojanPassword: "73729e94-c0ed-4d6f-831b-fcfd4b7c8af6",
			Network:        "tcp",
			Security:       "tls",
			TLS: &models.TLSSettings{
				ServerName:    "iosapps.itunes.apple.com",
				AllowInsecure: true,
			},
		},
	}

	cfg, err := BuildConfig(rule)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cfg.ToJSON()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(raw, `"allowInsecure": true`) {
		t.Fatalf("配置中缺少 allowInsecure，伪装 SNI 节点将无法握手:\n%s", raw)
	}
	if !strings.Contains(raw, "iosapps.itunes.apple.com") {
		t.Fatalf("配置中缺少 SNI:\n%s", raw)
	}

	// 确认出站确实是 trojan 且密码正确
	var probe struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				Servers []struct {
					Address  string `json:"address"`
					Port     int    `json:"port"`
					Password string `json:"password"`
				} `json:"servers"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		t.Fatal(err)
	}
	s := probe.Outbounds[0].Settings.Servers
	if probe.Outbounds[0].Protocol != "trojan" || len(s) != 1 {
		t.Fatalf("出站配置不对: %+v", probe.Outbounds[0])
	}
	if s[0].Password != "73729e94-c0ed-4d6f-831b-fcfd4b7c8af6" || s[0].Port != 56017 {
		t.Fatalf("服务器信息不对: %+v", s[0])
	}

	// 真实内核校验：配置必须能被 Xray 接受
	bin := locateXray()
	if bin == "" {
		t.Skip("未找到内置 Xray，跳过内核校验")
	}
	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "run", "-test", "-c", p).CombinedOutput()
	if err != nil {
		t.Fatalf("Xray 拒绝加载 Trojan 配置: %v\n%s", err, out)
	}
}
