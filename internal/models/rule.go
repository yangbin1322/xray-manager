package models

// ProxyRule 代理规则结构
type ProxyRule struct {
	ID         string        `json:"id"`         // 唯一标识
	Alias      string        `json:"alias"`      // 别名
	LocalType  string        `json:"localType"`  // 本地代理类型: socks 或 http
	LocalPort  int           `json:"localPort"`  // 本地代理端口
	Protocol   string        `json:"protocol"`   // 远程协议类型: shadowsocks, vmess, vless, trojan
	ServerAddr string        `json:"serverAddr"` // 服务器地址
	ServerPort int           `json:"serverPort"` // 服务器端口
	Settings   ProxySettings `json:"settings"`   // 协议相关配置
	RealIP     string        `json:"realIp"`     // 真实IP
	Enabled    bool          `json:"enabled"`    // 启动状态
	ProcessID  int           `json:"processId"`  // 进程ID
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
	AutoStart bool        `json:"autoStart"` // 开机自启
	Rules     []ProxyRule `json:"rules"`     // 代理规则列表
}
