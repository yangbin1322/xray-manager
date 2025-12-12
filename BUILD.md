# 📦 构建指南

本文档介绍如何在不同平台上构建和打包 Xray 管理器。

## 🍎 macOS 平台

### 前置要求

1. **macOS 系统** (10.13 或更高版本)
2. **Go 1.21+**
   ```bash
   brew install go
   ```
3. **Node.js 和 npm**
   ```bash
   brew install node
   ```

### 方法一：使用自动化脚本（推荐）

```bash
# 在项目根目录执行
./build-mac.sh
```

这将自动完成：
- ✅ 构建前端资源
- ✅ 编译 Go 程序
- ✅ 创建 .app 应用包
- ✅ 生成 DMG 安装包（如果可用）
- ✅ 创建 ZIP 压缩包

构建产物位于 `build/` 目录：
- `Xray管理器.app` - 可直接运行的应用
- `Xray管理器-1.0.0.dmg` - DMG 安装包
- `Xray管理器-1.0.0.zip` - ZIP 压缩包

### 方法二：手动构建

```bash
# 1. 构建前端
cd frontend
npm install
npm run build
cd ..

# 2. 编译程序
CGO_ENABLED=1 go build -tags desktop,production -ldflags "-s -w" -o xray-manager

# 3. 运行
./xray-manager
```

### 创建 Universal Binary（支持 Intel + Apple Silicon）

```bash
# 编译 AMD64 版本
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build \
    -tags desktop,production \
    -ldflags "-s -w" \
    -o xray-manager-amd64

# 编译 ARM64 版本
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build \
    -tags desktop,production \
    -ldflags "-s -w" \
    -o xray-manager-arm64

# 合并为 Universal Binary
lipo -create -output xray-manager xray-manager-amd64 xray-manager-arm64

# 清理临时文件
rm xray-manager-amd64 xray-manager-arm64
```

### 代码签名（可选，用于分发）

```bash
# 使用 Apple 开发者证书签名
codesign --deep --force --verify --verbose \
    --sign "Developer ID Application: Your Name" \
    "Xray管理器.app"

# 公证（Notarization）
xcrun altool --notarize-app \
    --primary-bundle-id "com.xraymanager.app" \
    --username "your@email.com" \
    --password "@keychain:AC_PASSWORD" \
    --file "Xray管理器-1.0.0.dmg"
```

## 🪟 Windows 平台

### 前置要求

1. **Windows 10/11**
2. **Go 1.21+**
3. **Node.js 和 npm**
4. **WebView2 Runtime**（通常已预装）

### 构建步骤

```bash
# 1. 构建前端
cd frontend
npm install
npm run build
cd ..

# 2. 编译程序（生成 .exe）
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build ^
    -tags desktop,production ^
    -ldflags "-s -w -H windowsgui" ^
    -o xray-manager.exe

# 3. 运行
xray-manager.exe
```

### 创建安装包（使用 NSIS 或 Inno Setup）

可以使用 NSIS 或 Inno Setup 创建安装包：

**使用 Inno Setup:**

创建 `installer.iss` 文件：

```ini
[Setup]
AppName=Xray管理器
AppVersion=1.0.0
DefaultDirName={pf}\XrayManager
DefaultGroupName=Xray管理器
OutputDir=build
OutputBaseFilename=XrayManager-Setup-1.0.0

[Files]
Source: "xray-manager.exe"; DestDir: "{app}"

[Icons]
Name: "{group}\Xray管理器"; Filename: "{app}\xray-manager.exe"
Name: "{userdesktop}\Xray管理器"; Filename: "{app}\xray-manager.exe"
```

然后编译：
```bash
iscc installer.iss
```

## 🐧 Linux 平台

### 前置要求

1. **Linux 系统** (Ubuntu/Debian/Fedora 等)
2. **Go 1.21+**
3. **Node.js 和 npm**
4. **系统依赖**
   ```bash
   # Ubuntu/Debian
   sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev

   # Fedora
   sudo dnf install gtk3-devel webkit2gtk3-devel
   ```

### 构建步骤

```bash
# 1. 构建前端
cd frontend
npm install
npm run build
cd ..

# 2. 编译程序
CGO_ENABLED=1 go build -tags desktop,production -ldflags "-s -w" -o xray-manager

# 3. 运行
./xray-manager
```

### 创建 .deb 包（Debian/Ubuntu）

```bash
# 创建目录结构
mkdir -p xray-manager_1.0.0/DEBIAN
mkdir -p xray-manager_1.0.0/usr/bin
mkdir -p xray-manager_1.0.0/usr/share/applications

# 复制可执行文件
cp xray-manager xray-manager_1.0.0/usr/bin/

# 创建控制文件
cat > xray-manager_1.0.0/DEBIAN/control << EOF
Package: xray-manager
Version: 1.0.0
Section: net
Priority: optional
Architecture: amd64
Maintainer: XrayManager <yangbin1322@gmail.com>
Description: Xray 代理规则管理工具
 提供图形化界面管理 Xray 代理规则
EOF

# 创建 desktop 文件
cat > xray-manager_1.0.0/usr/share/applications/xray-manager.desktop << EOF
[Desktop Entry]
Name=Xray管理器
Exec=/usr/bin/xray-manager
Type=Application
Categories=Network;
EOF

# 构建 .deb 包
dpkg-deb --build xray-manager_1.0.0
```

### 创建 AppImage（通用 Linux）

使用 `appimagetool` 创建 AppImage：

```bash
# 下载 appimagetool
wget https://github.com/AppImage/AppImageKit/releases/download/continuous/appimagetool-x86_64.AppImage
chmod +x appimagetool-x86_64.AppImage

# 创建 AppDir 结构
mkdir -p XrayManager.AppDir/usr/bin
cp xray-manager XrayManager.AppDir/usr/bin/

# 创建 .desktop 文件
cat > XrayManager.AppDir/xray-manager.desktop << EOF
[Desktop Entry]
Name=Xray管理器
Exec=xray-manager
Type=Application
Categories=Network;
EOF

# 创建 AppRun
cat > XrayManager.AppDir/AppRun << 'EOF'
#!/bin/bash
SELF=$(readlink -f "$0")
HERE=${SELF%/*}
export PATH="${HERE}/usr/bin:${PATH}"
exec "${HERE}/usr/bin/xray-manager" "$@"
EOF
chmod +x XrayManager.AppDir/AppRun

# 构建 AppImage
./appimagetool-x86_64.AppImage XrayManager.AppDir XrayManager-1.0.0-x86_64.AppImage
```

## 🔧 交叉编译

### 在 macOS 上编译其他平台

```bash
# 编译 Windows 版本
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -tags desktop,production -o xray-manager.exe

# 编译 Linux 版本
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags desktop,production -o xray-manager
```

## 📋 构建选项说明

### Go 编译标签 (Build Tags)
- `desktop`: 启用桌面应用特性
- `production`: 生产环境构建，禁用调试信息

### ldflags 参数
- `-s`: 去除符号表
- `-w`: 去除 DWARF 调试信息
- `-H windowsgui`: (Windows) 隐藏控制台窗口
- `-X main.version=1.0.0`: 设置版本号

## 🐛 常见问题

### macOS: "应用已损坏，无法打开"

```bash
# 移除隔离属性
xattr -cr "Xray管理器.app"

# 或者在系统设置中允许运行
sudo spctl --master-disable
```

### Windows: SmartScreen 警告

需要使用有效的代码签名证书对程序进行签名。

### Linux: 缺少依赖库

```bash
# 检查依赖
ldd xray-manager

# 安装缺失的库
sudo apt install libgtk-3-0 libwebkit2gtk-4.0-37
```

## 📦 发布清单

发布新版本前的检查清单：

- [ ] 更新版本号（main.go, package.json, wails.json）
- [ ] 更新 CHANGELOG.md
- [ ] 构建所有平台的安装包
- [ ] 测试每个平台的安装包
- [ ] 代码签名（macOS/Windows）
- [ ] 创建 GitHub Release
- [ ] 上传安装包到 Release
- [ ] 更新文档

## 📚 相关资源

- [Wails 文档](https://wails.io/)
- [Go 交叉编译](https://golang.org/doc/install/source#environment)
- [macOS 应用分发](https://developer.apple.com/documentation/security/notarizing_macos_software_before_distribution)
