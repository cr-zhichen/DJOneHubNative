package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentAudioTranscriptStoresAndExtractsCodeThenClearsOnStop(t *testing.T) {
	instance := &app{}
	instance.agentAudio.CaptureSessionID = "session-1"
	handler := instance.routes()
	t.Setenv(appTokenEnvironment, "native-app-transcript-test-token")

	request := httptest.NewRequest(http.MethodPut, "/api/agent/audio/transcript", bytes.NewBufferString(`{"text":"验证码是 一 二 三 四","final":true,"session_id":"session-1"}`))
	request.Header.Set(appTokenHeader, "native-app-transcript-test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("transcript status=%d body=%s", response.Code, response.Body.String())
	}
	var state agentAudioState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Transcript != "验证码是 一 二 三 四" || !state.TranscriptFinal || state.VerificationCode != "1234" || state.ASRAvailable {
		t.Fatalf("unexpected transcript state=%+v", state)
	}
	instance.agentAudioMu.Lock()
	instance.agentAudio.CaptureSessionID = "session-1"
	instance.agentAudio.CaptureStartedAt = time.Now().Add(-time.Second)
	instance.agentAudio.FramesReceived = 50
	instance.agentAudio.NonzeroFrames = 40
	instance.agentAudio.BytesReceived = 16000
	instance.agentAudio.PeakSample = 9000
	instance.agentAudio.TranscriptSessionID = "session-1"
	instance.agentAudioMu.Unlock()

	request = httptest.NewRequest(http.MethodPost, "/api/agent/audio/stop", nil)
	response = httptest.NewRecorder()
	instance.agentAudioStop(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stop status=%d body=%s", response.Code, response.Body.String())
	}
	state = agentAudioState{}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Transcript != "" || state.VerificationCode != "" {
		t.Fatalf("transcript not cleared: %+v", state)
	}
	if state.LastCapture == nil || state.LastCapture.SessionID != "session-1" ||
		state.LastCapture.Transcript != "验证码是 一 二 三 四" ||
		state.LastCapture.VerificationCode != "1234" || !state.LastCapture.TranscriptFinal {
		t.Fatalf("completed capture summary missing transcript result: %+v", state.LastCapture)
	}
}

func TestCompletedCaptureRejectsUnboundTranscript(t *testing.T) {
	instance := &app{}
	instance.agentAudio.CaptureSessionID = "session-1"
	instance.agentAudio.Transcript = "验证码 1234"
	instance.agentAudio.TranscriptFinal = true
	instance.agentAudio.VerificationCode = "1234"
	instance.agentAudio.TranscriptSessionID = ""
	completed := instance.completedCaptureLocked("", time.Now())
	if completed == nil {
		t.Fatal("missing completed capture")
	}
	if completed.Transcript != "" || completed.TranscriptFinal || completed.VerificationCode != "" {
		t.Fatalf("unbound transcript survived completion: %+v", completed)
	}
}

func TestAgentAudioTranscriptRejectsMissingAndStaleSession(t *testing.T) {
	instance := &app{}
	instance.agentAudio.CaptureSessionID = "session-2"
	handler := instance.routes()
	t.Setenv(appTokenEnvironment, "native-app-transcript-session-test-token")
	for _, body := range []string{
		`{"text":"验证码 1111","final":true}`,
		`{"text":"验证码 1111","final":true,"session_id":"session-1"}`,
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/agent/audio/transcript", bytes.NewBufferString(body))
		request.Header.Set(appTokenHeader, "native-app-transcript-session-test-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	state := instance.agentAudioSnapshot()
	if state.Transcript != "" || state.VerificationCode != "" || state.ASRAvailable {
		t.Fatalf("stale transcript polluted active session: %+v", state)
	}
}

func TestAgentAudioSnapshotExpiresSensitiveVerificationResult(t *testing.T) {
	instance := &app{}
	instance.agentAudio.LastCapture = &captureMetrics{
		SessionID: "session-1", Transcript: "验证码是 一 二 三 四",
		TranscriptFinal: true, VerificationCode: "1234",
		ResultExpiresAt: time.Now().Add(-time.Second),
	}
	state := instance.agentAudioSnapshot()
	if state.LastCapture == nil || state.LastCapture.Transcript != "" ||
		state.LastCapture.VerificationCode != "" || state.LastCapture.TranscriptFinal || state.ASRAvailable {
		t.Fatalf("expired result remained visible: %+v", state)
	}
	instance.agentAudioMu.RLock()
	defer instance.agentAudioMu.RUnlock()
	if instance.agentAudio.LastCapture.Transcript != "" || instance.agentAudio.LastCapture.VerificationCode != "" {
		t.Fatalf("expired result remained in memory: %+v", instance.agentAudio.LastCapture)
	}
}

func TestAgentAudioSensitiveResultExpiresWithoutSnapshotRead(t *testing.T) {
	instance := &app{}
	instance.agentAudioMu.Lock()
	instance.setLastCaptureLocked(&captureMetrics{
		SessionID: "session-1", Transcript: "验证码 1234", TranscriptFinal: true, VerificationCode: "1234",
	}, 20*time.Millisecond)
	instance.agentAudioMu.Unlock()
	time.Sleep(100 * time.Millisecond)
	instance.agentAudioMu.RLock()
	defer instance.agentAudioMu.RUnlock()
	if instance.agentAudio.LastCapture == nil || instance.agentAudio.LastCapture.Transcript != "" ||
		instance.agentAudio.LastCapture.TranscriptFinal || instance.agentAudio.LastCapture.VerificationCode != "" {
		t.Fatalf("sensitive result was not erased by expiry timer: %+v", instance.agentAudio.LastCapture)
	}
}
