# Xray 管理器 - 新增功能文档

本文档描述了在 Wails v3 迁移后新增的所有功能模块。

## 📋 新增功能列表

### 1. 端口检测功能 ✅

**位置**: `internal/utils/port.go`

**功能**:
- `CheckPortAvailable(port int)`: 检查端口是否可用
- `FindAvailablePort(startPort int)`: 从起始端口查找可用端口
- `FindAvailablePorts(startPort, count int)`: 查找多个可用端口
- `RecommendPort()`: 推荐可用端口（从常用端口开始）

**API**:
- `CheckPortAvailable(port int) bool`
- `RecommendPort() int`

**使用场景**:
- 添加规则时自动检测端口冲突
- 编辑规则时验证端口可用性
- 启动规则时自动推荐可用端口

---

### 2. 节点测速功能 ✅

**位置**: `internal/speedtest/speedtest.go`

**功能**:
- TCP 延迟测试（ping 代理服务器）
- HTTP/HTTPS 下载测速（通过本地代理测试真实速度）
- 支持单节点测速和批量测速
- 多线程并发测速（可配置并发数）
- 超时控制（使用 context）

**测速结果保存在规则字段中**:
- `Latency`: TCP 延迟（毫秒）
- `DownloadSpeed`: 下载速度（MB/s）
- `LastTestTime`: 最后测速时间
- `TestStatus`: 测速状态（idle, testing, success, failed）

**API**:
- `TestRuleSpeed(ruleID string) error`: 测试单个规则
- `TestAllRulesSpeed() error`: 批量测试所有规则

**事件**:
- `ruleUpdated`: 规则更新事件
- `speedTestResult`: 测速结果事件
- `allSpeedTestComplete`: 批量测速完成事件

---

### 3. 订阅解析与自动更新 ✅

**位置**: `internal/subscription/`

**支持的订阅格式**:
- ✅ Clash YAML 格式
- ✅ V2Ray JSON 格式
- ✅ SIP008 Shadowsocks JSON
- ✅ Base64 编码（支持 vmess://, vless://, ss://, trojan://）

**功能**:
- 自动检测订阅类型
- 一键导入订阅节点
- 自动定时更新（可配置间隔）
- 智能节点合并（新增、删除、更新）
- 节点变更提醒

**订阅配置**:
```go
type Subscription struct {
    ID             string
    Name           string
    URL            string
    Type           string  // clash, v2ray, sip008, base64
    AutoUpdate     bool
    UpdateInterval int     // 小时
    LastUpdate     string
    NextUpdate     string
    NodeCount      int
}
```

**API**:
- `AddSubscription(name, url string, autoUpdate bool, updateInterval int) error`
- `UpdateSubscriptionByID(subID string) error`
- `GetSubscriptions() []Subscription`
- `DeleteSubscription(subID string) error`

**特性**:
- 订阅节点自动关联到对应分组
- 节点更新时保留原有启用状态
- 自动分配可用端口

---

### 4. 节点分组功能 ✅

**位置**: `internal/group/manager.go`

**功能**:
- 手动创建自定义分组
- 订阅自动创建分组
- 按分组展示节点
- 批量操作分组节点（一键启动/停止）

**分组模型**:
```go
type Group struct {
    ID             string
    Name           string
    Description    string
    Source         string  // manual, subscription
    SubscriptionID string  // 关联的订阅ID
    CreatedAt      string
}
```

**API**:
- `CreateGroup(name, description string) error`
- `GetGroups() []Group`
- `DeleteGroup(groupID string) error`
- `StartAllRulesInGroup(groupID string) error`
- `StopAllRulesInGroup(groupID string) error`

---

### 5. 系统托盘功能 ✅

**位置**: `main.go`

**功能**:
- 最小化到系统托盘
- 托盘图标提示
- 右键菜单：
  - 显示/隐藏主窗口
  - 一键启动所有节点
  - 一键停止所有节点
  - 退出程序
- 托盘图标点击切换窗口显示/隐藏

**平台支持**:
- ✅ Windows
- ✅ macOS
- ✅ Linux

---

### 6. 日志搜索与过滤 ✅

**位置**: `internal/logger/filter.go`

**功能**:
- 日志级别过滤（ALL, INFO, WARN, ERROR, XRAY）
- 关键字搜索
- 按来源过滤
- 实时流式过滤
- 日志缓冲（默认保存最近 1000 条）

**日志模型**:
```go
type LogEntry struct {
    Timestamp string
    Level     LogLevel
    Message   string
    Source    string  // 来源：系统、规则别名等
}
```

**API**:
- `GetLogs() []LogEntry`
- `SearchLogs(keyword, level string) []LogEntry`
- `FilterLogsByLevel(level string) []LogEntry`
- `ClearLogs()`

**事件**:
- `log`: 原始日志事件
- `logEntry`: 结构化日志事件

---

## 🔧 数据模型扩展

### ProxyRule 扩展字段

```go
type ProxyRule struct {
    // ... 原有字段 ...

    // 测速相关
    Latency       int     // TCP 延迟（毫秒）
    DownloadSpeed float64 // 下载速度（MB/s）
    LastTestTime  string  // 最后测速时间
    TestStatus    string  // 测速状态

    // 分组相关
    GroupID         string // 所属分组ID
    GroupName       string // 所属分组名称
    SubscriptionURL string // 订阅链接
    Source          string // 来源: manual, subscription
}
```

### Config 扩展字段

```go
type Config struct {
    AutoStart     bool
    Rules         []ProxyRule
    Groups        []Group        // 新增：分组列表
    Subscriptions []Subscription // 新增：订阅列表
}
```

---

## 📦 依赖变更

新增依赖：
```
gopkg.in/yaml.v3  # 用于 Clash YAML 解析
```

---

## 🎯 使用示例

### 1. 端口检测

```javascript
// 前端调用
const isAvailable = await window.go.main.MyService.CheckPortAvailable(10808);
if (!isAvailable) {
    const recommendPort = await window.go.main.MyService.RecommendPort();
    console.log(`推荐端口: ${recommendPort}`);
}
```

### 2. 节点测速

```javascript
// 测试单个节点
await window.go.main.MyService.TestRuleSpeed(ruleId);

// 批量测试所有节点
await window.go.main.MyService.TestAllRulesSpeed();

// 监听测速结果
window.runtime.EventsOn('speedTestResult', (result) => {
    console.log(`延迟: ${result.latency}ms, 速度: ${result.downloadSpeed}MB/s`);
});
```

### 3. 订阅管理

```javascript
// 添加订阅
await window.go.main.MyService.AddSubscription(
    "My Subscription",
    "https://example.com/sub",
    true,  // autoUpdate
    6      // 每6小时更新一次
);

// 手动更新订阅
await window.go.main.MyService.UpdateSubscriptionByID(subId);

// 获取所有订阅
const subs = await window.go.main.MyService.GetSubscriptions();
```

### 4. 分组操作

```javascript
// 创建分组
await window.go.main.MyService.CreateGroup("US Servers", "美国节点");

// 一键启动分组
await window.go.main.MyService.StartAllRulesInGroup(groupId);

// 一键停止分组
await window.go.main.MyService.StopAllRulesInGroup(groupId);
```

### 5. 日志搜索

```javascript
// 搜索日志
const logs = await window.go.main.MyService.SearchLogs("连接失败", "ERROR");

// 按级别过滤
const errorLogs = await window.go.main.MyService.FilterLogsByLevel("ERROR");

// 清空日志
await window.go.main.MyService.ClearLogs();
```

---

## 🚀 开发说明

### 编译项目

```bash
# 安装依赖
go mod tidy

# 开发模式
wails3 dev

# 构建生产版本
wails3 build
```

### 测试功能

1. **端口检测**: 在添加/编辑规则时会自动触发
2. **测速**: 在节点列表中添加"测速"按钮
3. **订阅**: 在设置中添加"订阅管理"面板
4. **分组**: 在侧边栏显示分组列表
5. **托盘**: 运行程序后自动显示在系统托盘
6. **日志**: 在日志面板添加搜索和过滤功能

---

## ⚠️ 注意事项

1. **测速功能**: 只有已启动的节点才能测试下载速度，未启动的节点只能测试延迟
2. **订阅更新**: 自动更新会在后台静默执行，不会中断正在运行的节点
3. **端口冲突**: 导入订阅时会自动分配可用端口，避免冲突
4. **日志缓冲**: 默认只保存最近 1000 条日志，防止内存占用过多
5. **分组删除**: 只能删除空分组，有节点的分组需要先删除所有节点

---

## 📝 待实现功能（前端）

前端需要实现以下 UI 组件：

1. **节点列表**
   - [ ] 显示测速结果（延迟、速度）
   - [ ] 测速按钮和状态指示
   - [ ] 按分组展示节点

2. **订阅管理面板**
   - [ ] 添加订阅对话框
   - [ ] 订阅列表显示
   - [ ] 手动更新按钮
   - [ ] 订阅配置编辑

3. **分组侧边栏**
   - [ ] 分组列表
   - [ ] 分组操作菜单
   - [ ] 统计信息显示

4. **日志面板增强**
   - [ ] 搜索输入框
   - [ ] 级别过滤下拉框
   - [ ] 清空日志按钮

5. **规则编辑**
   - [ ] 端口冲突检测提示
   - [ ] 推荐端口按钮
   - [ ] 分组选择器

---

## 🎉 总结

所有后端功能已完整实现，包括：

✅ 端口检测与推荐
✅ 节点测速（TCP延迟 + 下载速度）
✅ 订阅解析与自动更新（支持 Clash/V2Ray/SIP008/Base64）
✅ 节点分组管理
✅ 系统托盘功能
✅ 日志搜索与过滤

前端只需要调用对应的 API 方法即可使用这些功能。所有功能都经过模块化设计，易于维护和扩展。
