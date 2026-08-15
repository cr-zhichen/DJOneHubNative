package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSMSDryRunIsStructuredAndDoesNotNeedBackend(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"sms", "send", "--to", "+8613800138000", "--message", "hello", "--dry-run", "--request-id", "sms-test-0001",
	}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", exitCode, stderr.String())
	}
	var envelope outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Command != "sms.send" || envelope.RequestID != "sms-test-0001" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestMutationRequiresExplicitConfirmation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"sms", "send", "--to", "+8613800138000", "--message", "hello", "--request-id", "sms-test-0002",
	}, &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	var envelope outputEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "confirmation_required" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestCapabilitiesUsesUnixSocketAndBearerToken(t *testing.T) {
	temp, err := os.MkdirTemp("/tmp", "djcli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temp)
	socketPath := filepath.Join(temp, "backend.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	configPath := filepath.Join(temp, "cli-access.json")
	token := "0123456789abcdef0123456789abcdef"
	configData, _ := json.Marshal(accessConfig{Version: 1, Enabled: true, Token: token, Scopes: []string{"device.read"}})
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_version":"v1","actor":"cli"}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	var stdout, stderr bytes.Buffer
	exitCode := execute(context.Background(), []string{
		"capabilities", "--socket", socketPath, "--config", configPath,
	}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", exitCode, stderr.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["ok"] != true || envelope["command"] != "capabilities" {
		t.Fatalf("envelope=%v", envelope)
	}
}
