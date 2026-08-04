package portregistry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOwner(t *testing.T, root, name string) (string, string) {
	t.Helper()
	executable := filepath.Join(root, name+".exe")
	config := filepath.Join(root, name+".json")
	if err := os.WriteFile(executable, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	return executable, config
}

func TestClaimConflictIncludesOwnerDetails(t *testing.T) {
	root := t.TempDir()
	registry := NewAt(filepath.Join(root, "registry.json"))
	executableA, configA := testOwner(t, root, "client-a")
	executableB, configB := testOwner(t, root, "client-b")

	if err := registry.Claim(Entry{ExecutablePath: executableA, ConfigPath: configA, ResourceID: "a", ResourceType: "rule", Alias: "香港节点", Port: 10808}); err != nil {
		t.Fatal(err)
	}
	err := registry.Claim(Entry{ExecutablePath: executableB, ConfigPath: configB, ResourceID: "b", ResourceType: "rule", Alias: "日本节点", Port: 10808})
	if err == nil || !strings.Contains(err.Error(), executableA) || !strings.Contains(err.Error(), configA) || !strings.Contains(err.Error(), "香港节点") {
		t.Fatalf("expected detailed conflict, got %v", err)
	}

	if err := registry.Release(executableA, configA, "a"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Claim(Entry{ExecutablePath: executableB, ConfigPath: configB, ResourceID: "b", ResourceType: "rule", Alias: "日本节点", Port: 10808}); err != nil {
		t.Fatalf("claim after release failed: %v", err)
	}
}

func TestEntriesCleansDeletedOwnersAndExpiredReservations(t *testing.T) {
	root := t.TempDir()
	registry := NewAt(filepath.Join(root, "registry.json"))
	executable, config := testOwner(t, root, "client")
	executableDeleted, configKept := testOwner(t, root, "deleted-client")
	if err := registry.Claim(Entry{ExecutablePath: executable, ConfigPath: config, ResourceID: "rule", Port: 10808}); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReserveTemporary(Entry{ExecutablePath: executable, ConfigPath: config, ResourceID: "temporary", Port: 10809}, -time.Second); err != nil {
		t.Fatal(err)
	}
	if err := registry.Claim(Entry{ExecutablePath: executableDeleted, ConfigPath: configKept, ResourceID: "deleted-executable", Port: 10810}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(executableDeleted); err != nil {
		t.Fatal(err)
	}
	entries, err := registry.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected stale entries to be cleaned, got %+v", entries)
	}
}

func TestReplaceOwnerRemovesDeletedResources(t *testing.T) {
	root := t.TempDir()
	registry := NewAt(filepath.Join(root, "registry.json"))
	executable, config := testOwner(t, root, "client")
	entries := []Entry{
		{ExecutablePath: executable, ConfigPath: config, ResourceID: "keep", Port: 10808},
		{ExecutablePath: executable, ConfigPath: config, ResourceID: "delete", Port: 10809},
	}
	if conflicts, err := registry.ReplaceOwner(executable, config, entries); err != nil || len(conflicts) != 0 {
		t.Fatalf("initial replace failed: conflicts=%v err=%v", conflicts, err)
	}
	if conflicts, err := registry.ReplaceOwner(executable, config, entries[:1]); err != nil || len(conflicts) != 0 {
		t.Fatalf("second replace failed: conflicts=%v err=%v", conflicts, err)
	}
	actual, err := registry.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 1 || actual[0].ResourceID != "keep" {
		t.Fatalf("unexpected entries: %+v", actual)
	}
}

func TestReplaceOwnerConvertsTemporaryReservation(t *testing.T) {
	root := t.TempDir()
	registry := NewAt(filepath.Join(root, "registry.json"))
	executable, config := testOwner(t, root, "client")
	if err := registry.ReserveTemporary(Entry{ExecutablePath: executable, ConfigPath: config, ResourceID: "temporary", Port: 10808}, time.Minute); err != nil {
		t.Fatal(err)
	}
	desired := []Entry{{ExecutablePath: executable, ConfigPath: config, ResourceID: "rule", ResourceType: "rule", Alias: "节点", Port: 10808}}
	conflicts, err := registry.ReplaceOwner(executable, config, desired)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("replace failed: conflicts=%v err=%v", conflicts, err)
	}
	entries, err := registry.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Temporary || entries[0].ResourceID != "rule" {
		t.Fatalf("temporary reservation was not converted: %+v", entries)
	}
}

func TestReserveTemporaryBatchSkipsPortsOwnedByOthers(t *testing.T) {
	root := t.TempDir()
	registry := NewAt(filepath.Join(root, "registry.json"))
	executableA, configA := testOwner(t, root, "client-a")
	executableB, configB := testOwner(t, root, "client-b")

	// 另一个实例先占用 10810
	if err := registry.Claim(Entry{ExecutablePath: executableA, ConfigPath: configA, ResourceID: "a", ResourceType: "rule", Alias: "香港节点", Port: 10810}); err != nil {
		t.Fatal(err)
	}

	entries := []Entry{
		{ExecutablePath: executableB, ConfigPath: configB, ResourceID: "r1", ResourceType: "reservation", Port: 10809},
		{ExecutablePath: executableB, ConfigPath: configB, ResourceID: "r2", ResourceType: "reservation", Port: 10810},
		{ExecutablePath: executableB, ConfigPath: configB, ResourceID: "r3", ResourceType: "reservation", Port: 10811},
	}
	reserved, err := registry.ReserveTemporaryBatch(entries, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if len(reserved) != 2 {
		t.Fatalf("expected 2 reserved ports, got %d", len(reserved))
	}
	for _, entry := range reserved {
		if entry.Port == 10810 {
			t.Fatalf("port 10810 is owned by another client and must be skipped")
		}
		if !entry.Temporary || entry.ExpiresAt == "" {
			t.Fatalf("reserved entry must be temporary with an expiry: %+v", entry)
		}
	}

	// 已预留的端口不能再被其他实例抢走
	if err := registry.Claim(Entry{ExecutablePath: executableA, ConfigPath: configA, ResourceID: "a2", ResourceType: "rule", Alias: "日本节点", Port: 10809}); err == nil {
		t.Fatal("expected conflict claiming a port reserved by the batch")
	}
}

func TestReserveTemporaryBatchIsIdempotentForSameOwner(t *testing.T) {
	root := t.TempDir()
	registry := NewAt(filepath.Join(root, "registry.json"))
	executable, config := testOwner(t, root, "client")

	entries := []Entry{
		{ExecutablePath: executable, ConfigPath: config, ResourceID: "r1", ResourceType: "reservation", Port: 20000},
		{ExecutablePath: executable, ConfigPath: config, ResourceID: "r2", ResourceType: "reservation", Port: 20001},
	}
	if _, err := registry.ReserveTemporaryBatch(entries, time.Minute); err != nil {
		t.Fatal(err)
	}
	// 同一实例重复预留同样的端口应当成功（复用已有预留），而不是被判为冲突
	reserved, err := registry.ReserveTemporaryBatch(entries, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserved) != 2 {
		t.Fatalf("expected same owner to keep both reservations, got %d", len(reserved))
	}
}
