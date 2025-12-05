package assets

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// 嵌入不同平台的 xray 二进制文件
// 注意：需要将对应平台的 xray 二进制文件放置在以下目录：
// - Windows: internal/assets/xray/windows/xray.exe
// - Linux:   internal/assets/xray/linux/xray
// - macOS:   internal/assets/xray/darwin/xray

//go:embed xray/windows/xray.exe
var xrayWindows []byte

//go:embed xray/linux/xray
var xrayLinux []byte

//go:embed xray/darwin/xray
var xrayDarwin []byte

// ExtractXrayBinary 提取并返回当前平台的 xray 二进制文件路径
func ExtractXrayBinary() (string, error) {
	var binaryData []byte
	var binaryName string

	// 根据操作系统选择对应的二进制文件
	switch runtime.GOOS {
	case "windows":
		binaryData = xrayWindows
		binaryName = "xray.exe"
	case "linux":
		binaryData = xrayLinux
		binaryName = "xray"
	case "darwin":
		binaryData = xrayDarwin
		binaryName = "xray"
	default:
		return "", fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	// 检查是否成功嵌入二进制文件
	if len(binaryData) == 0 {
		return "", fmt.Errorf("未找到 %s 平台的 xray 二进制文件，请确保已将文件放置在 internal/assets/xray/%s/ 目录下", runtime.GOOS, runtime.GOOS)
	}

	// 获取可执行文件所在目录
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取可执行文件路径失败: %v", err)
	}
	exeDir := filepath.Dir(exePath)

	// 创建 xray 目录
	xrayDir := filepath.Join(exeDir, "xray-bin")
	if err := os.MkdirAll(xrayDir, 0755); err != nil {
		return "", fmt.Errorf("创建 xray 目录失败: %v", err)
	}

	// 二进制文件路径
	binaryPath := filepath.Join(xrayDir, binaryName)

	// 检查文件是否已存在且大小一致
	if fileInfo, err := os.Stat(binaryPath); err == nil {
		if int(fileInfo.Size()) == len(binaryData) {
			// 文件已存在且大小一致，直接返回
			return binaryPath, nil
		}
	}

	// 写入二进制文件
	if err := os.WriteFile(binaryPath, binaryData, 0755); err != nil {
		return "", fmt.Errorf("写入 xray 二进制文件失败: %v", err)
	}

	// 确保文件有执行权限 (Unix 系统)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(binaryPath, 0755); err != nil {
			return "", fmt.Errorf("设置执行权限失败: %v", err)
		}
	}

	return binaryPath, nil
}

// GetXrayVersion 获取嵌入的 xray 版本信息（可选功能）
func GetXrayVersion() string {
	return "embedded"
}
