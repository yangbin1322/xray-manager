package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"
	"xray-manager/internal/models"
)

type fakeHTTPAPIService struct {
	rules          []models.ProxyRule
	groups         []models.Group
	subs           []models.Subscription
	lbs            []models.LoadBalanceNode
	chains         []models.ChainProxy
	relays         []models.SessionRelay
	startedID      string
	preProxyID     string
	deletedNodeIDs []string

	lastSubGroupID string
}

func (f *fakeHTTPAPIService) GetRules() []models.ProxyRule { return f.rules }
func (f *fakeHTTPAPIService) AddRule(rule models.ProxyRule) error {
	rule.ID = "rule_new"
	f.rules = append(f.rules, rule)
	return nil
}
func (f *fakeHTTPAPIService) UpdateRule(string, models.ProxyRule) error { return nil }
func (f *fakeHTTPAPIService) DeleteRule(string) error                   { return nil }
func (f *fakeHTTPAPIService) StartRule(id string) error                 { f.startedID = id; return nil }
func (f *fakeHTTPAPIService) StopRule(string) error                     { return nil }
func (f *fakeHTTPAPIService) StartNodes([]string) error                 { return nil }
func (f *fakeHTTPAPIService) StopNodes([]string) error                  { return nil }
func (f *fakeHTTPAPIService) DeleteNodes(ids []string) error {
	f.deletedNodeIDs = append(f.deletedNodeIDs, ids...)
	return nil
}
func (f *fakeHTTPAPIService) GetGroups() []models.Group                { return f.groups }
func (f *fakeHTTPAPIService) CreateGroup(string, string) error         { return nil }
func (f *fakeHTTPAPIService) UpdateGroup(string, string, string) error { return nil }
func (f *fakeHTTPAPIService) DeleteGroup(string) error                 { return nil }
func (f *fakeHTTPAPIService) StartAllRulesInGroup(string) error        { return nil }
func (f *fakeHTTPAPIService) StopAllRulesInGroup(string) error         { return nil }
func (f *fakeHTTPAPIService) GetSubscriptions() []models.Subscription  { return f.subs }
func (f *fakeHTTPAPIService) AddSubscription(_, _ string, _ bool, _ int, _, _ string, groupID string) error {
	f.lastSubGroupID = groupID
	f.subs = append(f.subs, models.Subscription{ID: "sub_new", GroupID: groupID})
	return nil
}
func (f *fakeHTTPAPIService) EditSubscription(_, _, _ string, _ bool, _ int, _, _ string, groupID string) error {
	f.lastSubGroupID = groupID
	return nil
}
func (f *fakeHTTPAPIService) UpdateSubscriptionByID(string) error        { return nil }
func (f *fakeHTTPAPIService) DeleteSubscription(string) error            { return nil }
func (f *fakeHTTPAPIService) GetLoadBalancers() []models.LoadBalanceNode { return f.lbs }
func (f *fakeHTTPAPIService) AddLoadBalancer(item models.LoadBalanceNode) error {
	item.ID = "lb_new"
	f.lbs = append(f.lbs, item)
	return nil
}
func (f *fakeHTTPAPIService) UpdateLoadBalancer(item models.LoadBalanceNode) error {
	for i := range f.lbs {
		if f.lbs[i].ID == item.ID {
			f.lbs[i] = item
		}
	}
	return nil
}
func (f *fakeHTTPAPIService) DeleteLoadBalancer(string) error      { return nil }
func (f *fakeHTTPAPIService) StartLoadBalancer(string) error       { return nil }
func (f *fakeHTTPAPIService) StopLoadBalancer(string) error        { return nil }
func (f *fakeHTTPAPIService) GetChainProxies() []models.ChainProxy { return f.chains }
func (f *fakeHTTPAPIService) AddChainProxy(item models.ChainProxy) error {
	item.ID = "chain_new"
	f.chains = append(f.chains, item)
	return nil
}
func (f *fakeHTTPAPIService) UpdateChainProxy(item models.ChainProxy) error {
	for i := range f.chains {
		if f.chains[i].ID == item.ID {
			f.chains[i] = item
		}
	}
	return nil
}
func (f *fakeHTTPAPIService) DeleteChainProxy(string) error           { return nil }
func (f *fakeHTTPAPIService) StartChainProxy(string) error            { return nil }
func (f *fakeHTTPAPIService) StopChainProxy(string) error             { return nil }
func (f *fakeHTTPAPIService) GetSessionRelays() []models.SessionRelay { return f.relays }
func (f *fakeHTTPAPIService) AddSessionRelay(item models.SessionRelay) error {
	item.ID = "relay_new"
	f.relays = append(f.relays, item)
	return nil
}
func (f *fakeHTTPAPIService) UpdateSessionRelay(item models.SessionRelay) error {
	for i := range f.relays {
		if f.relays[i].ID == item.ID {
			f.relays[i] = item
		}
	}
	return nil
}
func (f *fakeHTTPAPIService) DeleteSessionRelay(string) error { return nil }
func (f *fakeHTTPAPIService) StartSessionRelay(string) error  { return nil }
func (f *fakeHTTPAPIService) StopSessionRelay(string) error   { return nil }
func (f *fakeHTTPAPIService) GetPreProxy() models.PreProxyConfig {
	cfg := models.PreProxyConfig{NodeID: f.preProxyID}
	for _, r := range f.rules {
		if r.ID == f.preProxyID {
			cfg.Alias = r.Alias
			break
		}
	}
	return cfg
}
func (f *fakeHTTPAPIService) SetPreProxy(id string) error {
	if id == "" {
		f.preProxyID = ""
		return nil
	}
	for _, r := range f.rules {
		if r.ID == id {
			f.preProxyID = id
			return nil
		}
	}
	return fmt.Errorf("前置代理节点不存在: %s", id)
}

func (f *fakeHTTPAPIService) SetPreProxyConfig(cfg models.PreProxyConfig) error {
	return f.SetPreProxy(cfg.NodeID)
}

func TestHTTPAPIRequiresConfiguredToken(t *testing.T) {
	handler := newHTTPAPIHandler(&fakeHTTPAPIService{}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestHTTPAPIDocumentationDoesNotRequireToken(t *testing.T) {
	handler := newHTTPAPIHandler(&fakeHTTPAPIService{}, "secret")

	for _, path := range []string{"/api/docs/", "/api/openapi.yaml"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("expected %s to return 200, got %d", path, response.Code)
		}
	}
}

func TestOpenAPISpecIsValidYAML(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatalf("invalid OpenAPI YAML: %v", err)
	}
	if document["openapi"] != "3.0.3" {
		t.Fatalf("unexpected OpenAPI version: %v", document["openapi"])
	}
}

func TestOpenAPISpecDocumentsAllBusinessRoutes(t *testing.T) {
	var document struct {
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatalf("invalid OpenAPI YAML: %v", err)
	}
	wantPaths := []string{
		"/health", "/nodes", "/nodes/start", "/nodes/stop", "/nodes/delete", "/nodes/{id}",
		"/nodes/{id}/start", "/nodes/{id}/stop", "/local-proxies", "/local-proxies/enabled",
		"/groups", "/groups/{id}", "/groups/{id}/start", "/groups/{id}/stop",
		"/groups/{id}/local-proxies",
		"/subscriptions", "/subscriptions/{id}",
		"/subscriptions/{id}/update", "/subscriptions/{id}/local-proxies",
		"/load-balancers", "/load-balancers/{id}", "/load-balancers/{id}/start",
		"/load-balancers/{id}/stop", "/load-balancers/{id}/local-proxy",
		"/load-balancers/local-proxies", "/load-balancers/local-proxies/enabled",
		"/chain-proxies", "/chain-proxies/{id}", "/chain-proxies/{id}/start",
		"/chain-proxies/{id}/stop", "/chain-proxies/{id}/local-proxy",
		"/chain-proxies/local-proxies", "/chain-proxies/local-proxies/enabled",
		"/session-relays", "/session-relays/{id}", "/session-relays/{id}/start",
		"/session-relays/{id}/stop",
	}
	for _, path := range wantPaths {
		if _, exists := document.Paths[path]; !exists {
			t.Errorf("OpenAPI spec does not document %s", path)
		}
	}
}

func TestHTTPAPICreatesAndReturnsNode(t *testing.T) {
	service := &fakeHTTPAPIService{}
	handler := newHTTPAPIHandler(service, "secret")
	body := []byte(`{"alias":"test","protocol":"vmess","serverAddr":"example.com","serverPort":443,"settings":{}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if len(service.rules) != 1 || service.rules[0].ID != "rule_new" {
		t.Fatalf("node was not created: %#v", service.rules)
	}
}

func TestHTTPAPIStartsNodeFromPath(t *testing.T) {
	service := &fakeHTTPAPIService{}
	handler := newHTTPAPIHandler(service, "")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/rule_123/start", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.startedID != "rule_123" {
		t.Fatalf("expected rule_123 to start, status=%d id=%q", response.Code, service.startedID)
	}
}

func TestHTTPAPIRejectsInvalidSubscription(t *testing.T) {
	handler := newHTTPAPIHandler(&fakeHTTPAPIService{}, "")
	body := []byte(`{"name":"sub","url":"https://example.com/sub","updateMode":"proxy"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", bytes.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestHTTPAPIRejectsInvalidCompositeProxyRequests(t *testing.T) {
	handler := newHTTPAPIHandler(&fakeHTTPAPIService{}, "")
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "load balancer without nodes", path: "/api/v1/load-balancers", body: `{"alias":"LB","localPort":2080,"nodeIds":[]}`},
		{name: "load balancer invalid port", path: "/api/v1/load-balancers", body: `{"alias":"LB","localPort":70000,"nodeIds":["rule_1"]}`},
		{name: "chain with one node", path: "/api/v1/chain-proxies", body: `{"alias":"Chain","localPort":3080,"chainNodes":["rule_1"]}`},
		{name: "chain without alias", path: "/api/v1/chain-proxies", body: `{"localPort":3080,"chainNodes":["rule_1","rule_2"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestValidateHTTPAPIConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  models.HTTPAPIConfig
		wantErr bool
	}{
		{name: "disabled", config: models.HTTPAPIConfig{Enabled: false}},
		{name: "local without auth", config: models.HTTPAPIConfig{Enabled: true, Host: "127.0.0.1", Port: 9090}},
		{name: "remote with auth", config: models.HTTPAPIConfig{Enabled: true, Host: "0.0.0.0", Port: 9090, AuthEnabled: true, Token: "secret"}},
		{name: "remote without auth", config: models.HTTPAPIConfig{Enabled: true, Host: "0.0.0.0", Port: 9090}, wantErr: true},
		{name: "auth without token", config: models.HTTPAPIConfig{Enabled: true, Host: "127.0.0.1", Port: 9090, AuthEnabled: true}, wantErr: true},
		{name: "invalid port", config: models.HTTPAPIConfig{Enabled: true, Host: "127.0.0.1", Port: 70000}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateHTTPAPIConfig(test.config)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateHTTPAPIConfig() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestHTTPAPILocalProxyFilters(t *testing.T) {
	service := &fakeHTTPAPIService{
		rules: []models.ProxyRule{
			{ID: "enabled", Alias: "Enabled", LocalPort: 1080, Enabled: true, GroupID: "group_1", Source: "manual"},
			{ID: "stopped", Alias: "Stopped", LocalPort: 1081, GroupID: "group_2", Source: "subscription"},
		},
		groups: []models.Group{{ID: "group_1"}, {ID: "group_2"}},
		subs:   []models.Subscription{{ID: "sub_1", GroupID: "group_2"}},
		lbs:    []models.LoadBalanceNode{{ID: "lb_enabled", Alias: "LB", LocalPort: 2080, Enabled: true, GroupID: "group_1"}},
		chains: []models.ChainProxy{{ID: "chain_stopped", Alias: "Chain", LocalPort: 3080, GroupID: "group_1"}},
	}
	handler := newHTTPAPIHandler(service, "")

	tests := []struct {
		path       string
		wantIDs    []string
		rejectIDs  []string
		wantText   string
		wantStatus int
	}{
		{path: "/api/v1/local-proxies", wantIDs: []string{"enabled", "stopped", "lb_enabled", "chain_stopped"}, wantText: "socks5://127.0.0.1:1080", wantStatus: http.StatusOK},
		{path: "/api/v1/local-proxies/enabled", wantIDs: []string{"enabled", "lb_enabled"}, rejectIDs: []string{"stopped", "chain_stopped"}, wantStatus: http.StatusOK},
		{path: "/api/v1/groups/group_1/local-proxies", wantIDs: []string{"enabled", "lb_enabled", "chain_stopped"}, wantStatus: http.StatusOK},
		{path: "/api/v1/subscriptions/sub_1/local-proxies", wantIDs: []string{"stopped"}, wantStatus: http.StatusOK},
		{path: "/api/v1/load-balancers/local-proxies/enabled", wantIDs: []string{"lb_enabled"}, wantText: `"type":"loadBalancer"`, wantStatus: http.StatusOK},
		{path: "/api/v1/chain-proxies/local-proxies", wantIDs: []string{"chain_stopped"}, wantText: `"type":"chainProxy"`, wantStatus: http.StatusOK},
		{path: "/api/v1/groups/missing/local-proxies", wantStatus: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
			for _, id := range test.wantIDs {
				if !bytes.Contains(response.Body.Bytes(), []byte(`"id":"`+id+`"`)) {
					t.Fatalf("response does not contain %q: %s", id, response.Body.String())
				}
			}
			for _, id := range test.rejectIDs {
				if bytes.Contains(response.Body.Bytes(), []byte(`"id":"`+id+`"`)) {
					t.Fatalf("response unexpectedly contains %q: %s", id, response.Body.String())
				}
			}
			if test.wantText != "" && !bytes.Contains(response.Body.Bytes(), []byte(test.wantText)) {
				t.Fatalf("response does not contain %q: %s", test.wantText, response.Body.String())
			}
		})
	}
}

func TestHTTPAPIRouteContract(t *testing.T) {
	service := &fakeHTTPAPIService{
		rules:  []models.ProxyRule{{ID: "rule_1", Protocol: "vmess", ServerAddr: "example.com", ServerPort: 443, Settings: models.ProxySettings{}}},
		groups: []models.Group{{ID: "group_1", Name: "Group"}},
		subs:   []models.Subscription{{ID: "sub_1", Name: "Sub", URL: "https://example.com/sub", GroupID: "group_1"}},
		lbs:    []models.LoadBalanceNode{{ID: "lb_1", Alias: "LB", LocalPort: 2080, NodeIDs: []string{"rule_1"}}},
		chains: []models.ChainProxy{{ID: "chain_1", Alias: "Chain", LocalPort: 3080, ChainNodes: []string{"rule_1", "lb_1"}}},
	}
	handler := newHTTPAPIHandler(service, "")
	nodeBody := `{"alias":"test","protocol":"vmess","serverAddr":"example.com","serverPort":443,"settings":{}}`
	subscriptionBody := `{"name":"Sub","url":"https://example.com/sub","updateMode":"direct"}`
	loadBalancerBody := `{"alias":"LB","localPort":2080,"nodeIds":["rule_1"]}`
	chainProxyBody := `{"alias":"Chain","localPort":3080,"chainNodes":["rule_1","lb_1"]}`

	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/api/v1/health", "", http.StatusOK},
		{http.MethodGet, "/api/v1/nodes", "", http.StatusOK},
		{http.MethodPost, "/api/v1/nodes", nodeBody, http.StatusCreated},
		{http.MethodGet, "/api/v1/nodes/rule_1", "", http.StatusOK},
		{http.MethodPut, "/api/v1/nodes/rule_1", nodeBody, http.StatusOK},
		{http.MethodDelete, "/api/v1/nodes/rule_1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/nodes/rule_1/start", "", http.StatusOK},
		{http.MethodPost, "/api/v1/nodes/rule_1/stop", "", http.StatusOK},
		{http.MethodPost, "/api/v1/nodes/start", `{"ids":["rule_1"]}`, http.StatusOK},
		{http.MethodPost, "/api/v1/nodes/stop", `{"ids":["rule_1"]}`, http.StatusOK},
		{http.MethodGet, "/api/v1/groups", "", http.StatusOK},
		{http.MethodPost, "/api/v1/groups", `{"name":"Group"}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/groups/group_1", "", http.StatusOK},
		{http.MethodPut, "/api/v1/groups/group_1", `{"name":"Updated"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/groups/group_1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/groups/group_1/start", "", http.StatusOK},
		{http.MethodPost, "/api/v1/groups/group_1/stop", "", http.StatusOK},
		{http.MethodGet, "/api/v1/groups/group_1/local-proxies", "", http.StatusOK},
		{http.MethodGet, "/api/v1/subscriptions", "", http.StatusOK},
		{http.MethodPost, "/api/v1/subscriptions", subscriptionBody, http.StatusCreated},
		{http.MethodGet, "/api/v1/subscriptions/sub_1", "", http.StatusOK},
		{http.MethodPut, "/api/v1/subscriptions/sub_1", subscriptionBody, http.StatusOK},
		{http.MethodDelete, "/api/v1/subscriptions/sub_1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/subscriptions/sub_1/update", "", http.StatusOK},
		{http.MethodGet, "/api/v1/subscriptions/sub_1/local-proxies", "", http.StatusOK},
		{http.MethodGet, "/api/v1/load-balancers", "", http.StatusOK},
		{http.MethodPost, "/api/v1/load-balancers", loadBalancerBody, http.StatusCreated},
		{http.MethodGet, "/api/v1/load-balancers/lb_1", "", http.StatusOK},
		{http.MethodPut, "/api/v1/load-balancers/lb_1", loadBalancerBody, http.StatusOK},
		{http.MethodDelete, "/api/v1/load-balancers/lb_1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/load-balancers/lb_1/start", "", http.StatusOK},
		{http.MethodPost, "/api/v1/load-balancers/lb_1/stop", "", http.StatusOK},
		{http.MethodGet, "/api/v1/load-balancers/lb_1/local-proxy", "", http.StatusOK},
		{http.MethodGet, "/api/v1/load-balancers/local-proxies", "", http.StatusOK},
		{http.MethodGet, "/api/v1/load-balancers/local-proxies/enabled", "", http.StatusOK},
		{http.MethodGet, "/api/v1/chain-proxies", "", http.StatusOK},
		{http.MethodPost, "/api/v1/chain-proxies", chainProxyBody, http.StatusCreated},
		{http.MethodGet, "/api/v1/chain-proxies/chain_1", "", http.StatusOK},
		{http.MethodPut, "/api/v1/chain-proxies/chain_1", chainProxyBody, http.StatusOK},
		{http.MethodDelete, "/api/v1/chain-proxies/chain_1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/chain-proxies/chain_1/start", "", http.StatusOK},
		{http.MethodPost, "/api/v1/chain-proxies/chain_1/stop", "", http.StatusOK},
		{http.MethodGet, "/api/v1/chain-proxies/chain_1/local-proxy", "", http.StatusOK},
		{http.MethodGet, "/api/v1/chain-proxies/local-proxies", "", http.StatusOK},
		{http.MethodGet, "/api/v1/chain-proxies/local-proxies/enabled", "", http.StatusOK},
		{http.MethodGet, "/api/v1/settings/pre-proxy", "", http.StatusOK},
		{http.MethodPut, "/api/v1/settings/pre-proxy", `{"nodeId":""}`, http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("expected %d, got %d: %s", test.status, response.Code, response.Body.String())
			}
		})
	}
}

func TestHTTPAPIPreProxy(t *testing.T) {
	svc := &fakeHTTPAPIService{
		rules: []models.ProxyRule{
			{ID: "rule1", Alias: "HK", Protocol: "vmess", ServerAddr: "1.1.1.1", ServerPort: 443},
		},
	}
	handler := newHTTPAPIHandler(svc, "")

	// get empty
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/pre-proxy", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// set
	body := bytes.NewBufferString(`{"nodeId":"rule1"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings/pre-proxy", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.preProxyID != "rule1" {
		t.Fatalf("preProxyID want rule1, got %s", svc.preProxyID)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"nodeId":"rule1"`)) {
		t.Fatalf("response missing nodeId: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"alias":"HK"`)) {
		t.Fatalf("response missing alias: %s", rec.Body.String())
	}

	// invalid
	body = bytes.NewBufferString(`{"nodeId":"missing"}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings/pre-proxy", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid node expected 400, got %d", rec.Code)
	}

	// clear
	body = bytes.NewBufferString(`{"nodeId":""}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings/pre-proxy", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear expected 200, got %d", rec.Code)
	}
	if svc.preProxyID != "" {
		t.Fatalf("preProxyID should be cleared")
	}
}

func TestHTTPAPIPassesSubscriptionGroupID(t *testing.T) {
	service := &fakeHTTPAPIService{}
	handler := newHTTPAPIHandler(service, "")
	body := []byte(`{"name":"sub","url":"https://example.com/sub","groupId":"group_existing"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", bytes.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	// 多个订阅汇入同一分组的前提：groupId 必须原样传到服务层
	if service.lastSubGroupID != "group_existing" {
		t.Fatalf("groupId 未传递到服务层，实际 %q", service.lastSubGroupID)
	}
}

// 批量删除接口应把 ids 原样传给服务层
func TestHTTPAPIDeleteNodesBatch(t *testing.T) {
	service := &fakeHTTPAPIService{}
	handler := newHTTPAPIHandler(service, "")

	body := []byte(`{"ids":["rule_1","lb_2","chain_3"]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/delete", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	want := []string{"rule_1", "lb_2", "chain_3"}
	if len(service.deletedNodeIDs) != len(want) {
		t.Fatalf("应删除 %d 个节点，实际 %v", len(want), service.deletedNodeIDs)
	}
	for i := range want {
		if service.deletedNodeIDs[i] != want[i] {
			t.Fatalf("第 %d 个 ID 应为 %s，实际 %s", i, want[i], service.deletedNodeIDs[i])
		}
	}
}

// 空 ids 应被拒绝，避免误触发全量操作
func TestHTTPAPIDeleteNodesRejectsEmpty(t *testing.T) {
	service := &fakeHTTPAPIService{}
	handler := newHTTPAPIHandler(service, "")

	request := httptest.NewRequest(http.MethodPost, "/api/v1/nodes/delete", bytes.NewReader([]byte(`{"ids":[]}`)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code == http.StatusOK {
		t.Fatal("空 ids 不应被接受")
	}
	if len(service.deletedNodeIDs) != 0 {
		t.Fatalf("不应触发删除，实际 %v", service.deletedNodeIDs)
	}
}

// 新增的节点字段必须写进 OpenAPI，否则 REST 调用方无从得知它们的存在
func TestOpenAPIDocumentsNodeUserFields(t *testing.T) {
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(openAPISpec, &document); err != nil {
		t.Fatalf("invalid OpenAPI YAML: %v", err)
	}
	props := document.Components.Schemas["NodeInput"].Properties
	if props == nil {
		t.Fatal("NodeInput schema 缺失或没有 properties")
	}
	for _, field := range []string{"remark", "bindExitIP", "boundExitIP"} {
		if _, ok := props[field]; !ok {
			t.Errorf("NodeInput 未文档化字段 %s", field)
		}
	}
}
