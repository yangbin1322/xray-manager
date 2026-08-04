package main

import (
	"os"
	"path/filepath"
	"testing"
	"xray-manager/internal/models"
	"xray-manager/internal/portregistry"
)

// newPortAllocService 构造一个带真实端口注册表的服务实例。
// otherPorts 模拟本机上另一个 xray-manager 实例已注册占用的端口。
func newPortAllocService(t *testing.T, otherPorts []int) *MyService {
	t.Helper()
	dir := t.TempDir()

	mine := filepath.Join(dir, "mine.exe")
	myCfg := filepath.Join(dir, "mine.json")
	other := filepath.Join(dir, "other.exe")
	otherCfg := filepath.Join(dir, "other.json")
	for _, p := range []string{mine, myCfg, other, otherCfg} {
		if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	reg := portregistry.NewAt(filepath.Join(dir, "registry.json"))
	for i, port := range otherPorts {
		entry := portregistry.Entry{
			ExecutablePath: other, ConfigPath: otherCfg,
			ResourceID:   "other_" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			ResourceType: "rule", Port: port,
		}
		if err := reg.Claim(entry); err != nil {
			t.Fatalf("预置占用端口 %d 失败: %v", port, err)
		}
	}

	return &MyService{
		config:         &models.Config{},
		portRegistry:   reg,
		executablePath: mine,
		configPath:     myCfg,
	}
}

// 核心回归：本机另一个实例占满了起始端口段时，仍应能分配到足够端口。
//
// 修复前 allocateLocalPorts 只看本实例配置挑端口，挑中的全被别人占了，
// 提交注册表时整批剔除，表现为"53 个节点只分到 13 个端口"。
func TestAllocateLocalPortsSkipsPortsHeldByOtherInstances(t *testing.T) {
	// 另一个实例占据 11000 起的连续 200 个端口
	var occupied []int
	for p := 11000; p < 11200; p++ {
		occupied = append(occupied, p)
	}
	svc := newPortAllocService(t, occupied)

	const want = 53
	ports := svc.allocateLocalPorts(want)
	defer func() {
		for _, p := range ports {
			svc.releasePortReservationLocked(p)
		}
	}()

	if len(ports) != want {
		t.Fatalf("应分配到 %d 个端口，实际只有 %d 个（被其他实例占用的端口没有跳过）", want, len(ports))
	}

	// 分到的端口不能和别人的重叠
	occupiedSet := map[int]bool{}
	for _, p := range occupied {
		occupiedSet[p] = true
	}
	for _, p := range ports {
		if occupiedSet[p] {
			t.Fatalf("端口 %d 已被其他实例占用，不应分配", p)
		}
	}

	// 也不能自己重复
	seen := map[int]bool{}
	for _, p := range ports {
		if seen[p] {
			t.Fatalf("端口 %d 被重复分配", p)
		}
		seen[p] = true
	}
}

// 没有其他实例时，应当从推荐起始端口开始紧凑分配
func TestAllocateLocalPortsStartsAtRecommendedPort(t *testing.T) {
	svc := newPortAllocService(t, nil)

	ports := svc.allocateLocalPorts(3)
	defer func() {
		for _, p := range ports {
			svc.releasePortReservationLocked(p)
		}
	}()

	if len(ports) != 3 {
		t.Fatalf("应分配 3 个端口，实际 %d 个", len(ports))
	}
	if ports[0] < 11000 {
		t.Fatalf("应从推荐起始端口 11000 开始，实际 %d", ports[0])
	}
}

// 本实例自己注册的端口不应被当成"别人占用"而跳过统计
func TestForeignRegisteredPortsExcludesOwnEntries(t *testing.T) {
	svc := newPortAllocService(t, []int{12000, 12001})

	// 以本实例身份也注册一个端口
	if err := svc.claimPortLocked("rule", "my_rule", "我的节点", 12500); err != nil {
		t.Fatal(err)
	}

	foreign := svc.foreignRegisteredPortsLocked()
	set := map[int]bool{}
	for _, p := range foreign {
		set[p] = true
	}

	if !set[12000] || !set[12001] {
		t.Fatalf("其他实例占用的端口应被识别，实际 %v", foreign)
	}
	if set[12500] {
		t.Fatal("本实例自己注册的端口不应算作其他实例占用")
	}
}

// 注册表不可用时应退化为原有行为，而不是一个端口都分不出来
func TestAllocateLocalPortsWorksWithoutRegistry(t *testing.T) {
	svc := &MyService{config: &models.Config{}}

	ports := svc.allocateLocalPorts(5)
	defer func() {
		for _, p := range ports {
			svc.releasePortReservationLocked(p)
		}
	}()

	if len(ports) != 5 {
		t.Fatalf("无注册表时应仍能分配 5 个端口，实际 %d 个", len(ports))
	}
}
