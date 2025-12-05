# Xray 二进制文件目录

请将对应平台的 xray 二进制文件放置在以下位置：

- **Windows**: `windows/xray.exe`
- **Linux**: `linux/xray`
- **macOS**: `darwin/xray`

这些文件将通过 `go:embed` 嵌入到最终的可执行文件中。

## 下载 Xray-core

可以从以下地址下载 Xray-core 的官方发行版：
https://github.com/XTLS/Xray-core/releases

请下载对应平台的版本，并将 xray 可执行文件放置到相应目录。
