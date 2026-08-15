package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentAccessAllowsConfiguredScopeAndRejectsMissingScope(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cli-access.json")
	writeTestCLIConfig(t, configPath, 0o600, cliAccessConfig{
		Version: 1,
		Enabled: true,
		Token:   "0123456789abcdef0123456789abcdef0123456789abcdef",
		Scopes:  []string{"device.read"},
	})
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")
	t.Setenv(cliConfigEnvironment, configPath)

	handler := (&app{}).agentAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"actor": principalFromRequest(r).Kind})
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef0123456789abcdef")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/sms/send", bytes.NewBufferString(`{"phone":"123","message":"hi"}`))
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef0123456789abcdef")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("POST status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload["required_scope"] != "sms.send" {
		t.Fatalf("unexpected payload: %s", response.Body.String())
	}
}

func TestAgentAccessAppTokenHasFullAccess(t *testing.T) {
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")
	t.Setenv(cliConfigEnvironment, filepath.Join(t.TempDir(), "missing.json"))
	handler := (&app{}).agentAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principalFromRequest(r).Kind != "app" {
			t.Fatalf("principal=%q", principalFromRequest(r).Kind)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/at", nil)
	request.Header.Set(appTokenHeader, "native-app-test-token-0123456789")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentAccessRejectsInsecureConfigPermissions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cli-access.json")
	writeTestCLIConfig(t, configPath, 0o644, cliAccessConfig{
		Version: 1,
		Enabled: true,
		Token:   "0123456789abcdef0123456789abcdef",
		Scopes:  []string{"device.read"},
	})
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")
	t.Setenv(cliConfigEnvironment, configPath)
	handler := (&app{}).agentAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentAccessFailsClosedForUnmappedCLIRoute(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cli-access.json")
	writeTestCLIConfig(t, configPath, 0o600, cliAccessConfig{
		Version: 1,
		Enabled: true,
		Token:   "0123456789abcdef0123456789abcdef",
		Scopes:  []string{"device.read"},
	})
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")
	t.Setenv(cliConfigEnvironment, configPath)
	handler := (&app{}).agentAccessMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/future-route", nil)
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCLIIdempotencyReplaysWithoutExecutingTwice(t *testing.T) {
	store := newIdempotencyStore()
	var calls atomic.Int32
	handler := store.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count := calls.Add(1)
		writeJSON(w, http.StatusOK, map[string]int32{"calls": count})
	}))
	principal := requestPrincipal{Kind: "cli", ID: "cli-test", Scopes: map[string]struct{}{"sms.send": {}}}

	perform := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/sms/send", bytes.NewBufferString(body))
		request.Header.Set("Idempotency-Key", "request-0001")
		request = request.WithContext(contextWithPrincipal(request, principal))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	first := perform(`{"phone":"123","message":"hi"}`)
	second := perform(`{"phone":"123","message":"hi"}`)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("first=%d second=%d calls=%d", first.Code, second.Code, calls.Load())
	}
	if first.Header().Get("Idempotency-Replayed") != "false" || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay headers first=%q second=%q", first.Header().Get("Idempotency-Replayed"), second.Header().Get("Idempotency-Replayed"))
	}

	conflict := perform(`{"phone":"456","message":"different"}`)
	if conflict.Code != http.StatusConflict || calls.Load() != 1 {
		t.Fatalf("conflict=%d calls=%d body=%s", conflict.Code, calls.Load(), conflict.Body.String())
	}
}

func TestUnixSocketIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "djonehub.sock")
	server := &http.Server{}
	listener, err := listenWith("unix:"+path, server)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode=%04o", got)
	}
}

func TestCLISMSListHidesContentUntilExactIDRead(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cli-access.json")
	token := "0123456789abcdef0123456789abcdef"
	writeTestCLIConfig(t, configPath, 0o600, cliAccessConfig{
		Version: 1,
		Enabled: true,
		Token:   token,
		Scopes:  []string{"sms.read"},
	})
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")
	t.Setenv(cliConfigEnvironment, configPath)
	message := receivedSMS{
		Sender: "+8613800138000", Content: "verification code 123456",
		Timestamp: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC), Direction: "in",
	}
	instance := &app{sms: []receivedSMS{message}}
	handler := instance.routes()

	listRequest := httptest.NewRequest(http.MethodGet, "/api/sms?limit=20", nil)
	listRequest.Header.Set("Authorization", "Bearer "+token)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var metadata []map[string]any
	if err := json.Unmarshal(listResponse.Body.Bytes(), &metadata); err != nil || len(metadata) != 1 {
		t.Fatalf("metadata=%v err=%v", metadata, err)
	}
	if _, exposed := metadata[0]["content"]; exposed {
		t.Fatalf("list exposed content: %s", listResponse.Body.String())
	}

	id := smsStableID(message)
	getRequest := httptest.NewRequest(http.MethodGet, "/api/sms?id="+url.QueryEscape(id), nil)
	getRequest.Header.Set("Authorization", "Bearer "+token)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !bytes.Contains(getResponse.Body.Bytes(), []byte("123456")) {
		t.Fatalf("get status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}

	appRequest := httptest.NewRequest(http.MethodGet, "/api/sms", nil)
	appRequest.Header.Set(appTokenHeader, "native-app-test-token-0123456789")
	appResponse := httptest.NewRecorder()
	handler.ServeHTTP(appResponse, appRequest)
	if appResponse.Code != http.StatusOK || !bytes.Contains(appResponse.Body.Bytes(), []byte("123456")) {
		t.Fatalf("app status=%d body=%s", appResponse.Code, appResponse.Body.String())
	}
}

func writeTestCLIConfig(t *testing.T, path string, mode os.FileMode, config cliAccessConfig) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func contextWithPrincipal(request *http.Request, principal requestPrincipal) context.Context {
	return context.WithValue(request.Context(), principalContextKey{}, principal)
}
