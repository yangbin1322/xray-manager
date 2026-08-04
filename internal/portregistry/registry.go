package portregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const lockTimeout = 5 * time.Second

type Entry struct {
	ExecutablePath string `json:"executablePath"`
	ConfigPath     string `json:"configPath"`
	ResourceID     string `json:"resourceId"`
	ResourceType   string `json:"resourceType"`
	Alias          string `json:"alias"`
	Port           int    `json:"port"`
	Temporary      bool   `json:"temporary,omitempty"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
	UpdatedAt      string `json:"updatedAt"`
}

type ConflictError struct {
	RequestedPort int
	Owner         Entry
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("本地端口 %d 已被 %s 中的%s「%s」使用（配置: %s）",
		e.RequestedPort, e.Owner.ExecutablePath, typeLabel(e.Owner.ResourceType), e.Owner.Alias, e.Owner.ConfigPath)
}

type fileData struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

type Registry struct {
	path     string
	lockPath string
}

func New() (*Registry, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return NewAt(filepath.Join(configDir, "xray-manager", "port-registry.json")), nil
}

func NewAt(path string) *Registry {
	return &Registry{path: path, lockPath: path + ".lock"}
}

func (r *Registry) Path() string { return r.path }

func (r *Registry) Claim(entry Entry) error {
	return r.withLock(func(data *fileData) error {
		cleanup(data)
		normalizeEntry(&entry)
		filtered := data.Entries[:0]
		for _, existing := range data.Entries {
			if sameResource(existing, entry) || (existing.Temporary && sameOwner(existing, entry) && existing.Port == entry.Port) {
				continue
			}
			filtered = append(filtered, existing)
		}
		data.Entries = filtered
		if owner := findPortOwner(data.Entries, entry.Port); owner != nil {
			return &ConflictError{RequestedPort: entry.Port, Owner: *owner}
		}
		data.Entries = append(data.Entries, entry)
		return nil
	})
}

func (r *Registry) ReserveTemporary(entry Entry, lifetime time.Duration) error {
	entry.Temporary = true
	entry.ExpiresAt = time.Now().Add(lifetime).UTC().Format(time.RFC3339Nano)
	return r.Claim(entry)
}

// ReserveTemporaryBatch 在一次文件锁+写入内批量预留多个端口，返回实际预留成功的条目。
// 逐个调用 ReserveTemporary 时每个端口都要抢文件锁、读全表、写回磁盘，导入上万节点时
// 这部分开销是主要瓶颈；批量版本只做一次 IO，并用端口索引避免逐条线性查冲突。
// 与 Claim 一致：被其他实例占用的端口会被跳过（不计入返回值），而不是让整批失败。
func (r *Registry) ReserveTemporaryBatch(entries []Entry, lifetime time.Duration) ([]Entry, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	var reserved []Entry
	err := r.withLock(func(data *fileData) error {
		cleanup(data)

		expiresAt := time.Now().Add(lifetime).UTC().Format(time.RFC3339Nano)
		// 端口 -> data.Entries 下标，避免每个待预留端口都全表扫描
		portIndex := make(map[int]int, len(data.Entries))
		for i := range data.Entries {
			portIndex[data.Entries[i].Port] = i
		}

		reserved = make([]Entry, 0, len(entries))
		for _, entry := range entries {
			entry.Temporary = true
			entry.ExpiresAt = expiresAt
			normalizeEntry(&entry)

			if idx, exists := portIndex[entry.Port]; exists {
				existing := data.Entries[idx]
				// 端口已被本实例的临时预留占着：视为已预留，复用即可
				if existing.Temporary && sameOwner(existing, entry) {
					reserved = append(reserved, existing)
					continue
				}
				// 被别的实例/资源占用，跳过该端口
				continue
			}

			portIndex[entry.Port] = len(data.Entries)
			data.Entries = append(data.Entries, entry)
			reserved = append(reserved, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reserved, nil
}

func (r *Registry) Release(executablePath, configPath, resourceID string) error {
	return r.withLock(func(data *fileData) error {
		cleanup(data)
		filtered := data.Entries[:0]
		for _, entry := range data.Entries {
			if samePath(entry.ExecutablePath, executablePath) && samePath(entry.ConfigPath, configPath) && entry.ResourceID == resourceID {
				continue
			}
			filtered = append(filtered, entry)
		}
		data.Entries = filtered
		return nil
	})
}

func (r *Registry) ReplaceOwner(executablePath, configPath string, desired []Entry) (map[string]*ConflictError, error) {
	conflicts := make(map[string]*ConflictError)
	err := r.withLock(func(data *fileData) error {
		cleanup(data)
		filtered := data.Entries[:0]
		for _, entry := range data.Entries {
			if samePath(entry.ExecutablePath, executablePath) && samePath(entry.ConfigPath, configPath) && !entry.Temporary {
				continue
			}
			filtered = append(filtered, entry)
		}
		data.Entries = filtered
		for _, entry := range desired {
			normalizeEntry(&entry)
			filtered = data.Entries[:0]
			for _, existing := range data.Entries {
				if existing.Temporary && sameOwner(existing, entry) && existing.Port == entry.Port {
					continue
				}
				filtered = append(filtered, existing)
			}
			data.Entries = filtered
			if owner := findPortOwner(data.Entries, entry.Port); owner != nil {
				conflicts[entry.ResourceID] = &ConflictError{RequestedPort: entry.Port, Owner: *owner}
				continue
			}
			data.Entries = append(data.Entries, entry)
		}
		return nil
	})
	return conflicts, err
}

func (r *Registry) Entries() ([]Entry, error) {
	var result []Entry
	err := r.withLock(func(data *fileData) error {
		cleanup(data)
		result = append(result, data.Entries...)
		return nil
	})
	return result, err
}

func (r *Registry) withLock(action func(*fileData) error) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err := os.Mkdir(r.lockPath, 0700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(r.lockPath); statErr == nil && time.Since(info.ModTime()) > lockTimeout {
			_ = os.RemoveAll(r.lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待全局端口注册表锁超时")
		}
		time.Sleep(25 * time.Millisecond)
	}
	defer os.RemoveAll(r.lockPath)

	data, err := r.load()
	if err != nil {
		return err
	}
	if err := action(data); err != nil {
		return err
	}
	return r.save(data)
}

func (r *Registry) load() (*fileData, error) {
	data := &fileData{Version: 1, Entries: []Entry{}}
	raw, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("读取全局端口注册表失败: %w", err)
	}
	return data, nil
}

func (r *Registry) save(data *fileData) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporaryPath := r.path + ".tmp"
	if err := os.WriteFile(temporaryPath, raw, 0600); err != nil {
		return err
	}
	_ = os.Remove(r.path)
	return os.Rename(temporaryPath, r.path)
}

func cleanup(data *fileData) {
	now := time.Now()
	filtered := data.Entries[:0]
	for _, entry := range data.Entries {
		if entry.Temporary {
			expiresAt, err := time.Parse(time.RFC3339Nano, entry.ExpiresAt)
			if err != nil || now.After(expiresAt) {
				continue
			}
		}
		if !entry.Temporary && (!pathExists(entry.ExecutablePath) || !pathExists(entry.ConfigPath)) {
			continue
		}
		filtered = append(filtered, entry)
	}
	data.Entries = filtered
}

func normalizeEntry(entry *Entry) {
	entry.ExecutablePath = cleanPath(entry.ExecutablePath)
	entry.ConfigPath = cleanPath(entry.ConfigPath)
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
}

func findPortOwner(entries []Entry, port int) *Entry {
	for i := range entries {
		if entries[i].Port == port {
			return &entries[i]
		}
	}
	return nil
}

func sameResource(left, right Entry) bool {
	return sameOwner(left, right) && left.ResourceID == right.ResourceID
}

func sameOwner(left, right Entry) bool {
	return samePath(left.ExecutablePath, right.ExecutablePath) && samePath(left.ConfigPath, right.ConfigPath)
}

func samePath(left, right string) bool {
	return strings.EqualFold(cleanPath(left), cleanPath(right))
}

func cleanPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func typeLabel(resourceType string) string {
	switch resourceType {
	case "rule":
		return "规则"
	case "loadBalancer":
		return "故障转移"
	case "chainProxy":
		return "链式代理"
	default:
		return "资源"
	}
}
