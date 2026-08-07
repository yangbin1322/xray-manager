package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"xray-manager/internal/models"
)

//go:embed openapi.yaml
var openAPISpec []byte

const swaggerUIHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Xray Manager HTTP API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: '/api/openapi.yaml',
      dom_id: '#swagger-ui',
      deepLinking: true,
      persistAuthorization: true,
      displayRequestDuration: true,
      filter: true
    })
  </script>
</body>
</html>`

type httpAPIService interface {
	GetRules() []models.ProxyRule
	AddRule(models.ProxyRule) error
	UpdateRule(string, models.ProxyRule) error
	DeleteRule(string) error
	StartRule(string) error
	StopRule(string) error
	StartNodes([]string) error
	StopNodes([]string) error
	DeleteNodes([]string) error
	GetGroups() []models.Group
	CreateGroup(string, string) error
	UpdateGroup(string, string, string) error
	DeleteGroup(string) error
	StartAllRulesInGroup(string) error
	StopAllRulesInGroup(string) error
	GetSubscriptions() []models.Subscription
	AddSubscription(string, string, bool, int, string, string, string) error
	EditSubscription(string, string, string, bool, int, string, string, string) error
	UpdateSubscriptionByID(string) error
	DeleteSubscription(string) error
	GetLoadBalancers() []models.LoadBalanceNode
	AddLoadBalancer(models.LoadBalanceNode) error
	UpdateLoadBalancer(models.LoadBalanceNode) error
	DeleteLoadBalancer(string) error
	StartLoadBalancer(string) error
	StopLoadBalancer(string) error
	GetChainProxies() []models.ChainProxy
	AddChainProxy(models.ChainProxy) error
	UpdateChainProxy(models.ChainProxy) error
	DeleteChainProxy(string) error
	StartChainProxy(string) error
	StopChainProxy(string) error
	GetSessionRelays() []models.SessionRelay
	AddSessionRelay(models.SessionRelay) error
	UpdateSessionRelay(models.SessionRelay) error
	DeleteSessionRelay(string) error
	StartSessionRelay(string) error
	StopSessionRelay(string) error
	GetPreProxy() models.PreProxyConfig
	SetPreProxy(string) error
	SetPreProxyConfig(models.PreProxyConfig) error
}

type httpAPI struct {
	service httpAPIService
	token   string
}

type apiResponse struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type subscriptionRequest struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	AutoUpdate     bool   `json:"autoUpdate"`
	UpdateInterval int    `json:"updateInterval"`
	UpdateMode     string `json:"updateMode"`
	UpdateProxyID  string `json:"updateProxyId"`
	// GroupID 目标分组。新增时为空表示按订阅名新建分组；
	// 编辑时为空表示保持当前分组。多个订阅可指定同一分组，把节点汇入同一处管理。
	GroupID string `json:"groupId"`
}

type localProxy struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Alias     string `json:"alias"`
	LocalPort int    `json:"localPort"`
	HTTPURL   string `json:"httpUrl"`
	SOCKS5URL string `json:"socks5Url"`
	Enabled   bool   `json:"enabled"`
	GroupID   string `json:"groupId,omitempty"`
	GroupName string `json:"groupName,omitempty"`
	Source    string `json:"source"`
}

// startHTTPAPI 在应用启动时拉起 HTTP API。
//
// HTTP API 是可选功能，端口被占用（常见于同时运行多个客户端实例）或配置
// 有误都不该拖垮主程序——记录日志后继续启动，用户仍可在设置中改端口重试。
func (a *MyService) startHTTPAPI() {
	a.mu.RLock()
	cfg := a.config.HTTPAPI
	a.mu.RUnlock()

	if err := a.startHTTPAPIWithConfig(cfg); err != nil {
		a.logError("HTTP API 未能启动，其余功能不受影响（可在设置中更换端口后重试）", err)
	}
}

func (a *MyService) startHTTPAPIWithConfig(cfg models.HTTPAPIConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if err := validateHTTPAPIConfig(cfg); err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("启动 HTTP API 失败 (%s): %w", addr, err)
	}
	server := &http.Server{
		Handler:           newHTTPAPIHandler(a, httpAPIToken(cfg)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	a.httpServer = server
	a.log(fmt.Sprintf("HTTP API 已监听 http://%s/api/v1", addr))
	// 捕获 server 到局部变量：重启 API 时 a.httpServer 会被替换，
	// goroutine 必须持有自己那个实例，否则会 Serve 到新 server 上
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			a.logError("HTTP API 服务异常退出", serveErr)
		}
	}()
	return nil
}

func httpAPIToken(cfg models.HTTPAPIConfig) string {
	if cfg.AuthEnabled {
		return cfg.Token
	}
	return ""
}

func validateHTTPAPIConfig(cfg models.HTTPAPIConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if net.ParseIP(cfg.Host) == nil && !strings.EqualFold(cfg.Host, "localhost") {
		return fmt.Errorf("HTTP API 监听地址无效")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("HTTP API 端口必须在 1 到 65535 之间")
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	if !isLoopbackAddress(addr) && !cfg.AuthEnabled {
		return fmt.Errorf("HTTP API 监听非本机地址时必须启用鉴权")
	}
	if cfg.AuthEnabled && strings.TrimSpace(cfg.Token) == "" {
		return fmt.Errorf("启用 HTTP API 鉴权时 Token 不能为空")
	}
	return nil
}

func isLoopbackAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *MyService) stopHTTPAPI() {
	if a.httpServer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.httpServer.Shutdown(ctx)
	a.httpServer = nil
}

// GetHTTPAPIConfig 获取当前 HTTP API 配置。
func (a *MyService) GetHTTPAPIConfig() models.HTTPAPIConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.HTTPAPI
}

// SetHTTPAPIConfig 保存配置并立即重启 HTTP API 服务。
func (a *MyService) SetHTTPAPIConfig(cfg models.HTTPAPIConfig) error {
	cfg.Configured = true
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Token = strings.TrimSpace(cfg.Token)
	if err := validateHTTPAPIConfig(cfg); err != nil {
		return err
	}

	a.mu.Lock()
	old := a.config.HTTPAPI
	a.config.HTTPAPI = cfg
	if err := a.saveConfig(); err != nil {
		a.config.HTTPAPI = old
		a.mu.Unlock()
		return err
	}
	a.mu.Unlock()

	a.stopHTTPAPI()
	if err := a.startHTTPAPIWithConfig(cfg); err != nil {
		a.mu.Lock()
		a.config.HTTPAPI = old
		_ = a.saveConfig()
		a.mu.Unlock()
		_ = a.startHTTPAPIWithConfig(old)
		return err
	}
	return nil
}

func newHTTPAPIHandler(service httpAPIService, token string) http.Handler {
	api := &httpAPI{service: service, token: token}
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/v1/health", api.health)
	apiMux.HandleFunc("GET /api/v1/nodes", api.listNodes)
	apiMux.HandleFunc("POST /api/v1/nodes", api.createNode)
	apiMux.HandleFunc("POST /api/v1/nodes/start", api.startNodes)
	apiMux.HandleFunc("POST /api/v1/nodes/stop", api.stopNodes)
	apiMux.HandleFunc("POST /api/v1/nodes/delete", api.deleteNodes)
	apiMux.HandleFunc("GET /api/v1/nodes/{id}", api.getNode)
	apiMux.HandleFunc("PUT /api/v1/nodes/{id}", api.updateNode)
	apiMux.HandleFunc("DELETE /api/v1/nodes/{id}", api.deleteNode)
	apiMux.HandleFunc("POST /api/v1/nodes/{id}/start", api.startNode)
	apiMux.HandleFunc("POST /api/v1/nodes/{id}/stop", api.stopNode)
	apiMux.HandleFunc("GET /api/v1/local-proxies", api.listLocalProxies)
	apiMux.HandleFunc("GET /api/v1/local-proxies/enabled", api.listEnabledLocalProxies)
	apiMux.HandleFunc("GET /api/v1/load-balancers", api.listLoadBalancers)
	apiMux.HandleFunc("POST /api/v1/load-balancers", api.createLoadBalancer)
	apiMux.HandleFunc("GET /api/v1/load-balancers/local-proxies", api.listLoadBalancerLocalProxies)
	apiMux.HandleFunc("GET /api/v1/load-balancers/local-proxies/enabled", api.listEnabledLoadBalancerLocalProxies)
	apiMux.HandleFunc("GET /api/v1/load-balancers/{id}", api.getLoadBalancer)
	apiMux.HandleFunc("PUT /api/v1/load-balancers/{id}", api.updateLoadBalancer)
	apiMux.HandleFunc("DELETE /api/v1/load-balancers/{id}", api.deleteLoadBalancer)
	apiMux.HandleFunc("POST /api/v1/load-balancers/{id}/start", api.startLoadBalancer)
	apiMux.HandleFunc("POST /api/v1/load-balancers/{id}/stop", api.stopLoadBalancer)
	apiMux.HandleFunc("GET /api/v1/load-balancers/{id}/local-proxy", api.getLoadBalancerLocalProxy)
	apiMux.HandleFunc("GET /api/v1/chain-proxies", api.listChainProxies)
	apiMux.HandleFunc("POST /api/v1/chain-proxies", api.createChainProxy)
	apiMux.HandleFunc("GET /api/v1/chain-proxies/local-proxies", api.listChainProxyLocalProxies)
	apiMux.HandleFunc("GET /api/v1/chain-proxies/local-proxies/enabled", api.listEnabledChainProxyLocalProxies)
	apiMux.HandleFunc("GET /api/v1/chain-proxies/{id}", api.getChainProxy)
	apiMux.HandleFunc("PUT /api/v1/chain-proxies/{id}", api.updateChainProxy)
	apiMux.HandleFunc("DELETE /api/v1/chain-proxies/{id}", api.deleteChainProxy)
	apiMux.HandleFunc("POST /api/v1/chain-proxies/{id}/start", api.startChainProxy)
	apiMux.HandleFunc("POST /api/v1/chain-proxies/{id}/stop", api.stopChainProxy)
	apiMux.HandleFunc("GET /api/v1/chain-proxies/{id}/local-proxy", api.getChainProxyLocalProxy)
	apiMux.HandleFunc("GET /api/v1/session-relays", api.listSessionRelays)
	apiMux.HandleFunc("POST /api/v1/session-relays", api.createSessionRelay)
	apiMux.HandleFunc("GET /api/v1/session-relays/{id}", api.getSessionRelay)
	apiMux.HandleFunc("PUT /api/v1/session-relays/{id}", api.updateSessionRelay)
	apiMux.HandleFunc("DELETE /api/v1/session-relays/{id}", api.deleteSessionRelay)
	apiMux.HandleFunc("POST /api/v1/session-relays/{id}/start", api.startSessionRelay)
	apiMux.HandleFunc("POST /api/v1/session-relays/{id}/stop", api.stopSessionRelay)
	apiMux.HandleFunc("GET /api/v1/groups", api.listGroups)
	apiMux.HandleFunc("POST /api/v1/groups", api.createGroup)
	apiMux.HandleFunc("GET /api/v1/groups/{id}", api.getGroup)
	apiMux.HandleFunc("PUT /api/v1/groups/{id}", api.updateGroup)
	apiMux.HandleFunc("DELETE /api/v1/groups/{id}", api.deleteGroup)
	apiMux.HandleFunc("POST /api/v1/groups/{id}/start", api.startGroup)
	apiMux.HandleFunc("POST /api/v1/groups/{id}/stop", api.stopGroup)
	apiMux.HandleFunc("GET /api/v1/groups/{id}/local-proxies", api.listGroupLocalProxies)
	apiMux.HandleFunc("GET /api/v1/subscriptions", api.listSubscriptions)
	apiMux.HandleFunc("POST /api/v1/subscriptions", api.createSubscription)
	apiMux.HandleFunc("GET /api/v1/subscriptions/{id}", api.getSubscription)
	apiMux.HandleFunc("PUT /api/v1/subscriptions/{id}", api.updateSubscription)
	apiMux.HandleFunc("DELETE /api/v1/subscriptions/{id}", api.deleteSubscription)
	apiMux.HandleFunc("POST /api/v1/subscriptions/{id}/update", api.refreshSubscription)
	apiMux.HandleFunc("GET /api/v1/subscriptions/{id}/local-proxies", api.listSubscriptionLocalProxies)
	apiMux.HandleFunc("GET /api/v1/settings/pre-proxy", api.getPreProxy)
	apiMux.HandleFunc("PUT /api/v1/settings/pre-proxy", api.setPreProxy)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api/docs/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /api/docs/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(swaggerUIHTML))
	})
	mux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(openAPISpec)
	})
	mux.Handle("/api/v1/", api.auth(apiMux))
	return mux
}

func (a *httpAPI) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token != "" {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) != 1 {
				writeAPI(w, http.StatusUnauthorized, nil, "未授权")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *httpAPI) health(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, map[string]string{"status": "ok"}, "")
}
func (a *httpAPI) listNodes(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetRules(), "")
}
func (a *httpAPI) listGroups(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetGroups(), "")
}
func (a *httpAPI) listSubscriptions(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetSubscriptions(), "")
}
func (a *httpAPI) listLoadBalancers(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetLoadBalancers(), "")
}
func (a *httpAPI) listChainProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetChainProxies(), "")
}
func (a *httpAPI) listSessionRelays(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetSessionRelays(), "")
}

func (a *httpAPI) listLocalProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.buildLocalProxies("", false), "")
}

func (a *httpAPI) listEnabledLocalProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.buildLocalProxies("", true), "")
}

func (a *httpAPI) listLoadBalancerLocalProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, buildLoadBalancerLocalProxies(a.service.GetLoadBalancers(), "", false), "")
}

func (a *httpAPI) listEnabledLoadBalancerLocalProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, buildLoadBalancerLocalProxies(a.service.GetLoadBalancers(), "", true), "")
}

func (a *httpAPI) listChainProxyLocalProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, buildChainLocalProxies(a.service.GetChainProxies(), "", false), "")
}

func (a *httpAPI) listEnabledChainProxyLocalProxies(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, buildChainLocalProxies(a.service.GetChainProxies(), "", true), "")
}

func (a *httpAPI) listGroupLocalProxies(w http.ResponseWriter, r *http.Request) {
	groupID := r.PathValue("id")
	if !groupExists(a.service.GetGroups(), groupID) {
		writeAPI(w, http.StatusNotFound, nil, "分组不存在")
		return
	}
	writeAPI(w, http.StatusOK, a.buildLocalProxies(groupID, false), "")
}

func (a *httpAPI) listSubscriptionLocalProxies(w http.ResponseWriter, r *http.Request) {
	subscriptionID := r.PathValue("id")
	for _, subscription := range a.service.GetSubscriptions() {
		if subscription.ID == subscriptionID {
			writeAPI(w, http.StatusOK, buildRuleLocalProxies(a.service.GetRules(), subscription.GroupID, false), "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "订阅不存在")
}

func (a *httpAPI) buildLocalProxies(groupID string, enabledOnly bool) []localProxy {
	proxies := buildRuleLocalProxies(a.service.GetRules(), groupID, enabledOnly)
	proxies = append(proxies, buildLoadBalancerLocalProxies(a.service.GetLoadBalancers(), groupID, enabledOnly)...)
	proxies = append(proxies, buildChainLocalProxies(a.service.GetChainProxies(), groupID, enabledOnly)...)
	return proxies
}

func buildRuleLocalProxies(rules []models.ProxyRule, groupID string, enabledOnly bool) []localProxy {
	proxies := make([]localProxy, 0)
	for _, rule := range rules {
		if rule.LocalPort <= 0 || (groupID != "" && rule.GroupID != groupID) || (enabledOnly && !rule.Enabled) {
			continue
		}
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(rule.LocalPort))
		proxies = append(proxies, localProxy{
			ID:        rule.ID,
			Type:      "rule",
			Alias:     rule.Alias,
			LocalPort: rule.LocalPort,
			HTTPURL:   "http://" + address,
			SOCKS5URL: "socks5://" + address,
			Enabled:   rule.Enabled,
			GroupID:   rule.GroupID,
			GroupName: rule.GroupName,
			Source:    rule.Source,
		})
	}
	return proxies
}

func buildLoadBalancerLocalProxies(items []models.LoadBalanceNode, groupID string, enabledOnly bool) []localProxy {
	proxies := make([]localProxy, 0)
	for _, item := range items {
		if item.LocalPort <= 0 || (groupID != "" && item.GroupID != groupID) || (enabledOnly && !item.Enabled) {
			continue
		}
		proxies = append(proxies, newLocalProxy(item.ID, "loadBalancer", item.Alias, item.LocalPort, item.Enabled, item.GroupID, item.GroupName, "manual"))
	}
	return proxies
}

func buildChainLocalProxies(items []models.ChainProxy, groupID string, enabledOnly bool) []localProxy {
	proxies := make([]localProxy, 0)
	for _, item := range items {
		if item.LocalPort <= 0 || (groupID != "" && item.GroupID != groupID) || (enabledOnly && !item.Enabled) {
			continue
		}
		proxies = append(proxies, newLocalProxy(item.ID, "chainProxy", item.Alias, item.LocalPort, item.Enabled, item.GroupID, item.GroupName, "manual"))
	}
	return proxies
}

func newLocalProxy(id, proxyType, alias string, port int, enabled bool, groupID, groupName, source string) localProxy {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return localProxy{
		ID: id, Type: proxyType, Alias: alias, LocalPort: port,
		HTTPURL: "http://" + address, SOCKS5URL: "socks5://" + address,
		Enabled: enabled, GroupID: groupID, GroupName: groupName, Source: source,
	}
}

func groupExists(groups []models.Group, groupID string) bool {
	for _, group := range groups {
		if group.ID == groupID {
			return true
		}
	}
	return false
}

func (a *httpAPI) getNode(w http.ResponseWriter, r *http.Request) {
	for _, item := range a.service.GetRules() {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "节点不存在")
}
func (a *httpAPI) getGroup(w http.ResponseWriter, r *http.Request) {
	for _, item := range a.service.GetGroups() {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "分组不存在")
}
func (a *httpAPI) getSubscription(w http.ResponseWriter, r *http.Request) {
	for _, item := range a.service.GetSubscriptions() {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "订阅不存在")
}

func (a *httpAPI) getLoadBalancer(w http.ResponseWriter, r *http.Request) {
	for _, item := range a.service.GetLoadBalancers() {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "故障转移节点不存在")
}

func (a *httpAPI) getChainProxy(w http.ResponseWriter, r *http.Request) {
	for _, item := range a.service.GetChainProxies() {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "链式代理不存在")
}

func (a *httpAPI) getSessionRelay(w http.ResponseWriter, r *http.Request) {
	for _, item := range a.service.GetSessionRelays() {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "动态会话代理不存在")
}

func (a *httpAPI) getLoadBalancerLocalProxy(w http.ResponseWriter, r *http.Request) {
	for _, item := range buildLoadBalancerLocalProxies(a.service.GetLoadBalancers(), "", false) {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "故障转移节点不存在")
}

func (a *httpAPI) getChainProxyLocalProxy(w http.ResponseWriter, r *http.Request) {
	for _, item := range buildChainLocalProxies(a.service.GetChainProxies(), "", false) {
		if item.ID == r.PathValue("id") {
			writeAPI(w, http.StatusOK, item, "")
			return
		}
	}
	writeAPI(w, http.StatusNotFound, nil, "链式代理不存在")
}

func (a *httpAPI) createNode(w http.ResponseWriter, r *http.Request) {
	before := nodeIDs(a.service.GetRules())
	var rule models.ProxyRule
	if !decodeAPI(w, r, &rule) {
		return
	}
	if err := rule.Validate(); err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	if err := a.service.AddRule(rule); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusCreated, findNewNode(a.service.GetRules(), before), "")
}
func (a *httpAPI) updateNode(w http.ResponseWriter, r *http.Request) {
	var rule models.ProxyRule
	if !decodeAPI(w, r, &rule) {
		return
	}
	if err := rule.Validate(); err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	if err := a.service.UpdateRule(r.PathValue("id"), rule); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusOK, map[string]string{"status": "updated"}, "")
}

func (a *httpAPI) createLoadBalancer(w http.ResponseWriter, r *http.Request) {
	before := loadBalancerIDs(a.service.GetLoadBalancers())
	var item models.LoadBalanceNode
	if !decodeAPI(w, r, &item) || !validateLoadBalancer(w, &item) {
		return
	}
	if err := a.service.AddLoadBalancer(item); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusCreated, findNewLoadBalancer(a.service.GetLoadBalancers(), before), "")
}

func (a *httpAPI) updateLoadBalancer(w http.ResponseWriter, r *http.Request) {
	var item models.LoadBalanceNode
	if !decodeAPI(w, r, &item) || !validateLoadBalancer(w, &item) {
		return
	}
	item.ID = r.PathValue("id")
	if err := a.service.UpdateLoadBalancer(item); err != nil {
		writeServiceError(w, err)
		return
	}
	a.getLoadBalancer(w, r)
}

func (a *httpAPI) createChainProxy(w http.ResponseWriter, r *http.Request) {
	before := chainProxyIDs(a.service.GetChainProxies())
	var item models.ChainProxy
	if !decodeAPI(w, r, &item) || !validateChainProxy(w, &item) {
		return
	}
	if err := a.service.AddChainProxy(item); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusCreated, findNewChainProxy(a.service.GetChainProxies(), before), "")
}

func (a *httpAPI) updateChainProxy(w http.ResponseWriter, r *http.Request) {
	var item models.ChainProxy
	if !decodeAPI(w, r, &item) || !validateChainProxy(w, &item) {
		return
	}
	item.ID = r.PathValue("id")
	if err := a.service.UpdateChainProxy(item); err != nil {
		writeServiceError(w, err)
		return
	}
	a.getChainProxy(w, r)
}
func (a *httpAPI) createSessionRelay(w http.ResponseWriter, r *http.Request) {
	before := sessionRelayIDs(a.service.GetSessionRelays())
	var item models.SessionRelay
	if !decodeAPI(w, r, &item) {
		return
	}
	if err := item.Validate(); err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	if err := a.service.AddSessionRelay(item); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusCreated, findNewSessionRelay(a.service.GetSessionRelays(), before), "")
}

func (a *httpAPI) updateSessionRelay(w http.ResponseWriter, r *http.Request) {
	var item models.SessionRelay
	if !decodeAPI(w, r, &item) {
		return
	}
	item.ID = r.PathValue("id")
	if err := item.Validate(); err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	if err := a.service.UpdateSessionRelay(item); err != nil {
		writeServiceError(w, err)
		return
	}
	a.getSessionRelay(w, r)
}

func (a *httpAPI) createGroup(w http.ResponseWriter, r *http.Request) {
	before := groupIDs(a.service.GetGroups())
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeAPI(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Name) == "" {
		writeAPI(w, http.StatusBadRequest, nil, "分组名称不能为空")
		return
	}
	if err := a.service.CreateGroup(request.Name, request.Description); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusCreated, findNewGroup(a.service.GetGroups(), before), "")
}
func (a *httpAPI) updateGroup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeAPI(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Name) == "" {
		writeAPI(w, http.StatusBadRequest, nil, "分组名称不能为空")
		return
	}
	if err := a.service.UpdateGroup(r.PathValue("id"), request.Name, request.Description); err != nil {
		writeServiceError(w, err)
		return
	}
	a.getGroup(w, r)
}
func (a *httpAPI) createSubscription(w http.ResponseWriter, r *http.Request) {
	before := subscriptionIDs(a.service.GetSubscriptions())
	var request subscriptionRequest
	if !decodeAPI(w, r, &request) || !validateSubscription(w, &request) {
		return
	}
	if err := a.service.AddSubscription(request.Name, request.URL, request.AutoUpdate, request.UpdateInterval, request.UpdateMode, request.UpdateProxyID, request.GroupID); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusCreated, findNewSubscription(a.service.GetSubscriptions(), before), "")
}
func (a *httpAPI) updateSubscription(w http.ResponseWriter, r *http.Request) {
	var request subscriptionRequest
	if !decodeAPI(w, r, &request) || !validateSubscription(w, &request) {
		return
	}
	if err := a.service.EditSubscription(r.PathValue("id"), request.Name, request.URL, request.AutoUpdate, request.UpdateInterval, request.UpdateMode, request.UpdateProxyID, request.GroupID); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusOK, map[string]string{"status": "updated"}, "")
}

func (a *httpAPI) deleteNode(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.DeleteRule(r.PathValue("id")) })
}
func (a *httpAPI) startNode(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StartRule(r.PathValue("id")) })
}
func (a *httpAPI) stopNode(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StopRule(r.PathValue("id")) })
}
func (a *httpAPI) deleteGroup(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.DeleteGroup(r.PathValue("id")) })
}
func (a *httpAPI) startGroup(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StartAllRulesInGroup(r.PathValue("id")) })
}
func (a *httpAPI) stopGroup(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StopAllRulesInGroup(r.PathValue("id")) })
}
func (a *httpAPI) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.DeleteSubscription(r.PathValue("id")) })
}
func (a *httpAPI) refreshSubscription(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.UpdateSubscriptionByID(r.PathValue("id")) })
}
func (a *httpAPI) deleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.DeleteLoadBalancer(r.PathValue("id")) })
}
func (a *httpAPI) startLoadBalancer(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StartLoadBalancer(r.PathValue("id")) })
}
func (a *httpAPI) stopLoadBalancer(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StopLoadBalancer(r.PathValue("id")) })
}
func (a *httpAPI) deleteChainProxy(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.DeleteChainProxy(r.PathValue("id")) })
}
func (a *httpAPI) startChainProxy(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StartChainProxy(r.PathValue("id")) })
}
func (a *httpAPI) stopChainProxy(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StopChainProxy(r.PathValue("id")) })
}

func (a *httpAPI) deleteSessionRelay(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.DeleteSessionRelay(r.PathValue("id")) })
}
func (a *httpAPI) startSessionRelay(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StartSessionRelay(r.PathValue("id")) })
}
func (a *httpAPI) stopSessionRelay(w http.ResponseWriter, r *http.Request) {
	a.run(w, func() error { return a.service.StopSessionRelay(r.PathValue("id")) })
}

func (a *httpAPI) startNodes(w http.ResponseWriter, r *http.Request) {
	a.nodesAction(w, r, a.service.StartNodes)
}
func (a *httpAPI) stopNodes(w http.ResponseWriter, r *http.Request) {
	a.nodesAction(w, r, a.service.StopNodes)
}

// deleteNodes 批量删除。用 POST /nodes/delete 而非 DELETE /nodes：
// DELETE 带请求体在部分客户端/代理上支持不佳，与批量启停保持同样的形式更一致。
func (a *httpAPI) deleteNodes(w http.ResponseWriter, r *http.Request) {
	a.nodesAction(w, r, a.service.DeleteNodes)
}
func (a *httpAPI) nodesAction(w http.ResponseWriter, r *http.Request, action func([]string) error) {
	var request struct {
		IDs []string `json:"ids"`
	}
	if !decodeAPI(w, r, &request) {
		return
	}
	if len(request.IDs) == 0 {
		writeAPI(w, http.StatusBadRequest, nil, "ids 不能为空")
		return
	}
	if err := action(request.IDs); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusOK, map[string]string{"status": "ok"}, "")
}
func (a *httpAPI) run(w http.ResponseWriter, action func() error) {
	if err := action(); err != nil {
		writeServiceError(w, err)
		return
	}
	writeAPI(w, http.StatusOK, map[string]string{"status": "ok"}, "")
}

func validateSubscription(w http.ResponseWriter, request *subscriptionRequest) bool {
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.URL) == "" {
		writeAPI(w, http.StatusBadRequest, nil, "订阅名称和地址不能为空")
		return false
	}
	if request.AutoUpdate && request.UpdateInterval < 1 {
		writeAPI(w, http.StatusBadRequest, nil, "自动更新间隔必须大于等于 1 小时")
		return false
	}
	if request.UpdateMode == "" {
		request.UpdateMode = "direct"
	}
	if request.UpdateMode != "direct" && request.UpdateMode != "system" && request.UpdateMode != "proxy" {
		writeAPI(w, http.StatusBadRequest, nil, "updateMode 必须为 direct、system 或 proxy")
		return false
	}
	if request.UpdateMode == "proxy" && request.UpdateProxyID == "" {
		writeAPI(w, http.StatusBadRequest, nil, "proxy 模式必须提供 updateProxyId")
		return false
	}
	return true
}

func validateLoadBalancer(w http.ResponseWriter, item *models.LoadBalanceNode) bool {
	item.Alias = strings.TrimSpace(item.Alias)
	if item.Alias == "" {
		writeAPI(w, http.StatusBadRequest, nil, "故障转移名称不能为空")
		return false
	}
	if item.LocalPort < 1 || item.LocalPort > 65535 {
		writeAPI(w, http.StatusBadRequest, nil, "本地端口必须在 1 到 65535 之间")
		return false
	}
	if len(item.NodeIDs) == 0 {
		writeAPI(w, http.StatusBadRequest, nil, "故障转移节点需要至少一个子节点")
		return false
	}
	if item.LocalType == "" {
		item.LocalType = "mixed"
	}
	return true
}

func (a *httpAPI) getPreProxy(w http.ResponseWriter, _ *http.Request) {
	writeAPI(w, http.StatusOK, a.service.GetPreProxy(), "")
}

func (a *httpAPI) setPreProxy(w http.ResponseWriter, r *http.Request) {
	// groupIds 为空表示对全部节点生效；excludedIds 里的节点始终直连
	var body struct {
		NodeID      string   `json:"nodeId"`
		GroupIDs    []string `json:"groupIds"`
		ExcludedIDs []string `json:"excludedIds"`
	}
	if !decodeAPI(w, r, &body) {
		return
	}
	err := a.service.SetPreProxyConfig(models.PreProxyConfig{
		NodeID:      body.NodeID,
		GroupIDs:    body.GroupIDs,
		ExcludedIDs: body.ExcludedIDs,
	})
	if err != nil {
		writeAPI(w, http.StatusBadRequest, nil, err.Error())
		return
	}
	writeAPI(w, http.StatusOK, a.service.GetPreProxy(), "")
}

func validateChainProxy(w http.ResponseWriter, item *models.ChainProxy) bool {
	item.Alias = strings.TrimSpace(item.Alias)
	if item.Alias == "" {
		writeAPI(w, http.StatusBadRequest, nil, "链式代理名称不能为空")
		return false
	}
	if item.LocalPort < 1 || item.LocalPort > 65535 {
		writeAPI(w, http.StatusBadRequest, nil, "本地端口必须在 1 到 65535 之间")
		return false
	}
	if len(item.ChainNodes) < 2 {
		writeAPI(w, http.StatusBadRequest, nil, "链式代理需要至少两个节点")
		return false
	}
	if item.LocalType == "" {
		item.LocalType = "mixed"
	}
	return true
}

func nodeIDs(items []models.ProxyRule) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}
func groupIDs(items []models.Group) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}
func subscriptionIDs(items []models.Subscription) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}
func loadBalancerIDs(items []models.LoadBalanceNode) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}
func chainProxyIDs(items []models.ChainProxy) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}
func sessionRelayIDs(items []models.SessionRelay) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		result[item.ID] = true
	}
	return result
}
func findNewNode(items []models.ProxyRule, before map[string]bool) any {
	for _, item := range items {
		if !before[item.ID] {
			return item
		}
	}
	return map[string]string{"status": "created"}
}
func findNewGroup(items []models.Group, before map[string]bool) any {
	for _, item := range items {
		if !before[item.ID] {
			return item
		}
	}
	return map[string]string{"status": "created"}
}
func findNewSubscription(items []models.Subscription, before map[string]bool) any {
	for _, item := range items {
		if !before[item.ID] {
			return item
		}
	}
	return map[string]string{"status": "created"}
}
func findNewLoadBalancer(items []models.LoadBalanceNode, before map[string]bool) any {
	for _, item := range items {
		if !before[item.ID] {
			return item
		}
	}
	return map[string]string{"status": "created"}
}
func findNewChainProxy(items []models.ChainProxy, before map[string]bool) any {
	for _, item := range items {
		if !before[item.ID] {
			return item
		}
	}
	return map[string]string{"status": "created"}
}
func findNewSessionRelay(items []models.SessionRelay, before map[string]bool) any {
	for _, item := range items {
		if !before[item.ID] {
			return item
		}
	}
	return map[string]string{"status": "created"}
}

func decodeAPI(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeAPI(w, http.StatusBadRequest, nil, "请求 JSON 无效: "+err.Error())
		return false
	}
	return true
}
func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if strings.Contains(err.Error(), "不存在") {
		status = http.StatusNotFound
	}
	writeAPI(w, status, nil, err.Error())
}
func writeAPI(w http.ResponseWriter, status int, data any, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiResponse{Data: data, Error: message})
}
