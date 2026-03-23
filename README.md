# Xray Manager

基于 [Wails v3](https://wails.io/) + [Vue 3](https://vuejs.org/) 开发的 [Xray-core](https://github.com/XTLS/Xray-core) 代理规则可视化管理工具，提供简洁易用的图形界面来管理 Xray 代理规则。

支持 Windows、macOS、Linux 三平台。

## 功能特性

**代理协议**
- 支持 Shadowsocks、VMess、VLESS、Trojan、HTTP、SOCKS 协议
- 支持 TCP、WebSocket、gRPC、HTTP/2 传输协议
- 支持 TLS / Reality 传输层安全配置

**规则管理**
- 添加、编辑、删除代理规则，支持拖拽排序
- 导入/导出规则为 JSON 文件
- 解析分享链接（vmess://、vless://、ss://、trojan://）
- 批量选择、启动、停止、删除

**高级功能**
- 链式代理：多节点链式串联，可拖拽调整节点顺序
- 负载均衡：多节点自动分配流量
- 系统代理：一键将节点设为系统代理（支持普通节点、链式代理、负载均衡）
- 节点测速：TCP 延迟测试 + HTTP 下载测速

**订阅管理**
- 支持 Clash YAML、V2Ray JSON、SIP008、Base64 格式
- 自动定时更新，智能节点合并
- 订阅节点自动关联分组

**分组管理**
- 手动创建自定义分组
- 订阅自动创建分组
- 按分组批量启停节点

**系统集成**
- 系统托盘：最小化到托盘，右键菜单快捷操作（支持一键启停所有节点、负载均衡、链式代理）
- 开机自启：支持设置开机自动启动
- 状态恢复：关闭应用时自动记录已启用的节点，重新打开后自动恢复（包括普通节点、负载均衡、链式代理）
- 深色模式：亮色/深色主题切换
- 实时日志：支持搜索、过滤和级别筛选
- 嵌入式二进制：使用 go:embed 打包 Xray 核心，无需额外安装

## 下载安装

前往 [Releases](../../releases) 页面下载对应平台的安装包，或通过 GitHub Actions 获取最新构建。

- **Windows**: 下载 `xray-manager-windows-amd64.zip`，解压后运行 `xray-manager.exe`
- **macOS**: 下载 `xray-manager-macos-arm64.dmg`，拖入 Applications 即可

## 从源码构建

### 前置依赖

1. **Go** 1.24+
   ```bash
   go version
   ```

2. **Node.js** 18+
   ```bash
   node --version
   ```

3. **Wails v3 CLI**
   ```bash
   go install github.com/wailsapp/wails/v3/cmd/wails3@latest
   ```

4. **Task**（任务运行器）
   ```bash
   go install github.com/go-task/task/v3/cmd/task@latest
   ```

5. **Xray-core 二进制文件**

   从 [Xray-core Releases](https://github.com/XTLS/Xray-core/releases) 下载对应平台的二进制文件，放置到：
   - Windows: `internal/assets/xray/windows/xray.exe`
   - macOS: `internal/assets/xray/darwin/xray`
   - Linux: `internal/assets/xray/linux/xray`

### 系统依赖

| 平台 | 依赖 |
|------|------|
| Windows | WebView2 Runtime（Windows 10+ 自带） |
| macOS | 无需额外依赖 |
| Linux | `libgtk-3-dev` `libwebkit2gtk-4.1-dev`（Ubuntu/Debian）<br>`gtk3-devel` `webkit2gtk4.1-devel`（Fedora） |

### 开发模式

```bash
wails3 dev
```

### 编译生产版本

```bash
# Windows
wails3 task windows:build PRODUCTION=true

# macOS（.app 包）
wails3 task darwin:package

# Linux
wails3 task linux:build
```

编译产物位于 `bin/` 目录。

## 使用说明

### 添加代理规则

1. 点击 **"添加规则"** 按钮
2. 填写基本信息（别名、本地代理类型和端口）
3. 选择协议类型并填写服务器信息
4. 根据需要配置传输层和 TLS

### 链式代理

1. 点击 **"添加链式代理"**
2. 从可用节点中添加节点，拖拽调整顺序
3. 流量方向：第 1 个 → 第 2 个 → ... → 落地

### 负载均衡

1. 点击 **"添加负载均衡"**
2. 选择参与均衡的子节点，启动后自动分配流量

### 系统代理

- 点击节点行的 **"代理"** 按钮设为系统代理
- 点击顶部 **"取消系统代理"** 按钮取消

### 导入 / 导出

- 导出：选择规则后点击 **"导出规则"**，保存为 JSON 文件
- 导入：点击 **"导入规则"**，选择 JSON 配置文件

## 项目结构

```
xray-manager/
├── main.go                     # 程序入口、系统托盘
├── app.go                      # 应用核心逻辑（API 方法）
├── frontend/                   # Vue 3 前端
│   └── src/
│       ├── components/         # 组件（NodeList, NodeEditor, Sidebar 等）
│       ├── stores/             # Pinia 状态管理
│       └── api.js              # 前端 API 层
├── internal/
│   ├── models/                 # 数据模型
│   ├── config/                 # 配置管理、开机自启、系统代理
│   ├── process/                # Xray 进程管理
│   ├── xray/                   # Xray 配置生成器
│   ├── subscription/           # 订阅解析
│   ├── speedtest/              # 节点测速
│   ├── group/                  # 分组管理
│   ├── logger/                 # 日志过滤
│   └── assets/xray/            # Xray 核心文件（需手动放置）
├── build/                      # 各平台构建配置
└── .github/workflows/          # GitHub Actions CI/CD
```

## 技术栈

- **桌面框架**: [Wails v3](https://wails.io/)
- **后端语言**: Go 1.24
- **前端框架**: Vue 3 + Vite + Pinia
- **代理核心**: [Xray-core](https://github.com/XTLS/Xray-core)

## 致谢

- [Wails](https://wails.io/) - Go 桌面应用框架
- [Xray-core](https://github.com/XTLS/Xray-core) - 代理核心
- [Vue.js](https://vuejs.org/) - 前端框架

## License

MIT
