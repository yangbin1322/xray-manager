// Package singbox 生成 sing-box 内核配置。
// Xray-core 不支持 Hysteria2 / TUIC 协议，凡是涉及这两种协议的节点
//（普通节点、链式代理、故障转移）统一改用 sing-box 内核运行。
package singbox

import (
	"encoding/json"
	"fmt"
	"strings"
	"xray-manager/internal/models"
)

// NeedsSingBox 判断协议是否需要 sing-box 内核运行
func NeedsSingBox(protocol string) bool {
	return protocol == "hysteria2" || protocol == "tuic"
}

// normalizePorts 将端口跳跃范围规范为 sing-box server_ports 格式（"起:止"）。
// 接受 "35000-39000" 或 "35000:39000"，无效则返回空字符串。
func normalizePorts(ports string) string {
	ports = strings.TrimSpace(ports)
	if ports == "" {
		return ""
	}
	ports = strings.ReplaceAll(ports, "-", ":")
	// 必须形如 a:b
	if !strings.Contains(ports, ":") {
		return ""
	}
	return ports
}

// RulesNeedSingBox 判断一组节点中是否有需要 sing-box 内核的协议
func RulesNeedSingBox(rules []*models.ProxyRule) bool {
	for _, r := range rules {
		if NeedsSingBox(r.Protocol) {
			return true
		}
	}
	return false
}

// Config sing-box 配置结构（使用 map 保持灵活性）
type Config struct {
	Log          map[string]interface{}   `json:"log,omitempty"`
	Inbounds     []map[string]interface{} `json:"inbounds"`
	Outbounds    []map[string]interface{} `json:"outbounds"`
	Route        map[string]interface{}   `json:"route,omitempty"`
	Experimental map[string]interface{}   `json:"experimental,omitempty"`
}

// ToJSON 序列化为 JSON
func (c *Config) ToJSON() (string, error) {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化 sing-box 配置失败: %v", err)
	}
	return string(data), nil
}

// buildMixedInbound 混合入站（同时支持 HTTP/SOCKS5）
func buildMixedInbound(localPort int) map[string]interface{} {
	return map[string]interface{}{
		"type":        "mixed",
		"tag":         "inbound",
		"listen":      "0.0.0.0",
		"listen_port": localPort,
	}
}

// buildTLS 构建 sing-box TLS 配置
func buildTLS(rule *models.ProxyRule, defaultEnabled bool) map[string]interface{} {
	enabled := defaultEnabled || rule.Settings.Security == "tls" || rule.Settings.Security == "reality"
	if !enabled {
		return nil
	}
	tls := map[string]interface{}{
		"enabled": true,
	}
	if rule.Settings.TLS != nil {
		if rule.Settings.TLS.ServerName != "" {
			tls["server_name"] = rule.Settings.TLS.ServerName
		}
		if rule.Settings.TLS.AllowInsecure {
			tls["insecure"] = true
		}
		if len(rule.Settings.TLS.ALPN) > 0 {
			tls["alpn"] = rule.Settings.TLS.ALPN
		}
	}
	return tls
}

// buildTransport 构建 sing-box 传输层配置（ws/grpc/h2）
func buildTransport(rule *models.ProxyRule) map[string]interface{} {
	switch rule.Settings.Network {
	case "ws":
		transport := map[string]interface{}{"type": "ws"}
		if rule.Settings.WS != nil {
			if rule.Settings.WS.Path != "" {
				transport["path"] = rule.Settings.WS.Path
			}
			if len(rule.Settings.WS.Headers) > 0 {
				transport["headers"] = rule.Settings.WS.Headers
			}
		}
		return transport
	case "grpc":
		transport := map[string]interface{}{"type": "grpc"}
		if rule.Settings.GRPC != nil && rule.Settings.GRPC.ServiceName != "" {
			transport["service_name"] = rule.Settings.GRPC.ServiceName
		}
		return transport
	case "h2":
		transport := map[string]interface{}{"type": "http"}
		if rule.Settings.H2 != nil {
			if rule.Settings.H2.Path != "" {
				transport["path"] = rule.Settings.H2.Path
			}
			if len(rule.Settings.H2.Host) > 0 {
				transport["host"] = rule.Settings.H2.Host
			}
		}
		return transport
	}
	return nil
}

// BuildOutbound 将节点转换为 sing-box 出站配置
func BuildOutbound(rule *models.ProxyRule, tag string) (map[string]interface{}, error) {
	outbound := map[string]interface{}{
		"tag":         tag,
		"server":      rule.ServerAddr,
		"server_port": rule.ServerPort,
	}

	switch rule.Protocol {
	case "hysteria2":
		outbound["type"] = "hysteria2"
		outbound["password"] = rule.Settings.Hy2Password
		if rule.Settings.Hy2UpMbps > 0 {
			outbound["up_mbps"] = rule.Settings.Hy2UpMbps
		}
		if rule.Settings.Hy2DownMbps > 0 {
			outbound["down_mbps"] = rule.Settings.Hy2DownMbps
		}
		if rule.Settings.Hy2Obfs != "" {
			outbound["obfs"] = map[string]interface{}{
				"type":     rule.Settings.Hy2Obfs,
				"password": rule.Settings.Hy2ObfsPassword,
			}
		}
		// 端口跳跃：mport 范围 "35000-39000" → sing-box server_ports ["35000:39000"]
		if ports := normalizePorts(rule.Settings.Hy2Ports); ports != "" {
			outbound["server_ports"] = []string{ports}
		}
		// Hysteria2 强制 TLS
		tls := buildTLS(rule, true)
		if _, ok := tls["alpn"]; !ok {
			tls["alpn"] = []string{"h3"}
		}
		outbound["tls"] = tls

	case "tuic":
		outbound["type"] = "tuic"
		outbound["uuid"] = rule.Settings.TUICUserID
		outbound["password"] = rule.Settings.TUICPassword
		congestion := rule.Settings.TUICCongestion
		if congestion == "" {
			congestion = "bbr"
		}
		outbound["congestion_control"] = congestion
		udpMode := rule.Settings.TUICUDPRelayMode
		if udpMode == "" {
			udpMode = "native"
		}
		outbound["udp_relay_mode"] = udpMode
		// TUIC 强制 TLS
		tls := buildTLS(rule, true)
		if _, ok := tls["alpn"]; !ok {
			tls["alpn"] = []string{"h3"}
		}
		outbound["tls"] = tls

	case "shadowsocks":
		outbound["type"] = "shadowsocks"
		outbound["method"] = rule.Settings.SSMethod
		outbound["password"] = rule.Settings.SSPassword

	case "vmess":
		outbound["type"] = "vmess"
		outbound["uuid"] = rule.Settings.VMessUserID
		outbound["alter_id"] = rule.Settings.VMessAlterID
		security := rule.Settings.VMessSecurity
		if security == "" {
			security = "auto"
		}
		outbound["security"] = security
		if tls := buildTLS(rule, false); tls != nil {
			outbound["tls"] = tls
		}
		if transport := buildTransport(rule); transport != nil {
			outbound["transport"] = transport
		}

	case "vless":
		outbound["type"] = "vless"
		outbound["uuid"] = rule.Settings.VLessUserID
		if rule.Settings.VLessFlow != "" {
			outbound["flow"] = rule.Settings.VLessFlow
		}
		if tls := buildTLS(rule, false); tls != nil {
			outbound["tls"] = tls
		}
		if transport := buildTransport(rule); transport != nil {
			outbound["transport"] = transport
		}

	case "trojan":
		outbound["type"] = "trojan"
		outbound["password"] = rule.Settings.TrojanPassword
		// Trojan 默认 TLS
		if tls := buildTLS(rule, true); tls != nil {
			outbound["tls"] = tls
		}
		if transport := buildTransport(rule); transport != nil {
			outbound["transport"] = transport
		}

	case "http":
		outbound["type"] = "http"
		if rule.Settings.HTTPUsername != "" {
			outbound["username"] = rule.Settings.HTTPUsername
			outbound["password"] = rule.Settings.HTTPPassword
		}

	case "socks":
		outbound["type"] = "socks"
		outbound["version"] = "5"
		if rule.Settings.SOCKSUsername != "" {
			outbound["username"] = rule.Settings.SOCKSUsername
			outbound["password"] = rule.Settings.SOCKSPassword
		}

	default:
		return nil, fmt.Errorf("sing-box 不支持的协议类型: %s", rule.Protocol)
	}

	return outbound, nil
}

// newBaseConfig 创建基础配置
func newBaseConfig(localPort int) *Config {
	return &Config{
		Log: map[string]interface{}{
			"level": "warn",
		},
		Inbounds: []map[string]interface{}{buildMixedInbound(localPort)},
	}
}

// AddClashAPI 添加 Clash API（用于流量统计查询 /connections）
func AddClashAPI(config *Config, apiPort int) {
	config.Experimental = map[string]interface{}{
		"clash_api": map[string]interface{}{
			"external_controller": fmt.Sprintf("127.0.0.1:%d", apiPort),
		},
	}
}

// BuildConfig 构建单节点配置
func BuildConfig(rule *models.ProxyRule) (*Config, error) {
	config := newBaseConfig(rule.LocalPort)

	outbound, err := BuildOutbound(rule, "proxy")
	if err != nil {
		return nil, err
	}

	config.Outbounds = []map[string]interface{}{
		outbound,
		{"type": "direct", "tag": "direct"},
	}
	config.Route = map[string]interface{}{
		"final": "proxy",
	}

	return config, nil
}

// BuildChainConfig 构建链式代理配置
// chainRules 按顺序排列：第一个是入口节点，最后一个是落地节点。
// sing-box 通过 outbound 的 detour 字段实现链式转发。
func BuildChainConfig(localPort int, chainRules []*models.ProxyRule) (*Config, error) {
	if len(chainRules) < 2 {
		return nil, fmt.Errorf("链式代理需要至少2个节点")
	}

	config := newBaseConfig(localPort)

	var outbounds []map[string]interface{}
	for i, rule := range chainRules {
		tag := fmt.Sprintf("chain_%d", i)
		outbound, err := BuildOutbound(rule, tag)
		if err != nil {
			return nil, err
		}
		// 每个节点通过前一个节点建立连接（第一个节点直连）
		if i > 0 {
			outbound["detour"] = fmt.Sprintf("chain_%d", i-1)
		}
		outbounds = append(outbounds, outbound)
	}
	outbounds = append(outbounds, map[string]interface{}{"type": "direct", "tag": "direct"})

	config.Outbounds = outbounds
	config.Route = map[string]interface{}{
		"final": fmt.Sprintf("chain_%d", len(chainRules)-1),
	}

	return config, nil
}

// BuildLoadBalanceConfig 构建故障转移配置（urltest 自动选择延迟最低的节点）
// preProxy 非空时，各子节点通过 detour 经前置代理出站（子节点自身等于前置节点时除外）
func BuildLoadBalanceConfig(localPort int, nodes []*models.ProxyRule, preProxy *models.ProxyRule) (*Config, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("故障转移节点需要至少一个子节点")
	}

	config := newBaseConfig(localPort)

	var outbounds []map[string]interface{}
	var memberTags []string

	if preProxy != nil {
		preOutbound, err := BuildOutbound(preProxy, "pre_proxy")
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, preOutbound)
	}

	for i, node := range nodes {
		tag := fmt.Sprintf("proxy_%d", i)
		outbound, err := BuildOutbound(node, tag)
		if err != nil {
			return nil, err
		}
		if preProxy != nil && node.ID != preProxy.ID {
			outbound["detour"] = "pre_proxy"
		}
		outbounds = append(outbounds, outbound)
		memberTags = append(memberTags, tag)
	}

	outbounds = append(outbounds, map[string]interface{}{
		"type":      "urltest",
		"tag":       "proxy",
		"outbounds": memberTags,
		"url":       "https://www.gstatic.com/generate_204",
		"interval":  "1m",
	})
	outbounds = append(outbounds, map[string]interface{}{"type": "direct", "tag": "direct"})

	config.Outbounds = outbounds
	config.Route = map[string]interface{}{
		"final": "proxy",
	}

	return config, nil
}
