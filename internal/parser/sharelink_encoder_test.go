package parser

import (
	"testing"

	"xray-manager/internal/models"
)

func TestShareLinkRoundTrip(t *testing.T) {
	parser := NewShareLinkParser()
	tests := []models.ProxyRule{
		{Alias: "VMess WS", Protocol: "vmess", ServerAddr: "vmess.example.com", ServerPort: 443, Settings: models.ProxySettings{VMessUserID: "11111111-1111-1111-1111-111111111111", VMessSecurity: "auto", Network: "ws", Security: "tls", TLS: &models.TLSSettings{ServerName: "cdn.example.com"}, WS: &models.WSSettings{Path: "/ws", Headers: map[string]string{"Host": "cdn.example.com"}}}},
		{Alias: "VLESS gRPC", Protocol: "vless", ServerAddr: "vless.example.com", ServerPort: 443, Settings: models.ProxySettings{VLessUserID: "22222222-2222-2222-2222-222222222222", VLessEncryption: "none", Network: "grpc", Security: "tls", TLS: &models.TLSSettings{ServerName: "vless.example.com", ALPN: []string{"h2"}}, GRPC: &models.GRPCSettings{ServiceName: "grpc-service"}}},
		{Alias: "SS", Protocol: "shadowsocks", ServerAddr: "ss.example.com", ServerPort: 8388, Settings: models.ProxySettings{SSMethod: "aes-256-gcm", SSPassword: "p@ss:word"}},
		{Alias: "Trojan", Protocol: "trojan", ServerAddr: "trojan.example.com", ServerPort: 443, Settings: models.ProxySettings{TrojanPassword: "secret", Network: "tcp", Security: "tls", TLS: &models.TLSSettings{ServerName: "trojan.example.com"}}},
		{Alias: "HTTP", Protocol: "http", ServerAddr: "http.example.com", ServerPort: 8080, Settings: models.ProxySettings{HTTPUsername: "user", HTTPPassword: "pass"}},
		{Alias: "SOCKS", Protocol: "socks", ServerAddr: "socks.example.com", ServerPort: 1080, Settings: models.ProxySettings{SOCKSUsername: "user", SOCKSPassword: "pass", SOCKSVersion: "socks5"}},
		{Alias: "Hy2", Protocol: "hysteria2", ServerAddr: "hy2.example.com", ServerPort: 443, Settings: models.ProxySettings{Hy2Password: "hy2-secret", Hy2Obfs: "salamander", Hy2ObfsPassword: "obfs-secret", Hy2Ports: "35000-39000", Security: "tls", TLS: &models.TLSSettings{ServerName: "hy2.example.com", AllowInsecure: true}}},
		{Alias: "TUIC", Protocol: "tuic", ServerAddr: "tuic.example.com", ServerPort: 443, Settings: models.ProxySettings{TUICUserID: "33333333-3333-3333-3333-333333333333", TUICPassword: "tuic-secret", TUICCongestion: "bbr", TUICUDPRelayMode: "native", Security: "tls", TLS: &models.TLSSettings{ServerName: "tuic.example.com", ALPN: []string{"h3"}}}},
	}

	for _, original := range tests {
		t.Run(original.Protocol, func(t *testing.T) {
			link, err := parser.EncodeLink(original)
			if err != nil {
				t.Fatalf("EncodeLink() error = %v", err)
			}
			decoded, err := parser.ParseLink(link)
			if err != nil {
				t.Fatalf("ParseLink(%q) error = %v", link, err)
			}
			if decoded.Protocol != original.Protocol || decoded.ServerAddr != original.ServerAddr || decoded.ServerPort != original.ServerPort || decoded.Alias != original.Alias {
				t.Fatalf("round trip mismatch: original=%#v decoded=%#v", original, decoded)
			}
			assertCredentials(t, original, decoded)
		})
	}
}

func assertCredentials(t *testing.T, original, decoded models.ProxyRule) {
	t.Helper()
	switch original.Protocol {
	case "vmess":
		if decoded.Settings.VMessUserID != original.Settings.VMessUserID {
			t.Fatal("VMess UUID mismatch")
		}
	case "vless":
		if decoded.Settings.VLessUserID != original.Settings.VLessUserID || decoded.Settings.GRPC == nil || decoded.Settings.GRPC.ServiceName != original.Settings.GRPC.ServiceName {
			t.Fatal("VLESS credentials or gRPC mismatch")
		}
	case "shadowsocks":
		if decoded.Settings.SSMethod != original.Settings.SSMethod || decoded.Settings.SSPassword != original.Settings.SSPassword {
			t.Fatal("Shadowsocks credentials mismatch")
		}
	case "trojan":
		if decoded.Settings.TrojanPassword != original.Settings.TrojanPassword {
			t.Fatal("Trojan password mismatch")
		}
	case "http":
		if decoded.Settings.HTTPUsername != original.Settings.HTTPUsername || decoded.Settings.HTTPPassword != original.Settings.HTTPPassword {
			t.Fatal("HTTP credentials mismatch")
		}
	case "socks":
		if decoded.Settings.SOCKSUsername != original.Settings.SOCKSUsername || decoded.Settings.SOCKSPassword != original.Settings.SOCKSPassword {
			t.Fatal("SOCKS credentials mismatch")
		}
	case "hysteria2":
		if decoded.Settings.Hy2Password != original.Settings.Hy2Password || decoded.Settings.Hy2ObfsPassword != original.Settings.Hy2ObfsPassword {
			t.Fatal("Hysteria2 credentials mismatch")
		}
	case "tuic":
		if decoded.Settings.TUICUserID != original.Settings.TUICUserID || decoded.Settings.TUICPassword != original.Settings.TUICPassword {
			t.Fatal("TUIC credentials mismatch")
		}
	}
}
