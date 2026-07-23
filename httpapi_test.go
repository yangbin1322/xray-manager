package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"
	"xray-manager/internal/models"
)

type fakeHTTPAPIService struct {
	rules     []models.ProxyRule
	groups    []models.Group
	subs      []models.Subscription
	startedID string
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
func (f *fakeHTTPAPIService) GetGroups() []models.Group                 { return f.groups }
func (f *fakeHTTPAPIService) CreateGroup(string, string) error          { return nil }
func (f *fakeHTTPAPIService) UpdateGroup(string, string, string) error  { return nil }
func (f *fakeHTTPAPIService) DeleteGroup(string) error                  { return nil }
func (f *fakeHTTPAPIService) StartAllRulesInGroup(string) error         { return nil }
func (f *fakeHTTPAPIService) StopAllRulesInGroup(string) error          { return nil }
func (f *fakeHTTPAPIService) GetSubscriptions() []models.Subscription   { return f.subs }
func (f *fakeHTTPAPIService) AddSubscription(string, string, bool, int, string, string) error {
	return nil
}
func (f *fakeHTTPAPIService) EditSubscription(string, string, string, bool, int, string, string) error {
	return nil
}
func (f *fakeHTTPAPIService) UpdateSubscriptionByID(string) error { return nil }
func (f *fakeHTTPAPIService) DeleteSubscription(string) error     { return nil }

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
		"/health", "/nodes", "/nodes/start", "/nodes/stop", "/nodes/{id}",
		"/nodes/{id}/start", "/nodes/{id}/stop", "/local-proxies", "/local-proxies/enabled",
		"/groups", "/groups/{id}", "/groups/{id}/start", "/groups/{id}/stop",
		"/groups/{id}/local-proxies", "/subscriptions", "/subscriptions/{id}",
		"/subscriptions/{id}/update", "/subscriptions/{id}/local-proxies",
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
	}
	handler := newHTTPAPIHandler(service, "")

	tests := []struct {
		path       string
		wantIDs    []string
		rejectIDs  []string
		wantText   string
		wantStatus int
	}{
		{path: "/api/v1/local-proxies", wantIDs: []string{"enabled", "stopped"}, wantText: "socks5://127.0.0.1:1080", wantStatus: http.StatusOK},
		{path: "/api/v1/local-proxies/enabled", wantIDs: []string{"enabled"}, rejectIDs: []string{"stopped"}, wantStatus: http.StatusOK},
		{path: "/api/v1/groups/group_1/local-proxies", wantIDs: []string{"enabled"}, wantStatus: http.StatusOK},
		{path: "/api/v1/subscriptions/sub_1/local-proxies", wantIDs: []string{"stopped"}, wantStatus: http.StatusOK},
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
	}
	handler := newHTTPAPIHandler(service, "")
	nodeBody := `{"alias":"test","protocol":"vmess","serverAddr":"example.com","serverPort":443,"settings":{}}`
	subscriptionBody := `{"name":"Sub","url":"https://example.com/sub","updateMode":"direct"}`

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
