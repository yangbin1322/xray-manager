package xray

import (
	"encoding/json"
	"fmt"
	"xray-manager/internal/models"
)

// XrayConfig Xray-core 配置结构
type XrayConfig struct {
	Log       *LogConfig       `json:"log,omitempty"`
	Inbounds  []InboundConfig  `json:"inbounds"`
	Outbounds []OutboundConfig `json:"outbounds"`
	Routing   *RoutingConfig   `json:"routing,omitempty"`
}

// RoutingConfig 路由配置
type RoutingConfig struct {
	Rules []RoutingRule `json:"rules"`
}

// RoutingRule 路由规则
type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

// LogConfig 日志配置
type LogConfig struct {
	Loglevel string `json:"loglevel"`
}

// InboundConfig 入站配置
type InboundConfig struct {
	Listen   string                 `json:"listen"`
	Port     int                    `json:"port"`
	Protocol string                 `json:"protocol"`
	Settings map[string]interface{} `json:"settings,omitempty"`
	Tag      string                 `json:"tag"`
}

// OutboundConfig 出站配置
type OutboundConfig struct {
	Protocol       string                 `json:"protocol"`
	Settings       map[string]interface{} `json:"settings,omitempty"`
	StreamSettings *StreamSettings        `json:"streamSettings,omitempty"`
	Tag            string                 `json:"tag"`
	ProxySettings  *ProxySettingsConfig   `json:"proxySettings,omitempty"`
}

// ProxySettingsConfig 代理链配置（指定下一跳）
type ProxySettingsConfig struct {
	Tag                string `json:"tag"`
	TransportLayer     bool   `json:"transportLayer"`
}

// StreamSettings 传输层配置
type StreamSettings struct {
	Network      string                 `json:"network,omitempty"`
	Security     string                 `json:"security,omitempty"`
	TLSSettings  *TLSConfig             `json:"tlsSettings,omitempty"`
	WSSettings   *WebSocketConfig       `json:"wsSettings,omitempty"`
	GRPCSettings *GRPCConfig            `json:"grpcSettings,omitempty"`
	HTTPSettings *HTTPConfig            `json:"httpSettings,omitempty"`
	TCPSettings  map[string]interface{} `json:"tcpSettings,omitempty"`
}

// TLSConfig TLS配置
type TLSConfig struct {
	ServerName        string   `json:"serverName,omitempty"`
	AllowInsecure     bool     `json:"allowInsecure,omitempty"`
	ALPN              []string `json:"alpn,omitempty"`
	DisableSystemRoot bool     `json:"disableSystemRoot,omitempty"`
}

// WebSocketConfig WebSocket配置
type WebSocketConfig struct {
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// GRPCConfig gRPC配置
type GRPCConfig struct {
	ServiceName string `json:"serviceName,omitempty"`
}

// HTTPConfig HTTP/2配置
type HTTPConfig struct {
	Path []string `json:"path,omitempty"`
	Host []string `json:"host,omitempty"`
}

// BuildConfig 根据规则生成 Xray 配置
func BuildConfig(rule *models.ProxyRule) (*XrayConfig, error) {
	config := &XrayConfig{
		Log: &LogConfig{
			Loglevel: "warning",
		},
		Inbounds:  buildInbounds(rule),
		Outbounds: buildOutbounds(rule),
	}

	return config, nil
}

// buildInbounds 构建入站配置
func buildInbounds(rule *models.ProxyRule) []InboundConfig {
	inbound := InboundConfig{
		Listen:   "0.0.0.0",
		Port:     rule.LocalPort,
		Protocol: rule.LocalType,
		Tag:      "inbound",
	}

	// 根据本地代理类型设置
	switch rule.LocalType {
	case "socks5", "socks":
		inbound.Settings = map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
		}
	case "http":
		inbound.Settings = map[string]interface{}{
			"allowTransparent": false,
		}
	}

	return []InboundConfig{inbound}
}

// buildOutbounds 构建出站配置
func buildOutbounds(rule *models.ProxyRule) []OutboundConfig {
	var outbounds []OutboundConfig

	// 主要出站配置
	mainOutbound := OutboundConfig{
		Tag:      "proxy",
		Protocol: rule.Protocol,
	}

	// 根据协议类型构建配置
	switch rule.Protocol {
	case "shadowsocks":
		mainOutbound.Settings = buildShadowsocksSettings(rule)
	case "vmess":
		mainOutbound.Settings = buildVMessSettings(rule)
	case "vless":
		mainOutbound.Settings = buildVLessSettings(rule)
	case "trojan":
		mainOutbound.Settings = buildTrojanSettings(rule)
	case "http":
		mainOutbound.Settings = buildHTTPSettings(rule)
	case "socks":
		mainOutbound.Settings = buildSOCKSSettings(rule)
	default:
		mainOutbound.Settings = map[string]interface{}{}
	}

	// 构建传输层配置
	mainOutbound.StreamSettings = buildStreamSettings(rule)

	outbounds = append(outbounds, mainOutbound)

	// 添加直连出站（freedom）
	outbounds = append(outbounds, OutboundConfig{
		Protocol: "freedom",
		Tag:      "direct",
	})

	// 添加黑洞出站（blackhole）
	outbounds = append(outbounds, OutboundConfig{
		Protocol: "blackhole",
		Tag:      "block",
	})

	return outbounds
}

// buildShadowsocksSettings 构建 Shadowsocks 设置
func buildShadowsocksSettings(rule *models.ProxyRule) map[string]interface{} {
	servers := []map[string]interface{}{
		{
			"address":  rule.ServerAddr,
			"port":     rule.ServerPort,
			"method":   rule.Settings.SSMethod,
			"password": rule.Settings.SSPassword,
		},
	}

	return map[string]interface{}{
		"servers": servers,
	}
}

// buildVMessSettings 构建 VMess 设置
func buildVMessSettings(rule *models.ProxyRule) map[string]interface{} {
	security := rule.Settings.VMessSecurity
	if security == "" {
		security = "auto"
	}

	users := []map[string]interface{}{
		{
			"id":       rule.Settings.VMessUserID,
			"alterId":  rule.Settings.VMessAlterID,
			"security": security,
		},
	}

	vnext := []map[string]interface{}{
		{
			"address": rule.ServerAddr,
			"port":    rule.ServerPort,
			"users":   users,
		},
	}

	return map[string]interface{}{
		"vnext": vnext,
	}
}

// buildVLessSettings 构建 VLESS 设置
func buildVLessSettings(rule *models.ProxyRule) map[string]interface{} {
	encryption := rule.Settings.VLessEncryption
	if encryption == "" {
		encryption = "none"
	}

	user := map[string]interface{}{
		"id":         rule.Settings.VLessUserID,
		"encryption": encryption,
	}

	// 如果有 flow 配置（XTLS）
	if rule.Settings.VLessFlow != "" {
		user["flow"] = rule.Settings.VLessFlow
	}

	vnext := []map[string]interface{}{
		{
			"address": rule.ServerAddr,
			"port":    rule.ServerPort,
			"users":   []map[string]interface{}{user},
		},
	}

	return map[string]interface{}{
		"vnext": vnext,
	}
}

// buildTrojanSettings 构建 Trojan 设置
func buildTrojanSettings(rule *models.ProxyRule) map[string]interface{} {
	servers := []map[string]interface{}{
		{
			"address":  rule.ServerAddr,
			"port":     rule.ServerPort,
			"password": rule.Settings.TrojanPassword,
		},
	}

	return map[string]interface{}{
		"servers": servers,
	}
}

// buildStreamSettings 构建传输层设置
func buildStreamSettings(rule *models.ProxyRule) *StreamSettings {
	stream := &StreamSettings{
		Network:  rule.Settings.Network,
		Security: rule.Settings.Security,
	}

	// 如果没有指定 network，默认为 tcp
	if stream.Network == "" {
		stream.Network = "tcp"
	}

	// 如果没有指定 security，默认为 none
	if stream.Security == "" {
		stream.Security = "none"
	}

	// TLS 配置
	if stream.Security == "tls" && rule.Settings.TLS != nil {
		stream.TLSSettings = &TLSConfig{
			ServerName:    rule.Settings.TLS.ServerName,
			AllowInsecure: rule.Settings.TLS.AllowInsecure,
			ALPN:          rule.Settings.TLS.ALPN,
		}
	}

	// WebSocket 配置
	if stream.Network == "ws" && rule.Settings.WS != nil {
		stream.WSSettings = &WebSocketConfig{
			Path:    rule.Settings.WS.Path,
			Headers: rule.Settings.WS.Headers,
		}
	}

	// gRPC 配置
	if stream.Network == "grpc" && rule.Settings.GRPC != nil {
		stream.GRPCSettings = &GRPCConfig{
			ServiceName: rule.Settings.GRPC.ServiceName,
		}
	}

	// HTTP/2 配置
	if stream.Network == "h2" && rule.Settings.H2 != nil {
		stream.HTTPSettings = &HTTPConfig{
			Path: []string{rule.Settings.H2.Path},
			Host: rule.Settings.H2.Host,
		}
	}

	return stream
}

// buildHTTPSettings 构建 HTTP 代理设置
func buildHTTPSettings(rule *models.ProxyRule) map[string]interface{} {
	server := map[string]interface{}{
		"address": rule.ServerAddr,
		"port":    rule.ServerPort,
	}

	// 如果提供了用户名和密码，添加认证
	if rule.Settings.HTTPUsername != "" && rule.Settings.HTTPPassword != "" {
		users := []map[string]interface{}{
			{
				"user": rule.Settings.HTTPUsername,
				"pass": rule.Settings.HTTPPassword,
			},
		}
		server["users"] = users
	}

	return map[string]interface{}{
		"servers": []map[string]interface{}{server},
	}
}

// buildSOCKSSettings 构建 SOCKS 代理设置
func buildSOCKSSettings(rule *models.ProxyRule) map[string]interface{} {
	// 确定 SOCKS 版本，默认为 socks5
	version := rule.Settings.SOCKSVersion
	if version == "" {
		version = "5"
	} else if version == "socks5" {
		version = "5"
	} else if version == "socks4" {
		version = "4"
	}

	server := map[string]interface{}{
		"address": rule.ServerAddr,
		"port":    rule.ServerPort,
	}

	// 如果提供了用户名和密码，添加认证（仅 SOCKS5 支持）
	if version == "5" && rule.Settings.SOCKSUsername != "" && rule.Settings.SOCKSPassword != "" {
		users := []map[string]interface{}{
			{
				"user": rule.Settings.SOCKSUsername,
				"pass": rule.Settings.SOCKSPassword,
			},
		}
		server["users"] = users
	}

	return map[string]interface{}{
		"servers": []map[string]interface{}{server},
	}
}

// BuildLoadBalanceConfig 构建负载均衡配置
// nodes: 子节点列表，lb: 负载均衡节点本身
func BuildLoadBalanceConfig(lb *models.LoadBalanceNode, nodes []*models.ProxyRule) (*XrayConfig, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("负载均衡节点需要至少一个子节点")
	}

	config := &XrayConfig{
		Log: &LogConfig{
			Loglevel: "warning",
		},
	}

	// 入站
	inbound := InboundConfig{
		Listen:   "0.0.0.0",
		Port:     lb.LocalPort,
		Protocol: lb.LocalType,
		Tag:      "inbound",
	}
	switch lb.LocalType {
	case "socks5", "socks":
		inbound.Settings = map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
		}
	case "http":
		inbound.Settings = map[string]interface{}{
			"allowTransparent": false,
		}
	}
	config.Inbounds = []InboundConfig{inbound}

	// 为每个子节点创建出站
	var outbounds []OutboundConfig
	var balancerSelectors []string

	for i, node := range nodes {
		tag := fmt.Sprintf("proxy_%d", i)
		outbound := OutboundConfig{
			Tag:      tag,
			Protocol: node.Protocol,
		}

		switch node.Protocol {
		case "shadowsocks":
			outbound.Settings = buildShadowsocksSettings(node)
		case "vmess":
			outbound.Settings = buildVMessSettings(node)
		case "vless":
			outbound.Settings = buildVLessSettings(node)
		case "trojan":
			outbound.Settings = buildTrojanSettings(node)
		case "http":
			outbound.Settings = buildHTTPSettings(node)
		case "socks":
			outbound.Settings = buildSOCKSSettings(node)
		}
		outbound.StreamSettings = buildStreamSettings(node)

		outbounds = append(outbounds, outbound)
		balancerSelectors = append(balancerSelectors, tag)
	}

	// 直连和黑洞
	outbounds = append(outbounds, OutboundConfig{Protocol: "freedom", Tag: "direct"})
	outbounds = append(outbounds, OutboundConfig{Protocol: "blackhole", Tag: "block"})

	config.Outbounds = outbounds

	// 路由：入站 → 第一个节点，若失败则下一个（通过 Xray 的 balancer 实现 fallback）
	// 注意：Xray 原生不直接支持 outbound fallback，使用路由规则按顺序尝试
	// 这里我们使用第一个节点作为默认出站，客户端应用层实现 fallback
	config.Routing = &RoutingConfig{
		Rules: []RoutingRule{
			{
				Type:        "field",
				InboundTag:  []string{"inbound"},
				OutboundTag: balancerSelectors[0],
			},
		},
	}

	return config, nil
}

// BuildChainConfig 构建链式代理配置
// chainRules: 按顺序排列的代理节点列表（最后一个是落地节点）
// localType: 本地代理类型, localPort: 本地代理端口
func BuildChainConfig(localType string, localPort int, chainRules []*models.ProxyRule) (*XrayConfig, error) {
	if len(chainRules) < 2 {
		return nil, fmt.Errorf("链式代理需要至少2个节点")
	}

	config := &XrayConfig{
		Log: &LogConfig{
			Loglevel: "warning",
		},
	}

	// 入站
	inbound := InboundConfig{
		Listen:   "0.0.0.0",
		Port:     localPort,
		Protocol: localType,
		Tag:      "inbound",
	}
	switch localType {
	case "socks5", "socks":
		inbound.Settings = map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
		}
	case "http":
		inbound.Settings = map[string]interface{}{
			"allowTransparent": false,
		}
	}
	config.Inbounds = []InboundConfig{inbound}

	// 构建出站链
	// Xray chain: proxy_0 -> proxy_1 -> ... -> proxy_n
	// 通过 proxySettings.tag 链接
	var outbounds []OutboundConfig

	for i, rule := range chainRules {
		tag := fmt.Sprintf("chain_%d", i)
		outbound := OutboundConfig{
			Tag:      tag,
			Protocol: rule.Protocol,
		}

		switch rule.Protocol {
		case "shadowsocks":
			outbound.Settings = buildShadowsocksSettings(rule)
		case "vmess":
			outbound.Settings = buildVMessSettings(rule)
		case "vless":
			outbound.Settings = buildVLessSettings(rule)
		case "trojan":
			outbound.Settings = buildTrojanSettings(rule)
		case "http":
			outbound.Settings = buildHTTPSettings(rule)
		case "socks":
			outbound.Settings = buildSOCKSSettings(rule)
		}
		outbound.StreamSettings = buildStreamSettings(rule)

		// 链式代理：除了最后一个节点，每个节点指向下一跳
		if i < len(chainRules)-1 {
			nextTag := fmt.Sprintf("chain_%d", i+1)
			outbound.ProxySettings = &ProxySettingsConfig{
				Tag:            nextTag,
				TransportLayer: true,
			}
		}

		outbounds = append(outbounds, outbound)
	}

	// 直连和黑洞
	outbounds = append(outbounds, OutboundConfig{Protocol: "freedom", Tag: "direct"})
	outbounds = append(outbounds, OutboundConfig{Protocol: "blackhole", Tag: "block"})

	config.Outbounds = outbounds

	// 路由：将入站流量导向第一个节点
	config.Routing = &RoutingConfig{
		Rules: []RoutingRule{
			{
				Type:        "field",
				InboundTag:  []string{"inbound"},
				OutboundTag: "chain_0",
			},
		},
	}

	return config, nil
}

// ToJSON 将配置转换为 JSON 字符串
func (c *XrayConfig) ToJSON() (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化配置失败: %v", err)
	}
	return string(data), nil
}
