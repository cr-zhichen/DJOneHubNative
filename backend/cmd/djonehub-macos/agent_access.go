package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	appTokenEnvironment  = "DJONEHUB_APP_TOKEN"
	cliConfigEnvironment = "DJONEHUB_CLI_CONFIG"
	appTokenHeader       = "X-DJOneHub-App-Token"
)

type cliAccessConfig struct {
	Version   int      `json:"version"`
	Enabled   bool     `json:"enabled"`
	Token     string   `json:"token"`
	Scopes    []string `json:"scopes"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

type requestPrincipal struct {
	Kind   string
	ID     string
	Scopes map[string]struct{}
}

func (p requestPrincipal) grants(scope string) bool {
	if scope == "" || p.Kind == "app" || p.Kind == "legacy" {
		return true
	}
	_, ok := p.Scopes[scope]
	return ok
}

type principalContextKey struct{}

func principalFromRequest(r *http.Request) requestPrincipal {
	principal, _ := r.Context().Value(principalContextKey{}).(requestPrincipal)
	return principal
}

func requestIsCLI(r *http.Request) bool {
	return principalFromRequest(r).Kind == "cli"
}

func (a *app) agentAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, status, code, message := authenticateLocalRequest(r)
		if status != 0 {
			writeCodedError(w, status, code, message, false)
			return
		}

		required, exposed := requiredCLIScope(r.Method, r.URL.Path)
		if principal.Kind == "cli" && !exposed {
			writeCodedError(w, http.StatusForbidden, "cli_route_unavailable",
				"此后端路由没有向 CLI 开放", false)
			return
		}
		if !principal.grants(required) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":          "当前 AI 权限不允许此操作，请在 DJOneHub 的“AI 与 CLI”页面授权",
				"code":           "permission_denied",
				"recoverable":    false,
				"required_scope": required,
			})
			return
		}

		if principal.Kind == "cli" && r.Method != http.MethodGet {
			log.Printf("CLI request actor=%s method=%s path=%s request_id=%s",
				principal.ID, r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticateLocalRequest(r *http.Request) (requestPrincipal, int, string, string) {
	appToken := strings.TrimSpace(os.Getenv(appTokenEnvironment))
	if appToken == "" {
		// Standalone development and existing unit tests remain usable. The native
		// app always supplies a random token in production.
		return requestPrincipal{Kind: "legacy", ID: "legacy"}, 0, "", ""
	}

	if tokenMatches(r.Header.Get(appTokenHeader), appToken) {
		return requestPrincipal{Kind: "app", ID: "native-app"}, 0, "", ""
	}

	provided := bearerToken(r.Header.Get("Authorization"))
	if provided == "" {
		return requestPrincipal{}, http.StatusUnauthorized, "authentication_required", "需要 DJOneHub App 或已授权 CLI 的本机凭证"
	}
	config, err := loadCLIAccessConfig(cliAccessConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return requestPrincipal{}, http.StatusUnauthorized, "cli_access_not_configured", "尚未在 DJOneHub 中启用 AI 访问"
		}
		return requestPrincipal{}, http.StatusUnauthorized, "cli_access_invalid", "AI 权限配置无效，请在 DJOneHub 中重新保存"
	}
	if !config.Enabled {
		return requestPrincipal{}, http.StatusUnauthorized, "cli_access_disabled", "DJOneHub 中的 AI 访问已关闭"
	}
	if !tokenMatches(provided, config.Token) {
		return requestPrincipal{}, http.StatusUnauthorized, "invalid_token", "CLI 凭证已失效，请重新从 DJOneHub 启用 AI 访问"
	}

	scopes := make(map[string]struct{}, len(config.Scopes))
	for _, scope := range config.Scopes {
		scopes[scope] = struct{}{}
	}
	return requestPrincipal{Kind: "cli", ID: shortTokenID(config.Token), Scopes: scopes}, 0, "", ""
}

func tokenMatches(provided, expected string) bool {
	if provided == "" || expected == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func shortTokenID(token string) string {
	if len(token) <= 8 {
		return "cli"
	}
	return "cli-" + token[len(token)-8:]
}

func cliAccessConfigPath() string {
	if path := strings.TrimSpace(os.Getenv(cliConfigEnvironment)); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "DJOneHubNative", "cli-access.json")
}

func loadCLIAccessConfig(path string) (cliAccessConfig, error) {
	var config cliAccessConfig
	if path == "" {
		return config, errors.New("CLI access path is unavailable")
	}
	info, err := os.Stat(path)
	if err != nil {
		return config, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return config, fmt.Errorf("CLI access config must use mode 0600, got %04o", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	if config.Version != 1 || len(config.Token) < 32 {
		return cliAccessConfig{}, errors.New("unsupported or incomplete CLI access config")
	}
	return config, nil
}

var cliScopeByRoute = map[string]string{
	"GET /api/agent/capabilities":          "",
	"GET /api/health":                      "device.read",
	"GET /api/status":                      "device.read",
	"GET /api/sms":                         "sms.read",
	"GET /api/sms/status":                  "sms.read",
	"GET /api/sms/storage":                 "sms.read",
	"GET /api/sms/adopt":                   "sms.read",
	"POST /api/sms/refresh":                "sms.read",
	"POST /api/sms/send":                   "sms.send",
	"POST /api/sms/delete":                 "sms.manage",
	"POST /api/sms/delete-sender":          "sms.manage",
	"POST /api/sms/adopt":                  "sms.manage",
	"POST /api/sms/clear":                  "sms.manage",
	"GET /api/network":                     "network.read",
	"GET /api/network/traffic":             "network.read",
	"POST /api/network/check-4g":           "network.read",
	"GET /api/network/services":            "network.read",
	"GET /api/routing":                     "network.read",
	"PUT /api/network/services-order":      "network.control",
	"PUT /api/routing/config":              "network.control",
	"POST /api/routing/preflight":          "network.control",
	"POST /api/routing/check-system-socks": "network.control",
	"POST /api/routing/start":              "network.control",
	"POST /api/routing/stop":               "network.control",
	"POST /api/routing/uninstall":          "network.admin",
	"GET /api/call/status":                 "call.read",
	"GET /api/calls":                       "call.read",
	"POST /api/calls/clear":                "call.manage",
	"POST /api/calls/delete":               "call.manage",
	"POST /api/call/dial":                  "call.dial",
	"POST /api/call/answer":                "call.answer",
	"POST /api/call/hangup":                "call.hangup",
	"POST /api/call/audio/start":           "call.audio",
	"POST /api/call/audio/stop":            "call.audio",
	"GET /api/voice/enabled":               "call.read",
	"POST /api/voice/enable":               "call.audio",
	"GET /api/esim":                        "esim.read",
	"GET /api/esim/health":                 "esim.read",
	"GET /api/esim/operation":              "esim.read",
	"GET /api/esim/notes":                  "esim.read",
	"GET /api/esim/module-notes":           "esim.read",
	"POST /api/esim/switch":                "esim.switch",
	"PUT /api/esim/notes":                  "esim.manage",
	"PUT /api/esim/module-notes":           "esim.manage",
	"POST /api/esim/phonebook/probe":       "esim.manage",
	"PATCH /api/esim/profile":              "esim.manage",
	"DELETE /api/esim/profile":             "esim.manage",
	"POST /api/esim/download":              "esim.manage",
	"POST /api/at":                         "module.admin",
	"GET /api/call-mode/status":            "module.admin",
	"POST /api/call-mode/enable":           "module.admin",
	"POST /api/call-mode/download":         "module.admin",
	"POST /api/call-mode/retry":            "module.admin",
	"GET /api/call-mode/backups":           "module.admin",
	"GET /api/call-mode/backups/export":    "module.admin",
	"POST /api/call-mode/backups/import":   "module.admin",
	"POST /api/call-mode/backups/delete":   "module.admin",
	"POST /api/call-mode/restore":          "module.admin",
	"POST /api/network/usbnet":             "module.admin",
	"POST /api/network/reboot-module":      "module.admin",
}

func requiredCLIScope(method, path string) (string, bool) {
	scope, ok := cliScopeByRoute[method+" "+path]
	return scope, ok
}

type agentCapability struct {
	Name          string `json:"name"`
	RequiredScope string `json:"required_scope"`
	Granted       bool   `json:"granted"`
	Mutating      bool   `json:"mutating"`
	Confirmation  bool   `json:"confirmation_required"`
}

func (a *app) agentCapabilities(w http.ResponseWriter, r *http.Request) {
	principal := principalFromRequest(r)
	definitions := []agentCapability{
		{Name: "device.status", RequiredScope: "device.read"},
		{Name: "network.status", RequiredScope: "network.read"},
		{Name: "network.traffic", RequiredScope: "network.read"},
		{Name: "sms.list", RequiredScope: "sms.read"},
		{Name: "sms.get", RequiredScope: "sms.read"},
		{Name: "sms.send", RequiredScope: "sms.send", Mutating: true, Confirmation: true},
		{Name: "call.status", RequiredScope: "call.read"},
		{Name: "call.history", RequiredScope: "call.read"},
		{Name: "call.events", RequiredScope: "call.read"},
		{Name: "call.dial", RequiredScope: "call.dial", Mutating: true, Confirmation: true},
		{Name: "call.answer", RequiredScope: "call.answer", Mutating: true, Confirmation: true},
		{Name: "call.hangup", RequiredScope: "call.hangup", Mutating: true},
		{Name: "esim.list", RequiredScope: "esim.read"},
		{Name: "esim.operation", RequiredScope: "esim.read"},
		{Name: "esim.switch", RequiredScope: "esim.switch", Mutating: true, Confirmation: true},
	}
	for index := range definitions {
		definitions[index].Granted = principal.grants(definitions[index].RequiredScope)
	}
	scopes := make([]string, 0, len(principal.Scopes))
	for scope := range principal.Scopes {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	writeJSON(w, http.StatusOK, map[string]any{
		"api_version":  "v1",
		"actor":        principal.Kind,
		"scopes":       scopes,
		"capabilities": definitions,
		"safety": map[string]any{
			"idempotency_required":  []string{"sms.send", "call.dial", "call.answer", "call.hangup", "esim.switch"},
			"raw_at_available":      false,
			"voice_agent_available": false,
		},
	})
}
