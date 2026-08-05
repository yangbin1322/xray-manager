package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xray-manager/internal/models"
	"xray-manager/internal/portregistry"
	"xray-manager/internal/utils"
)

// 跨实例的端口去重由全局端口注册表保证（生产环境 ServiceStartup 必定初始化它，
// 初始化失败会直接中止启动），因此这里让两个实例共享同一个注册表，
// 与真实运行方式一致。
func TestRecommendPortReservesAcrossServiceInstances(t *testing.T) {
	root := t.TempDir()
	registry := portregistry.NewAt(filepath.Join(root, "port-registry.json"))
	firstExecutable := filepath.Join(root, "client-a.exe")
	firstConfig := filepath.Join(root, "client-a.json")
	secondExecutable := filepath.Join(root, "client-b.exe")
	secondConfig := filepath.Join(root, "client-b.json")
	for _, path := range []string{firstExecutable, firstConfig, secondExecutable, secondConfig} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	first := &MyService{config: &models.Config{}, portRegistry: registry, executablePath: firstExecutable, configPath: firstConfig}
	second := &MyService{config: &models.Config{}, portRegistry: registry, executablePath: secondExecutable, configPath: secondConfig}
	t.Cleanup(first.releaseAllPortReservations)
	t.Cleanup(second.releaseAllPortReservations)

	firstPort := first.RecommendPort()
	secondPort := second.RecommendPort()
	if firstPort == 0 || secondPort == 0 {
		t.Fatalf("expected non-zero ports, got %d and %d", firstPort, secondPort)
	}
	if firstPort == secondPort {
		t.Fatalf("different service instances recommended the same port %d", firstPort)
	}
	if !first.CheckPortAvailable(firstPort) {
		t.Fatalf("service should recognize its own reservation for port %d", firstPort)
	}
	// 端口预留是记账而非常驻监听，因此不再断言端口对其他进程不可用；
	// 真正要保证的是两个实例不会推荐到同一个端口（已在上面断言）。
	if !first.portReservations[firstPort] {
		t.Fatalf("port %d was not recorded as reserved by the first instance", firstPort)
	}
}

func TestAddRuleReportsGlobalOwnerPath(t *testing.T) {
	root := t.TempDir()
	registry := portregistry.NewAt(filepath.Join(root, "port-registry.json"))
	ownerExecutable := filepath.Join(root, "client-a.exe")
	ownerConfig := filepath.Join(root, "client-a.json")
	requesterExecutable := filepath.Join(root, "client-b.exe")
	requesterConfig := filepath.Join(root, "client-b.json")
	for _, path := range []string{ownerExecutable, ownerConfig, requesterExecutable, requesterConfig} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Claim(portregistry.Entry{ExecutablePath: ownerExecutable, ConfigPath: ownerConfig, ResourceID: "owner", ResourceType: "rule", Alias: "已占用规则", Port: 10991}); err != nil {
		t.Fatal(err)
	}
	service := &MyService{config: &models.Config{}, portRegistry: registry, executablePath: requesterExecutable, configPath: requesterConfig}
	err := service.AddRule(models.ProxyRule{Alias: "新规则", LocalPort: 10991})
	if err == nil || !strings.Contains(err.Error(), ownerExecutable) || !strings.Contains(err.Error(), ownerConfig) || !strings.Contains(err.Error(), "已占用规则") {
		t.Fatalf("expected owner details, got %v", err)
	}
}

func TestDeleteRuleRemovesGlobalRegistration(t *testing.T) {
	root := t.TempDir()
	registry := portregistry.NewAt(filepath.Join(root, "port-registry.json"))
	executable := filepath.Join(root, "client.exe")
	configPath := filepath.Join(root, "config.json")
	for _, path := range []string{executable, configPath} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := portregistry.Entry{ExecutablePath: executable, ConfigPath: configPath, ResourceID: "rule_1", ResourceType: "rule", Alias: "待删除规则", Port: 10992}
	if err := registry.Claim(entry); err != nil {
		t.Fatal(err)
	}
	service := &MyService{
		config:         &models.Config{Rules: []models.ProxyRule{{ID: "rule_1", Alias: "待删除规则", LocalPort: 10992}}},
		portRegistry:   registry,
		executablePath: executable,
		configPath:     configPath,
	}
	if err := service.DeleteRule("rule_1"); err != nil {
		t.Fatal(err)
	}
	entries, err := registry.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected registration to be removed, got %+v", entries)
	}
}

func TestResolveOnlySelectedPortConflicts(t *testing.T) {
	root := t.TempDir()
	registry := portregistry.NewAt(filepath.Join(root, "port-registry.json"))
	ownerExecutable := filepath.Join(root, "owner.exe")
	ownerConfig := filepath.Join(root, "owner.json")
	clientExecutable := filepath.Join(root, "client.exe")
	clientConfig := filepath.Join(root, "client.json")
	for _, path := range []string{ownerExecutable, ownerConfig, clientExecutable, clientConfig} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []portregistry.Entry{
		{ExecutablePath: ownerExecutable, ConfigPath: ownerConfig, ResourceID: "owner_1", ResourceType: "rule", Alias: "占用一", Port: 10993},
		{ExecutablePath: ownerExecutable, ConfigPath: ownerConfig, ResourceID: "owner_2", ResourceType: "rule", Alias: "占用二", Port: 10994},
	} {
		if err := registry.Claim(entry); err != nil {
			t.Fatal(err)
		}
	}
	service := &MyService{
		config: &models.Config{Rules: []models.ProxyRule{
			{ID: "rule_1", Alias: "冲突一", LocalPort: 10993},
			{ID: "rule_2", Alias: "冲突二", LocalPort: 10994},
		}},
		portRegistry: registry, executablePath: clientExecutable, configPath: clientConfig,
	}
	t.Cleanup(service.releaseAllPortReservations)
	if err := service.syncPortRegistryLocked(false); err != nil {
		t.Fatal(err)
	}
	if len(service.GetPendingPortConflicts()) != 2 {
		t.Fatalf("expected two conflicts, got %+v", service.GetPendingPortConflicts())
	}
	remaining, err := service.ResolvePortConflicts([]string{"rule_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ResourceID != "rule_2" {
		t.Fatalf("expected only rule_2 to remain, got %+v", remaining)
	}
	if service.config.Rules[0].LocalPort == 10993 {
		t.Fatal("selected conflict kept its old port")
	}
	if service.config.Rules[1].LocalPort != 10994 {
		t.Fatal("unselected conflict changed port")
	}
}

func TestStoppedConfiguredPortIsReserved(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	owner := &MyService{config: &models.Config{Rules: []models.ProxyRule{{ID: "rule_1", LocalPort: port}}}}
	other := &MyService{config: &models.Config{}}
	t.Cleanup(owner.releaseAllPortReservations)
	t.Cleanup(other.releaseAllPortReservations)

	owner.reserveStoppedPorts()
	// 未启动节点的端口只做记账，不再常驻监听（上千节点会耗尽句柄并卡死启动），
	// 因此这里断言的是记账结果，而不是端口对外是否可绑定。
	owner.mu.RLock()
	recorded := owner.portReservations[port]
	owner.mu.RUnlock()
	if !recorded {
		t.Fatalf("stopped configured port %d was not reserved", port)
	}
	// 跨实例的端口归属改由全局端口注册表保证（见 TestAddRuleReportsGlobalOwnerPath），
	// 未接入注册表的实例不再被本地监听挡住。
}

func TestReleasedReservationBecomesAvailable(t *testing.T) {
	service := &MyService{config: &models.Config{}}
	port := service.RecommendPort()
	if port == 0 {
		t.Fatal("expected a recommended port")
	}
	service.releaseAllPortReservations()
	if !utils.CheckPortAvailable(port) {
		t.Fatalf("port %d remained occupied after releasing reservation", port)
	}
}
