package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	GitHubOwner = "yangbin1322"
	GitHubRepo  = "xray-manager"
	apiLatest   = "https://api.github.com/repos/%s/%s/releases/latest"
)

// Info 更新检查结果
type Info struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	ReleaseName    string `json:"releaseName"`
	ReleaseNotes   string `json:"releaseNotes"`
	ReleaseURL     string `json:"releaseURL"`
	AssetName      string `json:"assetName"`
	AssetURL       string `json:"assetURL"`
	AssetSize      int64  `json:"assetSize"`
	PublishedAt    string `json:"publishedAt"`
	CheckedAt      string `json:"checkedAt"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

// NormalizeVersion 去掉 v/V 前缀并修剪空白
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

// CompareVersions 比较两个版本号。返回 -1(a<b) / 0 / 1(a>b)。
// 支持 x / x.y / x.y.z，非法段按 0 处理。
func CompareVersions(a, b string) int {
	ap := parseVersion(a)
	bp := parseVersion(b)
	n := len(ap)
	if len(bp) > n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) []int {
	v = NormalizeVersion(v)
	// 去掉预发布后缀 1.2.3-beta
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// CheckLatest 从 GitHub Releases 检查最新版本
func CheckLatest(currentVersion string) (*Info, error) {
	url := fmt.Sprintf(apiLatest, GitHubOwner, GitHubRepo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "xray-manager/"+NormalizeVersion(currentVersion))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub Releases 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 Releases 响应失败: %w", err)
	}
	if rel.Draft {
		return nil, fmt.Errorf("最新 Release 仍为草稿")
	}

	info := &Info{
		CurrentVersion: NormalizeVersion(currentVersion),
		LatestVersion:  NormalizeVersion(rel.TagName),
		ReleaseName:    rel.Name,
		ReleaseNotes:   rel.Body,
		ReleaseURL:     rel.HTMLURL,
		PublishedAt:    rel.PublishedAt,
		CheckedAt:      time.Now().Format(time.RFC3339),
	}
	info.HasUpdate = CompareVersions(info.CurrentVersion, info.LatestVersion) < 0

	asset := pickAsset(rel.Assets)
	if asset != nil {
		info.AssetName = asset.Name
		info.AssetURL = asset.BrowserDownloadURL
		info.AssetSize = asset.Size
	}
	return info, nil
}

func pickAsset(assets []ghAsset) *ghAsset {
	if len(assets) == 0 {
		return nil
	}
	osName := runtime.GOOS
	arch := runtime.GOARCH

	score := func(name string) int {
		n := strings.ToLower(name)
		s := 0
		switch osName {
		case "windows":
			if strings.Contains(n, "windows") || strings.Contains(n, "win") || strings.HasSuffix(n, ".exe") {
				s += 10
			}
			if strings.HasSuffix(n, ".exe") {
				s += 3
			}
			if strings.HasSuffix(n, ".msi") || strings.HasSuffix(n, ".zip") {
				s += 2
			}
			if strings.Contains(n, "linux") || strings.Contains(n, "darwin") || strings.Contains(n, "macos") {
				s -= 20
			}
		case "darwin":
			if strings.Contains(n, "darwin") || strings.Contains(n, "macos") || strings.Contains(n, "mac") {
				s += 10
			}
			if strings.HasSuffix(n, ".dmg") || strings.HasSuffix(n, ".zip") {
				s += 3
			}
			if strings.Contains(n, "windows") || strings.Contains(n, "linux") || strings.HasSuffix(n, ".exe") {
				s -= 20
			}
		case "linux":
			if strings.Contains(n, "linux") {
				s += 10
			}
			if !strings.Contains(n, ".") || strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".zip") {
				s += 2
			}
			if strings.Contains(n, "windows") || strings.Contains(n, "darwin") || strings.Contains(n, "macos") || strings.HasSuffix(n, ".exe") || strings.HasSuffix(n, ".dmg") {
				s -= 20
			}
		}
		if strings.Contains(n, arch) {
			s += 5
		}
		if arch == "amd64" && (strings.Contains(n, "x64") || strings.Contains(n, "x86_64") || strings.Contains(n, "amd64")) {
			s += 5
		}
		if arch == "arm64" && (strings.Contains(n, "arm64") || strings.Contains(n, "aarch64")) {
			s += 5
		}
		// 优先可直接替换的二进制
		base := filepath.Base(n)
		if base == "xray-manager" || base == "xray-manager.exe" {
			s += 4
		}
		return s
	}

	var best *ghAsset
	bestScore := -1000
	for i := range assets {
		s := score(assets[i].Name)
		if s > bestScore {
			bestScore = s
			best = &assets[i]
		}
	}
	if bestScore <= 0 {
		return nil
	}
	return best
}

// Download 下载资源到目标路径
func Download(assetURL, destPath string) error {
	if assetURL == "" {
		return fmt.Errorf("没有可下载的更新资源")
	}
	req, err := http.NewRequest(http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "xray-manager")
	req.Header.Set("Accept", "application/octet-stream")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(destPath)
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ApplyDownloadedUpdate 应用已下载的更新包。
// 对可执行文件：安排退出后替换；对安装包/DMG：打开文件让用户安装。
// 返回 needQuit=true 时调用方应退出进程。
func ApplyDownloadedUpdate(downloadedPath string) (needQuit bool, err error) {
	lower := strings.ToLower(downloadedPath)
	switch {
	case strings.HasSuffix(lower, ".dmg"), strings.HasSuffix(lower, ".msi"), strings.HasSuffix(lower, ".pkg"):
		return false, openPath(downloadedPath)
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		// 压缩包：打开所在目录，由用户解压替换
		return false, openPath(filepath.Dir(downloadedPath))
	default:
		// 视为可执行文件，安排替换
		return true, scheduleReplace(downloadedPath)
	}
}

func scheduleReplace(newBinary string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前程序路径失败: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	// 解析符号链接（macOS 等）
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	switch runtime.GOOS {
	case "windows":
		return scheduleReplaceWindows(exe, newBinary)
	default:
		return scheduleReplaceUnix(exe, newBinary)
	}
}

// toCRLF 把 LF 换行统一为 CRLF（已是 CRLF 的不重复转换）。
func toCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func scheduleReplaceWindows(exe, newBinary string) error {
	bat := filepath.Join(os.TempDir(), fmt.Sprintf("xray-manager-update-%d.bat", time.Now().UnixNano()))
	// 等待当前进程退出后复制并重启
	content := fmt.Sprintf(`@echo off
setlocal
set "TARGET=%s"
set "SOURCE=%s"
:wait
timeout /t 1 /nobreak >nul
tasklist /FI "IMAGENAME eq %s" 2>NUL | find /I "%s" >NUL
if not errorlevel 1 goto wait
copy /Y "%%SOURCE%%" "%%TARGET%%" >nul
start "" "%%TARGET%%"
del "%%~f0"
`, exe, newBinary, filepath.Base(exe), filepath.Base(exe))
	// 批处理必须以 CRLF 换行：cmd.exe 解析 .bat 时按字节偏移推进，安装目录
	// 含中文（或其他多字节字符）时，纯 LF 会让它在错误的位置切行，表现为
	// 'ARGET'、'local' 之类的残缺命令，变量赋值失败，更新静默中止。
	// 纯 ASCII 路径下偏移恰好对齐，所以该缺陷只在中文路径上暴露。
	if err := os.WriteFile(bat, []byte(toCRLF(content)), 0644); err != nil {
		return err
	}
	cmd := exec.Command("cmd", "/C", "start", "", bat)
	return cmd.Start()
}

func scheduleReplaceUnix(exe, newBinary string) error {
	script := filepath.Join(os.TempDir(), fmt.Sprintf("xray-manager-update-%d.sh", time.Now().UnixNano()))
	content := fmt.Sprintf(`#!/bin/sh
TARGET=%q
SOURCE=%q
PID=%d
while kill -0 "$PID" 2>/dev/null; do
  sleep 1
done
chmod +x "$SOURCE"
cp -f "$SOURCE" "$TARGET" || mv -f "$SOURCE" "$TARGET"
chmod +x "$TARGET"
nohup "$TARGET" >/dev/null 2>&1 &
rm -f %q
`, exe, newBinary, os.Getpid(), script)
	if err := os.WriteFile(script, []byte(content), 0755); err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", script)
	return cmd.Start()
}

func openPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
