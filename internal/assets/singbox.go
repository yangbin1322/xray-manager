package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FindSingBoxBinary 获取 sing-box 内核二进制文件路径。
// Hysteria2 / TUIC 协议由 sing-box 内核运行（Xray-core 不支持这两种协议）。
// 优先提取内置的 sing-box；提取失败时回退查找外部二进制（可执行文件目录/PATH），
// 便于用户用自备版本覆盖。
func FindSingBoxBinary() (string, error) {
	// 优先使用内置（embed）的 sing-box
	if path, err := ExtractSingBoxBinary(); err == nil {
		return path, nil
	}

	// 回退：查找外部二进制
	binaryName := "sing-box"
	if runtime.GOOS == "windows" {
		binaryName = "sing-box.exe"
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates := []string{
			filepath.Join(exeDir, binDirName, binaryName),
			filepath.Join(exeDir, binaryName),
		}
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}

	if path, err := exec.LookPath(binaryName); err == nil {
		return path, nil
	}

	// 内置提取失败且未找到外部二进制，返回内置提取的错误
	_, err := ExtractSingBoxBinary()
	return "", err
}
