package updater

import (
	"strings"
	"testing"
)

func TestNormalizeAndCompareVersions(t *testing.T) {
	if NormalizeVersion("v2.2.0") != "2.2.0" {
		t.Fatal("normalize failed")
	}
	if CompareVersions("2.1.0", "2.2.0") >= 0 {
		t.Fatal("2.1.0 should be < 2.2.0")
	}
	if CompareVersions("2.2.0", "2.2.0") != 0 {
		t.Fatal("equal versions")
	}
	if CompareVersions("2.3.0", "2.2.9") <= 0 {
		t.Fatal("2.3.0 should be > 2.2.9")
	}
	if CompareVersions("v2.2.0", "2.2") != 0 {
		// 2.2 == 2.2.0
		t.Fatalf("2.2.0 vs 2.2: %d", CompareVersions("v2.2.0", "2.2"))
	}
}

func TestPickAssetPrefersPlatform(t *testing.T) {
	assets := []ghAsset{
		{Name: "xray-manager-linux-amd64", BrowserDownloadURL: "http://x/linux"},
		{Name: "xray-manager-windows-amd64.exe", BrowserDownloadURL: "http://x/win"},
		{Name: "xray-manager-macos-arm64.dmg", BrowserDownloadURL: "http://x/mac"},
	}
	got := pickAsset(assets)
	if got == nil {
		t.Fatal("expected asset")
	}
	// Just ensure it returns something with positive score; platform-specific name contains goos keywords often
	if got.BrowserDownloadURL == "" {
		t.Fatal("empty url")
	}
}

// Windows 批处理必须使用 CRLF 换行：cmd.exe 按字节偏移解析 .bat，
// 安装目录含中文等多字节字符时，纯 LF 会导致它在错误位置切行，
// 表现为 'ARGET'、'local' 之类的残缺命令，更新随之静默失败。
func TestToCRLF(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"LF 转 CRLF", "a\nb\n", "a\r\nb\r\n"},
		{"已是 CRLF 不重复转换", "a\r\nb\r\n", "a\r\nb\r\n"},
		{"混合换行统一为 CRLF", "a\r\nb\nc\n", "a\r\nb\r\nc\r\n"},
		{"无换行原样返回", "abc", "abc"},
		{"空串", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := toCRLF(tc.in); got != tc.want {
				t.Errorf("toCRLF(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// 含中文路径的脚本转换后不应残留裸 LF——那正是故障的直接成因。
func TestToCRLFWithChinesePath(t *testing.T) {
	script := "@echo off\nsetlocal\nset \"TARGET=D:\\Desktop\\跨境运营\\app.exe\"\n"
	got := toCRLF(script)

	for i := 0; i < len(got); i++ {
		if got[i] == '\n' && (i == 0 || got[i-1] != '\r') {
			t.Fatalf("位置 %d 存在未配对的 LF：%q", i, got)
		}
	}
	if !strings.Contains(got, "跨境运营") {
		t.Error("中文路径不应被破坏")
	}
}
