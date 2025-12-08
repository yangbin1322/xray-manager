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

	"gopkg.in/yaml.v3"
)

// Parser 订阅解析器
type Parser struct {
	logFunc func(string)
}

// NewParser 创建解析器
func NewParser(logFunc func(string)) *Parser {
	return &Parser{
		logFunc: logFunc,
	}
}

// FetchAndParse 获取并解析订阅
func (p *Parser) FetchAndParse(subscriptionURL string) ([]models.ProxyRule, string, error) {
	p.log(fmt.Sprintf("[订阅] 正在获取订阅: %s", subscriptionURL))

	// 下载订阅内容
	client := &http.Client{
		Timeout: 30 * time.Second,
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

	var rules []models.ProxyRule
	for i, proxy := range clashConfig.Proxies {
		rule, err := p.parseClashProxy(proxy, i)
		if err != nil {
			p.log(fmt.Sprintf("[订阅] 解析节点 %d 失败: %v", i, err))
			continue
		}
		rules = append(rules, rule)
	}

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
	var rules []models.ProxyRule

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		rule, err := p.parseProxyURL(line, i)
		if err != nil {
			p.log(fmt.Sprintf("[订阅] 解析链接失败: %v", err))
			continue
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

// parseProxyURL 解析代理 URL (vmess://, vless://, ss://, trojan://)
func (p *Parser) parseProxyURL(proxyURL string, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		LocalType: "socks",
		Source:    "subscription",
	}

	if strings.HasPrefix(proxyURL, "vmess://") {
		return p.parseVMessURL(proxyURL, index)
	} else if strings.HasPrefix(proxyURL, "vless://") {
		return p.parseVLessURL(proxyURL, index)
	} else if strings.HasPrefix(proxyURL, "ss://") {
		return p.parseSSURL(proxyURL, index)
	} else if strings.HasPrefix(proxyURL, "trojan://") {
		return p.parseTrojanURL(proxyURL, index)
	}

	return rule, fmt.Errorf("不支持的协议: %s", proxyURL[:10])
}

// parseVMessURL 解析 VMess URL
func (p *Parser) parseVMessURL(vmessURL string, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "vmess",
		LocalType: "socks",
		Source:    "subscription",
	}

	// 移除 vmess:// 前缀
	encoded := strings.TrimPrefix(vmessURL, "vmess://")

	// Base64 解码
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			return rule, fmt.Errorf("VMess Base64 解码失败: %v", err)
		}
	}

	// 解析 JSON
	var vmessConfig map[string]interface{}
	if err := json.Unmarshal(decoded, &vmessConfig); err != nil {
		return rule, fmt.Errorf("VMess JSON 解析失败: %v", err)
	}

	// 提取配置
	if ps, ok := vmessConfig["ps"].(string); ok {
		rule.Alias = ps
	} else {
		rule.Alias = fmt.Sprintf("VMess_%d", index+1)
	}

	if add, ok := vmessConfig["add"].(string); ok {
		rule.ServerAddr = add
	}

	if port, ok := vmessConfig["port"].(string); ok {
		if portInt, err := strconv.Atoi(port); err == nil {
			rule.ServerPort = portInt
		}
	} else if portFloat, ok := vmessConfig["port"].(float64); ok {
		rule.ServerPort = int(portFloat)
	}

	if id, ok := vmessConfig["id"].(string); ok {
		rule.Settings.VMessUserID = id
	}

	if aid, ok := vmessConfig["aid"].(string); ok {
		if aidInt, err := strconv.Atoi(aid); err == nil {
			rule.Settings.VMessAlterID = aidInt
		}
	} else if aidFloat, ok := vmessConfig["aid"].(float64); ok {
		rule.Settings.VMessAlterID = int(aidFloat)
	}

	if net, ok := vmessConfig["net"].(string); ok {
		rule.Settings.Network = net
	}

	if tls, ok := vmessConfig["tls"].(string); ok && tls == "tls" {
		rule.Settings.Security = "tls"
		if sni, ok := vmessConfig["sni"].(string); ok {
			rule.Settings.TLS = &models.TLSSettings{
				ServerName: sni,
			}
		} else if host, ok := vmessConfig["host"].(string); ok {
			rule.Settings.TLS = &models.TLSSettings{
				ServerName: host,
			}
		}
	}

	// WebSocket 配置
	if rule.Settings.Network == "ws" {
		rule.Settings.WS = &models.WSSettings{}
		if path, ok := vmessConfig["path"].(string); ok {
			rule.Settings.WS.Path = path
		}
		if host, ok := vmessConfig["host"].(string); ok {
			rule.Settings.WS.Headers = map[string]string{"Host": host}
		}
	}

	return rule, nil
}

// parseVLessURL 解析 VLESS URL
func (p *Parser) parseVLessURL(vlessURL string, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "vless",
		LocalType: "socks",
		Source:    "subscription",
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
		rule.Alias = u.Fragment
	} else {
		rule.Alias = fmt.Sprintf("VLESS_%d", index+1)
	}

	// 查询参数
	query := u.Query()
	if encryption := query.Get("encryption"); encryption != "" {
		rule.Settings.VLessEncryption = encryption
	}
	if flow := query.Get("flow"); flow != "" {
		rule.Settings.VLessFlow = flow
	}
	if security := query.Get("security"); security != "" {
		rule.Settings.Security = security
		if security == "tls" {
			rule.Settings.TLS = &models.TLSSettings{}
			if sni := query.Get("sni"); sni != "" {
				rule.Settings.TLS.ServerName = sni
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
		}
	}

	return rule, nil
}

// parseSSURL 解析 Shadowsocks URL
func (p *Parser) parseSSURL(ssURL string, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "shadowsocks",
		LocalType: "socks",
		Source:    "subscription",
	}

	// 移除 ss:// 前缀
	ssURL = strings.TrimPrefix(ssURL, "ss://")

	// 分离备注
	parts := strings.SplitN(ssURL, "#", 2)
	if len(parts) == 2 {
		rule.Alias, _ = url.QueryUnescape(parts[1])
	} else {
		rule.Alias = fmt.Sprintf("SS_%d", index+1)
	}

	// 解析主体部分
	mainPart := parts[0]

	// SIP002 格式: method:password@server:port
	if strings.Contains(mainPart, "@") {
		parts := strings.SplitN(mainPart, "@", 2)
		if len(parts) != 2 {
			return rule, fmt.Errorf("无效的 SS URL 格式")
		}

		// 解码 method:password
		decoded, err := base64.URLEncoding.DecodeString(parts[0])
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(parts[0])
			if err != nil {
				// 如果解码失败，可能是明文
				decoded = []byte(parts[0])
			}
		}

		methodPassword := strings.SplitN(string(decoded), ":", 2)
		if len(methodPassword) == 2 {
			rule.Settings.SSMethod = methodPassword[0]
			rule.Settings.SSPassword = methodPassword[1]
		}

		// 解析 server:port
		serverPort := strings.SplitN(parts[1], ":", 2)
		if len(serverPort) == 2 {
			rule.ServerAddr = serverPort[0]
			if port, err := strconv.Atoi(serverPort[1]); err == nil {
				rule.ServerPort = port
			}
		}
	} else {
		// 旧格式: Base64(method:password@server:port)
		decoded, err := base64.URLEncoding.DecodeString(mainPart)
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(mainPart)
			if err != nil {
				return rule, fmt.Errorf("Base64 解码失败: %v", err)
			}
		}

		parts := strings.SplitN(string(decoded), "@", 2)
		if len(parts) != 2 {
			return rule, fmt.Errorf("无效的 SS URL 格式")
		}

		methodPassword := strings.SplitN(parts[0], ":", 2)
		if len(methodPassword) == 2 {
			rule.Settings.SSMethod = methodPassword[0]
			rule.Settings.SSPassword = methodPassword[1]
		}

		serverPort := strings.SplitN(parts[1], ":", 2)
		if len(serverPort) == 2 {
			rule.ServerAddr = serverPort[0]
			if port, err := strconv.Atoi(serverPort[1]); err == nil {
				rule.ServerPort = port
			}
		}
	}

	return rule, nil
}

// parseTrojanURL 解析 Trojan URL
func (p *Parser) parseTrojanURL(trojanURL string, index int) (models.ProxyRule, error) {
	rule := models.ProxyRule{
		Protocol:  "trojan",
		LocalType: "socks",
		Source:    "subscription",
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
		rule.Alias = u.Fragment
	} else {
		rule.Alias = fmt.Sprintf("Trojan_%d", index+1)
	}

	// 查询参数
	query := u.Query()
	if security := query.Get("security"); security != "" {
		rule.Settings.Security = security
		if security == "tls" {
			rule.Settings.TLS = &models.TLSSettings{}
			if sni := query.Get("sni"); sni != "" {
				rule.Settings.TLS.ServerName = sni
			}
		}
	} else {
		// Trojan 默认使用 TLS
		rule.Settings.Security = "tls"
		rule.Settings.TLS = &models.TLSSettings{}
		if sni := query.Get("sni"); sni != "" {
			rule.Settings.TLS.ServerName = sni
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
