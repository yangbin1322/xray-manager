package xray

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"xray-manager/internal/models"
)

func realityRule() *models.ProxyRule {
	return &models.ProxyRule{
		ID: "rule_reality", Alias: "REALITY节点", Protocol: "vless",
		ServerAddr: "1.2.3.4", ServerPort: 443,
		Settings: models.ProxySettings{
			VLessUserID: "adf8d585-7c01-40be-84f6-d4d2a31caa49",
			VLessFlow:   "xtls-rprx-vision", VLessEncryption: "none",
			Network: "tcp", Security: "reality",
			Reality: &models.RealitySettings{
				PublicKey:   "gKMEfvBQK0mE8ZlZmYFvvNSnRRl3TjLPKGpXfLwMFXM",
				ShortID:     "6ba85179e30d4fc2",
				Fingerprint: "chrome",
				ServerName:  "www.microsoft.com",
			},
		},
	}
}

// REALITY 节点必须生成 realitySettings，否则 Xray 拒绝加载整份配置，
// 表现为进程刚启动就退出。
func TestRealityStreamSettingsAreEmitted(t *testing.T) {
	stream := buildStreamSettings(realityRule())
	if stream.Security != "reality" {
		t.Fatalf("security 应为 reality，实际 %q", stream.Security)
	}
	if stream.RealitySettings == nil {
		t.Fatal("security=reality 时必须生成 realitySettings")
	}
	rs := stream.RealitySettings
	if rs.PublicKey == "" || rs.Password == "" {
		t.Fatalf("公钥必须同时写入 publicKey 和 password（新版别名）: %+v", rs)
	}
	if rs.ServerName != "www.microsoft.com" || rs.ShortID != "6ba85179e30d4fc2" {
		t.Fatalf("REALITY 参数丢失: %+v", rs)
	}
}

// 缺少公钥的历史节点必须在启动前就被拦下：
// 放行的话 Xray 会拒绝加载配置、进程刚起来就退出，用户只看到"启动成功"却连不上。
func TestRealityWithoutPublicKeyIsRejected(t *testing.T) {
	rule := &models.ProxyRule{
		Protocol: "vless", ServerAddr: "1.2.3.4", ServerPort: 443,
		Settings: models.ProxySettings{Security: "reality"},
	}
	err := rule.ValidateForXray()
	if err == nil {
		t.Fatal("缺少 pbk 的 REALITY 节点应校验失败")
	}
	if !strings.Contains(err.Error(), "pbk") {
		t.Fatalf("错误信息应指明缺少 pbk，实际 %q", err)
	}

	// 参数齐全的节点不应被误伤
	rule.Settings.Reality = &models.RealitySettings{PublicKey: "abc"}
	if err := rule.ValidateForXray(); err != nil {
		t.Fatalf("参数齐全的 REALITY 节点不应被拦下: %v", err)
	}
}

// locateXray 返回仓库内置的 Xray 可执行文件路径，找不到则返回空串。
func locateXray() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	name := "xray"
	if runtime.GOOS == "windows" {
		name = "xray.exe"
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "assets", "xray", runtime.GOOS, name)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// 真实内核校验：REALITY 节点生成的配置必须能被 Xray 接受。
// 这是本次修复的核心——修复前 Xray 会报 "REALITY: Empty realitySettings" 并拒绝启动。
func TestRealityConfigLoadsInXray(t *testing.T) {
	bin := locateXray()
	if bin == "" {
		t.Skip("未找到内置 Xray")
	}

	rule := realityRule()
	rule.LocalPort = 19001
	cfg, err := BuildConfig(rule)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cfg.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "realitySettings") {
		t.Fatalf("配置中缺少 realitySettings:\n%s", raw)
	}

	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "run", "-test", "-c", p).CombinedOutput()
	if err != nil {
		t.Fatalf("Xray 拒绝加载 REALITY 配置: %v\n%s", err, out)
	}
}
