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
	return BuildInbound(localPort, "inbound")
}

// BuildInbound 构建混合入站（同时支持 HTTP/SOCKS5）。
//
// 与 BuildOutbound / BuildRouteRule 一起构成配置生成的三层原语：
// 单节点、链式、故障转移、分片配置都由这三者组装而成，避免各自分叉——
// REALITY 曾因 sing-box 侧独立实现而被漏写，节点连不通却无任何配置报错。
func BuildInbound(localPort int, tag string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "mixed",
		"tag":         tag,
		"listen":      "0.0.0.0",
		"listen_port": localPort,
	}
}

// BuildRouteRule 构建「入站 -> 出站」的路由规则。
// 分片配置里每个节点各占一条，把自己的本地端口绑到自己的出站上。
func BuildRouteRule(inboundTag, outboundTag string) map[string]interface{} {
	return map[string]interface{}{
		"inbound":  []string{inboundTag},
		"outbound": outboundTag,
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
	applyReality(rule, tls)
	return tls
}

// applyReality 为 sing-box 的 tls 段补上 REALITY 与 uTLS 配置。
//
// 缺了这一段时 REALITY 节点会退化成普通 TLS 握手：公钥/short_id 被丢弃，
// 服务端不认这个握手而返回明文，客户端读到首字节 'H'(72) 报
// "unknown version: 72"，表现为节点启动后连不通。
//
// 注意与 Xray 的字段命名差异：sing-box 用 snake_case（public_key/short_id），
// 且 REALITY 必须搭配 utls —— 没有 uTLS 指纹时 sing-box 会拒绝加载配置。
func applyReality(rule *models.ProxyRule, tls map[string]interface{}) {
	if rule.Settings.Security != "reality" {
		return
	}

	reality := map[string]interface{}{"enabled": true}
	fingerprint := ""
	serverName := ""

	if r := rule.Settings.Reality; r != nil {
		if r.PublicKey != "" {
			reality["public_key"] = r.PublicKey
		}
		if r.ShortID != "" {
			reality["short_id"] = r.ShortID
		}
		fingerprint = r.Fingerprint
		serverName = r.ServerName
	}

	// SNI / 指纹可能记录在 TLS 段里，缺失时回退取用（与 Xray 侧一致）
	if rule.Settings.TLS != nil {
		if serverName == "" {
			serverName = rule.Settings.TLS.ServerName
		}
		if fingerprint == "" {
			fingerprint = rule.Settings.TLS.Fingerprint
		}
	}
	if serverName != "" {
		tls["server_name"] = serverName
	}

	// sing-box 的 REALITY 依赖 uTLS，指纹为空会导致配置加载失败
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	tls["utls"] = map[string]interface{}{
		"enabled":     true,
		"fingerprint": fingerprint,
	}

	// REALITY 自带证书校验，insecure 会被内核拒绝
	delete(tls, "insecure")

	tls["reality"] = reality
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

// ShardNodeTags 分片配置中一个节点占用的 inbound / outbound 标签。
type ShardNodeTags struct {
	Inbound  string
	Outbound string
}

// ShardInboundTag 返回节点在分片配置中的入站标签。
func ShardInboundTag(nodeID string) string { return "in_" + nodeID }

// ShardOutboundTag 返回节点在分片配置中的出站标签。
// 流量统计按 Clash API 连接的 chains 反查节点时也用这个标签。
func ShardOutboundTag(nodeID string) string { return "out_" + nodeID }

// SkippedNode 分片构建时被跳过的节点及原因。
type SkippedNode struct {
	NodeID string
	Alias  string
	Err    error
}

// BuildShardConfig 构建一份承载多个节点的分片配置。
//
// 每个节点占一组 inbound + outbound + route rule，各自监听原有的 LocalPort，
// 因此对上层完全透明：健康检查、测速、系统代理仍然连 127.0.0.1:<LocalPort>，
// 只是背后从「一节点一进程」变成了共享进程。
//
// 单个节点配置非法时跳过该节点并计入 skipped，而不是让整份配置失败——
// 否则一个坏节点会拖垮同片其余几百个正常节点。
// 返回的 skipped 供调用方记录日志、在界面上标记问题节点。
func BuildShardConfig(nodes []*models.ProxyRule, apiPort int) (*Config, []SkippedNode, error) {
	return BuildShardConfigWithPreProxy(nodes, nil, apiPort)
}

// BuildShardConfigWithPreProxy 构建分片配置，并让各节点经共享的前置代理出站。
//
// 前置代理在整份配置里只有一份出站，各节点用 detour 指向它——
// 因此设置了全局前置代理时，节点依然可以共享同一个进程，
// 不必退化成一节点一进程。
// preProxy 为 nil 时行为与 BuildShardConfig 一致（直连出站）。
func BuildShardConfigWithPreProxy(nodes []*models.ProxyRule, preProxy *models.ProxyRule, apiPort int) (*Config, []SkippedNode, error) {
	if len(nodes) == 0 {
		return nil, nil, fmt.Errorf("分片至少需要一个节点")
	}

	config := &Config{
		Log: map[string]interface{}{"level": "warn"},
	}

	var (
		inbounds  []map[string]interface{}
		outbounds []map[string]interface{}
		rules     []map[string]interface{}
		skipped   []SkippedNode
		seenPorts = make(map[int]string, len(nodes))
	)

	// 前置代理在整份配置里只有一份出站，各节点通过 detour 共用它
	const preProxyTag = "pre_proxy"
	usePreProxy := false
	if preProxy != nil {
		preOutbound, err := BuildOutbound(preProxy, preProxyTag)
		if err != nil {
			return nil, nil, fmt.Errorf("生成前置代理出站失败: %v", err)
		}
		outbounds = append(outbounds, preOutbound)
		usePreProxy = true
	}

	for _, node := range nodes {
		if node.LocalPort <= 0 {
			skipped = append(skipped, SkippedNode{node.ID, node.Alias,
				fmt.Errorf("没有分配本地端口")})
			continue
		}
		// 同一份配置里两个 inbound 抢同一个端口会导致整个进程起不来，
		// 这里提前挡下并说明是跟谁冲突
		if owner, exists := seenPorts[node.LocalPort]; exists {
			skipped = append(skipped, SkippedNode{node.ID, node.Alias,
				fmt.Errorf("本地端口 %d 与节点 %s 冲突", node.LocalPort, owner)})
			continue
		}

		outboundTag := ShardOutboundTag(node.ID)
		outbound, err := BuildOutbound(node, outboundTag)
		if err != nil {
			skipped = append(skipped, SkippedNode{node.ID, node.Alias, err})
			continue
		}
		// 节点自身就是前置代理时不能 detour 到自己，否则会成环
		if usePreProxy && node.ID != preProxy.ID {
			outbound["detour"] = preProxyTag
		}

		inboundTag := ShardInboundTag(node.ID)
		inbounds = append(inbounds, BuildInbound(node.LocalPort, inboundTag))
		outbounds = append(outbounds, outbound)
		rules = append(rules, BuildRouteRule(inboundTag, outboundTag))
		seenPorts[node.LocalPort] = node.Alias
	}

	if len(inbounds) == 0 {
		return nil, skipped, fmt.Errorf("分片内 %d 个节点全部无法构建配置", len(nodes))
	}

	// direct 出站兜底：route.final 必须指向一个存在的出站，
	// 而分片内没有「默认节点」的概念，未命中任何规则的流量走直连。
	outbounds = append(outbounds, map[string]interface{}{"type": "direct", "tag": "direct"})

	config.Inbounds = inbounds
	config.Outbounds = outbounds
	config.Route = map[string]interface{}{
		"rules": rules,
		"final": "direct",
	}

	if apiPort > 0 {
		AddClashAPI(config, apiPort)
	}

	return config, skipped, nil
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
