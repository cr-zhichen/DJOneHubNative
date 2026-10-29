package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMiMoTTSProviderSynthesizesAndConvertsWAVToAgentPCM(t *testing.T) {
	wav := testPCM16WAV(24000, 1, 480, func(index int) int16 {
		if index%24 < 12 {
			return 12000
		}
		return -12000
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("api-key") != "test-secret" {
			t.Fatalf("missing API key header")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "mimo-v2.5-tts" {
			t.Fatalf("model=%v", payload["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(wav)},
			}}},
		})
	}))
	defer server.Close()

	provider := newMiMoTTSProvider(server.URL+"/v1/chat/completions", "test-secret", server.Client())
	result, err := provider.Synthesize(context.Background(), ttsSynthesisRequest{
		SessionID: "call-1", Generation: 7, Text: "你好", Voice: "冰糖",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Encoding != "pcm_s16le" || result.SampleRate != 8000 || result.Channels != 1 {
		t.Fatalf("unexpected format: %+v", result)
	}
	if len(result.PCM) != 320 {
		t.Fatalf("expected one 20ms Agent frame, got %d bytes", len(result.PCM))
	}
	if allZeroPCM(result.PCM) {
		t.Fatal("converted PCM is silent")
	}
}

func TestTTSControllerRejectsLateSynthesisAfterSessionStop(t *testing.T) {
	provider := &blockingTTSProvider{started: make(chan struct{}), release: make(chan struct{})}
	controller := newTTSController(provider, 4)
	generation := controller.StartSession("call-1")
	done := make(chan error, 1)
	go func() {
		done <- controller.Synthesize(context.Background(), ttsSynthesisRequest{
			SessionID: "call-1", Generation: generation, Text: "测试", Voice: "冰糖",
		})
	}()
	<-provider.started
	controller.StopSession("call-1", generation)
	close(provider.release)
	if err := <-done; err == nil {
		t.Fatal("late synthesis must be rejected")
	}
	if frame := controller.PopFrame("call-1", generation); frame != nil {
		t.Fatal("late provider output reached playback queue")
	}
}

func TestTTSControllerRejectsAudioLargerThanQueue(t *testing.T) {
	pcm := make([]byte, agentPCMFrameBytes*3)
	for index := range pcm {
		pcm[index] = byte(index%251 + 1)
	}
	controller := newTTSController(staticTTSProvider{result: ttsSynthesisResult{
		Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1, PCM: pcm,
	}}, 2)
	generation := controller.StartSession("call-1")
	if err := controller.Synthesize(context.Background(), ttsSynthesisRequest{
		SessionID: "call-1", Generation: generation, Text: "测试", Voice: "冰糖",
	}); err == nil {
		t.Fatal("oversized synthesis must not be silently truncated")
	}
	if got := controller.QueuedFrames("call-1", generation); got != 0 {
		t.Fatalf("rejected audio reached queue: %d frames", got)
	}
	if frame := controller.PopFrame("call-1", generation+1); frame != nil {
		t.Fatal("wrong generation accessed playback queue")
	}
}

type staticTTSProvider struct{ result ttsSynthesisResult }

func (p staticTTSProvider) Synthesize(context.Context, ttsSynthesisRequest) (ttsSynthesisResult, error) {
	return p.result, nil
}

type ignoringCancelTTSProvider struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (p *ignoringCancelTTSProvider) Synthesize(context.Context, ttsSynthesisRequest) (ttsSynthesisResult, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	pcm := make([]byte, agentPCMFrameBytes)
	pcm[0] = 1
	return ttsSynthesisResult{Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1, PCM: pcm}, nil
}

type blockingTTSProvider struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (p *blockingTTSProvider) Synthesize(ctx context.Context, _ ttsSynthesisRequest) (ttsSynthesisResult, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-ctx.Done():
		return ttsSynthesisResult{}, ctx.Err()
	case <-p.release:
		pcm := make([]byte, agentPCMFrameBytes)
		pcm[0] = 1
		return ttsSynthesisResult{Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1, PCM: pcm}, nil
	}
}

func testPCM16WAV(sampleRate, channels, frames int, sample func(int) int16) []byte {
	dataSize := frames * channels * 2
	wav := make([]byte, 44+dataSize)
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(wav[32:34], uint16(channels*2))
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(dataSize))
	offset := 44
	for frame := 0; frame < frames; frame++ {
		for channel := 0; channel < channels; channel++ {
			binary.LittleEndian.PutUint16(wav[offset:offset+2], uint16(sample(frame)))
			offset += 2
		}
	}
	return wav
}

func allZeroPCM(pcm []byte) bool {
	for _, value := range pcm {
		if value != 0 {
			return false
		}
	}
	return true
}

func TestTTSControllerCancellationIsPrompt(t *testing.T) {
	provider := &blockingTTSProvider{started: make(chan struct{}), release: make(chan struct{})}
	controller := newTTSController(provider, 2)
	generation := controller.StartSession("call-1")
	done := make(chan error, 1)
	go func() {
		done <- controller.Synthesize(context.Background(), ttsSynthesisRequest{
			SessionID: "call-1", Generation: generation, Text: "测试", Voice: "冰糖",
		})
	}()
	<-provider.started
	controller.StopSession("call-1", generation)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("provider synthesis did not cancel promptly")
	}
}

func TestTTSControllerRejectsResultWhenParentCancelledAndProviderIgnoresContext(t *testing.T) {
	provider := &ignoringCancelTTSProvider{started: make(chan struct{}), release: make(chan struct{})}
	controller := newTTSController(provider, 2)
	generation := controller.StartSession("call-1")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- controller.Synthesize(ctx, ttsSynthesisRequest{
			SessionID: "call-1", Generation: generation, Text: "测试", Voice: "冰糖",
		})
	}()
	<-provider.started
	cancel()
	close(provider.release)
	if err := <-done; err == nil {
		t.Fatal("cancelled request accepted a late successful provider result")
	}
	if frame := controller.PopFrame("call-1", generation); frame != nil {
		t.Fatal("cancelled result reached playback queue")
	}
}

func TestMiMoTTSProviderDoesNotFollowRedirectsWithAPIKey(t *testing.T) {
	var redirectTargetCalled atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalled.Store(true)
	}))
	defer redirectTarget.Close()
	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirectSource.Close()

	provider := newMiMoTTSProvider(redirectSource.URL, "test-secret", redirectSource.Client())
	_, err := provider.Synthesize(context.Background(), ttsSynthesisRequest{
		SessionID: "probe", Generation: 1, Text: "测试", Voice: "冰糖",
	})
	if err == nil {
		t.Fatal("redirected TTS request unexpectedly succeeded")
	}
	if redirectTargetCalled.Load() {
		t.Fatal("API key bearing request followed a redirect")
	}
}

func TestValidateTTSEndpointRejectsNonLoopbackHTTP(t *testing.T) {
	if err := validateTTSEndpoint("http://example.com/v1/chat/completions"); err == nil {
		t.Fatal("non-loopback HTTP TTS endpoint was accepted")
	}
}

func TestParsePCM16WAVAcceptsOddChunkWithPadding(t *testing.T) {
	wav := testPCM16WAV(24000, 1, 480, func(int) int16 { return 1000 })
	wav = append(wav, []byte{'J', 'U', 'N', 'K', 1, 0, 0, 0, 0x7f, 0x00}...)
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	if _, err := parsePCM16WAV(wav); err != nil {
		t.Fatalf("valid odd-sized padded chunk was rejected: %v", err)
	}
}

func TestParsePCM16WAVRejectsMissingOddChunkPadding(t *testing.T) {
	wav := testPCM16WAV(24000, 1, 480, func(int) int16 { return 1000 })
	wav = append(wav, []byte{'J', 'U', 'N', 'K', 1, 0, 0, 0, 0x7f}...)
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	if _, err := parsePCM16WAV(wav); err == nil {
		t.Fatal("WAV with missing odd-chunk padding was accepted")
	}
}

func TestParsePCM16WAVRejectsInconsistentRIFFSizeAndPartialHeader(t *testing.T) {
	valid := testPCM16WAV(24000, 1, 480, func(int) int16 { return 1000 })
	wrongSize := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(wrongSize[4:8], uint32(len(wrongSize)-9))
	if _, err := parsePCM16WAV(wrongSize); err == nil {
		t.Fatal("WAV with inconsistent RIFF size was accepted")
	}

	partial := append(append([]byte(nil), valid...), 'J', 'U', 'N')
	binary.LittleEndian.PutUint32(partial[4:8], uint32(len(partial)-8))
	if _, err := parsePCM16WAV(partial); err == nil {
		t.Fatal("WAV with trailing partial chunk header was accepted")
	}
}

func TestAgentTTSAvailabilityExpires(t *testing.T) {
	instance := &app{agentAudio: agentAudioState{TTSAvailable: true, TTSCheckedAt: time.Now().Add(-2 * ttsAvailabilityTTL)}}
	if state := instance.agentAudioSnapshot(); state.TTSAvailable {
		t.Fatal("stale TTS probe remained available")
	}
}

func TestAgentTTSProbeRejectsLegacyPrincipal(t *testing.T) {
	pcm := make([]byte, agentPCMFrameBytes)
	pcm[0] = 1
	instance := &app{ttsProvider: staticTTSProvider{result: ttsSynthesisResult{
		Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1, PCM: pcm,
	}}}
	request := httptest.NewRequest(http.MethodPost, "/api/agent/audio/tts/probe", nil)
	request = request.WithContext(contextWithPrincipal(request, requestPrincipal{Kind: "legacy"}))
	response := httptest.NewRecorder()
	instance.agentTTSProbe(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("legacy probe status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentTTSProbeDoesNotCommitAvailabilityAfterRequestCancellation(t *testing.T) {
	provider := &ignoringCancelTTSProvider{started: make(chan struct{}), release: make(chan struct{})}
	instance := &app{ttsProvider: provider}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/agent/audio/tts/probe", nil).WithContext(ctx)
	request = request.WithContext(contextWithPrincipal(request, requestPrincipal{Kind: "app"}))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		instance.agentTTSProbe(response, request)
		close(done)
	}()
	<-provider.started
	cancel()
	close(provider.release)
	<-done
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("cancelled probe status=%d body=%s", response.Code, response.Body.String())
	}
	if state := instance.agentAudioSnapshot(); state.TTSAvailable {
		t.Fatal("cancelled probe committed TTS availability")
	}
}

func TestAgentTTSProbeMarksCapabilityAvailableOnlyAfterRealSynthesis(t *testing.T) {
	pcm := make([]byte, agentPCMFrameBytes)
	pcm[0] = 1
	instance := &app{ttsProvider: staticTTSProvider{result: ttsSynthesisResult{
		Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1, PCM: pcm,
	}}}
	t.Setenv(appTokenEnvironment, "native-app-tts-probe-token")
	request := httptest.NewRequest(http.MethodPost, "/api/agent/audio/tts/probe", nil)
	request.Header.Set(appTokenHeader, "native-app-tts-probe-token")
	response := httptest.NewRecorder()
	instance.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", response.Code, response.Body.String())
	}
	var state agentAudioState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.TTSAvailable {
		t.Fatalf("successful synthesis did not enable TTS: %+v", state)
	}

	capabilityResponse := httptest.NewRecorder()
	capabilityRequest := httptest.NewRequest(http.MethodGet, "/api/agent/capabilities", nil)
	capabilityRequest = capabilityRequest.WithContext(contextWithPrincipal(capabilityRequest, requestPrincipal{Kind: "app"}))
	instance.agentCapabilities(capabilityResponse, capabilityRequest)
	var capabilities map[string]any
	if err := json.Unmarshal(capabilityResponse.Body.Bytes(), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities["safety"].(map[string]any)["tts_available"] != true {
		t.Fatalf("capability did not reflect TTS probe: %s", capabilityResponse.Body.String())
	}

	cooldownRequest := httptest.NewRequest(http.MethodPost, "/api/agent/audio/tts/probe", nil)
	cooldownRequest.Header.Set(appTokenHeader, "native-app-tts-probe-token")
	cooldownResponse := httptest.NewRecorder()
	instance.routes().ServeHTTP(cooldownResponse, cooldownRequest)
	if cooldownResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("probe cooldown status=%d body=%s", cooldownResponse.Code, cooldownResponse.Body.String())
	}
}

func TestMiMoTTSProviderLive(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("MIMO_API_KEY"))
	if apiKey == "" {
		t.Skip("MIMO_API_KEY not configured")
	}
	provider := newMiMoTTSProvider(os.Getenv("MIMO_TTS_ENDPOINT"), apiKey, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := provider.Synthesize(ctx, ttsSynthesisRequest{
		SessionID: "live-probe", Generation: 1, Text: "你好，这是语音合成测试。", Voice: defaultMiMoTTSVoice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Encoding != "pcm_s16le" || result.SampleRate != 8000 || result.Channels != 1 ||
		len(result.PCM) < agentPCMFrameBytes || len(result.PCM)%agentPCMFrameBytes != 0 || !pcmContainsSignal(result.PCM) {
		t.Fatalf("invalid live TTS output: format=%s/%d/%d bytes=%d", result.Encoding, result.SampleRate, result.Channels, len(result.PCM))
	}
}
