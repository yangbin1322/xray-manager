package assets

import (
	_ "embed"
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

// 嵌入不同平台的 sing-box 二进制文件（Hysteria2/TUIC 协议由 sing-box 内核运行）
// - Windows: internal/assets/singbox/windows/sing-box.exe
// - Linux:   internal/assets/singbox/linux/sing-box
// - macOS:   internal/assets/singbox/darwin/sing-box

//go:embed singbox/windows/sing-box.exe
var singboxWindows []byte

//go:embed singbox/linux/sing-box
var singboxLinux []byte

//go:embed singbox/darwin/sing-box
var singboxDarwin []byte

// binDirName 内核二进制提取目录（位于可执行文件同级）
const binDirName = "xray-bin"

// ExtractXrayBinary 提取并返回当前平台的 xray 二进制文件路径
func ExtractXrayBinary() (string, error) {
	var binaryData []byte
	var binaryName string

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

	return extractBinary(binaryData, binaryName, "xray", "internal/assets/xray")
}

// ExtractSingBoxBinary 提取并返回当前平台的 sing-box 二进制文件路径
func ExtractSingBoxBinary() (string, error) {
	var binaryData []byte
	var binaryName string

	switch runtime.GOOS {
	case "windows":
		binaryData = singboxWindows
		binaryName = "sing-box.exe"
	case "linux":
		binaryData = singboxLinux
		binaryName = "sing-box"
	case "darwin":
		binaryData = singboxDarwin
		binaryName = "sing-box"
	default:
		return "", fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	return extractBinary(binaryData, binaryName, "sing-box", "internal/assets/singbox")
}

// extractBinary 将嵌入的二进制数据提取到可执行文件同级的 xray-bin 目录并返回路径。
// 若目标已存在且大小一致则直接复用。core 用于错误提示，srcHint 提示放置源文件的目录。
func extractBinary(binaryData []byte, binaryName, core, srcHint string) (string, error) {
	if len(binaryData) == 0 {
		return "", fmt.Errorf("未找到 %s 平台的 %s 二进制文件，请确保已将文件放置在 %s/%s/ 目录下",
			runtime.GOOS, core, srcHint, runtime.GOOS)
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取可执行文件路径失败: %v", err)
	}
	exeDir := filepath.Dir(exePath)

	binDir := filepath.Join(exeDir, binDirName)
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("创建 %s 目录失败: %v", binDirName, err)
	}

	binaryPath := filepath.Join(binDir, binaryName)

	// 已存在且大小一致，直接复用
	if fileInfo, err := os.Stat(binaryPath); err == nil {
		if int(fileInfo.Size()) == len(binaryData) {
			return binaryPath, nil
		}
	}

	if err := os.WriteFile(binaryPath, binaryData, 0755); err != nil {
		return "", fmt.Errorf("写入 %s 二进制文件失败: %v", core, err)
	}

	// Unix 系统确保执行权限
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
