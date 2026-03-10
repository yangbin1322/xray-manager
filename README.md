# Xray 管理器

一个基于 Wails v3 框架开发的 Xray-core 代理规则可视化管理工具，提供简洁易用的图形界面来管理 Xray 代理规则。

## 功能特性

- **多协议支持**：支持 Shadowsocks、VMess、VLESS、Trojan、HTTP、SOCKS 协议
- **传输层配置**：支持 TCP、WebSocket、gRPC、HTTP/2 传输协议
- **TLS 加密**：支持 TLS 传输层安全配置
- **规则管理**：添加、编辑、删除代理规则，支持拖拽排序
- **规则导入/导出**：支持导出规则为 JSON 文件，以及从 JSON 文件导入规则
- **分组管理**：支持按分组组织和筛选节点
- **链式代理**：支持多节点链式代理（流量方向: 第1个 → 第2个 → ... → 落地），可拖拽调整节点顺序
- **负载均衡**：支持多节点负载均衡，自动分配流量
- **系统代理**：支持将普通节点、链式代理、负载均衡设置为系统代理
- **节点测速**：支持对选中节点进行速度测试
- **一键启停**：快速启动/停止代理规则
- **批量操作**：支持多选规则进行批量启动/停止/删除
- **实时日志**：查看 Xray 进程的实时运行日志，支持搜索和过滤
- **IP 显示**：自动获取并显示代理的真实 IP
- **深色模式**：支持亮色/深色主题切换
- **配置持久化**：自动保存规则配置到本地文件
- **开机自启**：支持设置程序开机自动启动
- **嵌入式二进制**：使用 go:embed 打包 Xray 核心，无需额外安装
- **多平台支持**：支持 Windows、Linux、macOS

## 系统要求

### 前置依赖

1. **Go 语言环境**（1.22 或更高版本）
   ```bash
   # 下载安装：https://golang.org/dl/
   go version
   ```

2. **Wails v3 CLI**
   ```bash
   go install github.com/wailsapp/wails/v3/cmd/wails3@latest
   ```

3. **Task**（任务运行器）
   ```bash
   go install github.com/go-task/task/v3/cmd/task@latest
   ```

4. **Xray-core 程序**

   从 [Xray-core Releases](https://github.com/XTLS/Xray-core/releases) 下载对应平台的二进制文件，并放置到：
   - Windows: `internal/assets/xray/windows/xray.exe`
   - Linux: `internal/assets/xray/linux/xray`
   - macOS: `internal/assets/xray/darwin/xray`

   编译时会自动将 Xray 核心嵌入到程序中。

### 系统依赖

- **Windows**: WebView2 Runtime（Windows 10+ 自带）
- **Linux**: `webkit2gtk-4.0` 和 `gtk3`
  ```bash
  # Ubuntu/Debian
  sudo apt install libgtk-3-dev libwebkit2gtk-4.0-dev

  # Fedora
  sudo dnf install gtk3-devel webkit2gtk3-devel
  ```
- **macOS**: 无需额外依赖

## 快速开始

### 1. 克隆项目

```bash
git clone <repository-url>
cd xray-manager
```

### 2. 开发模式运行

```bash
wails3 dev
```

### 3. 编译生产版本

#### Windows

```bash
wails3 task windows:build PRODUCTION=true
```

编译后的可执行文件位于 `bin/xray-manager.exe`。

#### macOS

```bash
# 编译并打包为 .app
wails3 task darwin:package

# 编译 Universal Binary（支持 Intel + Apple Silicon）
wails3 task darwin:package:universal
```

编译后的应用位于 `bin/xray-manager.app`。

#### Linux

```bash
wails3 task linux:build
```

编译后的可执行文件位于 `bin/xray-manager`。

## 使用说明

### 添加代理规则

1. 点击 **"添加规则"** 按钮
2. 填写规则信息：
   - **基本信息**：别名、本地代理类型（socks5/http）、本地代理端口
   - **服务器信息**：协议类型、服务器地址、服务器端口
   - **协议配置**：根据协议类型填写对应参数
   - **传输层配置**：传输协议、TLS 配置等

### 链式代理

1. 点击 **"添加链式代理"** 按钮
2. 设置别名、本地代理类型和端口
3. 从可用节点列表中添加节点（支持普通节点和负载均衡节点）
4. 拖拽已选节点调整顺序（流量方向: 第1个 → 第2个 → ... → 落地）
5. 保存后启动即可使用

### 负载均衡

1. 点击 **"添加负载均衡"** 按钮
2. 设置别名、本地代理类型和端口
3. 选择参与负载均衡的子节点
4. 保存后启动即可自动分配流量

### 系统代理

- 点击节点行的 **"代理"** 按钮即可将该节点设为系统代理（支持普通节点、链式代理、负载均衡）
- 点击顶部 **"取消系统代理"** 按钮取消

### 启动/停止规则

- 点击规则行的 **"启动"/"停止"** 按钮控制单个规则
- 勾选多个规则后使用底部批量操作按钮

### 导入/导出规则

- **导出**：点击 **"导出规则"** 按钮，规则将以 JSON 格式导出
- **导入**：点击 **"导入规则"** 按钮，选择 JSON 配置文件导入

## 项目结构

```
xray-manager/
├── main.go                        # 程序入口
├── app.go                         # 应用核心逻辑
├── Taskfile.yml                   # 任务配置
├── go.mod                         # Go 模块配置
├── frontend/                      # 前端代码
│   ├── index.html                # 主界面 HTML
│   ├── style.css                 # 样式文件
│   └── app.js                    # 前端逻辑
├── internal/                      # 内部包
│   ├── models/                   # 数据模型
│   │   └── rule.go              # 规则结构定义
│   ├── config/                   # 配置管理
│   │   ├── config.go            # 配置文件读写
│   │   ├── autostart.go         # 开机自启管理
│   │   └── sysproxy*.go         # 系统代理管理（多平台）
│   ├── process/                  # 进程管理
│   │   └── manager.go           # Xray 进程管理
│   ├── xray/                     # Xray 配置
│   │   └── config_builder.go    # Xray 配置生成器
│   └── assets/                   # 嵌入式资源
│       ├── binary.go            # 二进制文件提取逻辑
│       └── xray/                # Xray 核心文件（需手动放置）
│           ├── windows/
│           ├── linux/
│           └── darwin/
├── build/                         # 构建配置
│   ├── darwin/                   # macOS 构建配置
│   ├── windows/                  # Windows 构建配置
│   └── linux/                    # Linux 构建配置
└── .github/workflows/            # CI/CD
    └── build.yml                 # GitHub Actions 构建
```

## 技术栈

- **后端框架**：[Wails v3](https://wails.io/) - Go + Web 技术构建桌面应用
- **编程语言**：Go 1.22+
- **前端技术**：HTML5 + CSS3 + JavaScript（原生）
- **代理核心**：[Xray-core](https://github.com/XTLS/Xray-core)

## 致谢

- [Wails](https://wails.io/) - 优秀的 Go 桌面应用框架
- [Xray-core](https://github.com/XTLS/Xray-core) - 强大的代理核心
