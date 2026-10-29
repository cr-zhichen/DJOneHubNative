package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const verificationResultRetention = 5 * time.Minute

type captureMetrics struct {
	SessionID        string    `json:"session_id"`
	FramesReceived   uint64    `json:"frames_received"`
	NonzeroFrames    uint64    `json:"nonzero_frames"`
	BytesReceived    uint64    `json:"bytes_received"`
	PeakSample       int       `json:"peak_sample"`
	StartedAt        time.Time `json:"started_at"`
	EndedAt          time.Time `json:"ended_at"`
	Error            string    `json:"error,omitempty"`
	Transcript       string    `json:"transcript,omitempty"`
	TranscriptFinal  bool      `json:"transcript_final"`
	VerificationCode string    `json:"verification_code,omitempty"`
	ResultExpiresAt  time.Time `json:"result_expires_at,omitempty"`
}

type agentAudioFormat struct {
	Encoding          string `json:"encoding"`
	SampleRate        int    `json:"sample_rate"`
	Channels          int    `json:"channels"`
	FrameMilliseconds int    `json:"frame_ms"`
	FrameBytes        int    `json:"frame_bytes"`
}

type agentAudioState struct {
	Compiled              bool             `json:"compiled"`
	TransportReady        bool             `json:"transport_ready"`
	ModuleIOReady         bool             `json:"module_io_ready"`
	PCMTransportAvailable bool             `json:"pcm_transport_available"`
	VoiceAgentAvailable   bool             `json:"voice_agent_available"`
	Active                bool             `json:"active"`
	SocketPath            string           `json:"socket_path,omitempty"`
	ClientConnected       bool             `json:"client_connected"`
	CaptureActive         bool             `json:"capture_active"`
	CaptureSessionID      string           `json:"capture_session_id,omitempty"`
	CaptureStartedAt      time.Time        `json:"-"`
	FramesReceived        uint64           `json:"frames_received"`
	NonzeroFrames         uint64           `json:"nonzero_frames"`
	BytesReceived         uint64           `json:"bytes_received"`
	PeakSample            int              `json:"peak_sample"`
	CaptureError          string           `json:"capture_error,omitempty"`
	LastCapture           *captureMetrics  `json:"last_capture,omitempty"`
	ASRAvailable          bool             `json:"asr_available"`
	ASRCompiled           bool             `json:"asr_compiled"`
	ASRRunning            bool             `json:"asr_running"`
	ASRError              string           `json:"asr_error,omitempty"`
	TTSAvailable          bool             `json:"tts_available"`
	TTSCheckedAt          time.Time        `json:"-"`
	Format                agentAudioFormat `json:"format"`
	Error                 string           `json:"error,omitempty"`
	Transcript            string           `json:"transcript,omitempty"`
	TranscriptFinal       bool             `json:"transcript_final"`
	TranscriptSessionID   string           `json:"transcript_session_id,omitempty"`
	TranscriptUpdatedAt   time.Time        `json:"transcript_updated_at,omitempty"`
	VerificationCode      string           `json:"verification_code,omitempty"`
}

type agentAudioTransportUpdate struct {
	Compiled        bool   `json:"compiled"`
	TransportReady  bool   `json:"transport_ready"`
	ModuleIOReady   bool   `json:"module_io_ready"`
	SocketPath      string `json:"socket_path"`
	ClientConnected bool   `json:"client_connected"`
	ASRCompiled     bool   `json:"asr_compiled"`
	ASRRunning      bool   `json:"asr_running"`
	ASRError        string `json:"asr_error"`
	Error           string `json:"error"`
}

type agentAudioTranscriptUpdate struct {
	Text      string `json:"text"`
	Final     bool   `json:"final"`
	SessionID string `json:"session_id,omitempty"`
}

func fixedAgentAudioFormat() agentAudioFormat {
	return agentAudioFormat{
		Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1,
		FrameMilliseconds: 20, FrameBytes: 320,
	}
}

func clearCaptureSensitiveResult(result *captureMetrics) {
	if result == nil {
		return
	}
	result.Transcript = ""
	result.TranscriptFinal = false
	result.VerificationCode = ""
	result.ResultExpiresAt = time.Time{}
}

// setLastCaptureLocked replaces the completed summary and independently erases
// sensitive transcript data at expiry even when no status endpoint is read.
// agentAudioMu must be held by the caller.
func (a *app) setLastCaptureLocked(result *captureMetrics, retention time.Duration) {
	if a.agentAudioResultExpiryTimer != nil {
		a.agentAudioResultExpiryTimer.Stop()
		a.agentAudioResultExpiryTimer = nil
	}
	a.agentAudio.LastCapture = result
	if result == nil || (strings.TrimSpace(result.Transcript) == "" && strings.TrimSpace(result.VerificationCode) == "") {
		return
	}
	if retention < 0 {
		retention = 0
	}
	expiresAt := time.Now().Add(retention)
	result.ResultExpiresAt = expiresAt
	a.agentAudioResultExpiryTimer = time.AfterFunc(retention, func() {
		a.agentAudioMu.Lock()
		defer a.agentAudioMu.Unlock()
		if a.agentAudio.LastCapture == result && result.ResultExpiresAt.Equal(expiresAt) {
			clearCaptureSensitiveResult(result)
			a.agentAudioResultExpiryTimer = nil
		}
	})
}

func (a *app) agentAudioSnapshot() agentAudioState {
	a.agentAudioMu.Lock()
	if last := a.agentAudio.LastCapture; last != nil && !last.ResultExpiresAt.IsZero() && time.Now().After(last.ResultExpiresAt) {
		clearCaptureSensitiveResult(last)
	}
	state := a.agentAudio
	if state.LastCapture != nil {
		last := *state.LastCapture
		state.LastCapture = &last
	}
	a.agentAudioMu.Unlock()
	state.Format = fixedAgentAudioFormat()
	state.PCMTransportAvailable = state.Compiled && state.TransportReady && strings.TrimSpace(state.SocketPath) != ""
	state.VoiceAgentAvailable = state.PCMTransportAvailable && state.ClientConnected && state.ModuleIOReady
	state.ASRAvailable = state.ASRCompiled && state.ASRRunning && strings.TrimSpace(state.ASRError) == ""
	if state.TTSAvailable && (state.TTSCheckedAt.IsZero() || time.Since(state.TTSCheckedAt) > ttsAvailabilityTTL) {
		state.TTSAvailable = false
	}
	if !state.VoiceAgentAvailable {
		state.Active = false
	}
	return state
}

func (a *app) agentAudioTransport(w http.ResponseWriter, r *http.Request) {
	if principalFromRequest(r).Kind == "cli" {
		writeCodedError(w, http.StatusForbidden, "cli_route_unavailable", "该路由仅供原生应用注册传输状态", false)
		return
	}
	defer r.Body.Close()
	var update agentAudioTransportUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_agent_audio_state", "Agent 音频传输状态无效", false)
		return
	}
	update.SocketPath = strings.TrimSpace(update.SocketPath)
	if update.TransportReady && (!update.Compiled || update.SocketPath == "" || !filepath.IsAbs(update.SocketPath)) {
		writeCodedError(w, http.StatusBadRequest, "invalid_agent_audio_transport", "就绪的 Agent 音频传输必须由已编译功能提供绝对 socket 路径", false)
		return
	}
	a.agentAudioMu.Lock()
	previousSocket := a.agentAudio.SocketPath
	previousTransportReady := a.agentAudio.TransportReady
	a.agentAudio.Compiled = update.Compiled
	a.agentAudio.TransportReady = update.TransportReady
	a.agentAudio.ModuleIOReady = update.ModuleIOReady
	a.agentAudio.SocketPath = update.SocketPath
	a.agentAudio.ClientConnected = update.ClientConnected
	a.agentAudio.ASRCompiled = update.ASRCompiled
	a.agentAudio.ASRRunning = update.ASRRunning
	a.agentAudio.ASRError = strings.TrimSpace(update.ASRError)
	a.agentAudio.Active = a.agentAudio.Active && update.Compiled && update.TransportReady && update.ClientConnected && update.ModuleIOReady
	a.agentAudio.Error = strings.TrimSpace(update.Error)
	a.agentAudioMu.Unlock()
	if previousTransportReady && (!update.Compiled || !update.TransportReady || previousSocket != update.SocketPath) {
		a.stopVoiceVerificationAudio()
	}
	writeJSON(w, http.StatusOK, a.agentAudioSnapshot())
}

func (a *app) agentAudioStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.agentAudioSnapshot())
}

func (a *app) agentAudioTranscript(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var update agentAudioTranscriptUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		writeCodedError(w, http.StatusBadRequest, "invalid_agent_audio_transcript", "原生转录结果无效", false)
		return
	}
	update.Text = strings.TrimSpace(update.Text)
	update.SessionID = strings.TrimSpace(update.SessionID)
	if len(update.Text) > 4000 {
		writeCodedError(w, http.StatusBadRequest, "agent_audio_transcript_too_large", "原生转录结果过长", false)
		return
	}
	a.agentAudioMu.Lock()
	if update.SessionID == "" || update.SessionID != a.agentAudio.CaptureSessionID {
		a.agentAudioMu.Unlock()
		writeCodedError(w, http.StatusConflict, "stale_agent_audio_transcript", "转录结果不属于当前采集会话", false)
		return
	}
	a.agentAudio.Transcript = update.Text
	a.agentAudio.TranscriptFinal = update.Final
	a.agentAudio.TranscriptSessionID = update.SessionID
	a.agentAudio.VerificationCode = extractVerificationDigits(update.Text)
	a.agentAudio.TranscriptUpdatedAt = time.Now()
	a.agentAudioMu.Unlock()
	writeJSON(w, http.StatusOK, a.agentAudioSnapshot())
}

func (a *app) agentAudioStart(w http.ResponseWriter, _ *http.Request) {
	state := a.agentAudioSnapshot()
	if !state.VoiceAgentAvailable {
		writeCodedError(w, http.StatusConflict, "agent_audio_transport_unavailable", "Agent PCM 传输尚未就绪", true)
		return
	}
	a.agentAudioMu.Lock()
	a.agentAudio.Active = true
	a.agentAudioMu.Unlock()
	writeJSON(w, http.StatusOK, a.agentAudioSnapshot())
}

func (a *app) agentAudioStop(w http.ResponseWriter, _ *http.Request) {
	a.stopVoiceVerificationAudio()
	a.updateVoiceVerification(func(window *voiceVerificationWindow) {
		window.AudioAuthorized = false
	})
	a.agentAudioMu.Lock()
	a.agentAudio.Active = false
	a.agentAudio.Transcript = ""
	a.agentAudio.TranscriptFinal = false
	a.agentAudio.TranscriptSessionID = ""
	a.agentAudio.VerificationCode = ""
	a.agentAudio.TranscriptUpdatedAt = time.Time{}
	a.agentAudioMu.Unlock()
	writeJSON(w, http.StatusOK, a.agentAudioSnapshot())
}
