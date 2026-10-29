package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentAudioUnavailableUntilCompiledTransportIsReady(t *testing.T) {
	instance := &app{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/capabilities", nil)
	request = request.WithContext(contextWithPrincipal(request, requestPrincipal{Kind: "app"}))
	instance.agentCapabilities(response, request)
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	safety := payload["safety"].(map[string]any)
	if safety["voice_agent_available"] != false || safety["asr_available"] != false || safety["tts_available"] != false {
		t.Fatalf("unexpected safety: %#v", safety)
	}
}

func TestAgentAudioRegistrationStatusAndControl(t *testing.T) {
	instance := &app{}
	handler := instance.routes()
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")

	registration := bytes.NewBufferString(`{"compiled":true,"transport_ready":true,"module_io_ready":true,"client_connected":true,"asr_compiled":true,"asr_running":true,"socket_path":"/private/tmp/agent.sock"}`)
	request := httptest.NewRequest(http.MethodPut, "/api/agent/audio/transport", registration)
	request.Header.Set(appTokenHeader, "native-app-test-token-0123456789")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", response.Code, response.Body.String())
	}

	configPath := filepath.Join(t.TempDir(), "cli-access.json")
	token := "0123456789abcdef0123456789abcdef"
	writeTestCLIConfig(t, configPath, 0o600, cliAccessConfig{Version: 1, Enabled: true, Token: token, Scopes: []string{"call.agent_audio"}})
	t.Setenv(cliConfigEnvironment, configPath)

	request = httptest.NewRequest(http.MethodPost, "/api/agent/audio/start", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Idempotency-Key", "audio-start-test-0001")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", response.Code, response.Body.String())
	}
	var state agentAudioState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Active || !state.VoiceAgentAvailable || state.SocketPath == "" {
		t.Fatalf("start state=%+v", state)
	}
	if state.Format.Encoding != "pcm_s16le" || state.Format.SampleRate != 8000 || state.Format.Channels != 1 || state.Format.FrameBytes != 320 || state.Format.FrameMilliseconds != 20 {
		t.Fatalf("format=%+v", state.Format)
	}
	if !state.ASRAvailable || !state.ASRCompiled || !state.ASRRunning || state.TTSAvailable {
		t.Fatalf("explicit ASR/TTS state is incorrect: %+v", state)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/agent/audio/stop", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Idempotency-Key", "audio-stop-test-0001")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || state.Active {
		t.Fatalf("stop state=%+v err=%v", state, err)
	}
}

func TestAgentAudioRejectsStartWhenTransportNotReady(t *testing.T) {
	instance := &app{}
	request := httptest.NewRequest(http.MethodPost, "/api/agent/audio/start", nil)
	request = request.WithContext(contextWithPrincipal(request, requestPrincipal{Kind: "app"}))
	response := httptest.NewRecorder()
	instance.agentAudioStart(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentAudioTransportRevocationStopsCapture(t *testing.T) {
	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	instance := &app{}
	instance.agentAudio = agentAudioState{Compiled: true, TransportReady: true, SocketPath: "/private/tmp/agent.sock"}
	instance.agentPCMRunner = func(ctx context.Context, _ string, _ func([]byte)) error {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	}
	instance.startVoiceVerificationAudio(context.Background(), "session-1")
	<-started
	request := httptest.NewRequest(http.MethodPut, "/api/agent/audio/transport", bytes.NewBufferString(`{"compiled":false,"transport_ready":false,"module_io_ready":false,"socket_path":""}`))
	request = request.WithContext(contextWithPrincipal(request, requestPrincipal{Kind: "app"}))
	response := httptest.NewRecorder()
	instance.agentAudioTransport(response, request)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("capture remained active after transport revocation")
	}
}
func TestAgentAudioRoutesRequireDedicatedScope(t *testing.T) {
	for _, route := range []string{"GET /api/agent/audio/status", "POST /api/agent/audio/start", "POST /api/agent/audio/stop"} {
		parts := bytes.SplitN([]byte(route), []byte(" "), 2)
		scope, exposed := requiredCLIScope(string(parts[0]), string(parts[1]))
		if !exposed || scope != "call.agent_audio" {
			t.Fatalf("route %s scope=%q exposed=%v", route, scope, exposed)
		}
	}
	if _, exposed := requiredCLIScope(http.MethodPut, "/api/agent/audio/transport"); exposed {
		t.Fatal("native transport registration exposed to CLI")
	}
}

func TestAgentAudioRequiresConnectedClientBeforeAdvertisingAvailability(t *testing.T) {
	instance := &app{agentAudio: agentAudioState{
		Compiled: true, TransportReady: true, ModuleIOReady: true,
		SocketPath: "/private/tmp/agent.sock", ClientConnected: false,
	}}
	state := instance.agentAudioSnapshot()
	if state.VoiceAgentAvailable {
		t.Fatalf("disconnected client must not be available: %+v", state)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/agent/audio/start", nil)
	request = request.WithContext(contextWithPrincipal(request, requestPrincipal{Kind: "app"}))
	response := httptest.NewRecorder()
	instance.agentAudioStart(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("start status=%d body=%s", response.Code, response.Body.String())
	}
}
