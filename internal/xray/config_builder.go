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
	Protocol      string                 `json:"protocol"`
	Settings      map[string]interface{} `json:"settings,omitempty"`
	StreamSettings *StreamSettings        `json:"streamSettings,omitempty"`
	Tag           string                 `json:"tag"`
}

// StreamSettings 传输层配置
type StreamSettings struct {
	Network     string                 `json:"network,omitempty"`
	Security    string                 `json:"security,omitempty"`
	TLSSettings *TLSConfig             `json:"tlsSettings,omitempty"`
	WSSettings  *WebSocketConfig       `json:"wsSettings,omitempty"`
	GRPCSettings *GRPCConfig           `json:"grpcSettings,omitempty"`
	HTTPSettings *HTTPConfig           `json:"httpSettings,omitempty"`
	TCPSettings  map[string]interface{} `json:"tcpSettings,omitempty"`
}

// TLSConfig TLS配置
type TLSConfig struct {
	ServerName         string   `json:"serverName,omitempty"`
	AllowInsecure      bool     `json:"allowInsecure,omitempty"`
	ALPN               []string `json:"alpn,omitempty"`
	DisableSystemRoot  bool     `json:"disableSystemRoot,omitempty"`
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

// ToJSON 将配置转换为 JSON 字符串
func (c *XrayConfig) ToJSON() (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化配置失败: %v", err)
	}
	return string(data), nil
}
