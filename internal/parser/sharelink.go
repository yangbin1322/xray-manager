package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"xray-manager/internal/models"
)

// ShareLinkParser 分享链接解析器
type ShareLinkParser struct{}

// NewShareLinkParser 创建分享链接解析器
func NewShareLinkParser() *ShareLinkParser {
	return &ShareLinkParser{}
}

// ParseMultipleLinks 批量解析分享链接
func (p *ShareLinkParser) ParseMultipleLinks(text string) ([]models.ProxyRule, []string) {
	lines := strings.Split(text, "\n")
	var rules []models.ProxyRule
	var errors []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		rule, err := p.ParseLink(line)
		if err != nil {
			errors = append(errors, fmt.Sprintf("解析失败 [%s]: %v", truncate(line, 40), err))
			continue
		}
		rules = append(rules, rule)
	}

	return rules, errors
}

// ParseLink 解析单个分享链接
func (p *ShareLinkParser) ParseLink(link string) (models.ProxyRule, error) {
	link = strings.TrimSpace(link)

	if strings.HasPrefix(link, "vmess://") {
		return p.ParseVMess(link)
	} else if strings.HasPrefix(link, "vless://") {
		return p.ParseVless(link)
	} else if strings.HasPrefix(link, "ss://") {
		return p.ParseSS(link)
	} else if strings.HasPrefix(link, "trojan://") {
		return p.ParseTrojan(link)
	} else if strings.HasPrefix(link, "hysteria2://") || strings.HasPrefix(link, "hy2://") {
		return p.ParseHysteria2(link)
	} else if strings.HasPrefix(link, "tuic://") {
		return p.ParseTUIC(link)
	}

	return models.ProxyRule{}, fmt.Errorf("不支持的链接格式")
}

// ParseVMess 解析 vmess:// 链接
func (p *ShareLinkParser) ParseVMess(vmessURL string) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "vmess",
		LocalType: "socks",
		Source:    "manual",
	}

	encoded := strings.TrimPrefix(vmessURL, "vmess://")
	encoded = strings.TrimSpace(encoded)

	// 尝试多种 base64 解码
	decoded, err := tryBase64Decode([]byte(encoded))
	if err != nil {
		return rule, fmt.Errorf("VMess Base64 解码失败: %v", err)
	}

	var vmessConfig map[string]interface{}
	if err := json.Unmarshal(decoded, &vmessConfig); err != nil {
		return rule, fmt.Errorf("VMess JSON 解析失败: %v", err)
	}

	// 提取别名
	if ps, ok := vmessConfig["ps"].(string); ok {
		rule.Alias = ps
	} else {
		rule.Alias = "VMess节点"
	}

	// 服务器地址
	if add, ok := vmessConfig["add"].(string); ok {
		rule.ServerAddr = add
	}

	// 端口
	rule.ServerPort = getIntFromInterface(vmessConfig["port"])

	// UUID
	if id, ok := vmessConfig["id"].(string); ok {
		rule.Settings.VMessUserID = id
	}

	// AlterID
	rule.Settings.VMessAlterID = getIntFromInterface(vmessConfig["aid"])

	// Security
	if scy, ok := vmessConfig["scy"].(string); ok && scy != "" {
		rule.Settings.VMessSecurity = scy
	} else {
		rule.Settings.VMessSecurity = "auto"
	}

	// 传输协议
	if net, ok := vmessConfig["net"].(string); ok {
		rule.Settings.Network = net
	}

	// TLS
	if tls, ok := vmessConfig["tls"].(string); ok && tls == "tls" {
		rule.Settings.Security = "tls"
		rule.Settings.TLS = &models.TLSSettings{}
		if sni, ok := vmessConfig["sni"].(string); ok && sni != "" {
			rule.Settings.TLS.ServerName = sni
		} else if host, ok := vmessConfig["host"].(string); ok && host != "" {
			rule.Settings.TLS.ServerName = host
		}
	}

	// WebSocket
	if rule.Settings.Network == "ws" {
		rule.Settings.WS = &models.WSSettings{}
		if path, ok := vmessConfig["path"].(string); ok {
			rule.Settings.WS.Path = path
		}
		if host, ok := vmessConfig["host"].(string); ok && host != "" {
			rule.Settings.WS.Headers = map[string]string{"Host": host}
		}
	}

	return rule, nil
}

// ParseVless 解析 vless:// 链接
func (p *ShareLinkParser) ParseVless(vlessURL string) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "vless",
		LocalType: "socks",
		Source:    "manual",
	}

	u, err := url.Parse(vlessURL)
	if err != nil {
		return rule, fmt.Errorf("解析 VLESS URL 失败: %v", err)
	}

	// UUID
	rule.Settings.VLessUserID = u.User.Username()

	// 服务器地址和端口
	rule.ServerAddr = u.Hostname()
	if port, err := strconv.Atoi(u.Port()); err == nil {
		rule.ServerPort = port
	}

	// 别名
	if u.Fragment != "" {
		decoded, err := url.QueryUnescape(u.Fragment)
		if err == nil {
			rule.Alias = decoded
		} else {
			rule.Alias = u.Fragment
		}
	} else {
		rule.Alias = "VLESS节点"
	}

	// 查询参数
	query := u.Query()
	if encryption := query.Get("encryption"); encryption != "" {
		rule.Settings.VLessEncryption = encryption
	} else {
		rule.Settings.VLessEncryption = "none"
	}

	if flow := query.Get("flow"); flow != "" {
		rule.Settings.VLessFlow = flow
	}

	if security := query.Get("security"); security != "" {
		rule.Settings.Security = security
		if security == "tls" || security == "reality" {
			rule.Settings.TLS = &models.TLSSettings{}
			if sni := query.Get("sni"); sni != "" {
				rule.Settings.TLS.ServerName = sni
			}
			if alpn := query.Get("alpn"); alpn != "" {
				rule.Settings.TLS.ALPN = strings.Split(alpn, ",")
			}
		}
	}

	if typeNet := query.Get("type"); typeNet != "" {
		rule.Settings.Network = typeNet
		if typeNet == "ws" {
			rule.Settings.WS = &models.WSSettings{}
			if path := query.Get("path"); path != "" {
				rule.Settings.WS.Path = path
			}
			if host := query.Get("host"); host != "" {
				rule.Settings.WS.Headers = map[string]string{"Host": host}
			}
		} else if typeNet == "grpc" {
			rule.Settings.GRPC = &models.GRPCSettings{}
			if serviceName := query.Get("serviceName"); serviceName != "" {
				rule.Settings.GRPC.ServiceName = serviceName
			}
		} else if typeNet == "h2" {
			rule.Settings.H2 = &models.H2Settings{}
			if path := query.Get("path"); path != "" {
				rule.Settings.H2.Path = path
			}
			if host := query.Get("host"); host != "" {
				rule.Settings.H2.Host = []string{host}
			}
		}
	}

	return rule, nil
}

// ParseSS 解析 ss:// 链接
func (p *ShareLinkParser) ParseSS(ssURL string) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "shadowsocks",
		LocalType: "socks",
		Source:    "manual",
	}

	raw := strings.TrimPrefix(ssURL, "ss://")

	// 分离备注
	parts := strings.SplitN(raw, "#", 2)
	if len(parts) == 2 {
		decoded, err := url.QueryUnescape(parts[1])
		if err == nil {
			rule.Alias = decoded
		} else {
			rule.Alias = parts[1]
		}
	} else {
		rule.Alias = "SS节点"
	}

	mainPart := parts[0]

	// SIP002 格式: base64(method:password)@server:port
	if strings.Contains(mainPart, "@") {
		atParts := strings.SplitN(mainPart, "@", 2)
		if len(atParts) != 2 {
			return rule, fmt.Errorf("无效的 SS URL 格式")
		}

		// 解码 method:password
		decoded, err := tryBase64Decode([]byte(atParts[0]))
		if err != nil {
			// 可能是明文
			decoded = []byte(atParts[0])
		}

		methodPassword := strings.SplitN(string(decoded), ":", 2)
		if len(methodPassword) == 2 {
			rule.Settings.SSMethod = methodPassword[0]
			rule.Settings.SSPassword = methodPassword[1]
		}

		// 解析 server:port (可能包含查询参数)
		serverPart := atParts[1]
		// 去除可能的查询参数
		if idx := strings.Index(serverPart, "?"); idx >= 0 {
			serverPart = serverPart[:idx]
		}
		// 去除可能的路径
		if idx := strings.Index(serverPart, "/"); idx >= 0 {
			serverPart = serverPart[:idx]
		}

		// 处理 IPv6
		if strings.HasPrefix(serverPart, "[") {
			// IPv6 格式 [addr]:port
			closeBracket := strings.Index(serverPart, "]")
			if closeBracket >= 0 {
				rule.ServerAddr = serverPart[1:closeBracket]
				if closeBracket+2 < len(serverPart) {
					if port, err := strconv.Atoi(serverPart[closeBracket+2:]); err == nil {
						rule.ServerPort = port
					}
				}
			}
		} else {
			// IPv4 格式 addr:port
			colonIdx := strings.LastIndex(serverPart, ":")
			if colonIdx >= 0 {
				rule.ServerAddr = serverPart[:colonIdx]
				if port, err := strconv.Atoi(serverPart[colonIdx+1:]); err == nil {
					rule.ServerPort = port
				}
			}
		}
	} else {
		// 旧格式: Base64(method:password@server:port)
		decoded, err := tryBase64Decode([]byte(mainPart))
		if err != nil {
			return rule, fmt.Errorf("SS Base64 解码失败: %v", err)
		}

		atParts := strings.SplitN(string(decoded), "@", 2)
		if len(atParts) != 2 {
			return rule, fmt.Errorf("无效的 SS URL 格式")
		}

		methodPassword := strings.SplitN(atParts[0], ":", 2)
		if len(methodPassword) == 2 {
			rule.Settings.SSMethod = methodPassword[0]
			rule.Settings.SSPassword = methodPassword[1]
		}

		colonIdx := strings.LastIndex(atParts[1], ":")
		if colonIdx >= 0 {
			rule.ServerAddr = atParts[1][:colonIdx]
			if port, err := strconv.Atoi(atParts[1][colonIdx+1:]); err == nil {
				rule.ServerPort = port
			}
		}
	}

	return rule, nil
}

// ParseTrojan 解析 trojan:// 链接
func (p *ShareLinkParser) ParseTrojan(trojanURL string) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "trojan",
		LocalType: "socks",
		Source:    "manual",
	}

	u, err := url.Parse(trojanURL)
	if err != nil {
		return rule, fmt.Errorf("解析 Trojan URL 失败: %v", err)
	}

	// 密码
	rule.Settings.TrojanPassword = u.User.Username()

	// 服务器地址和端口
	rule.ServerAddr = u.Hostname()
	if port, err := strconv.Atoi(u.Port()); err == nil {
		rule.ServerPort = port
	}

	// 别名
	if u.Fragment != "" {
		decoded, err := url.QueryUnescape(u.Fragment)
		if err == nil {
			rule.Alias = decoded
		} else {
			rule.Alias = u.Fragment
		}
	} else {
		rule.Alias = "Trojan节点"
	}

	// 查询参数
	query := u.Query()
	if security := query.Get("security"); security != "" {
		rule.Settings.Security = security
	} else {
		// Trojan 默认使用 TLS
		rule.Settings.Security = "tls"
	}

	if rule.Settings.Security == "tls" {
		rule.Settings.TLS = &models.TLSSettings{}
		if sni := query.Get("sni"); sni != "" {
			rule.Settings.TLS.ServerName = sni
		}
		if alpn := query.Get("alpn"); alpn != "" {
			rule.Settings.TLS.ALPN = strings.Split(alpn, ",")
		}
	}

	if typeNet := query.Get("type"); typeNet != "" {
		rule.Settings.Network = typeNet
		if typeNet == "ws" {
			rule.Settings.WS = &models.WSSettings{}
			if path := query.Get("path"); path != "" {
				rule.Settings.WS.Path = path
			}
			if host := query.Get("host"); host != "" {
				rule.Settings.WS.Headers = map[string]string{"Host": host}
			}
		} else if typeNet == "grpc" {
			rule.Settings.GRPC = &models.GRPCSettings{}
			if serviceName := query.Get("serviceName"); serviceName != "" {
				rule.Settings.GRPC.ServiceName = serviceName
			}
		}
	}

	return rule, nil
}

// ParseHysteria2 解析 hysteria2:// 或 hy2:// 链接
// 格式: hysteria2://password@host:port/?insecure=1&obfs=salamander&obfs-password=xxx&sni=example.com&alpn=h3#别名
func (p *ShareLinkParser) ParseHysteria2(hy2URL string) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "hysteria2",
		LocalType: "mixed",
		Source:    "manual",
	}

	// 统一前缀，方便 url.Parse
	normalized := strings.Replace(hy2URL, "hy2://", "hysteria2://", 1)

	u, err := url.Parse(normalized)
	if err != nil {
		return rule, fmt.Errorf("解析 Hysteria2 URL 失败: %v", err)
	}

	// 认证密码（可能是 user:pass 形式，整体作为密码）
	password := u.User.Username()
	if pass, hasPass := u.User.Password(); hasPass {
		password = password + ":" + pass
	}
	rule.Settings.Hy2Password = password

	// 服务器地址和端口
	rule.ServerAddr = u.Hostname()
	if port, err := strconv.Atoi(u.Port()); err == nil {
		rule.ServerPort = port
	} else {
		rule.ServerPort = 443 // Hysteria2 默认端口
	}

	// 别名
	if u.Fragment != "" {
		decoded, err := url.QueryUnescape(u.Fragment)
		if err == nil {
			rule.Alias = decoded
		} else {
			rule.Alias = u.Fragment
		}
	} else {
		rule.Alias = "Hysteria2节点"
	}

	// 查询参数
	query := u.Query()

	// Hysteria2 强制 TLS
	rule.Settings.Security = "tls"
	rule.Settings.TLS = &models.TLSSettings{}
	if sni := query.Get("sni"); sni != "" {
		rule.Settings.TLS.ServerName = sni
	}
	if alpn := query.Get("alpn"); alpn != "" {
		rule.Settings.TLS.ALPN = strings.Split(alpn, ",")
	}
	if insecure := query.Get("insecure"); insecure == "1" || insecure == "true" {
		rule.Settings.TLS.AllowInsecure = true
	}

	// 混淆
	if obfs := query.Get("obfs"); obfs != "" && obfs != "none" {
		rule.Settings.Hy2Obfs = obfs
		rule.Settings.Hy2ObfsPassword = query.Get("obfs-password")
	}

	// 带宽限制（非标准参数，部分客户端使用）
	if up := query.Get("upmbps"); up != "" {
		rule.Settings.Hy2UpMbps, _ = strconv.Atoi(up)
	}
	if down := query.Get("downmbps"); down != "" {
		rule.Settings.Hy2DownMbps, _ = strconv.Atoi(down)
	}

	return rule, nil
}

// ParseTUIC 解析 tuic:// 链接
// 格式: tuic://uuid:password@host:port?congestion_control=bbr&udp_relay_mode=native&alpn=h3&sni=xxx&allow_insecure=1#别名
func (p *ShareLinkParser) ParseTUIC(tuicURL string) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "tuic",
		LocalType: "mixed",
		Source:    "manual",
	}

	u, err := url.Parse(tuicURL)
	if err != nil {
		return rule, fmt.Errorf("解析 TUIC URL 失败: %v", err)
	}

	// UUID 和密码
	rule.Settings.TUICUserID = u.User.Username()
	if pass, hasPass := u.User.Password(); hasPass {
		rule.Settings.TUICPassword = pass
	}

	// 服务器地址和端口
	rule.ServerAddr = u.Hostname()
	if port, err := strconv.Atoi(u.Port()); err == nil {
		rule.ServerPort = port
	}

	// 别名
	if u.Fragment != "" {
		decoded, err := url.QueryUnescape(u.Fragment)
		if err == nil {
			rule.Alias = decoded
		} else {
			rule.Alias = u.Fragment
		}
	} else {
		rule.Alias = "TUIC节点"
	}

	// 查询参数
	query := u.Query()
	if congestion := query.Get("congestion_control"); congestion != "" {
		rule.Settings.TUICCongestion = congestion
	}
	if udpMode := query.Get("udp_relay_mode"); udpMode != "" {
		rule.Settings.TUICUDPRelayMode = udpMode
	}

	// TUIC 强制 TLS
	rule.Settings.Security = "tls"
	rule.Settings.TLS = &models.TLSSettings{}
	if sni := query.Get("sni"); sni != "" {
		rule.Settings.TLS.ServerName = sni
	}
	if alpn := query.Get("alpn"); alpn != "" {
		rule.Settings.TLS.ALPN = strings.Split(alpn, ",")
	}
	if insecure := query.Get("allow_insecure"); insecure == "1" || insecure == "true" {
		rule.Settings.TLS.AllowInsecure = true
	}

	return rule, nil
}

// === 辅助函数 ===

func tryBase64Decode(data []byte) ([]byte, error) {
	s := strings.TrimSpace(string(data))
	// 补全 padding
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}

	decoded, err := base64.StdEncoding.DecodeString(s)
	if err == nil {
		return decoded, nil
	}

	decoded, err = base64.URLEncoding.DecodeString(s)
	if err == nil {
		return decoded, nil
	}

	decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	if err == nil {
		return decoded, nil
	}

	decoded, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err == nil {
		return decoded, nil
	}

	return nil, fmt.Errorf("所有 Base64 解码方式均失败")
}

func getIntFromInterface(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		n, _ := strconv.Atoi(val)
		return n
	default:
		return 0
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
