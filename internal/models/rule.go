package models

// ForwardRule 转发规则结构
type ForwardRule struct {
	ID            string `json:"id"`            // 唯一标识
	Alias         string `json:"alias"`         // 别名
	ProxyInfo     string `json:"proxyInfo"`     // 代理信息 (目标地址)
	LocalType     string `json:"localType"`     // 本地代理类型: auto, socks5, socks4, http, http2
	LocalPort     int    `json:"localPort"`     // 本地代理端口
	RealIP        string `json:"realIp"`        // 真实IP
	UseIPProxy    bool   `json:"useIpProxy"`    // 使用IP代理
	Enabled       bool   `json:"enabled"`       // 启动状态
	ProcessID     int    `json:"processId"`     // 进程ID
}

// Config 配置文件结构
type Config struct {
	AutoStart bool          `json:"autoStart"` // 开机自启
	Rules     []ForwardRule `json:"rules"`     // 转发规则列表
}
