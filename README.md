# Gost 管理器

一个基于 Wails 框架开发的 Gost 转发规则可视化管理工具，提供简洁易用的图形界面来管理 Gost 代理转发规则。

## 功能特性

- ✅ **规则管理**：添加、编辑、删除转发规则
- ✅ **一键启停**：快速启动/停止代理规则
- ✅ **批量操作**：支持多选规则进行批量启动/停止/删除
- ✅ **实时日志**：查看 Gost 进程的实时运行日志
- ✅ **IP 显示**：自动获取并显示代理的真实 IP
- ✅ **配置持久化**：自动保存规则配置到本地文件
- ✅ **开机自启**：支持设置程序开机自动启动
- ✅ **多平台支持**：支持 Windows、Linux、macOS

## 界面预览

主窗口包含：
- **转发规则表格**：显示所有规则的详细信息
- **日志输出区**：实时显示操作日志和 Gost 输出
- **控制按钮区**：提供添加规则、批量操作等功能

## 系统要求

### 前置依赖

1. **Go 语言环境**（1.21 或更高版本）
   ```bash
   # 下载安装：https://golang.org/dl/
   go version
   ```

2. **Wails CLI**
   ```bash
   # 安装 Wails
   go install github.com/wailsapp/wails/v2/cmd/wails@latest
   ```

3. **Gost 程序**
   ```bash
   # 下载 Gost：https://github.com/ginuerzh/gost/releases
   # 确保 gost 命令在系统 PATH 中可用
   gost -V
   ```

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
cd gost-Forward
```

### 2. 安装依赖

```bash
# 下载 Go 模块依赖
go mod download
```

### 3. 开发模式运行

```bash
# 以开发模式运行（支持热重载）
wails dev
```

### 4. 编译生成可执行文件

```bash
# 编译生产版本
wails build

# 编译后的可执行文件位于 build/bin/ 目录
```

## 使用说明

### 添加转发规则

1. 点击右下角 **"添加规则"** 按钮
2. 填写规则信息：
   - **别名**：规则的名称（例如：香港代理）
   - **代理信息**：目标代理服务器地址（例如：`http://proxy.example.com:8080`）
   - **本地代理类型**：选择代理协议（auto、socks5、socks4、http、http2）
   - **本地代理端口**：本地监听端口（1-65535）
   - **使用 IP 代理**：是否通过代理获取真实 IP
3. 点击 **"保存"** 完成添加

### 启动/停止规则

**方式一：单个规则**
- 勾选表格中规则行的 **"启动"** 复选框即可启动
- 取消勾选则停止该规则

**方式二：批量操作**
1. 勾选要操作的规则（表格最左侧复选框）
2. 点击底部的 **"启动选中"** 或 **"停止选中"** 按钮

### 编辑规则

1. 点击规则行的 **"编辑"** 按钮
2. 修改规则信息
3. 点击 **"保存"** 应用更改

> **注意**：编辑正在运行的规则时，需要先停止该规则

### 删除规则

**删除单个规则**：点击规则行的 **"删除"** 按钮

**批量删除**：
1. 勾选要删除的规则
2. 点击底部的 **"删除选中"** 按钮

### 查看日志

- 日志区域实时显示所有操作信息和 Gost 进程输出
- 点击 **"清空日志"** 按钮可清空日志内容

### 开机自启

- 勾选左下角的 **"开机自启"** 复选框即可启用
- 程序会自动添加到系统启动项

## 配置文件

配置文件自动保存在程序所在目录：

```
gost-manager-config.json
```

配置文件包含：
- 所有转发规则
- 开机自启设置
- 规则运行状态

## 项目结构

```
gost-Forward/
├── main.go                    # 程序入口
├── app.go                     # 应用核心逻辑
├── wails.json                 # Wails 配置
├── go.mod                     # Go 模块配置
├── frontend/                  # 前端代码
│   ├── index.html            # 主界面 HTML
│   ├── style.css             # 样式文件
│   └── app.js                # 前端逻辑
├── internal/                  # 内部包
│   ├── models/               # 数据模型
│   │   └── rule.go          # 规则结构定义
│   ├── config/               # 配置管理
│   │   ├── config.go        # 配置文件读写
│   │   └── autostart.go     # 开机自启管理
│   └── process/              # 进程管理
│       └── manager.go        # Gost 进程管理
└── README.md                  # 项目说明
```

## 技术栈

- **后端框架**：[Wails v2](https://wails.io/) - Go + Web 技术构建桌面应用
- **编程语言**：Go 1.21+
- **前端技术**：HTML5 + CSS3 + JavaScript（原生）
- **代理工具**：[Gost](https://github.com/ginuerzh/gost)

## 常见问题

### 1. 启动规则失败

**原因**：
- Gost 未安装或不在 PATH 中
- 端口已被占用
- 代理信息配置错误

**解决方法**：
- 确认 `gost` 命令可用：`gost -V`
- 检查端口是否被占用
- 检查代理地址格式是否正确

### 2. 无法获取真实 IP

**原因**：
- 代理未正确连接
- 网络问题
- IP 查询服务不可用

**解决方法**：
- 检查代理配置是否正确
- 查看日志中的错误信息
- 等待一段时间后自动重试

### 3. 开机自启不生效

**原因**：
- 权限不足
- 系统不支持

**解决方法**：
- Windows：以管理员权限运行程序
- Linux：确保 `~/.config/autostart` 目录存在
- macOS：检查 `~/Library/LaunchAgents` 权限

## 编译选项

### 编译 Windows 版本

```bash
# 在 Windows 上编译
wails build

# 跨平台编译（在 Linux/Mac 上编译 Windows 版本）
wails build -platform windows/amd64
```

### 编译 Linux 版本

```bash
# 在 Linux 上编译
wails build

# 跨平台编译
wails build -platform linux/amd64
```

### 编译 macOS 版本

```bash
# 在 macOS 上编译
wails build

# 编译 Universal Binary（支持 Intel + Apple Silicon）
wails build -platform darwin/universal
```

## 开发说明

### 修改前端界面

前端文件位于 `frontend/` 目录：
- 修改 `index.html` 调整界面结构
- 修改 `style.css` 调整样式
- 修改 `app.js` 调整前端逻辑

修改后运行 `wails dev` 即可实时预览。

### 修改后端逻辑

后端代码位于根目录和 `internal/` 目录：
- `app.go`：应用主逻辑，前端调用的方法
- `internal/process/manager.go`：Gost 进程管理
- `internal/config/config.go`：配置文件管理
- `internal/models/rule.go`：数据结构定义

### 调试

```bash
# 开发模式带调试信息
wails dev -v

# 查看详细构建日志
wails build -v
```

## 贡献指南

欢迎提交 Issue 和 Pull Request！

1. Fork 本项目
2. 创建特性分支：`git checkout -b feature/AmazingFeature`
3. 提交更改：`git commit -m 'Add some AmazingFeature'`
4. 推送到分支：`git push origin feature/AmazingFeature`
5. 提交 Pull Request

## 许可证

本项目采用 MIT 许可证 - 详见 LICENSE 文件

## 致谢

- [Wails](https://wails.io/) - 优秀的 Go 桌面应用框架
- [Gost](https://github.com/ginuerzh/gost) - 强大的代理转发工具

## 联系方式

如有问题或建议，欢迎提交 Issue。