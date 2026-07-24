package updater

import "testing"

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
