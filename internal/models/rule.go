package models

import "fmt"

// ProxyRule 代理规则结构
type ProxyRule struct {
	ID         string        `json:"id"`         // 唯一标识
	Alias      string        `json:"alias"`      // 别名
	LocalType  string        `json:"localType"`  // 本地代理类型: mixed（同时支持 HTTP/SOCKS5），旧值 socks/http 兼容处理
	LocalPort  int           `json:"localPort"`  // 本地代理端口
	Protocol   string        `json:"protocol"`   // 远程协议类型: shadowsocks, vmess, vless, trojan, hysteria2, tuic
	ServerAddr string        `json:"serverAddr"` // 服务器地址
	ServerPort int           `json:"serverPort"` // 服务器端口
	Settings   ProxySettings `json:"settings"`   // 协议相关配置
	RealIP     string        `json:"realIp"`     // 真实IP
	Enabled    bool          `json:"enabled"`    // 启动状态
	ProcessID  int           `json:"processId"`  // 进程ID

	// 测速相关字段
	Latency       int     `json:"latency"`       // TCP 延迟（毫秒）
	DownloadSpeed float64 `json:"downloadSpeed"` // 下载速度（MB/s）
	LastTestTime  string  `json:"lastTestTime"`  // 最后测速时间
	TestStatus    string  `json:"testStatus"`    // 测速状态: idle, testing, success, failed

	// 健康检查相关字段
	HealthStatus    string `json:"healthStatus"`    // 健康状态: 空, checking, online, high_latency, timeout, dns_failed, tls_failed, reality_failed
	HealthLatency   int    `json:"healthLatency"`   // 健康检查延迟（毫秒）
	LastHealthCheck string `json:"lastHealthCheck"` // 最后健康检查时间

	// 流量统计相关字段
	Traffic       TrafficStats `json:"traffic"`       // 累计流量统计
	LastStartTime string       `json:"lastStartTime"` // 最近启动时间
	LastStopTime  string       `json:"lastStopTime"`  // 最近停止时间

	// 分组相关字段
	GroupID         string `json:"groupId"`                   // 所属分组ID
	GroupName       string `json:"groupName"`                 // 所属分组名称
	SubscriptionURL string `json:"subscriptionUrl,omitempty"` // 订阅链接（如果来自订阅）
	Source          string `json:"source"`                    // 来源: manual（手动添加）, subscription（订阅导入）
}

// TrafficStats 节点流量统计（字节）
type TrafficStats struct {
	TodayUp   int64  `json:"todayUp"`   // 今日上传
	TodayDown int64  `json:"todayDown"` // 今日下载
	TotalUp   int64  `json:"totalUp"`   // 累计上传
	TotalDown int64  `json:"totalDown"` // 累计下载
	TodayDate string `json:"todayDate"` // 今日统计对应的日期（yyyy-mm-dd），跨天自动清零
}

// TrafficSnapshot 实时流量快照（通过事件推送给前端，不持久化速度）
type TrafficSnapshot struct {
	RuleID    string  `json:"ruleId"`
	UpSpeed   float64 `json:"upSpeed"`   // 当前上传速度（字节/秒）
	DownSpeed float64 `json:"downSpeed"` // 当前下载速度（字节/秒）
	TodayUp   int64   `json:"todayUp"`
	TodayDown int64   `json:"todayDown"`
	TotalUp   int64   `json:"totalUp"`
	TotalDown int64   `json:"totalDown"`
}

// HealthCheckConfig 健康检查全局配置
type HealthCheckConfig struct {
	Enabled          bool `json:"enabled"`          // 是否启用后台自动检测
	IntervalSec      int  `json:"intervalSec"`      // 检测周期（秒）
	TimeoutSec       int  `json:"timeoutSec"`       // 单节点检测超时（秒）
	LatencyThreshold int  `json:"latencyThreshold"` // 延迟较高阈值（毫秒）
}

// HealthCheckResult 健康检查结果
type HealthCheckResult struct {
	RuleID    string `json:"ruleId"`
	Status    string `json:"status"`  // online, high_latency, timeout, dns_failed, tls_failed, reality_failed
	Latency   int    `json:"latency"` // 毫秒
	Error     string `json:"error"`
	Timestamp string `json:"timestamp"`
}

// Validate 校验规则字段合法性，缺失字段给默认值
func (r *ProxyRule) Validate() error {
	// 校验协议
	validProtocols := map[string]bool{
		"shadowsocks": true, "vmess": true, "vless": true,
		"trojan": true, "http": true, "socks": true,
		"hysteria2": true, "tuic": true,
	}
	if r.Protocol == "" {
		return fmt.Errorf("协议类型不能为空")
	}
	if !validProtocols[r.Protocol] {
		return fmt.Errorf("不支持的协议类型: %s", r.Protocol)
	}

	// 校验服务器地址
	if r.ServerAddr == "" {
		return fmt.Errorf("服务器地址不能为空")
	}

	// 校验端口范围
	if r.ServerPort <= 0 || r.ServerPort > 65535 {
		return fmt.Errorf("服务器端口无效: %d", r.ServerPort)
	}

	// 默认值填充（统一为混合端口，同时支持 HTTP/SOCKS5）
	if r.LocalType == "" {
		r.LocalType = "mixed"
	}
	if r.Source == "" {
		r.Source = "manual"
	}
	if r.Alias == "" {
		r.Alias = fmt.Sprintf("%s-%s:%d", r.Protocol, r.ServerAddr, r.ServerPort)
	}

	return nil
}

// IsDuplicateOf 判断是否与另一个规则重复（别名+协议+服务器地址+服务器端口+本地端口 完全相同）
func (r *ProxyRule) IsDuplicateOf(other *ProxyRule) bool {
	return r.Alias == other.Alias &&
		r.Protocol == other.Protocol &&
		r.ServerAddr == other.ServerAddr &&
		r.ServerPort == other.ServerPort &&
		r.LocalPort == other.LocalPort
}

// ProxySettings 代理协议设置（根据协议类型使用不同字段）
type ProxySettings struct {
	// Shadowsocks 配置
	SSMethod   string `json:"ssMethod,omitempty"`   // 加密方法
	SSPassword string `json:"ssPassword,omitempty"` // 密码

	// VMess 配置
	VMessUserID   string `json:"vmessUserId,omitempty"`   // 用户ID (UUID)
	VMessAlterID  int    `json:"vmessAlterId,omitempty"`  // 额外ID
	VMessSecurity string `json:"vmessSecurity,omitempty"` // 加密方式 (auto, aes-128-gcm, chacha20-poly1305, none)

	// VLESS 配置
	VLessUserID     string `json:"vlessUserId,omitempty"`     // 用户ID (UUID)
	VLessFlow       string `json:"vlessFlow,omitempty"`       // 流控模式 (xtls-rprx-vision等)
	VLessEncryption string `json:"vlessEncryption,omitempty"` // 加密方式（通常为 none）

	// Trojan 配置
	TrojanPassword string `json:"trojanPassword,omitempty"` // Trojan密码

	// HTTP 代理配置
	HTTPUsername string `json:"httpUsername,omitempty"` // HTTP 代理用户名（可选）
	HTTPPassword string `json:"httpPassword,omitempty"` // HTTP 代理密码（可选）

	// SOCKS 代理配置
	SOCKSUsername string `json:"socksUsername,omitempty"` // SOCKS 代理用户名（可选）
	SOCKSPassword string `json:"socksPassword,omitempty"` // SOCKS 代理密码（可选）
	SOCKSVersion  string `json:"socksVersion,omitempty"`  // SOCKS 版本 (socks4, socks5, 默认 socks5)

	// Hysteria2 配置
	Hy2Password     string `json:"hy2Password,omitempty"`     // 认证密码
	Hy2Obfs         string `json:"hy2Obfs,omitempty"`         // 混淆类型（salamander）
	Hy2ObfsPassword string `json:"hy2ObfsPassword,omitempty"` // 混淆密码
	Hy2UpMbps       int    `json:"hy2UpMbps,omitempty"`       // 上行带宽限制 (Mbps)，0 表示自动（BBR）
	Hy2DownMbps     int    `json:"hy2DownMbps,omitempty"`     // 下行带宽限制 (Mbps)，0 表示自动（BBR）

	// TUIC v5 配置
	TUICUserID       string `json:"tuicUserId,omitempty"`       // 用户 UUID
	TUICPassword     string `json:"tuicPassword,omitempty"`     // 密码
	TUICCongestion   string `json:"tuicCongestion,omitempty"`   // 拥塞控制: bbr, cubic, new_reno
	TUICUDPRelayMode string `json:"tuicUdpRelayMode,omitempty"` // UDP 中继模式: native, quic

	// 通用传输层配置
	Network  string        `json:"network,omitempty"`  // 传输协议: tcp, ws, grpc, h2
	Security string        `json:"security,omitempty"` // 传输层安全: none, tls
	TLS      *TLSSettings  `json:"tls,omitempty"`      // TLS配置
	WS       *WSSettings   `json:"ws,omitempty"`       // WebSocket配置
	GRPC     *GRPCSettings `json:"grpc,omitempty"`     // gRPC配置
	H2       *H2Settings   `json:"h2,omitempty"`       // HTTP/2配置
}

// TLSSettings TLS配置
type TLSSettings struct {
	ServerName    string   `json:"serverName,omitempty"`    // 服务器名称 (SNI)
	ALPN          []string `json:"alpn,omitempty"`          // ALPN协议列表
	AllowInsecure bool     `json:"allowInsecure,omitempty"` // 允许不安全连接
}

// WSSettings WebSocket配置
type WSSettings struct {
	Path    string            `json:"path,omitempty"`    // WebSocket路径
	Headers map[string]string `json:"headers,omitempty"` // HTTP头
}

// GRPCSettings gRPC配置
type GRPCSettings struct {
	ServiceName string `json:"serviceName,omitempty"` // gRPC服务名
}

// H2Settings HTTP/2配置
type H2Settings struct {
	Path string   `json:"path,omitempty"` // HTTP/2路径
	Host []string `json:"host,omitempty"` // HTTP/2 Host
}

// Config 配置文件结构
type Config struct {
	AutoStart     bool              `json:"autoStart"`     // 开机自启
	Rules         []ProxyRule       `json:"rules"`         // 代理规则列表
	Groups        []Group           `json:"groups"`        // 分组列表
	Subscriptions []Subscription    `json:"subscriptions"` // 订阅列表
	LoadBalancers []LoadBalanceNode `json:"loadBalancers"` // 故障转移节点列表
	ChainProxies  []ChainProxy      `json:"chainProxies"`  // 链式代理列表
	HealthCheck   HealthCheckConfig `json:"healthCheck"`   // 健康检查配置
}

// ExportData 导出数据结构（包含版本信息）
type ExportData struct {
	Version       string            `json:"version"`       // 导出格式版本
	ExportTime    string            `json:"exportTime"`    // 导出时间
	Rules         []ProxyRule       `json:"rules"`         // 代理规则列表
	Groups        []Group           `json:"groups"`        // 分组列表
	Subscriptions []Subscription    `json:"subscriptions"` // 订阅列表
	LoadBalancers []LoadBalanceNode `json:"loadBalancers"` // 故障转移节点列表
	ChainProxies  []ChainProxy      `json:"chainProxies"`  // 链式代理列表
}

// ImportResult 导入结果
type ImportResult struct {
	Success        bool     `json:"success"`
	RulesImported  int      `json:"rulesImported"` // 导入的规则数
	RulesSkipped   int      `json:"rulesSkipped"`  // 跳过的重复规则数
	GroupsImported int      `json:"groupsImported"`
	SubsImported   int      `json:"subsImported"`
	ChainImported  int      `json:"chainImported"`
	LBImported     int      `json:"lbImported"`
	Errors         []string `json:"errors"`   // 错误信息列表
	Warnings       []string `json:"warnings"` // 警告信息列表
}

// ImportShareResult 批量导入分享链接结果
type ImportShareResult struct {
	SuccessCount int      `json:"successCount"` // 成功数
	FailCount    int      `json:"failCount"`    // 失败数
	Errors       []string `json:"errors"`       // 每条失败的详细信息
}

// Group 节点分组
type Group struct {
	ID             string `json:"id"`                       // 分组ID
	Name           string `json:"name"`                     // 分组名称
	Description    string `json:"description"`              // 分组描述
	Source         string `json:"source"`                   // 来源: manual, subscription
	SubscriptionID string `json:"subscriptionId,omitempty"` // 关联的订阅ID（如果来自订阅）
	CreatedAt      string `json:"createdAt"`                // 创建时间
}

// Subscription 订阅配置
type Subscription struct {
	ID             string `json:"id"`                      // 订阅ID
	Name           string `json:"name"`                    // 订阅名称
	URL            string `json:"url"`                     // 订阅链接
	Type           string `json:"type"`                    // 订阅类型: clash, v2ray, sip008, base64
	GroupID        string `json:"groupId"`                 // 关联的分组ID
	Enabled        bool   `json:"enabled"`                 // 是否启用
	AutoUpdate     bool   `json:"autoUpdate"`              // 是否自动更新
	UpdateInterval int    `json:"updateInterval"`          // 更新间隔（小时）
	LastUpdate     string `json:"lastUpdate"`              // 最后更新时间
	NextUpdate     string `json:"nextUpdate"`              // 下次更新时间
	NodeCount      int    `json:"nodeCount"`               // 节点数量
	UpdateMode     string `json:"updateMode"`              // 更新方式: direct（直连，默认）, system（系统代理）, proxy（指定节点/链式/故障转移）
	UpdateProxyID  string `json:"updateProxyId,omitempty"` // UpdateMode 为 proxy 时使用的节点/链式代理/故障转移 ID
}

// SpeedTestResult 测速结果
type SpeedTestResult struct {
	RuleID        string  `json:"ruleId"`
	Latency       int     `json:"latency"`       // 延迟（毫秒）
	DownloadSpeed float64 `json:"downloadSpeed"` // 下载速度（MB/s）
	Success       bool    `json:"success"`
	Error         string  `json:"error"`
	Timestamp     string  `json:"timestamp"`
}

// LoadBalanceNode 故障转移节点
type LoadBalanceNode struct {
	ID        string   `json:"id"`        // 唯一标识
	Alias     string   `json:"alias"`     // 别名
	LocalType string   `json:"localType"` // 本地代理类型: mixed（同时支持 HTTP/SOCKS5）
	LocalPort int      `json:"localPort"` // 本地代理端口
	NodeIDs   []string `json:"nodeIds"`   // 子节点 ID 列表
	Enabled   bool     `json:"enabled"`   // 启动状态
	ProcessID int      `json:"processId"` // 进程ID
	GroupID   string   `json:"groupId"`   // 所属分组ID
	GroupName string   `json:"groupName"` // 所属分组名称

	// 测速相关字段（通过本地代理端口测试）
	Latency       int     `json:"latency"`       // 延迟（毫秒）
	DownloadSpeed float64 `json:"downloadSpeed"` // 下载速度（MB/s）
	LastTestTime  string  `json:"lastTestTime"`  // 最后测速时间
	TestStatus    string  `json:"testStatus"`    // 测速状态: idle, testing, success, failed

	// 健康检查相关字段（通过本地代理端口检测，需已启动）
	HealthStatus    string `json:"healthStatus"`    // online, high_latency, timeout, checking
	HealthLatency   int    `json:"healthLatency"`   // 健康检查延迟（毫秒）
	LastHealthCheck string `json:"lastHealthCheck"` // 最后健康检查时间

	// 流量统计相关字段
	Traffic       TrafficStats `json:"traffic"`       // 累计流量统计
	LastStartTime string       `json:"lastStartTime"` // 最近启动时间
	LastStopTime  string       `json:"lastStopTime"`  // 最近停止时间
}

// ResetRuntimeState 清除运行时状态（启用/进程/测速/健康/流量），
// 用于导出和导入，避免运行时数据污染配置文件。
func (lb *LoadBalanceNode) ResetRuntimeState() {
	lb.Enabled = false
	lb.ProcessID = 0
	lb.Latency = 0
	lb.DownloadSpeed = 0
	lb.TestStatus = ""
	lb.LastTestTime = ""
	lb.HealthStatus = ""
	lb.HealthLatency = 0
	lb.LastHealthCheck = ""
	lb.Traffic = TrafficStats{}
	lb.LastStartTime = ""
	lb.LastStopTime = ""
}

// ChainProxy 链式代理配置
type ChainProxy struct {
	ID         string   `json:"id"`         // 唯一标识
	Alias      string   `json:"alias"`      // 别名
	LocalType  string   `json:"localType"`  // 本地代理类型
	LocalPort  int      `json:"localPort"`  // 本地代理端口
	ChainNodes []string `json:"chainNodes"` // 链中的节点ID列表（可以包含普通节点或LB节点ID）
	Enabled    bool     `json:"enabled"`    // 启动状态
	ProcessID  int      `json:"processId"`  // 进程ID
	GroupID    string   `json:"groupId"`    // 所属分组ID
	GroupName  string   `json:"groupName"`  // 所属分组名称

	// 测速相关字段（通过本地代理端口测试）
	Latency       int     `json:"latency"`       // 延迟（毫秒）
	DownloadSpeed float64 `json:"downloadSpeed"` // 下载速度（MB/s）
	LastTestTime  string  `json:"lastTestTime"`  // 最后测速时间
	TestStatus    string  `json:"testStatus"`    // 测速状态: idle, testing, success, failed

	// 健康检查相关字段（通过本地代理端口检测，需已启动）
	HealthStatus    string `json:"healthStatus"`    // online, high_latency, timeout, checking
	HealthLatency   int    `json:"healthLatency"`   // 健康检查延迟（毫秒）
	LastHealthCheck string `json:"lastHealthCheck"` // 最后健康检查时间

	// 流量统计相关字段
	Traffic       TrafficStats `json:"traffic"`       // 累计流量统计
	LastStartTime string       `json:"lastStartTime"` // 最近启动时间
	LastStopTime  string       `json:"lastStopTime"`  // 最近停止时间
}

// ResetRuntimeState 清除运行时状态（启用/进程/测速/健康/流量），
// 用于导出和导入，避免运行时数据污染配置文件。
func (c *ChainProxy) ResetRuntimeState() {
	c.Enabled = false
	c.ProcessID = 0
	c.Latency = 0
	c.DownloadSpeed = 0
	c.TestStatus = ""
	c.LastTestTime = ""
	c.HealthStatus = ""
	c.HealthLatency = 0
	c.LastHealthCheck = ""
	c.Traffic = TrafficStats{}
	c.LastStartTime = ""
	c.LastStopTime = ""
}
