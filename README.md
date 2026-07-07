# Xray Manager

基于 [Wails v3](https://wails.io/) + [Vue 3](https://vuejs.org/) 开发的代理规则可视化管理工具，底层由 [Xray-core](https://github.com/XTLS/Xray-core) 与 [sing-box](https://github.com/SagerNet/sing-box) 双内核驱动，提供简洁易用的图形界面来管理代理节点。

支持 Windows、macOS、Linux 三平台。

## 功能特性

**代理协议**
- 支持 Shadowsocks、VMess、VLESS、Trojan、HTTP、SOCKS 协议（Xray 内核）
- 支持 Hysteria2、TUIC v5 协议（sing-box 内核，涉及这两种协议的节点自动切换内核运行）
- 支持 TCP、WebSocket、gRPC、HTTP/2 传输协议
- 支持 TLS / Reality 传输层安全配置

**规则管理**
- 添加、编辑、删除代理规则，支持拖拽排序
- 批量编辑：多选节点后统一编辑，每个节点一张可折叠卡片
- 导入/导出规则为 JSON 文件（仅导出选中节点及其关联的分组/故障转移/链式代理）
- 解析分享链接（vmess://、vless://、ss://、trojan://、hysteria2://、hy2://、tuic://）
- 批量选择、启动、停止、删除
- 本地代理统一为混合端口，同一端口同时支持 HTTP 和 SOCKS5

**故障转移与链式代理**
- 链式代理：多节点链式串联，流量依次经过各节点转发
- 故障转移：多节点主备切换，主节点故障时自动切换到备用节点
- 二者均支持测速、健康检测、流量监控，可作为系统代理

**节点测速与健康检测**
- 测速：普通节点直连测 TCP 延迟 + 通过代理测下载速度（限时采样，慢速网络不超时）
- 健康检测：无需启动即可检测普通节点连通性（DNS → TCP → TLS/Reality，QUIC 走 UDP 探测），支持后台定时自动检测
- 状态分级：在线、延迟较高、超时、TLS 失败、DNS 失败、Reality 失败

**流量监控**
- 实时上/下行速度
- 今日 / 累计流量统计（跨天自动清零），最近启停时间记录
- 支持手动清零

**订阅管理**
- 支持 Clash YAML、V2Ray JSON、SIP008、Base64 格式
- 添加与编辑订阅（名称、地址、自动更新、更新间隔、更新方式）
- 更新方式可选：直连 / 系统代理 / 指定节点（临时建立代理，适合本机无法直连订阅链接的情况）
- 自动定时更新，智能节点合并，订阅节点自动关联分组

**分组管理**
- 手动创建自定义分组
- 订阅自动创建分组
- 按分组批量启停节点

**系统集成**
- 系统代理：一键将节点设为系统代理（Windows/macOS 自动设置，Linux 需手动设置环境变量）
- 系统托盘：最小化到托盘，右键菜单快捷操作（一键启停所有节点、故障转移、链式代理）
- 开机自启：支持设置开机自动启动
- 状态恢复：关闭应用时自动记录已启用的节点，重新打开后自动恢复（包括普通节点、故障转移、链式代理）
- 进程管理：启停时自动清理端口残留进程，避免僵尸进程导致端口占用
- 深色模式：亮色/深色主题切换
- 实时日志：支持搜索、过滤和级别筛选
- 嵌入式二进制：使用 go:embed 打包 Xray 与 sing-box 双内核，开箱即用无需额外安装

## 下载安装

前往 [Releases](../../releases) 页面下载对应平台的安装包，或通过 GitHub Actions 获取最新构建产物。

- **Windows** (amd64): 下载 `xray-manager-windows-amd64`，解压后运行 `xray-manager.exe`
- **macOS** (arm64 / Apple Silicon): 下载 `xray-manager-macos-arm64.dmg`，拖入 Applications 即可
- **Linux** (amd64): 下载 `xray-manager-linux-amd64`，`chmod +x` 后运行；需桌面环境（图形界面），并安装运行库 `libgtk-3-0 libwebkit2gtk-4.1-0`

> 首次运行时会自动将内置的 Xray / sing-box 内核提取到可执行文件同级的 `xray-bin/` 目录，请在有写入权限的目录中运行。

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

3. **Wails v3 CLI**（需与项目锁定的版本一致，否则 Linux 可能出现 GTK 版本冲突）
   ```bash
   go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.41
   ```

4. **Task**（任务运行器）
   ```bash
   go install github.com/go-task/task/v3/cmd/task@latest
   ```

5. **内核二进制文件（已内置于仓库）**

   仓库已包含三平台的 Xray-core 与 sing-box 二进制（通过 `go:embed` 打包），
   正常构建无需额外下载。如需自行更新版本，替换以下文件即可：
   - Xray: `internal/assets/xray/{windows/xray.exe, linux/xray, darwin/xray}`
   - sing-box: `internal/assets/singbox/{windows/sing-box.exe, linux/sing-box, darwin/sing-box}`

   > 注意：同一平台的 Xray 与 sing-box 必须为相同架构（Windows/Linux 为 amd64，macOS 为 arm64）。

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

# Linux（裸二进制）
wails3 task linux:build

# Linux（打包为 AppImage / deb / rpm）
wails3 task linux:package
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

### 故障转移

1. 点击 **"添加故障转移"**
2. 选择参与的子节点，启动后默认走第一个节点，故障时切换到后续备用节点

### 测速与健康检测

- 单个节点：点击节点行的 **"测速"** / **"检测"** 按钮
- 批量：多选后点击工具栏 **"选中测速"** / **"健康检测"**（未选中时"健康检测"检测全部）
- 故障转移 / 链式代理需先启动，测速与检测通过其本地代理端口进行

### 批量编辑

1. 多选普通节点后点击工具栏 **"批量编辑"**
2. 每个节点一张可折叠卡片，改完后点 **"全部保存"** 统一提交

### 系统代理

- 点击节点行的 **"代理"** 按钮设为系统代理
- 点击顶部 **"取消系统代理"** 按钮取消
- Linux 下需按提示手动设置 `http_proxy` / `https_proxy` 环境变量

### 导入 / 导出

- 导出：选择节点后点击 **"导出规则"**，仅导出选中节点及其关联的分组、故障转移、链式代理（可选是否包含订阅）
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
│   ├── process/                # 进程管理（Xray/sing-box 内核、端口清理、流量轮询）
│   ├── xray/                   # Xray 配置生成器
│   ├── singbox/                # sing-box 配置生成器（Hysteria2/TUIC）
│   ├── subscription/           # 订阅解析
│   ├── parser/                 # 分享链接解析
│   ├── speedtest/              # 节点测速
│   ├── healthcheck/            # 节点健康检测
│   ├── group/                  # 分组管理
│   ├── logger/                 # 日志过滤
│   ├── utils/                  # 端口检测与进程清理等工具
│   └── assets/                 # 内置内核二进制（xray/、singbox/）与 go:embed 提取逻辑
├── build/                      # 各平台构建配置
└── .github/workflows/          # GitHub Actions CI/CD
```

## 技术栈

- **桌面框架**: [Wails v3](https://wails.io/)
- **后端语言**: Go 1.24
- **前端框架**: Vue 3 + Vite + Pinia
- **代理核心**: [Xray-core](https://github.com/XTLS/Xray-core)（主）、[sing-box](https://github.com/SagerNet/sing-box)（Hysteria2/TUIC）

## 致谢

- [Wails](https://wails.io/) - Go 桌面应用框架
- [Xray-core](https://github.com/XTLS/Xray-core) - 代理核心
- [sing-box](https://github.com/SagerNet/sing-box) - Hysteria2/TUIC 内核
- [Vue.js](https://vuejs.org/) - 前端框架

## License

MIT
