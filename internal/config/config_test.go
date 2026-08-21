package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"xray-manager/internal/models"
)

// 保存必须是原子的：写一半断电不能留下半截文件。
// 这里验证的是「写完之后不留临时文件、内容完整可解析」——
// 真正的断电无法在单测里模拟，但至少要保证正常路径不留垃圾。
func TestSaveIsAtomicAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	m := &Manager{configPath: path}

	cfg := &models.Config{
		Rules: []models.ProxyRule{{ID: "r1", Alias: "节点", LocalPort: 10808}},
	}
	if err := m.Save(cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("保存后不该残留 .tmp 文件")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back models.Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("保存的配置应是完整可解析的 JSON: %v", err)
	}
	if len(back.Rules) != 1 || back.Rules[0].ID != "r1" {
		t.Errorf("配置内容不对: %+v", back.Rules)
	}
}

// 覆盖保存（每次改动都会发生）必须能正确替换旧内容，
// Windows 上 rename 不能覆盖已存在文件，需要特别处理
func TestSaveOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	m := &Manager{configPath: path}

	if err := m.Save(&models.Config{Rules: []models.ProxyRule{{ID: "old"}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(&models.Config{Rules: []models.ProxyRule{{ID: "new"}}}); err != nil {
		t.Fatalf("覆盖保存失败: %v", err)
	}

	got, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rules) != 1 || got.Rules[0].ID != "new" {
		t.Errorf("覆盖后应只剩新内容，实际 %+v", got.Rules)
	}
}

// 配置文件损坏时必须明确报错，不能静默当成空配置——
// 那会让用户的全部节点、订阅、分组在下一次保存时被彻底覆盖掉。
// 这与端口注册表的取舍相反：注册表可重建，配置不可。
func TestLoadRefusesToSilentlyDiscardCorruptedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("\x00\x00\x00 garbage"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{configPath: path}

	if _, err := m.Load(); err == nil {
		t.Fatal("配置损坏必须报错，静默返回空配置会让用户的数据在下次保存时丢光")
	}
}
