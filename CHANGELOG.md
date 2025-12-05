# 更新日志

## [2.0.0] - 2025-12-05

### 新增功能

#### 1. 多协议支持扩展
- ✨ 新增 **HTTP 代理**协议支持
  - 支持带认证的 HTTP 代理（用户名/密码可选）
  - 完全兼容标准 HTTP 代理协议

- ✨ 新增 **SOCKS 代理**协议支持
  - 支持 SOCKS4 和 SOCKS5 协议
  - 支持带认证的 SOCKS5 代理（用户名/密码可选）
  - 自动版本检测和配置

#### 2. 规则导入/导出功能
- ✨ **导出规则**
  - 将当前所有规则和配置导出为 JSON 文件
  - 支持文件对话框选择保存位置
  - 完整保留所有规则配置信息

- ✨ **导入规则**
  - 从 JSON 文件导入规则
  - 智能合并：自动追加到现有规则
  - 端口冲突检测：跳过已占用端口的规则
  - 实时反馈导入结果（成功/跳过数量）

#### 3. 嵌入式 Xray 核心
- ✨ 使用 **go:embed** 打包多平台 Xray 核心
  - 支持 Windows、Linux、macOS 三大平台
  - 自动检测运行时操作系统
  - 智能提取二进制文件到临时目录
  - 自动设置执行权限（Unix 系统）
  - 文件缓存机制，避免重复提取

### 代码改进

#### 后端
- 📝 `internal/models/rule.go`
  - 新增 HTTP 代理配置字段（HTTPUsername, HTTPPassword）
  - 新增 SOCKS 代理配置字段（SOCKSUsername, SOCKSPassword, SOCKSVersion）

- 📝 `internal/xray/config_builder.go`
  - 新增 `buildHTTPSettings()` 方法
  - 新增 `buildSOCKSSettings()` 方法
  - 完善协议类型 switch 分支

- 📝 `app.go`
  - 新增 `ExportConfig()` 方法
  - 新增 `ImportConfig()` 方法
  - 集成文件对话框 API

- 📝 `internal/config/config.go`
  - 新增 `SaveTo()` 方法（保存到指定文件）
  - 新增 `LoadFrom()` 方法（从指定文件加载）

- 📝 `internal/process/manager.go`
  - 集成嵌入式二进制文件提取
  - 替换硬编码的 xray 路径

- ✨ **新增** `internal/assets/binary.go`
  - 实现 go:embed 嵌入逻辑
  - 实现多平台二进制文件提取
  - 实现智能缓存机制

#### 前端
- 📝 `frontend/index.html`
  - 新增 HTTP 协议配置面板
  - 新增 SOCKS 协议配置面板
  - 新增"导入规则"按钮
  - 新增"导出规则"按钮
  - 更新协议选择下拉菜单

- 📝 `frontend/app.js`
  - 新增 HTTP/SOCKS 协议切换逻辑
  - 新增 `importConfig()` 函数
  - 新增 `exportConfig()` 函数
  - 完善表单验证和数据绑定
  - 新增协议配置字段的读取/写入

### 文档更新
- 📚 更新 `README.md`
  - 添加新功能说明
  - 添加 HTTP/SOCKS 协议使用示例
  - 添加导入/导出功能说明
  - 添加嵌入式二进制使用说明
  - 更新项目结构图

- 📚 新增 `CHANGELOG.md`
  - 详细记录所有更改

- 📚 新增 `internal/assets/xray/README.md`
  - 说明如何放置 Xray 二进制文件

### 配置文件
- 📝 更新 `.gitignore`
  - 忽略 xray-configs/ 目录
  - 忽略 xray-bin/ 目录
  - 忽略配置文件

### 兼容性
- ✅ 保持所有原有功能不变
- ✅ 向后兼容旧的配置文件
- ✅ 支持所有原有的协议和传输方式
- ✅ 保持原有的 UI/UX 交互逻辑

### 技术债务
- 🔧 代码格式化（go fmt）
- 🔧 优化目录结构
- 🔧 改进错误处理

## [1.0.0] - 初始版本

### 基础功能
- ✅ Shadowsocks、VMess、VLESS、Trojan 协议支持
- ✅ TCP、WebSocket、gRPC、HTTP/2 传输协议
- ✅ TLS 加密支持
- ✅ 规则管理（添加、编辑、删除）
- ✅ 批量操作
- ✅ 实时日志
- ✅ IP 显示
- ✅ 配置持久化
- ✅ 开机自启
