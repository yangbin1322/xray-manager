package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"xray-manager/internal/models"
)

// EncodeLink 将规则编码为主流客户端可识别的分享链接。
func (p *ShareLinkParser) EncodeLink(rule models.ProxyRule) (string, error) {
	if err := rule.Validate(); err != nil {
		return "", err
	}
	switch rule.Protocol {
	case "vmess":
		return encodeVMess(rule)
	case "vless":
		return encodeVLESS(rule), nil
	case "shadowsocks":
		return encodeShadowsocks(rule), nil
	case "trojan":
		return encodeTrojan(rule), nil
	case "http":
		return encodeURLProxy(rule, "http", rule.Settings.HTTPUsername, rule.Settings.HTTPPassword), nil
	case "socks":
		return encodeURLProxy(rule, "socks5", rule.Settings.SOCKSUsername, rule.Settings.SOCKSPassword), nil
	case "hysteria2":
		return encodeHysteria2(rule), nil
	case "tuic":
		return encodeTUIC(rule), nil
	default:
		return "", fmt.Errorf("不支持导出的协议: %s", rule.Protocol)
	}
}

func encodeVMess(rule models.ProxyRule) (string, error) {
	config := map[string]any{
		"v": "2", "ps": rule.Alias, "add": rule.ServerAddr,
		"port": strconv.Itoa(rule.ServerPort), "id": rule.Settings.VMessUserID,
		"aid": rule.Settings.VMessAlterID, "scy": valueOr(rule.Settings.VMessSecurity, "auto"),
		"net": valueOr(rule.Settings.Network, "tcp"), "type": "none",
	}
	applyTransport(config, rule.Settings)
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(data), nil
}

func encodeVLESS(rule models.ProxyRule) string {
	query := transportQuery(rule.Settings)
	query.Set("encryption", valueOr(rule.Settings.VLessEncryption, "none"))
	if rule.Settings.VLessFlow != "" {
		query.Set("flow", rule.Settings.VLessFlow)
	}
	return buildURL("vless", rule.Settings.VLessUserID, "", rule, query)
}

func encodeShadowsocks(rule models.ProxyRule) string {
	credentials := rule.Settings.SSMethod + ":" + rule.Settings.SSPassword
	encoded := base64.RawURLEncoding.EncodeToString([]byte(credentials))
	host := net.JoinHostPort(rule.ServerAddr, strconv.Itoa(rule.ServerPort))
	return "ss://" + encoded + "@" + host + "#" + url.QueryEscape(rule.Alias)
}

func encodeTrojan(rule models.ProxyRule) string {
	return buildURL("trojan", rule.Settings.TrojanPassword, "", rule, transportQuery(rule.Settings))
}

func encodeHysteria2(rule models.ProxyRule) string {
	query := url.Values{}
	applyTLSQuery(query, rule.Settings)
	if rule.Settings.Hy2Obfs != "" {
		query.Set("obfs", rule.Settings.Hy2Obfs)
		query.Set("obfs-password", rule.Settings.Hy2ObfsPassword)
	}
	if rule.Settings.Hy2PinSHA256 != "" {
		query.Set("pinSHA256", rule.Settings.Hy2PinSHA256)
	}
	if rule.Settings.Hy2Ports != "" {
		query.Set("mport", rule.Settings.Hy2Ports)
	}
	if rule.Settings.Hy2UpMbps > 0 {
		query.Set("upmbps", strconv.Itoa(rule.Settings.Hy2UpMbps))
	}
	if rule.Settings.Hy2DownMbps > 0 {
		query.Set("downmbps", strconv.Itoa(rule.Settings.Hy2DownMbps))
	}
	return buildURL("hysteria2", rule.Settings.Hy2Password, "", rule, query)
}

func encodeTUIC(rule models.ProxyRule) string {
	query := url.Values{}
	applyTLSQuery(query, rule.Settings)
	if rule.Settings.TUICCongestion != "" {
		query.Set("congestion_control", rule.Settings.TUICCongestion)
	}
	if rule.Settings.TUICUDPRelayMode != "" {
		query.Set("udp_relay_mode", rule.Settings.TUICUDPRelayMode)
	}
	return buildURL("tuic", rule.Settings.TUICUserID, rule.Settings.TUICPassword, rule, query)
}

func encodeURLProxy(rule models.ProxyRule, scheme, username, password string) string {
	return buildURL(scheme, username, password, rule, nil)
}

func buildURL(scheme, username, password string, rule models.ProxyRule, query url.Values) string {
	u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(rule.ServerAddr, strconv.Itoa(rule.ServerPort)), Fragment: rule.Alias}
	if username != "" || password != "" {
		if password == "" {
			u.User = url.User(username)
		} else {
			u.User = url.UserPassword(username, password)
		}
	}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func transportQuery(settings models.ProxySettings) url.Values {
	query := url.Values{}
	if settings.Network != "" {
		query.Set("type", settings.Network)
	}
	applyTLSQuery(query, settings)
	if settings.WS != nil {
		query.Set("path", settings.WS.Path)
		if host := settings.WS.Headers["Host"]; host != "" {
			query.Set("host", host)
		}
	}
	if settings.GRPC != nil {
		query.Set("serviceName", settings.GRPC.ServiceName)
	}
	if settings.H2 != nil {
		query.Set("path", settings.H2.Path)
		if len(settings.H2.Host) > 0 {
			query.Set("host", settings.H2.Host[0])
		}
	}
	return query
}

func applyTLSQuery(query url.Values, settings models.ProxySettings) {
	if settings.Security != "" {
		query.Set("security", settings.Security)
	}
	// REALITY 参数要原样导出，否则导出的链接再导入回来就用不了
	if r := settings.Reality; r != nil {
		if r.PublicKey != "" {
			query.Set("pbk", r.PublicKey)
		}
		if r.ShortID != "" {
			query.Set("sid", r.ShortID)
		}
		if r.SpiderX != "" {
			query.Set("spx", r.SpiderX)
		}
		if r.Fingerprint != "" {
			query.Set("fp", r.Fingerprint)
		}
		if r.ServerName != "" {
			query.Set("sni", r.ServerName)
		}
	}
	if settings.TLS == nil {
		return
	}
	if settings.TLS.ServerName != "" {
		query.Set("sni", settings.TLS.ServerName)
	}
	if len(settings.TLS.ALPN) > 0 {
		query.Set("alpn", strings.Join(settings.TLS.ALPN, ","))
	}
	if settings.TLS.Fingerprint != "" {
		query.Set("fp", settings.TLS.Fingerprint)
	}
	if settings.TLS.AllowInsecure {
		query.Set("insecure", "1")
		query.Set("allow_insecure", "1")
	}
}

func applyTransport(config map[string]any, settings models.ProxySettings) {
	if settings.Security != "" && settings.Security != "none" {
		config["tls"] = settings.Security
	}
	if settings.TLS != nil {
		config["sni"] = settings.TLS.ServerName
		config["alpn"] = strings.Join(settings.TLS.ALPN, ",")
	}
	if settings.WS != nil {
		config["path"] = settings.WS.Path
		config["host"] = settings.WS.Headers["Host"]
	}
	if settings.GRPC != nil {
		config["path"] = settings.GRPC.ServiceName
	}
	if settings.H2 != nil {
		config["path"] = settings.H2.Path
		config["host"] = strings.Join(settings.H2.Host, ",")
	}
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
