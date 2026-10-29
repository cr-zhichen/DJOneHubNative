package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultMiMoTTSEndpoint = "https://api.xiaomimimo.com/v1/chat/completions"
	defaultMiMoTTSModel    = "mimo-v2.5-tts"
	defaultMiMoTTSVoice    = "冰糖"
	maxTTSTextBytes        = 2000
	maxTTSResponseBytes    = 16 << 20
	maxTTSPCMSamples       = 8000 * 30
	ttsAvailabilityTTL     = 10 * time.Minute
	ttsProbeCooldown       = time.Minute
)

type ttsSynthesisRequest struct {
	SessionID  string
	Generation uint64
	Text       string
	Voice      string
}

type ttsSynthesisResult struct {
	Encoding   string
	SampleRate int
	Channels   int
	PCM        []byte
}

type ttsProvider interface {
	Synthesize(context.Context, ttsSynthesisRequest) (ttsSynthesisResult, error)
}

type mimoTTSProvider struct {
	endpoint string
	apiKey   string
	client   *http.Client
}

func newMiMoTTSProvider(endpoint, apiKey string, client *http.Client) *mimoTTSProvider {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultMiMoTTSEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *client
		client = &clone
		if client.Timeout == 0 {
			client.Timeout = 30 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &mimoTTSProvider{endpoint: endpoint, apiKey: strings.TrimSpace(apiKey), client: client}
}

func validateTTSEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("tts_endpoint_invalid")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" {
		return errors.New("tts_endpoint_invalid")
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	address := net.ParseIP(host)
	if address == nil || !address.IsLoopback() {
		return errors.New("tts_endpoint_insecure")
	}
	return nil
}

func (a *app) configureTTSFromEnvironment() {
	if a == nil || a.ttsProvider != nil {
		return
	}
	apiKey := strings.TrimSpace(os.Getenv("MIMO_API_KEY"))
	if apiKey == "" {
		return
	}
	a.ttsProvider = newMiMoTTSProvider(os.Getenv("MIMO_TTS_ENDPOINT"), apiKey, nil)
}

func (a *app) agentTTSProbe(w http.ResponseWriter, r *http.Request) {
	if principalFromRequest(r).Kind != "app" {
		writeCodedError(w, http.StatusUnauthorized, "authentication_required", "TTS 探测仅允许已认证的原生应用调用", false)
		return
	}
	now := time.Now()
	a.ttsProbeMu.Lock()
	if a.ttsProbeInFlight {
		a.ttsProbeMu.Unlock()
		writeCodedError(w, http.StatusTooManyRequests, "tts_probe_in_progress", "TTS 探测正在进行", true)
		return
	}
	if !a.ttsProbeLastAt.IsZero() && now.Sub(a.ttsProbeLastAt) < ttsProbeCooldown {
		a.ttsProbeMu.Unlock()
		writeCodedError(w, http.StatusTooManyRequests, "tts_probe_cooldown", "TTS 探测冷却中", true)
		return
	}
	a.ttsProbeInFlight = true
	a.ttsProbeLastAt = now
	a.ttsProbeMu.Unlock()
	defer func() {
		a.ttsProbeMu.Lock()
		a.ttsProbeInFlight = false
		a.ttsProbeMu.Unlock()
	}()

	if a.ttsProvider == nil {
		a.agentAudioMu.Lock()
		a.agentAudio.TTSAvailable = false
		a.agentAudio.TTSCheckedAt = time.Time{}
		a.agentAudioMu.Unlock()
		writeCodedError(w, http.StatusServiceUnavailable, "tts_provider_not_configured", "TTS Provider 尚未配置", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := a.ttsProvider.Synthesize(ctx, ttsSynthesisRequest{
		SessionID: "offline-probe", Generation: 1, Text: "你好，这是语音合成测试。", Voice: defaultMiMoTTSVoice,
	})
	if contextErr := ctx.Err(); contextErr != nil {
		err = contextErr
	}
	available := err == nil && result.Encoding == "pcm_s16le" && result.SampleRate == 8000 && result.Channels == 1 &&
		len(result.PCM) >= agentPCMFrameBytes && len(result.PCM)%agentPCMFrameBytes == 0 && pcmContainsSignal(result.PCM)
	a.agentAudioMu.Lock()
	a.agentAudio.TTSAvailable = available
	if available {
		a.agentAudio.TTSCheckedAt = time.Now()
	} else {
		a.agentAudio.TTSCheckedAt = time.Time{}
	}
	a.agentAudioMu.Unlock()
	if !available {
		writeCodedError(w, http.StatusServiceUnavailable, "tts_probe_failed", "TTS 合成或音频转换探测失败", true)
		return
	}
	writeJSON(w, http.StatusOK, a.agentAudioSnapshot())
}

func pcmContainsSignal(pcm []byte) bool {
	for _, value := range pcm {
		if value != 0 {
			return true
		}
	}
	return false
}

func (p *mimoTTSProvider) Synthesize(ctx context.Context, request ttsSynthesisRequest) (ttsSynthesisResult, error) {
	text := strings.TrimSpace(request.Text)
	if p == nil || strings.TrimSpace(p.apiKey) == "" {
		return ttsSynthesisResult{}, errors.New("tts_provider_not_configured")
	}
	if err := validateTTSEndpoint(p.endpoint); err != nil {
		return ttsSynthesisResult{}, err
	}
	if text == "" || len([]byte(text)) > maxTTSTextBytes {
		return ttsSynthesisResult{}, errors.New("tts_text_invalid")
	}
	voice := strings.TrimSpace(request.Voice)
	if voice == "" {
		voice = defaultMiMoTTSVoice
	}
	payload := map[string]any{
		"model": defaultMiMoTTSModel,
		"messages": []map[string]string{
			{"role": "user", "content": "用自然、清晰、友好的中文语气朗读下面内容。"},
			{"role": "assistant", "content": text},
		},
		"audio": map[string]string{"format": "wav", "voice": voice},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ttsSynthesisResult{}, errors.New("tts_request_encode_failed")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return ttsSynthesisResult{}, errors.New("tts_request_create_failed")
	}
	httpRequest.Header.Set("api-key", p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			return ttsSynthesisResult{}, ctx.Err()
		}
		return ttsSynthesisResult{}, errors.New("tts_provider_request_failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ttsSynthesisResult{}, fmt.Errorf("tts_provider_status_%d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTTSResponseBytes+1))
	if err != nil || len(body) > maxTTSResponseBytes {
		return ttsSynthesisResult{}, errors.New("tts_provider_response_invalid")
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Choices) != 1 {
		return ttsSynthesisResult{}, errors.New("tts_provider_response_invalid")
	}
	audioData := strings.TrimSpace(decoded.Choices[0].Message.Audio.Data)
	if audioData == "" {
		return ttsSynthesisResult{}, errors.New("tts_provider_audio_missing")
	}
	wav, err := base64.StdEncoding.DecodeString(audioData)
	if err != nil {
		return ttsSynthesisResult{}, errors.New("tts_provider_audio_invalid")
	}
	pcm, err := convertPCM16WAVToAgentPCM(wav)
	if err != nil {
		return ttsSynthesisResult{}, err
	}
	return ttsSynthesisResult{Encoding: "pcm_s16le", SampleRate: 8000, Channels: 1, PCM: pcm}, nil
}

type pcmWAV struct {
	sampleRate int
	channels   int
	pcm        []byte
}

func parsePCM16WAV(wav []byte) (pcmWAV, error) {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return pcmWAV{}, errors.New("tts_audio_not_wav")
	}
	if uint64(binary.LittleEndian.Uint32(wav[4:8]))+8 != uint64(len(wav)) {
		return pcmWAV{}, errors.New("tts_audio_wav_size_invalid")
	}
	var format uint16
	var channels uint16
	var sampleRate uint32
	var bits uint16
	var pcm []byte
	for offset := 12; offset < len(wav); {
		if len(wav)-offset < 8 {
			return pcmWAV{}, errors.New("tts_audio_wav_truncated")
		}
		chunkID := string(wav[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))
		dataStart := offset + 8
		dataEnd := dataStart + chunkSize
		if chunkSize < 0 || dataEnd < dataStart || dataEnd > len(wav) {
			return pcmWAV{}, errors.New("tts_audio_wav_truncated")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return pcmWAV{}, errors.New("tts_audio_wav_format_invalid")
			}
			format = binary.LittleEndian.Uint16(wav[dataStart : dataStart+2])
			channels = binary.LittleEndian.Uint16(wav[dataStart+2 : dataStart+4])
			sampleRate = binary.LittleEndian.Uint32(wav[dataStart+4 : dataStart+8])
			bits = binary.LittleEndian.Uint16(wav[dataStart+14 : dataStart+16])
		case "data":
			pcm = append([]byte(nil), wav[dataStart:dataEnd]...)
		}
		offset = dataEnd
		if chunkSize%2 != 0 {
			if offset >= len(wav) {
				return pcmWAV{}, errors.New("tts_audio_wav_truncated")
			}
			offset++
		}
	}
	if format != 1 || bits != 16 || (channels != 1 && channels != 2) || sampleRate < 8000 || sampleRate > 192000 || len(pcm) == 0 {
		return pcmWAV{}, errors.New("tts_audio_wav_unsupported")
	}
	blockBytes := int(channels) * 2
	if len(pcm)%blockBytes != 0 {
		return pcmWAV{}, errors.New("tts_audio_wav_truncated")
	}
	return pcmWAV{sampleRate: int(sampleRate), channels: int(channels), pcm: pcm}, nil
}

func convertPCM16WAVToAgentPCM(wav []byte) ([]byte, error) {
	decoded, err := parsePCM16WAV(wav)
	if err != nil {
		return nil, err
	}
	sourceFrames := len(decoded.pcm) / (decoded.channels * 2)
	targetSamples := sourceFrames * 8000 / decoded.sampleRate
	if targetSamples <= 0 || targetSamples > maxTTSPCMSamples {
		return nil, errors.New("tts_audio_duration_invalid")
	}
	paddedSamples := ((targetSamples + 159) / 160) * 160
	output := make([]byte, paddedSamples*2)
	for target := 0; target < targetSamples; target++ {
		source := target * decoded.sampleRate / 8000
		if source >= sourceFrames {
			source = sourceFrames - 1
		}
		offset := source * decoded.channels * 2
		sample := int(int16(binary.LittleEndian.Uint16(decoded.pcm[offset : offset+2])))
		if decoded.channels == 2 {
			right := int(int16(binary.LittleEndian.Uint16(decoded.pcm[offset+2 : offset+4])))
			sample = (sample + right) / 2
		}
		binary.LittleEndian.PutUint16(output[target*2:target*2+2], uint16(int16(sample)))
	}
	return output, nil
}

type ttsController struct {
	mu         sync.Mutex
	provider   ttsProvider
	capacity   int
	sessionID  string
	generation uint64
	turn       uint64
	cancel     context.CancelFunc
	queue      [][]byte
}

func newTTSController(provider ttsProvider, capacity int) *ttsController {
	if capacity < 1 {
		capacity = 1
	}
	return &ttsController{provider: provider, capacity: capacity}
}

func (c *ttsController) StartSession(sessionID string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.generation++
	c.turn++
	c.sessionID = strings.TrimSpace(sessionID)
	c.queue = nil
	return c.generation
}

func (c *ttsController) StopSession(sessionID string, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != sessionID || c.generation != generation {
		return
	}
	c.generation++
	c.turn++
	c.sessionID = ""
	c.queue = nil
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

func (c *ttsController) Synthesize(parent context.Context, request ttsSynthesisRequest) error {
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Text = strings.TrimSpace(request.Text)
	if request.SessionID == "" || request.Text == "" || len([]byte(request.Text)) > maxTTSTextBytes {
		return errors.New("tts_request_invalid")
	}
	c.mu.Lock()
	if c.provider == nil || c.sessionID != request.SessionID || c.generation != request.Generation {
		c.mu.Unlock()
		return errors.New("tts_session_stale")
	}
	if c.cancel != nil {
		c.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.turn++
	turn := c.turn
	c.queue = nil
	provider := c.provider
	c.mu.Unlock()

	result, err := provider.Synthesize(ctx, request)
	contextErr := ctx.Err()
	cancel()
	c.mu.Lock()
	if c.turn == turn {
		c.cancel = nil
	}
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if contextErr != nil {
		return contextErr
	}
	if result.Encoding != "pcm_s16le" || result.SampleRate != 8000 || result.Channels != 1 || len(result.PCM) == 0 || len(result.PCM)%agentPCMFrameBytes != 0 {
		return errors.New("tts_audio_format_invalid")
	}
	frameCount := len(result.PCM) / agentPCMFrameBytes
	if frameCount > c.capacity {
		return errors.New("tts_queue_capacity_exceeded")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != request.SessionID || c.generation != request.Generation || c.turn != turn {
		return errors.New("tts_session_stale")
	}
	for offset := 0; offset < len(result.PCM); offset += agentPCMFrameBytes {
		c.queue = append(c.queue, append([]byte(nil), result.PCM[offset:offset+agentPCMFrameBytes]...))
	}
	return nil
}

func (c *ttsController) PopFrame(sessionID string, generation uint64) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != sessionID || c.generation != generation || len(c.queue) == 0 {
		return nil
	}
	frame := c.queue[0]
	c.queue = c.queue[1:]
	return append([]byte(nil), frame...)
}

func (c *ttsController) QueuedFrames(sessionID string, generation uint64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != sessionID || c.generation != generation {
		return 0
	}
	return len(c.queue)
}
