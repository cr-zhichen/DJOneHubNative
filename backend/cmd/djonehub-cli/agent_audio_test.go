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

func TestAgentAudioCLIUsesDedicatedAPI(t *testing.T) {
	temp, err := os.MkdirTemp("", "da-")
	if err != nil { t.Fatal(err) }
	defer os.RemoveAll(temp)
	socketPath := filepath.Join(temp, "b.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	token := "0123456789abcdef0123456789abcdef"
	configPath := filepath.Join(temp, "cli-access.json")
	config, _ := json.Marshal(accessConfig{Version: 1, Enabled: true, Token: token, Scopes: []string{"call.agent_audio"}})
	if err := os.WriteFile(configPath, config, 0o600); err != nil { t.Fatal(err) }

	requests := make(chan string, 3)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"voice_agent_available":true,"active":false}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	for _, test := range []struct{ args []string; route string }{
		{[]string{"agent-audio", "status"}, "GET /api/agent/audio/status"},
		{[]string{"agent-audio", "start", "--yes", "--request-id", "audio-start-0001"}, "POST /api/agent/audio/start"},
		{[]string{"agent-audio", "stop", "--yes", "--request-id", "audio-stop-0001"}, "POST /api/agent/audio/stop"},
	} {
		var stdout, stderr bytes.Buffer
		args := append(test.args, "--socket", socketPath, "--config", configPath)
		if code := execute(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, stderr.String())
		}
		if got := <-requests; got != test.route { t.Fatalf("got %q want %q", got, test.route) }
	}
}
