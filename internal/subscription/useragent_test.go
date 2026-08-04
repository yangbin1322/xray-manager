package subscription

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 机场按 User-Agent 返回不同内容：未知 UA（如 Go 默认的 Go-http-client）
// 常被返回网页或精简列表。默认必须伪装成 Shadowrocket。
func TestFetchSendsDefaultUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(
			"trojan://pw@example.com:443?security=tls#节点"))))
	}))
	defer srv.Close()

	if _, _, err := NewParser(nil).FetchAndParse(srv.URL); err != nil {
		t.Fatalf("拉取订阅失败: %v", err)
	}

	if got != DefaultUserAgent {
		t.Fatalf("应发送默认 UA %q，实际 %q", DefaultUserAgent, got)
	}
	// 明确断言不再是 Go 的默认 UA
	if got == "" || got == "Go-http-client/1.1" {
		t.Fatalf("不应使用 Go 默认 User-Agent: %q", got)
	}
}

// 自定义 UA 应原样发送
func TestFetchSendsCustomUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(
			"trojan://pw@example.com:443?security=tls#节点"))))
	}))
	defer srv.Close()

	const custom = "clash-verge/v1.5.11"
	p := NewParser(nil)
	p.SetUserAgent(custom)
	if _, _, err := p.FetchAndParse(srv.URL); err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Fatalf("应发送自定义 UA %q，实际 %q", custom, got)
	}
}

// 传空（含纯空白）应回退到默认，而不是发送空 UA
func TestEmptyUserAgentFallsBackToDefault(t *testing.T) {
	for _, ua := range []string{"", "   ", "\t"} {
		p := NewParser(nil)
		p.SetUserAgent(ua)
		if p.userAgent() != DefaultUserAgent {
			t.Fatalf("UA=%q 时应回退默认，实际 %q", ua, p.userAgent())
		}
	}
}

// 设过自定义 UA 后再清空，应能回到默认
func TestUserAgentCanBeReset(t *testing.T) {
	p := NewParser(nil)
	p.SetUserAgent("custom/1.0")
	if p.userAgent() != "custom/1.0" {
		t.Fatal("自定义 UA 未生效")
	}
	p.SetUserAgent("")
	if p.userAgent() != DefaultUserAgent {
		t.Fatalf("清空后应回到默认，实际 %q", p.userAgent())
	}
}

// 默认 UA 必须是用户指定的 Shadowrocket 标识
func TestDefaultUserAgentValue(t *testing.T) {
	const want = "Shadowrocket/2850 CFNetwork/1490.0.4 Darwin/23.2.0 iPhone15,4"
	if DefaultUserAgent != want {
		t.Fatalf("默认 UA 应为 %q，实际 %q", want, DefaultUserAgent)
	}
}
