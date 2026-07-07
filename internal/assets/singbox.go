package assets

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FindSingBoxBinary 查找 sing-box 内核二进制文件
// Hysteria2 / TUIC 协议由 sing-box 内核运行（Xray-core 不支持这两种协议）。
// 查找顺序：可执行文件目录下的 xray-bin/、可执行文件目录、系统 PATH。
func FindSingBoxBinary() (string, error) {
	binaryName := "sing-box"
	if runtime.GOOS == "windows" {
		binaryName = "sing-box.exe"
	}

	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		candidates := []string{
			filepath.Join(exeDir, "xray-bin", binaryName),
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

	return "", fmt.Errorf("未找到 sing-box 内核（Hysteria2/TUIC 协议需要），请从 https://github.com/SagerNet/sing-box/releases 下载并将 %s 放入程序目录的 xray-bin 文件夹中", binaryName)
}
