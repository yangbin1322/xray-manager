package models

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

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
	LastError  string        `json:"lastError"`  // 最近一次启动失败/不通的原因（成功后清空）

	// BypassPreProxy 为 true 时该节点直连出站，不经全局前置代理。
	//
	// 用于前置代理本身不可达、或该节点必须从本机 IP 出去的场景
	// （例如只允许特定来源 IP 的服务）。前置代理节点自身无需设置，
	// 构建配置时会自动跳过，避免 detour 成环。
	BypassPreProxy bool `json:"bypassPreProxy,omitempty"`

	// Verifying 表示已启动但连通性尚未验证完成。
	//
	// 启动只说明本地端口起来了，能否真正访问外网还要经代理请求一次探测点。
	// 批量启动上千节点时这一步要跑一阵子，期间若显示成「运行中」，
	// 用户会误以为已经可用；验证完成后由成功/失败分支清除该标记。
	// 不落盘：进程重启后一切从头验证，持久化没有意义。
	Verifying bool `json:"verifying,omitempty"`

	// 测速相关字段
	Latency       int     `json:"latency"`       // TCP 延迟（毫秒）
	DownloadSpeed float64 `json:"downloadSpeed"` // 下载速度（MB/s）
	LastTestTime  string  `json:"lastTestTime"`  // 最后测速时间
	TestStatus    string  `json:"testStatus"`    // 测速状态: idle, testing, success, failed

	// 健康检查相关字段
	HealthStatus    string `json:"healthStatus"`    // 健康状态: 空, checking, online, high_latency, timeout, dns_failed, tls_failed, reality_failed, ipv6_only
	HealthLatency   int    `json:"healthLatency"`   // 健康检查延迟（毫秒）
	LastHealthCheck string `json:"lastHealthCheck"` // 最后健康检查时间

	// 流量统计相关字段
	Traffic       TrafficStats `json:"traffic"`       // 累计流量统计
	LastStartTime string       `json:"lastStartTime"` // 最近启动时间
	LastStopTime  string       `json:"lastStopTime"`  // 最近停止时间

	// 分组相关字段
	GroupID         string `json:"groupId"`                   // 所属分组ID
	GroupName       string `json:"groupName"`                 // 所属分组名称
	SubscriptionID  string `json:"subscriptionId,omitempty"`  // 来源订阅ID（一个分组可含多个订阅的节点）
	SubscriptionURL string `json:"subscriptionUrl,omitempty"` // 订阅链接（如果来自订阅）
	Source          string `json:"source"`                    // 来源: manual（手动添加）, subscription（订阅导入）
}

// SubscriptionIdentity 返回用于订阅更新时匹配"同一个节点"的标识。
//
// 只用 serverAddr:serverPort 是不够的：机场常把多个节点挂在同一个 CDN IP 上
// （如 108.162.198.30:443 下挂着美国/日本等多个节点），仅靠地址端口会让它们
// 互相覆盖，每次更新都认不出旧节点，于是重复添加、越攒越多。
//
// 这里补上真正区分节点的字段：别名、协议，以及各协议的用户凭证和传输层参数
// （ws path / SNI / gRPC serviceName 等）。
func (r *ProxyRule) SubscriptionIdentity() string {
	s := &r.Settings

	// 各协议的用户标识（UUID/密码），同一 CDN 下不同节点通常凭证不同
	var credential string
	switch r.Protocol {
	case "vless":
		credential = s.VLessUserID
	case "vmess":
		credential = s.VMessUserID
	case "trojan":
		credential = s.TrojanPassword
	case "shadowsocks":
		credential = s.SSMethod + "|" + s.SSPassword
	case "hysteria2":
		credential = s.Hy2Password
	case "tuic":
		credential = s.TUICUserID + "|" + s.TUICPassword
	case "http":
		credential = s.HTTPUsername
	case "socks":
		credential = s.SOCKSUsername
	}

	// 传输层参数：同凭证同 IP 时，路径/SNI 往往才是唯一的区分点
	var path, host, sni string
	if s.WS != nil {
		path = s.WS.Path
		host = s.WS.Headers["Host"]
	}
	if s.GRPC != nil {
		path = s.GRPC.ServiceName
	}
	if s.H2 != nil {
		path = s.H2.Path
	}
	if s.TLS != nil {
		sni = s.TLS.ServerName
	}

	return strings.Join([]string{
		r.Alias, r.Protocol, r.ServerAddr, strconv.Itoa(r.ServerPort),
		credential, s.Network, s.Security, path, host, sni,
	}, "\x1f")
}

// ValidateForXray 校验该节点能否生成可被 Xray 接受的出站配置。
// 目前重点是 REALITY：security=reality 时 Xray 强制要求 realitySettings.publicKey，
// 缺失会直接拒绝加载整份配置，表现为进程刚启动就退出。
// 早期版本的分享链接解析丢弃了 pbk/sid/fp，这类历史节点需要重新更新订阅才能补齐。
func (r *ProxyRule) ValidateForXray() error {
	if r.Settings.Security != "reality" {
		return nil
	}
	if r.Settings.Reality == nil || r.Settings.Reality.PublicKey == "" {
		return fmt.Errorf("REALITY 节点缺少公钥(pbk)，请重新更新订阅以补全参数")
	}
	return nil
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
	Status    string `json:"status"`  // online, high_latency, timeout, dns_failed, tls_failed, reality_failed, ipv6_only
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
	Hy2PinSHA256    string `json:"hy2PinSHA256,omitempty"`    // 证书指纹固定（自签证书）；sing-box 无法用证书指纹校验，故有此值时启用 insecure
	Hy2Ports        string `json:"hy2Ports,omitempty"`        // 端口跳跃范围，如 "35000-39000"（对应 mport）

	// TUIC v5 配置
	TUICUserID       string `json:"tuicUserId,omitempty"`       // 用户 UUID
	TUICPassword     string `json:"tuicPassword,omitempty"`     // 密码
	TUICCongestion   string `json:"tuicCongestion,omitempty"`   // 拥塞控制: bbr, cubic, new_reno
	TUICUDPRelayMode string `json:"tuicUdpRelayMode,omitempty"` // UDP 中继模式: native, quic

	// 通用传输层配置
	Network  string           `json:"network,omitempty"`  // 传输协议: tcp, ws, grpc, h2
	Security string           `json:"security,omitempty"` // 传输层安全: none, tls, reality
	TLS      *TLSSettings     `json:"tls,omitempty"`      // TLS配置
	Reality  *RealitySettings `json:"reality,omitempty"`  // REALITY 配置（security=reality 时必填）
	WS       *WSSettings      `json:"ws,omitempty"`       // WebSocket配置
	GRPC     *GRPCSettings    `json:"grpc,omitempty"`     // gRPC配置
	H2       *H2Settings      `json:"h2,omitempty"`       // HTTP/2配置
}

// TLSSettings TLS配置
type TLSSettings struct {
	ServerName    string   `json:"serverName,omitempty"`    // 服务器名称 (SNI)
	ALPN          []string `json:"alpn,omitempty"`          // ALPN协议列表
	AllowInsecure bool     `json:"allowInsecure,omitempty"` // 允许不安全连接
	Fingerprint   string   `json:"fingerprint,omitempty"`   // uTLS 指纹 (fp)，如 chrome/firefox
}

// RealitySettings REALITY 配置。
// 分享链接中对应 pbk/sid/spx/fp 参数；Xray 要求 security=reality 时必须提供
// realitySettings，缺失会直接拒绝加载整份配置。
type RealitySettings struct {
	PublicKey   string `json:"publicKey,omitempty"`   // 服务端公钥 (pbk)
	ShortID     string `json:"shortId,omitempty"`     // 短 ID (sid)
	SpiderX     string `json:"spiderX,omitempty"`     // 爬虫路径 (spx)
	Fingerprint string `json:"fingerprint,omitempty"` // uTLS 指纹 (fp)
	ServerName  string `json:"serverName,omitempty"`  // SNI，通常取自 sni 参数
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
	AutoStart      bool               `json:"autoStart"`      // 开机自启
	Rules          []ProxyRule        `json:"rules"`          // 代理规则列表
	Groups         []Group            `json:"groups"`         // 分组列表
	Subscriptions  []Subscription     `json:"subscriptions"`  // 订阅列表
	LoadBalancers  []LoadBalanceNode  `json:"loadBalancers"`  // 故障转移节点列表
	ChainProxies   []ChainProxy       `json:"chainProxies"`   // 链式代理列表
	SessionRelays  []SessionRelay     `json:"sessionRelays"`  // 动态会话代理列表
	HealthCheck    HealthCheckConfig  `json:"healthCheck"`    // 健康检查配置
	SpeedTest      SpeedTestConfig    `json:"speedTest"`      // 测速配置
	Subscription   SubscriptionConfig `json:"subscription"`   // 订阅拉取配置
	HTTPAPI        HTTPAPIConfig      `json:"httpApi"`        // HTTP API 配置
	PreProxyNodeID string             `json:"preProxyNodeId"` // 前置代理节点 ID

	// PreProxyEnabled 前置代理开关。
	//
	// 与 PreProxyNodeID 分开：关掉开关时仍保留已选节点与生效范围，
	// 下次启用不必重新配一遍。
	// 兼容旧配置：老版本没有这个字段，加载时按「选了节点即启用」补齐。
	PreProxyEnabled bool `json:"preProxyEnabled"`

	// PreProxyGroupIDs 前置代理的生效范围：只有这些分组内的节点才经前置代理出站。
	// 为空表示对全部节点生效（兼容旧配置：此前前置代理是全局的，没有范围概念）。
	PreProxyGroupIDs []string `json:"preProxyGroupIds,omitempty"`

	// PreProxyExcludedIDs 例外节点：即使落在生效范围内也直连出站。
	// 用于个别节点必须从本机 IP 出去、或经前置代理反而不通的情况。
	PreProxyExcludedIDs []string `json:"preProxyExcludedIds,omitempty"`

	Update UpdateConfig `json:"update"` // 检测更新 / 自动更新
}

// SubscriptionConfig 订阅拉取的全局配置。
//
// 机场普遍按 User-Agent 返回不同格式：给 Shadowrocket / Clash 等客户端返回
// 完整节点列表，给未知 UA 则可能返回网页、精简列表甚至直接拒绝。
// 默认伪装成 Shadowrocket，兼容性最好。
type SubscriptionConfig struct {
	UserAgent string `json:"userAgent"` // 拉取订阅时使用的 User-Agent（空则用默认）
}

// PreProxyConfig 前置代理配置（用于 API 回显与设置）
type PreProxyConfig struct {
	NodeID  string `json:"nodeId"`  // 前置节点 ID
	Enabled bool   `json:"enabled"` // 是否启用（关闭时保留节点与范围配置）
	Alias   string `json:"alias"`   // 节点别名（只读回显）
	// Type 节点类型：rule / chain / lb，便于前端区分展示
	Type string `json:"type,omitempty"`

	// GroupIDs 生效范围，为空表示对全部节点生效
	GroupIDs []string `json:"groupIds,omitempty"`
	// ExcludedIDs 例外节点，即使在范围内也直连
	ExcludedIDs []string `json:"excludedIds,omitempty"`
}

// UpdateConfig 应用更新设置
type UpdateConfig struct {
	Configured   bool `json:"configured"`   // 是否已由用户/程序写过（区分零值与显式关闭）
	AutoCheck    bool `json:"autoCheck"`    // 启动时自动检查更新
	AutoDownload bool `json:"autoDownload"` // 发现新版本时自动下载并安装
}

// UpdateInfo 更新检查结果（API 回显）
type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	ReleaseName    string `json:"releaseName"`
	ReleaseNotes   string `json:"releaseNotes"`
	ReleaseURL     string `json:"releaseURL"`
	AssetName      string `json:"assetName"`
	AssetURL       string `json:"assetURL"`
	AssetSize      int64  `json:"assetSize"`
	PublishedAt    string `json:"publishedAt"`
	CheckedAt      string `json:"checkedAt"`
	Message        string `json:"message,omitempty"`
}

// HTTPAPIConfig HTTP API 服务配置。
type HTTPAPIConfig struct {
	Configured  bool   `json:"configured"`
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	AuthEnabled bool   `json:"authEnabled"`
	Token       string `json:"token,omitempty"`
}

// PortConflict 描述启动时发现的跨客户端本地端口冲突。
type PortConflict struct {
	ResourceID          string `json:"resourceId"`
	ResourceType        string `json:"resourceType"`
	Alias               string `json:"alias"`
	Port                int    `json:"port"`
	OwnerExecutablePath string `json:"ownerExecutablePath"`
	OwnerConfigPath     string `json:"ownerConfigPath"`
	OwnerResourceType   string `json:"ownerResourceType"`
	OwnerAlias          string `json:"ownerAlias"`
}

// SpeedTestConfig 测速配置（下载测速的目标 URL 与请求头）
type SpeedTestConfig struct {
	URL     string            `json:"url"`     // 测速下载 URL（空则用默认）
	Headers map[string]string `json:"headers"` // 自定义请求头（空则用默认浏览器请求头）
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
	SessionRelays []SessionRelay    `json:"sessionRelays"` // 动态会话代理列表
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
	RelayImported  int      `json:"relayImported"`
	Errors         []string `json:"errors"`   // 错误信息列表
	Warnings       []string `json:"warnings"` // 警告信息列表
}

// ImportShareResult 批量导入分享链接结果
type ImportShareResult struct {
	SuccessCount int      `json:"successCount"` // 成功数
	FailCount    int      `json:"failCount"`    // 失败数
	Errors       []string `json:"errors"`       // 每条失败的详细信息
}

// Group 节点分组。
//
// 一个分组可以关联多个订阅（多个订阅的节点汇入同一分组），因此这里不再保存
// 单个 SubscriptionID —— 归属关系以 Subscription.GroupID 为准，反向查询遍历订阅列表即可。
type Group struct {
	ID          string `json:"id"`          // 分组ID
	Name        string `json:"name"`        // 分组名称
	Description string `json:"description"` // 分组描述
	Source      string `json:"source"`      // 来源: manual, subscription
	CreatedAt   string `json:"createdAt"`   // 创建时间
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
	RealIP    string   `json:"realIp"`    // 真实IP
	LastError string   `json:"lastError"` // 最近一次启动失败/不通的原因（成功后清空）
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
	lb.RealIP = ""
	lb.LastError = ""
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

// FollowGlobalPreProxy 是 SessionRelay.PreProxyNodeID 的哨兵值，表示
// "跟随全局前置代理"。存哨兵而不是存具体节点 ID，这样用户改动全局设置后
// 会话代理会自动跟着变，不会留下一个已经过期的引用。
const FollowGlobalPreProxy = "__global__"

// SessionRelay 动态会话代理：单端口按客户端用户名动态切换住宅代理出口 IP。
//
// 住宅代理服务商把会话标识编码在用户名里（如 login__cr.au;sessid.123），
// 传统做法要为每个会话开一个端口。此类型只占一个端口，客户端换用户名
// 即换出口 IP，无需重启进程。上游连接可经前置节点加速。
// 监听端口为混合端口，HTTP 与 SOCKS5 客户端都可接入。
type SessionRelay struct {
	ID        string `json:"id"`        // 唯一标识
	Alias     string `json:"alias"`     // 别名
	LocalPort int    `json:"localPort"` // 本地监听端口（混合端口，同时支持 HTTP/SOCKS5 客户端）

	UpstreamAddr     string `json:"upstreamAddr"`     // 上游住宅网关，如 gw.dataimpulse.com:823
	UsernameTemplate string `json:"usernameTemplate"` // 上游用户名模板，含 {session} 占位符；为空表示透传客户端用户名
	UpstreamPassword string `json:"upstreamPassword"` // 上游固定密码
	LocalPassword    string `json:"localPassword"`    // 客户端需提供的密码（为空则不校验）

	// 前置加速节点 ID（普通节点/链式/故障转移）。
	// 空表示直连上游；FollowGlobalPreProxy 表示跟随全局前置代理设置。
	PreProxyNodeID string `json:"preProxyNodeId,omitempty"`

	Enabled   bool   `json:"enabled"`   // 启动状态
	LastError string `json:"lastError"` // 最近一次启动失败原因（成功后清空）
	GroupID   string `json:"groupId"`   // 所属分组ID
	GroupName string `json:"groupName"` // 所属分组名称

	// 运行时统计（不持久化速度，仅回显）
	ActiveConns  int64 `json:"activeConns"`  // 当前活跃连接数
	TotalConns   int64 `json:"totalConns"`   // 累计连接数
	SessionCount int   `json:"sessionCount"` // 出现过的不同会话数

	// 流量统计
	Traffic       TrafficStats `json:"traffic"`
	LastStartTime string       `json:"lastStartTime"`
	LastStopTime  string       `json:"lastStopTime"`
}

// SessionRelayStats 会话代理运行时统计快照（通过事件推送给前端，不持久化）。
type SessionRelayStats struct {
	RelayID      string  `json:"relayId"`
	ActiveConns  int64   `json:"activeConns"`  // 当前活跃连接数
	TotalConns   int64   `json:"totalConns"`   // 累计连接数
	FailedConns  int64   `json:"failedConns"`  // 建立上游失败的连接数
	SessionCount int     `json:"sessionCount"` // 出现过的不同会话标识数
	BytesUp      int64   `json:"bytesUp"`
	BytesDown    int64   `json:"bytesDown"`
	UpSpeed      float64 `json:"upSpeed"`   // 字节/秒
	DownSpeed    float64 `json:"downSpeed"` // 字节/秒
}

// Validate 校验动态会话代理配置。
func (s *SessionRelay) Validate() error {
	if s.UpstreamAddr == "" {
		return fmt.Errorf("上游网关地址不能为空")
	}
	if _, _, err := net.SplitHostPort(s.UpstreamAddr); err != nil {
		return fmt.Errorf("上游网关地址需为 host:port 格式，例如 gw.dataimpulse.com:823")
	}
	if s.UsernameTemplate != "" && !strings.Contains(s.UsernameTemplate, "{session}") {
		return fmt.Errorf("用户名模板必须包含 {session} 占位符")
	}
	if s.LocalPort < 0 || s.LocalPort > 65535 {
		return fmt.Errorf("本地端口无效: %d", s.LocalPort)
	}
	if s.Alias == "" {
		s.Alias = fmt.Sprintf("会话代理-%s", s.UpstreamAddr)
	}
	return nil
}

// ResetRuntimeState 清除运行时状态，用于导出和导入。
func (s *SessionRelay) ResetRuntimeState() {
	s.Enabled = false
	s.LastError = ""
	s.ActiveConns = 0
	s.TotalConns = 0
	s.SessionCount = 0
	s.Traffic = TrafficStats{}
	s.LastStartTime = ""
	s.LastStopTime = ""
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
	RealIP     string   `json:"realIp"`     // 真实IP
	LastError  string   `json:"lastError"`  // 最近一次启动失败/不通的原因（成功后清空）
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
	c.RealIP = ""
	c.LastError = ""
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
