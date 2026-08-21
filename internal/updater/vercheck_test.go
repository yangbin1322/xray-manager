package updater

import "testing"

// 2.10.0 必须大于 2.9.0：按字符串比会得出相反结论，
// 老用户就永远收不到更新提示
func TestTenIsNewerThanNine(t *testing.T) {
	if got := CompareVersions("2.10.0", "2.9.0"); got != 1 {
		t.Errorf("CompareVersions(2.10.0, 2.9.0) = %d，应为 1", got)
	}
	if got := CompareVersions("v2.10.0", "v2.9.0"); got != 1 {
		t.Errorf("带 v 前缀时也应为 1，实际 %d", got)
	}
	if got := CompareVersions("2.9.0", "2.10.0"); got != -1 {
		t.Errorf("反向应为 -1，实际 %d", got)
	}
}
