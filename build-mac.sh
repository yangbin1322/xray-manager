#!/bin/bash

# Mac 应用打包脚本
# 使用方法：./build-mac.sh

set -e

APP_NAME="Xray管理器"
BUNDLE_ID="com.xraymanager.app"
VERSION="1.0.0"
BUILD_DIR="build"
APP_DIR="${BUILD_DIR}/${APP_NAME}.app"

echo "🚀 开始构建 Mac 应用..."

# 1. 清理旧的构建
if [ -d "$BUILD_DIR" ]; then
    echo "📦 清理旧的构建文件..."
    rm -rf "$BUILD_DIR"
fi

# 2. 构建前端
echo "🎨 构建前端资源..."
cd frontend
npm install
npm run build
cd ..

# 3. 编译 Go 程序
echo "⚙️  编译 Go 程序..."
CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build \
    -tags desktop,production \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "${BUILD_DIR}/xray-manager"

# 4. 创建 .app 目录结构
echo "📂 创建应用目录结构..."
mkdir -p "${APP_DIR}/Contents/MacOS"
mkdir -p "${APP_DIR}/Contents/Resources"

# 5. 移动可执行文件
echo "📋 复制可执行文件..."
mv "${BUILD_DIR}/xray-manager" "${APP_DIR}/Contents/MacOS/"
chmod +x "${APP_DIR}/Contents/MacOS/xray-manager"

# 6. 创建 Info.plist
echo "📝 创建 Info.plist..."
cat > "${APP_DIR}/Contents/Info.plist" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>xray-manager</string>
    <key>CFBundleIdentifier</key>
    <string>${BUNDLE_ID}</string>
    <key>CFBundleName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key>
    <string>${APP_NAME}</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.13</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSUIElement</key>
    <false/>
</dict>
</plist>
EOF

# 7. 创建 DMG（可选）
if command -v hdiutil &> /dev/null; then
    echo "💿 创建 DMG 安装包..."
    DMG_NAME="${APP_NAME}-${VERSION}.dmg"

    # 创建临时目录
    mkdir -p "${BUILD_DIR}/dmg"
    cp -r "${APP_DIR}" "${BUILD_DIR}/dmg/"

    # 创建 DMG
    hdiutil create -volname "${APP_NAME}" \
        -srcfolder "${BUILD_DIR}/dmg" \
        -ov -format UDZO \
        "${BUILD_DIR}/${DMG_NAME}"

    # 清理临时文件
    rm -rf "${BUILD_DIR}/dmg"

    echo "✅ DMG 已创建: ${BUILD_DIR}/${DMG_NAME}"
fi

# 8. 创建 ZIP 压缩包
echo "📦 创建 ZIP 压缩包..."
cd "${BUILD_DIR}"
zip -r "${APP_NAME}-${VERSION}.zip" "${APP_NAME}.app"
cd ..

echo ""
echo "✅ 构建完成！"
echo "📍 输出目录: ${BUILD_DIR}/"
echo "   - ${APP_NAME}.app"
if [ -f "${BUILD_DIR}/${APP_NAME}-${VERSION}.dmg" ]; then
    echo "   - ${APP_NAME}-${VERSION}.dmg"
fi
echo "   - ${APP_NAME}-${VERSION}.zip"
echo ""
echo "💡 提示："
echo "   1. 双击 .app 文件运行应用"
echo "   2. 或将 .app 拖入应用程序文件夹"
echo "   3. 首次运行可能需要在系统设置中允许"
