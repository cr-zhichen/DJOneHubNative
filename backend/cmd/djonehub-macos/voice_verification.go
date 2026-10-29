package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type voiceVerificationWindow struct {
	Active          bool      `json:"active"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	AllowedNumbers  []string  `json:"allowed_numbers"`
	Answered        bool      `json:"answered"`
	SessionID       string    `json:"session_id,omitempty"`
	Consumed        bool      `json:"consumed"`
	AudioAuthorized bool      `json:"audio_authorized"`
	LastError       string    `json:"last_error,omitempty"`
	generation      uint64
	answering       bool
	attemptNumber   string
	attemptSession  string
	attemptCallID   int
}

type voiceVerificationAttempt struct {
	generation uint64
	number     string
	sessionID  string
	callID     int
}

func (w voiceVerificationWindow) MarshalJSON() ([]byte, error) {
	type wireWindow struct {
		Active          bool       `json:"active"`
		ExpiresAt       *time.Time `json:"expires_at,omitempty"`
		AllowedNumbers  []string   `json:"allowed_numbers"`
		Answered        bool       `json:"answered"`
		SessionID       string     `json:"session_id,omitempty"`
		Consumed        bool       `json:"consumed"`
		AudioAuthorized bool       `json:"audio_authorized"`
		LastError       string     `json:"last_error,omitempty"`
	}
	allowed := append([]string(nil), w.AllowedNumbers...)
	if allowed == nil {
		allowed = []string{}
	}
	var expiresAt *time.Time
	if !w.ExpiresAt.IsZero() {
		expires := w.ExpiresAt
		expiresAt = &expires
	}
	return json.Marshal(wireWindow{
		Active:          w.Active,
		ExpiresAt:       expiresAt,
		AllowedNumbers:  allowed,
		Answered:        w.Answered,
		SessionID:       w.SessionID,
		Consumed:        w.Consumed,
		AudioAuthorized: w.AudioAuthorized,
		LastError:       w.LastError,
	})
}

func (a *app) armVoiceVerification(duration time.Duration, allowed []string) voiceVerificationWindow {
	a.stopVoiceVerificationAudio()
	now := time.Now()
	window := voiceVerificationWindow{
		Active:         true,
		ExpiresAt:      now.Add(duration),
		AllowedNumbers: append([]string(nil), allowed...),
	}
	a.voiceVerificationMu.Lock()
	a.voiceVerificationGeneration++
	window.generation = a.voiceVerificationGeneration
	a.voiceVerification = &window
	a.voiceVerificationMu.Unlock()
	return window
}

func (a *app) voiceVerificationSnapshot() voiceVerificationWindow {
	a.voiceVerificationMu.Lock()
	defer a.voiceVerificationMu.Unlock()
	if a.voiceVerification == nil {
		return voiceVerificationWindow{}
	}
	a.expireVoiceVerificationLocked(time.Now())
	window := *a.voiceVerification
	window.AllowedNumbers = append([]string(nil), a.voiceVerification.AllowedNumbers...)
	return window
}

func clearVoiceVerificationWindowLocked(window *voiceVerificationWindow) {
	window.Active = false
	window.ExpiresAt = time.Time{}
	window.AllowedNumbers = nil
	window.Answered = false
	window.SessionID = ""
	window.Consumed = false
	window.AudioAuthorized = false
	window.LastError = ""
	window.answering = false
	window.attemptNumber = ""
	window.attemptSession = ""
	window.attemptCallID = 0
}

func (a *app) expireVoiceVerificationLocked(now time.Time) {
	if a.voiceVerification == nil || !a.voiceVerification.Active || a.voiceVerification.ExpiresAt.IsZero() || !now.After(a.voiceVerification.ExpiresAt) {
		return
	}
	a.voiceVerificationGeneration++
	clearVoiceVerificationWindowLocked(a.voiceVerification)
	a.voiceVerification.generation = a.voiceVerificationGeneration
}

func (a *app) disarmVoiceVerification() {
	a.voiceVerificationMu.Lock()
	a.voiceVerificationGeneration++
	if a.voiceVerification != nil {
		clearVoiceVerificationWindowLocked(a.voiceVerification)
		a.voiceVerification.generation = a.voiceVerificationGeneration
	}
	a.voiceVerificationMu.Unlock()
	a.stopVoiceVerificationAudio()
}

func (a *app) updateVoiceVerification(update func(*voiceVerificationWindow)) {
	a.voiceVerificationMu.Lock()
	defer a.voiceVerificationMu.Unlock()
	if a.voiceVerification != nil {
		update(a.voiceVerification)
	}
}

func (a *app) voiceVerificationNumberAllowed(number string) bool {
	if normalizedCallNumber(number) == "" {
		return false
	}
	window := a.voiceVerificationSnapshot()
	return voiceVerificationNumberAllowedLocked(&window, number)
}

func (a *app) runVoiceVerificationAutomation(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	defer a.stopVoiceVerificationAudio()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := a.currentCallState()
			if a.handleVoiceVerificationIncoming(state) {
				continue
			}
			a.reconcileVoiceVerificationAudio(ctx, state)
		}
	}
}

func (a *app) handleVoiceVerificationIncoming(state callStateInfo) bool {
	attempt, started, handled := a.reserveVoiceVerificationAttempt(state)
	if !handled {
		return false
	}
	if !started {
		return true
	}
	// Prewarm the module route while the caller still hears ringing. Once ATA
	// succeeds, the active-call reconciliation can open AC/AS immediately instead
	// of losing the first several seconds to route setup and CoreAudio settling.
	ownership, err := a.ensureModuleVoiceRouteOwned(attempt.sessionID)
	if err != nil {
		transient := errors.Is(err, errModuleVoiceStartInProgress) || errors.Is(err, errModuleVoiceRecoveryInProgress) || errors.Is(err, errModuleVoiceTeardownInProgress)
		if transient && !ownership.created {
			a.deferVoiceVerificationAttempt(attempt)
			return true
		}
		message := "模块语音路由预热失败：" + moduleVoiceErrorMessage(err)
		if cleanupErr := a.rollbackOwnedModuleVoiceRoute(ownership); cleanupErr != nil {
			message += "；模块音频预热清理未完成：" + cleanupErr.Error()
		}
		a.failVoiceVerificationAttempt(attempt, message)
		return true
	}

	// Keep callControlMu through the identity check and ATA so a concurrent call
	// operation cannot replace the checked incoming call before it is answered.
	a.callControlMu.Lock()
	currentCall := a.queryCallState()
	a.voiceVerificationMu.Lock()
	if !a.voiceVerificationAttemptValidLocked(attempt, currentCall) {
		if a.voiceVerificationAttemptCurrentLocked(attempt) {
			a.consumeVoiceVerificationFailureLocked("来电身份已变更，已取消自动接听")
		}
		a.voiceVerificationMu.Unlock()
		a.callControlMu.Unlock()
		if cleanupErr := a.rollbackOwnedModuleVoiceRoute(ownership); cleanupErr != nil {
			a.appendVoiceVerificationAttemptError(attempt, "模块音频预热清理未完成："+cleanupErr.Error())
		}
		return true
	}
	answer := a.voiceVerificationAnswer
	if answer == nil {
		answer = func() error { return a.answerCurrentCallLockedWithState(currentCall) }
	}
	if err := answer(); err != nil {
		a.consumeVoiceVerificationFailureLocked(err.Error())
		a.voiceVerificationMu.Unlock()
		a.callControlMu.Unlock()
		if cleanupErr := a.rollbackOwnedModuleVoiceRoute(ownership); cleanupErr != nil {
			a.appendVoiceVerificationAttemptError(attempt, "模块音频预热清理未完成："+cleanupErr.Error())
		}
		return true
	}
	a.voiceVerification.Active = false
	a.voiceVerification.Answered = true
	a.voiceVerification.Consumed = true
	a.voiceVerification.AudioAuthorized = true
	a.voiceVerification.SessionID = attempt.sessionID
	a.voiceVerification.LastError = ""
	a.voiceVerification.answering = false
	a.commitModuleVoiceRouteOwnership(ownership)
	a.voiceVerificationMu.Unlock()
	a.callControlMu.Unlock()
	return true
}

func (a *app) reserveVoiceVerificationAttempt(state callStateInfo) (voiceVerificationAttempt, bool, bool) {
	a.voiceVerificationMu.Lock()
	defer a.voiceVerificationMu.Unlock()
	a.expireVoiceVerificationLocked(time.Now())
	window := a.voiceVerification
	if window == nil || !window.Active || state.State != "incoming" || strings.TrimSpace(state.SessionID) == "" || !voiceVerificationNumberAllowedLocked(window, state.Number) {
		return voiceVerificationAttempt{}, false, false
	}
	if window.answering {
		return voiceVerificationAttempt{}, false, true
	}
	attempt := voiceVerificationAttempt{
		generation: window.generation,
		number:     normalizedCallNumber(state.Number),
		sessionID:  state.SessionID,
		callID:     state.moduleCallID,
	}
	window.answering = true
	window.attemptNumber = attempt.number
	window.attemptSession = attempt.sessionID
	window.attemptCallID = attempt.callID
	return attempt, true, true
}

func moduleVoiceErrorMessage(err error) string {
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		return errModuleVoiceStartInProgress.Error()
	}
	return err.Error()
}

func voiceVerificationNumberAllowedLocked(window *voiceVerificationWindow, number string) bool {
	number = normalizedCallNumber(number)
	if number == "" {
		return false
	}
	for _, allowed := range window.AllowedNumbers {
		if number == normalizedCallNumber(allowed) {
			return true
		}
	}
	return false
}

func (a *app) voiceVerificationAttemptCurrentLocked(attempt voiceVerificationAttempt) bool {
	window := a.voiceVerification
	return window != nil && window.generation == attempt.generation && window.answering && window.attemptNumber == attempt.number && window.attemptSession == attempt.sessionID && window.attemptCallID == attempt.callID
}

func (a *app) voiceVerificationAttemptValidLocked(attempt voiceVerificationAttempt, current callStateInfo) bool {
	a.expireVoiceVerificationLocked(time.Now())
	if !a.voiceVerificationAttemptCurrentLocked(attempt) || !a.voiceVerification.Active || current.State != "incoming" || normalizedCallNumber(current.Number) != attempt.number || current.SessionID != attempt.sessionID {
		return false
	}
	return attempt.callID != 0 && current.moduleCallID != 0 && current.moduleCallID == attempt.callID
}

func (a *app) consumeVoiceVerificationFailureLocked(message string) {
	a.voiceVerification.Active = false
	a.voiceVerification.Answered = false
	a.voiceVerification.Consumed = true
	a.voiceVerification.AudioAuthorized = false
	a.voiceVerification.SessionID = ""
	a.voiceVerification.LastError = message
	a.voiceVerification.answering = false
}

func (a *app) failVoiceVerificationAttempt(attempt voiceVerificationAttempt, message string) {
	a.voiceVerificationMu.Lock()
	defer a.voiceVerificationMu.Unlock()
	if a.voiceVerificationAttemptCurrentLocked(attempt) {
		a.consumeVoiceVerificationFailureLocked(message)
	}
}

func (a *app) deferVoiceVerificationAttempt(attempt voiceVerificationAttempt) {
	a.voiceVerificationMu.Lock()
	defer a.voiceVerificationMu.Unlock()
	if a.voiceVerificationAttemptCurrentLocked(attempt) {
		a.voiceVerification.answering = false
		a.voiceVerification.attemptNumber = ""
		a.voiceVerification.attemptSession = ""
		a.voiceVerification.attemptCallID = 0
	}
}

func (a *app) appendVoiceVerificationAttemptError(attempt voiceVerificationAttempt, message string) {
	a.voiceVerificationMu.Lock()
	defer a.voiceVerificationMu.Unlock()
	if a.voiceVerification != nil && a.voiceVerification.generation == attempt.generation && a.voiceVerification.Consumed {
		if a.voiceVerification.LastError != "" {
			a.voiceVerification.LastError += "；"
		}
		a.voiceVerification.LastError += message
	}
}

func (a *app) answerCurrentCall() error {
	a.callControlMu.Lock()
	defer a.callControlMu.Unlock()
	return a.answerCurrentCallLocked()
}

func (a *app) answerCurrentCallLocked() error {
	return a.answerCurrentCallLockedWithState(a.queryCallState())
}

func (a *app) answerCurrentCallLockedWithState(state callStateInfo) error {
	if state.State != "incoming" {
		return &voiceVerificationError{message: "当前通话状态为 " + state.State}
	}
	if err := a.callModeReadyForCall(); err != nil {
		return err
	}
	response, err := a.runCallCommand("ATA", "", 10*time.Second)
	if !a.callModeATCommandAccepted(response, err) {
		return &voiceVerificationError{message: "模块未确认接听"}
	}
	a.markCallAnswered()
	return nil
}

type voiceVerificationError struct{ message string }

func (e *voiceVerificationError) Error() string { return e.message }

func (a *app) voiceVerificationArm(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DurationSeconds int      `json:"duration_seconds"`
		AllowedNumbers  []string `json:"allowed_numbers"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.DurationSeconds < 30 || body.DurationSeconds > 900 {
		writeCodedError(w, http.StatusBadRequest, "invalid_duration", "接听窗口必须为 30 到 900 秒", false)
		return
	}
	if len(body.AllowedNumbers) == 0 || len(body.AllowedNumbers) > 10 {
		writeCodedError(w, http.StatusBadRequest, "invalid_allowed_numbers", "必须指定 1 到 10 个允许号码", false)
		return
	}
	allowed := make([]string, 0, len(body.AllowedNumbers))
	for _, number := range body.AllowedNumbers {
		number = strings.TrimSpace(number)
		if !validVoiceNumber(number) {
			writeCodedError(w, http.StatusBadRequest, "invalid_allowed_number", "允许号码格式无效", false)
			return
		}
		allowed = append(allowed, number)
	}
	writeJSON(w, http.StatusOK, a.armVoiceVerification(time.Duration(body.DurationSeconds)*time.Second, allowed))
}

func (a *app) voiceVerificationStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.voiceVerificationSnapshot())
}

func (a *app) voiceVerificationDisarm(w http.ResponseWriter, _ *http.Request) {
	a.disarmVoiceVerification()
	writeJSON(w, http.StatusOK, a.voiceVerificationSnapshot())
}
