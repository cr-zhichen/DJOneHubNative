package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type accessConfig struct {
	Version   int      `json:"version"`
	Enabled   bool     `json:"enabled"`
	Token     string   `json:"token"`
	Scopes    []string `json:"scopes"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

type apiClient struct {
	httpClient *http.Client
	token      string
}

type apiResponse struct {
	Body     json.RawMessage
	Status   int
	Replayed bool
}

type serverErrorPayload struct {
	Error         string `json:"error"`
	Code          string `json:"code"`
	Recoverable   bool   `json:"recoverable"`
	RequiredScope string `json:"required_scope"`
}

func newAPIClient(options globalOptions) (*apiClient, *commandError) {
	config, err := readAccessConfig(options.ConfigPath)
	if err != nil {
		code := "cli_access_not_configured"
		message := "AI access is not configured; enable it in DJOneHub’s AI 与 CLI page"
		if !errors.Is(err, os.ErrNotExist) {
			code = "cli_access_invalid"
			message = err.Error()
		}
		return nil, &commandError{Code: code, Message: message, ExitCode: 3}
	}
	if !config.Enabled {
		return nil, &commandError{
			Code: "cli_access_disabled", Message: "AI access is disabled in DJOneHub", ExitCode: 3,
		}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", options.SocketPath)
		},
		DisableKeepAlives: false,
	}
	return &apiClient{
		httpClient: &http.Client{Transport: transport, Timeout: options.Timeout},
		token:      config.Token,
	}, nil
}

func (c *apiClient) request(ctx context.Context, method, path string, body any, requestID string) (apiResponse, *commandError) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return apiResponse{}, &commandError{Code: "encoding_failed", Message: err.Error(), ExitCode: 1, RequestID: requestID}
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://djonehub.local"+path, bytes.NewReader(encoded))
	if err != nil {
		return apiResponse{}, &commandError{Code: "request_failed", Message: err.Error(), ExitCode: 1, RequestID: requestID}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("User-Agent", "djonehub-cli/"+version)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		request.Header.Set("Idempotency-Key", requestID)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return apiResponse{}, &commandError{
			Code: "backend_unavailable", Message: "cannot reach DJOneHub; make sure the app is running: " + err.Error(),
			Recoverable: true, ExitCode: 4, RequestID: requestID,
		}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if err != nil {
		return apiResponse{}, &commandError{Code: "response_read_failed", Message: err.Error(), Recoverable: true, ExitCode: 4, RequestID: requestID}
	}
	if len(data) > 8<<20 {
		return apiResponse{}, &commandError{Code: "response_too_large", Message: "backend response exceeded 8 MiB", ExitCode: 4, RequestID: requestID}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload := serverErrorPayload{Code: "http_" + fmt.Sprint(response.StatusCode), Error: http.StatusText(response.StatusCode)}
		_ = json.Unmarshal(data, &payload)
		details := map[string]any{"status": response.StatusCode}
		if payload.RequiredScope != "" {
			details["required_scope"] = payload.RequiredScope
		}
		return apiResponse{}, &commandError{
			Code: payload.Code, Message: payload.Error, Recoverable: payload.Recoverable,
			Details: details, ExitCode: 5, RequestID: requestID,
		}
	}
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	if !json.Valid(data) {
		return apiResponse{}, &commandError{Code: "invalid_backend_response", Message: "backend returned invalid JSON", ExitCode: 4, RequestID: requestID}
	}
	return apiResponse{
		Body: json.RawMessage(data), Status: response.StatusCode,
		Replayed: response.Header.Get("Idempotency-Replayed") == "true",
	}, nil
}

func readAccessConfig(path string) (accessConfig, error) {
	var config accessConfig
	if strings.TrimSpace(path) == "" {
		return config, errors.New("CLI access config path is empty")
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
		return config, fmt.Errorf("decode CLI access config: %w", err)
	}
	if config.Version != 1 || len(config.Token) < 32 {
		return accessConfig{}, errors.New("CLI access config is incomplete or unsupported")
	}
	return config, nil
}

func defaultSupportDirectory() string {
	if value := strings.TrimSpace(os.Getenv("DJONEHUB_SUPPORT_DIR")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "DJOneHubNative")
}

func defaultSocketPath() string {
	if value := strings.TrimSpace(os.Getenv("DJONEHUB_SOCKET")); value != "" {
		return value
	}
	return filepath.Join(defaultSupportDirectory(), "djonehub.sock")
}

func defaultConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("DJONEHUB_CLI_CONFIG")); value != "" {
		return value
	}
	return filepath.Join(defaultSupportDirectory(), "cli-access.json")
}

func queryPath(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}
