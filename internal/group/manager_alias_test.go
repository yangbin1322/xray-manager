package group

import (
	"testing"
	"xray-manager/internal/models"
)

// 分组管理器不能持有调用方切片的内部指针：
// 上层删除分组时用 append 就地前移剩余元素，旧指针会指向别的分组，
// 之后按 ID 改名就会改到无关分组头上（界面表现为删完分组后名字串了）。
func TestManagerDoesNotAliasCallerSlice(t *testing.T) {
	groups := []models.Group{
		{ID: "g1", Name: "免费节点"},
		{ID: "g2", Name: "宝可梦"},
		{ID: "g3", Name: "跨境隔离"},
	}

	m := NewManager(nil)
	m.LoadGroups(groups)

	// 模拟上层 DeleteGroup：从 config 切片里就地摘掉 g1，
	// 剩余元素在同一块底层数组里整体前移
	groups = append(groups[:0], groups[1:]...)
	m.DeleteGroup("g1")

	// 改 g2 的名字，不应影响任何其他分组
	if err := m.UpdateGroup("g2", "宝可梦-新", ""); err != nil {
		t.Fatal(err)
	}

	for _, g := range groups {
		if g.ID != "g2" && g.Name == "宝可梦-新" {
			t.Fatalf("改 g2 的名字污染了分组 %s，它的名字变成了 %q", g.ID, g.Name)
		}
	}
	if got, _ := m.GetGroup("g2"); got.Name != "宝可梦-新" {
		t.Errorf("g2 应改名成功，实际 %q", got.Name)
	}
	if got, _ := m.GetGroup("g3"); got.Name != "跨境隔离" {
		t.Errorf("g3 名字不应被改动，实际 %q", got.Name)
	}
}
