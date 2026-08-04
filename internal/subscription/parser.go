package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"xray-manager/internal/models"
	sharelink "xray-manager/internal/parser"

	"gopkg.in/yaml.v3"
)

// parseClashBandwidth 解析 Clash Hysteria2 的带宽字段（可能是 int 或 "100 Mbps" 形式的字符串）
func parseClashBandwidth(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		s := strings.TrimSpace(strings.ToLower(val))
		s = strings.TrimSuffix(s, "mbps")
		s = strings.TrimSpace(s)
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return 0
}

// maxParseErrorLogs 单次订阅解析最多输出多少条节点级失败日志。
// 每条日志都会推一个事件给前端，上万节点的订阅若逐条输出会拖垮界面。
const maxParseErrorLogs = 20

// Parser 订阅解析器
type Parser struct {
	logFunc func(string)
}

// logSkipped 汇总输出被截断的解析失败条数
func (p *Parser) logSkipped(failed int) {
	if failed > maxParseErrorLogs {
		p.log(fmt.Sprintf("[订阅] 另有 %d 个节点解析失败（已省略日志）", failed-maxParseErrorLogs))
	}
}

// NewParser 创建解析器
func NewParser(logFunc func(string)) *Parser {
	return &Parser{
		logFunc: logFunc,
	}
}

// FetchAndParse 获取并解析订阅（直连）
func (p *Parser) FetchAndParse(subscriptionURL string) ([]models.ProxyRule, string, error) {
	return p.FetchAndParseWithProxy(subscriptionURL, "")
}

// FetchAndParseWithProxy 通过指定代理获取并解析订阅
// proxyURL 支持 http:// 和 socks5:// 格式，为空时直连
func (p *Parser) FetchAndParseWithProxy(subscriptionURL string, proxyURL string) ([]models.ProxyRule, string, error) {
	if proxyURL != "" {
		p.log(fmt.Sprintf("[订阅] 正在通过代理 %s 获取订阅: %s", proxyURL, subscriptionURL))
	} else {
		p.log(fmt.Sprintf("[订阅] 正在获取订阅: %s", subscriptionURL))
	}

	// 下载订阅内容
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	if proxyURL != "" {
		parsedProxy, err := url.Parse(proxyURL)
		if err != nil {
			return nil, "", fmt.Errorf("代理地址无效: %v", err)
		}
		client.Transport = &http.Transport{
			Proxy: http.ProxyURL(parsedProxy),
		}
	}

	resp, err := client.Get(subscriptionURL)
	if err != nil {
		return nil, "", fmt.Errorf("下载订阅失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("HTTP 状态码: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("读取订阅内容失败: %v", err)
	}

	// 自动检测订阅类型
	subType := p.DetectType(content)
	p.log(fmt.Sprintf("[订阅] 检测到订阅类型: %s", subType))

	// 根据类型解析
	var rules []models.ProxyRule
	switch subType {
	case "clash":
		rules, err = p.ParseClash(content)
	case "v2ray":
		rules, err = p.ParseV2Ray(content)
	case "sip008":
		rules, err = p.ParseSIP008(content)
	case "base64":
		rules, err = p.ParseBase64(content)
	default:
		return nil, "", fmt.Errorf("未知的订阅类型")
	}

	if err != nil {
		return nil, "", err
	}

	p.log(fmt.Sprintf("[订阅] 解析成功，共 %d 个节点", len(rules)))
	return rules, subType, nil
}

// DetectType 自动检测订阅类型
func (p *Parser) DetectType(content []byte) string {
	contentStr := string(content)

	// 检测 YAML (Clash)
	if strings.Contains(contentStr, "proxies:") || strings.Contains(contentStr, "proxy-groups:") {
		return "clash"
	}

	// 检测 JSON (SIP008)
	if strings.HasPrefix(strings.TrimSpace(contentStr), "{") {
		var data map[string]interface{}
		if err := json.Unmarshal(content, &data); err == nil {
			if _, ok := data["servers"]; ok {
				return "sip008"
			}
			return "v2ray"
		}
	}

	// 检测 Base64
	if _, err := base64.StdEncoding.DecodeString(contentStr); err == nil {
		return "base64"
	}

	return "unknown"
}

// ParseClash 解析 Clash 订阅
func (p *Parser) ParseClash(content []byte) ([]models.ProxyRule, error) {
	var clashConfig struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}

	if err := yaml.Unmarshal(content, &clashConfig); err != nil {
		return nil, fmt.Errorf("解析 Clash YAML 失败: %v", err)
	}

	rules := make([]models.ProxyRule, 0, len(clashConfig.Proxies))
	failed := 0
	for i, proxy := range clashConfig.Proxies {
		rule, err := p.parseClashProxy(proxy, i)
		if err != nil {
			// 每条失败都打日志会向前端推同样多的事件，大订阅下足以拖垮界面，
			// 因此只打前若干条，其余汇总
			if failed < maxParseErrorLogs {
				p.log(fmt.Sprintf("[订阅] 解析节点 %d 失败: %v", i, err))
			}
			failed++
			continue
		}
		rules = append(rules, rule)
	}
	p.logSkipped(failed)

	return rules, nil
}

// parseClashProxy 解析单个 Clash 代理节点
func (p *Parser) parseClashProxy(proxy map[string]interface{}, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		LocalType: "socks",
		Source:    "subscription",
	}

	// 获取基本信息
	if name, ok := proxy["name"].(string); ok {
		rule.Alias = name
	} else {
		rule.Alias = fmt.Sprintf("Node_%d", index+1)
	}

	if server, ok := proxy["server"].(string); ok {
		rule.ServerAddr = server
	}

	if port, ok := proxy["port"].(int); ok {
		rule.ServerPort = port
	} else if portFloat, ok := proxy["port"].(float64); ok {
		rule.ServerPort = int(portFloat)
	}

	// 获取协议类型
	proxyType, _ := proxy["type"].(string)
	switch proxyType {
	case "ss":
		rule.Protocol = "shadowsocks"
		if cipher, ok := proxy["cipher"].(string); ok {
			rule.Settings.SSMethod = cipher
		}
		if password, ok := proxy["password"].(string); ok {
			rule.Settings.SSPassword = password
		}

	case "vmess":
		rule.Protocol = "vmess"
		if uuid, ok := proxy["uuid"].(string); ok {
			rule.Settings.VMessUserID = uuid
		}
		if alterId, ok := proxy["alterId"].(int); ok {
			rule.Settings.VMessAlterID = alterId
		} else if alterIdFloat, ok := proxy["alterId"].(float64); ok {
			rule.Settings.VMessAlterID = int(alterIdFloat)
		}
		if cipher, ok := proxy["cipher"].(string); ok {
			rule.Settings.VMessSecurity = cipher
		}

	case "vless":
		rule.Protocol = "vless"
		if uuid, ok := proxy["uuid"].(string); ok {
			rule.Settings.VLessUserID = uuid
		}
		if flow, ok := proxy["flow"].(string); ok {
			rule.Settings.VLessFlow = flow
		}

	case "trojan":
		rule.Protocol = "trojan"
		if password, ok := proxy["password"].(string); ok {
			rule.Settings.TrojanPassword = password
		}

	case "hysteria2":
		rule.Protocol = "hysteria2"
		if password, ok := proxy["password"].(string); ok {
			rule.Settings.Hy2Password = password
		}
		if obfs, ok := proxy["obfs"].(string); ok && obfs != "" && obfs != "none" {
			rule.Settings.Hy2Obfs = obfs
			if obfsPassword, ok := proxy["obfs-password"].(string); ok {
				rule.Settings.Hy2ObfsPassword = obfsPassword
			}
		}
		rule.Settings.Hy2UpMbps = parseClashBandwidth(proxy["up"])
		rule.Settings.Hy2DownMbps = parseClashBandwidth(proxy["down"])
		// Hysteria2 强制 TLS
		rule.Settings.Security = "tls"
		rule.Settings.TLS = &models.TLSSettings{}
		if sni, ok := proxy["sni"].(string); ok {
			rule.Settings.TLS.ServerName = sni
		}
		if alpn, ok := proxy["alpn"].([]interface{}); ok {
			for _, a := range alpn {
				if s, ok := a.(string); ok {
					rule.Settings.TLS.ALPN = append(rule.Settings.TLS.ALPN, s)
				}
			}
		}
		if skipCertVerify, ok := proxy["skip-cert-verify"].(bool); ok {
			rule.Settings.TLS.AllowInsecure = skipCertVerify
		}
		// 证书指纹固定（Clash Meta 的 fingerprint / sha256）：sing-box 无法用该指纹校验，启用 insecure
		if fp, ok := proxy["fingerprint"].(string); ok && fp != "" {
			rule.Settings.Hy2PinSHA256 = fp
			rule.Settings.TLS.AllowInsecure = true
		} else if fp, ok := proxy["sha256"].(string); ok && fp != "" {
			rule.Settings.Hy2PinSHA256 = fp
			rule.Settings.TLS.AllowInsecure = true
		}
		// 端口跳跃（Clash 的 ports 字段，如 "35000-39000"）
		if ports, ok := proxy["ports"].(string); ok && ports != "" {
			rule.Settings.Hy2Ports = ports
		}
		return rule, nil

	case "tuic":
		rule.Protocol = "tuic"
		if uuid, ok := proxy["uuid"].(string); ok {
			rule.Settings.TUICUserID = uuid
		}
		if password, ok := proxy["password"].(string); ok {
			rule.Settings.TUICPassword = password
		}
		if congestion, ok := proxy["congestion-controller"].(string); ok {
			rule.Settings.TUICCongestion = congestion
		}
		if udpMode, ok := proxy["udp-relay-mode"].(string); ok {
			rule.Settings.TUICUDPRelayMode = udpMode
		}
		// TUIC 强制 TLS
		rule.Settings.Security = "tls"
		rule.Settings.TLS = &models.TLSSettings{}
		if sni, ok := proxy["sni"].(string); ok {
			rule.Settings.TLS.ServerName = sni
		}
		if alpn, ok := proxy["alpn"].([]interface{}); ok {
			for _, a := range alpn {
				if s, ok := a.(string); ok {
					rule.Settings.TLS.ALPN = append(rule.Settings.TLS.ALPN, s)
				}
			}
		}
		if skipCertVerify, ok := proxy["skip-cert-verify"].(bool); ok {
			rule.Settings.TLS.AllowInsecure = skipCertVerify
		}
		return rule, nil

	default:
		return rule, fmt.Errorf("不支持的协议类型: %s", proxyType)
	}

	// 解析传输层配置
	if network, ok := proxy["network"].(string); ok {
		rule.Settings.Network = network
	}

	if tls, ok := proxy["tls"].(bool); ok && tls {
		rule.Settings.Security = "tls"
		if sni, ok := proxy["sni"].(string); ok {
			rule.Settings.TLS = &models.TLSSettings{
				ServerName: sni,
			}
		} else if serverName, ok := proxy["servername"].(string); ok {
			rule.Settings.TLS = &models.TLSSettings{
				ServerName: serverName,
			}
		}

		if skipCertVerify, ok := proxy["skip-cert-verify"].(bool); ok {
			if rule.Settings.TLS == nil {
				rule.Settings.TLS = &models.TLSSettings{}
			}
			rule.Settings.TLS.AllowInsecure = skipCertVerify
		}
	}

	// WebSocket 配置
	if rule.Settings.Network == "ws" {
		wsOpts, ok := proxy["ws-opts"].(map[string]interface{})
		if !ok {
			wsOpts, _ = proxy["ws-path"].(map[string]interface{})
		}
		if wsOpts != nil {
			rule.Settings.WS = &models.WSSettings{}
			if path, ok := wsOpts["path"].(string); ok {
				rule.Settings.WS.Path = path
			}
			if headers, ok := wsOpts["headers"].(map[string]interface{}); ok {
				rule.Settings.WS.Headers = make(map[string]string)
				for k, v := range headers {
					if vs, ok := v.(string); ok {
						rule.Settings.WS.Headers[k] = vs
					}
				}
			}
		} else if wsPath, ok := proxy["ws-path"].(string); ok {
			rule.Settings.WS = &models.WSSettings{Path: wsPath}
		}
	}

	// gRPC 配置
	if rule.Settings.Network == "grpc" {
		grpcOpts, ok := proxy["grpc-opts"].(map[string]interface{})
		if ok && grpcOpts != nil {
			rule.Settings.GRPC = &models.GRPCSettings{}
			if serviceName, ok := grpcOpts["grpc-service-name"].(string); ok {
				rule.Settings.GRPC.ServiceName = serviceName
			}
		}
	}

	return rule, nil
}

// ParseBase64 解析 Base64 编码的订阅
func (p *Parser) ParseBase64(content []byte) ([]models.ProxyRule, error) {
	// 解码 Base64
	decoded, err := base64.StdEncoding.DecodeString(string(content))
	if err != nil {
		// 尝试 URL 安全的 Base64
		decoded, err = base64.URLEncoding.DecodeString(string(content))
		if err != nil {
			return nil, fmt.Errorf("Base64 解码失败: %v", err)
		}
	}

	// 按行分割
	lines := strings.Split(string(decoded), "\n")
	rules := make([]models.ProxyRule, 0, len(lines))

	failed := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		rule, err := p.parseProxyURL(line, i)
		if err != nil {
			if failed < maxParseErrorLogs {
				p.log(fmt.Sprintf("[订阅] 解析链接失败: %v", err))
			}
			failed++
			continue
		}
		rules = append(rules, rule)
	}
	p.logSkipped(failed)

	return rules, nil
}

// parseProxyURL 解析代理 URL (vmess://, vless://, ss://, trojan://)
func (p *Parser) parseProxyURL(proxyURL string, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		LocalType: "socks",
		Source:    "subscription",
	}

	// 统一交给分享链接解析器。
	//
	// 这里原本为 vmess/vless/ss/trojan 各写了一份解析实现，与 internal/parser
	// 里的那套重复。两份代码不可避免地会分叉——例如 allowInsecure、REALITY 的
	// pbk/sid/fp、uTLS 指纹这些参数只在一边被支持，导致"同一个链接直接粘贴能用、
	// 从订阅导入却不能用"。现在只保留一份实现。
	rule, err := sharelink.NewShareLinkParser().ParseLink(proxyURL)
	if err != nil {
		return rule, err
	}
	rule.Source = "subscription"

	// 链接没带 #别名 时，用带序号的占位名，便于在列表里区分
	if rule.Alias == "" || isDefaultAlias(rule.Alias) {
		rule.Alias = fmt.Sprintf("%s_%d", defaultAliasPrefix(rule.Protocol), index+1)
	}
	return rule, nil
}

// defaultAliasPrefix 返回各协议的占位别名前缀
func defaultAliasPrefix(protocol string) string {
	switch protocol {
	case "vmess":
		return "VMess"
	case "vless":
		return "VLESS"
	case "shadowsocks":
		return "SS"
	case "trojan":
		return "Trojan"
	case "hysteria2":
		return "Hysteria2"
	case "tuic":
		return "TUIC"
	default:
		return "Node"
	}
}

// isDefaultAlias 判断别名是否是分享链接解析器给的兜底名称。
// 这类名称对订阅里的成百上千个节点毫无区分度，需要换成带序号的形式。
func isDefaultAlias(alias string) bool {
	switch alias {
	case "VMess节点", "VLESS节点", "SS节点", "Trojan节点", "Hysteria2节点", "TUIC节点":
		return true
	}
	return false
}

// ParseSIP008 解析 SIP008 Shadowsocks 订阅
func (p *Parser) ParseSIP008(content []byte) ([]models.ProxyRule, error) {
	var sip008 struct {
		Version int `json:"version"`
		Servers []struct {
			Server     string `json:"server"`
			ServerPort int    `json:"server_port"`
			Password   string `json:"password"`
			Method     string `json:"method"`
			Remarks    string `json:"remarks"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(content, &sip008); err != nil {
		return nil, fmt.Errorf("解析 SIP008 JSON 失败: %v", err)
	}

	var rules []models.ProxyRule
	for i, server := range sip008.Servers {
		rule := models.ProxyRule{
			Protocol:   "shadowsocks",
			LocalType:  "socks",
			Source:     "subscription",
			ServerAddr: server.Server,
			ServerPort: server.ServerPort,
			Alias:      server.Remarks,
			Settings: models.ProxySettings{
				SSMethod:   server.Method,
				SSPassword: server.Password,
			},
		}

		if rule.Alias == "" {
			rule.Alias = fmt.Sprintf("SS_%d", i+1)
		}

		rules = append(rules, rule)
	}

	return rules, nil
}

// ParseV2Ray 解析 V2Ray JSON 订阅
func (p *Parser) ParseV2Ray(content []byte) ([]models.ProxyRule, error) {
	// V2Ray JSON 格式通常是一个配置数组
	var configs []map[string]interface{}
	if err := json.Unmarshal(content, &configs); err != nil {
		return nil, fmt.Errorf("解析 V2Ray JSON 失败: %v", err)
	}

	var rules []models.ProxyRule
	for i, config := range configs {
		// 这里简化处理，实际情况可能更复杂
		rule := models.ProxyRule{
			LocalType: "socks",
			Source:    "subscription",
			Alias:     fmt.Sprintf("V2Ray_%d", i+1),
		}

		if ps, ok := config["ps"].(string); ok {
			rule.Alias = ps
		}

		// 根据实际的 V2Ray JSON 格式进行解析
		// 这里需要根据具体格式调整
		rules = append(rules, rule)
	}

	return rules, nil
}

// log 输出日志
func (p *Parser) log(message string) {
	if p.logFunc != nil {
		p.logFunc(message)
	}
}
