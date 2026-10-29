package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type agentPCMRunnerFunc func(context.Context, string, func([]byte)) error

var (
	errModuleVoiceRecoveryInProgress = errors.New("模块语音路由正在执行单次自动恢复")
	errModuleVoiceRecoveryExhausted  = errors.New("模块音频单次自动恢复已用尽，需要授权重启 CoreAudio")
	errModuleVoiceTeardownInProgress = errors.New("模块语音路由正在等待原生 CoreAudio 安全停止")
	errModuleVoiceStartInProgress    = errors.New("模块语音路由启动未完成")
)

func (a *app) completedCaptureLocked(captureError string, endedAt time.Time) *captureMetrics {
	if a.agentAudio.CaptureSessionID == "" {
		return nil
	}
	result := &captureMetrics{
		SessionID: a.agentAudio.CaptureSessionID, FramesReceived: a.agentAudio.FramesReceived,
		NonzeroFrames: a.agentAudio.NonzeroFrames, BytesReceived: a.agentAudio.BytesReceived,
		PeakSample: a.agentAudio.PeakSample, StartedAt: a.agentAudio.CaptureStartedAt,
		EndedAt: endedAt, Error: captureError,
	}
	if a.agentAudio.TranscriptSessionID == result.SessionID {
		result.Transcript = a.agentAudio.Transcript
		result.TranscriptFinal = a.agentAudio.TranscriptFinal
		result.VerificationCode = a.agentAudio.VerificationCode
	}
	return result
}

func (a *app) clearCurrentTranscriptLocked() {
	a.agentAudio.Transcript = ""
	a.agentAudio.TranscriptFinal = false
	a.agentAudio.TranscriptSessionID = ""
	a.agentAudio.VerificationCode = ""
	a.agentAudio.TranscriptUpdatedAt = time.Time{}
}

func (a *app) reconcileVoiceVerificationAudio(ctx context.Context, call callStateInfo) {
	window := a.voiceVerificationSnapshot()
	authorized := call.State == "active" && call.Active && window.Answered && window.Consumed && window.AudioAuthorized &&
		window.SessionID != "" && call.SessionID == window.SessionID
	if authorized {
		if a.moduleVoiceTeardownPending() {
			if err := a.stopModuleVoiceRouteSafelyPreservingRecovery(100 * time.Millisecond); err != nil {
				return
			}
		}
		if err := a.ensureModuleVoiceRoute(call.SessionID); err != nil {
			if errors.Is(err, errModuleVoiceRecoveryInProgress) || errors.Is(err, errModuleVoiceTeardownInProgress) {
				return
			}
			if errors.Is(err, errModuleVoiceRecoveryExhausted) {
				a.agentAudioMu.Lock()
				a.agentAudio.CaptureError = err.Error()
				a.agentAudioMu.Unlock()
				return
			}
			a.stopVoiceVerificationAudio()
			a.agentAudioMu.Lock()
			a.agentAudio.CaptureError = "模块语音路由启动失败：" + err.Error()
			a.agentAudioMu.Unlock()
			return
		}
		a.startVoiceVerificationAudio(ctx, call.SessionID)
		return
	}
	// Fail closed: any transition away from the exact authorized active session
	// immediately revokes capture, including held/unknown/replaced calls. Keep the
	// module route alive until the native process confirms both CoreAudio IOProcs
	// have stopped; tearing the route down first can wedge QDC507's AC endpoint.
	if err := a.stopModuleVoiceRouteSafely(2 * time.Second); err != nil {
		a.agentAudioMu.Lock()
		a.agentAudio.CaptureError = "模块音频清理等待原生 CoreAudio 停止超时：" + err.Error()
		a.agentAudioMu.Unlock()
	}
}

func (a *app) nativeModuleAudioStillActive() bool {
	a.agentAudioMu.RLock()
	defer a.agentAudioMu.RUnlock()
	return a.agentAudio.ClientConnected || a.agentAudio.ModuleIOReady
}

func (a *app) moduleVoiceTeardownPending() bool {
	if !a.moduleVoiceMu.TryLock() {
		return true
	}
	defer a.moduleVoiceMu.Unlock()
	return a.moduleVoiceTeardownInProgress
}

func tryLockMutexUntil(mu *sync.Mutex, deadline time.Time) bool {
	for {
		if mu.TryLock() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

func (a *app) stopModuleVoiceRouteSafely(timeout time.Duration) error {
	return a.stopModuleVoiceRouteSafelyWithPolicy(timeout, false)
}

func (a *app) stopModuleVoiceRouteSafelyPreservingRecovery(timeout time.Duration) error {
	return a.stopModuleVoiceRouteSafelyWithPolicy(timeout, true)
}

func (a *app) stopModuleVoiceRouteSafelyWithPolicy(timeout time.Duration, preserveRecoveryExhausted bool) error {
	deadline := time.Now().Add(timeout)
	if !tryLockMutexUntil(&a.moduleVoiceTeardownMu, deadline) {
		return errors.New("模块语音路由已有清理操作尚未结束")
	}
	defer a.moduleVoiceTeardownMu.Unlock()

	if !tryLockMutexUntil(&a.moduleVoiceMu, deadline) {
		return errors.New("模块语音路由状态切换尚未结束")
	}
	a.moduleVoiceTeardownInProgress = true
	a.moduleVoiceTransitionGeneration++
	a.moduleVoiceMu.Unlock()

	a.stopVoiceVerificationAudio()
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	if err := a.waitForNativeModuleAudioStop(remaining); err != nil {
		return err
	}
	if err := a.stopModuleVoiceRouteConfirmed(preserveRecoveryExhausted); err != nil {
		return err
	}
	a.moduleVoiceMu.Lock()
	a.moduleVoiceTeardownInProgress = false
	a.moduleVoiceMu.Unlock()
	return nil
}

func (a *app) stopModuleVoiceRouteConfirmed(preserveRecoveryExhausted bool) error {
	a.moduleVoiceMu.Lock()
	a.moduleVoiceRecoveryGeneration++
	if !preserveRecoveryExhausted {
		a.moduleVoiceRecoverySession = ""
		a.moduleVoiceRecoveryExhausted = ""
	}
	if a.moduleVoiceRecoveryInProgress {
		a.moduleVoiceRouteAttemptedSession = ""
		a.moduleVoiceRouteLastError = ""
		a.moduleVoiceMu.Unlock()
		return errors.New("模块语音路由恢复尚未结束")
	}
	active := a.moduleVoiceRoute
	stopper := a.moduleVoiceRouteStop
	if !a.moduleVoiceRouteStarting {
		a.moduleVoiceRouteAttemptedSession = ""
		a.moduleVoiceRouteLastError = ""
	}
	a.moduleVoiceMu.Unlock()
	if !active {
		a.moduleVoiceMu.Lock()
		a.clearStoppedModuleVoiceRouteLocked()
		a.moduleVoiceMu.Unlock()
		return nil
	}
	if stopper == nil {
		stopper = func(directory string) error { return a.stopModuleVoiceRoute(directory) }
	}
	if err := stopper(a.runtimeDirectory()); err != nil {
		return err
	}
	a.moduleVoiceMu.Lock()
	a.clearStoppedModuleVoiceRouteLocked()
	a.moduleVoiceMu.Unlock()
	return nil
}

func (a *app) clearStoppedModuleVoiceRouteLocked() {
	a.moduleVoiceRoute = false
	a.moduleVoiceRouteOwnerSession = ""
	a.moduleVoiceRouteOwnerGeneration = 0
	a.moduleVoiceRoutePendingOwner = 0
	if !a.moduleVoiceRouteStarting {
		a.moduleVoiceRouteAttemptedSession = ""
		a.moduleVoiceRouteLastError = ""
	}
}

func (a *app) waitForNativeModuleAudioStop(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for a.nativeModuleAudioStillActive() {
		if !time.Now().Before(deadline) {
			return fmt.Errorf("原生模块音频状态未确认停止")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

type moduleVoiceRouteOwnership struct {
	created         bool
	generation      uint64
	resetGeneration uint64
	sessionID       string
}

func (a *app) ensureModuleVoiceRoute(sessionID string) error {
	ownership, err := a.ensureModuleVoiceRouteOwned(sessionID)
	if err != nil && ownership.created {
		if cleanupErr := a.rollbackOwnedModuleVoiceRoute(ownership); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
	}
	return err
}

func (a *app) ensureModuleVoiceRouteOwned(sessionID string) (moduleVoiceRouteOwnership, error) {
	if !a.moduleVoiceAutoStart {
		return moduleVoiceRouteOwnership{}, nil
	}
	a.moduleVoiceMu.Lock()
	if a.moduleVoiceTeardownInProgress {
		a.moduleVoiceMu.Unlock()
		return moduleVoiceRouteOwnership{}, errModuleVoiceTeardownInProgress
	}
	if a.moduleVoiceRecoveryInProgress {
		a.moduleVoiceMu.Unlock()
		return moduleVoiceRouteOwnership{}, errModuleVoiceRecoveryInProgress
	}
	if a.moduleVoiceRecoveryExhausted == sessionID {
		a.moduleVoiceMu.Unlock()
		return moduleVoiceRouteOwnership{}, errModuleVoiceRecoveryExhausted
	}
	if a.moduleVoiceRoute {
		a.moduleVoiceMu.Unlock()
		return moduleVoiceRouteOwnership{}, nil
	}
	if a.moduleVoiceRouteStarting {
		a.moduleVoiceMu.Unlock()
		return moduleVoiceRouteOwnership{}, errModuleVoiceStartInProgress
	}
	if a.moduleVoiceRouteAttemptedSession == sessionID {
		err := a.moduleVoiceRouteLastError
		a.moduleVoiceMu.Unlock()
		return moduleVoiceRouteOwnership{}, errors.New(err)
	}
	a.moduleVoiceRouteAttemptedSession = sessionID
	a.moduleVoiceRouteLastError = ""
	a.moduleVoiceTransitionGeneration++
	transitionGeneration := a.moduleVoiceTransitionGeneration
	a.moduleVoiceRouteStarting = true
	a.moduleVoiceRouteStartGeneration = transitionGeneration
	resetGeneration := a.moduleVoiceResetGeneration
	ownership := moduleVoiceRouteOwnership{created: true, generation: transitionGeneration, resetGeneration: resetGeneration, sessionID: sessionID}
	starter := a.moduleVoiceRouteStart
	usesProductionStarter := starter == nil
	settle := a.moduleVoiceRouteSettle
	if starter == nil {
		starter = func(directory string) error {
			return a.startModuleVoiceRoute(directory)
		}
	}
	a.moduleVoiceMu.Unlock()
	if err := starter(a.runtimeDirectory()); err != nil {
		a.moduleVoiceMu.Lock()
		if a.moduleVoiceRouteStartGeneration == transitionGeneration {
			a.moduleVoiceRouteStarting = false
			a.moduleVoiceRouteStartGeneration = 0
		}
		if a.moduleVoiceTransitionGeneration == transitionGeneration && !a.moduleVoiceTeardownInProgress {
			a.moduleVoiceRouteLastError = err.Error()
		}
		a.moduleVoiceMu.Unlock()
		return ownership, err
	}
	if usesProductionStarter || settle != nil {
		// audio_enable=1 can re-enumerate AC/AS asynchronously. Give CoreAudio
		// a bounded settle window before the Agent client opens the endpoints.
		if settle != nil {
			settle()
		} else {
			time.Sleep(3 * time.Second)
		}
	}
	a.moduleVoiceMu.Lock()
	if a.moduleVoiceTeardownInProgress || a.moduleVoiceTransitionGeneration != transitionGeneration {
		if a.moduleVoiceResetGeneration != resetGeneration {
			if a.moduleVoiceRouteStartGeneration != transitionGeneration {
				a.moduleVoiceMu.Unlock()
				return ownership, errModuleVoiceTeardownInProgress
			}
			a.moduleVoiceRoute = true
			a.moduleVoiceRouteStarting = false
			a.moduleVoiceRouteStartGeneration = 0
			a.moduleVoiceRoutePendingOwner = transitionGeneration
			a.moduleVoiceTeardownInProgress = true
			a.moduleVoiceMu.Unlock()
			return ownership, errModuleVoiceTeardownInProgress
		}
		// The starter already succeeded, so the module has a real route side
		// effect even though this transition can no longer commit. Keep the
		// exact owner generation and block replacement starts until its caller
		// rolls the route back safely.
		a.moduleVoiceRoute = true
		a.moduleVoiceRouteStarting = false
		a.moduleVoiceRouteStartGeneration = 0
		a.moduleVoiceRoutePendingOwner = transitionGeneration
		a.moduleVoiceTeardownInProgress = true
		a.moduleVoiceMu.Unlock()
		return ownership, errModuleVoiceTeardownInProgress
	}
	a.moduleVoiceRoute = true
	a.moduleVoiceRouteStarting = false
	a.moduleVoiceRouteStartGeneration = 0
	a.moduleVoiceRoutePendingOwner = transitionGeneration
	a.moduleVoiceRouteLastError = ""
	a.moduleVoiceMu.Unlock()
	return ownership, nil
}

func (a *app) rollbackOwnedModuleVoiceRoute(ownership moduleVoiceRouteOwnership) error {
	if !ownership.created {
		return nil
	}
	deadline := time.Now().Add(2 * time.Second)
	if !tryLockMutexUntil(&a.moduleVoiceTeardownMu, deadline) {
		return errors.New("模块语音路由已有清理操作尚未结束")
	}
	defer a.moduleVoiceTeardownMu.Unlock()

	if !tryLockMutexUntil(&a.moduleVoiceMu, deadline) {
		return errors.New("模块语音路由状态切换尚未结束")
	}
	if a.moduleVoiceRouteOwnerGeneration != 0 && (a.moduleVoiceRouteOwnerGeneration != ownership.generation || a.moduleVoiceRouteOwnerSession != ownership.sessionID) {
		a.moduleVoiceMu.Unlock()
		return nil
	}
	ownsCurrentTransition := a.moduleVoiceTransitionGeneration == ownership.generation
	ownsInvalidatedStarter := a.moduleVoiceRouteStarting && a.moduleVoiceRouteStartGeneration == ownership.generation && a.moduleVoiceRouteAttemptedSession == ownership.sessionID
	pendingRollback := a.moduleVoiceRoutePendingOwner == ownership.generation && a.moduleVoiceRouteAttemptedSession == ownership.sessionID
	if !ownsCurrentTransition && !ownsInvalidatedStarter && !pendingRollback {
		a.moduleVoiceMu.Unlock()
		return nil
	}
	if ownsInvalidatedStarter || ownsCurrentTransition {
		a.moduleVoiceRoute = true
		a.moduleVoiceRoutePendingOwner = ownership.generation
	}
	a.moduleVoiceTeardownInProgress = true
	a.moduleVoiceTransitionGeneration++
	a.moduleVoiceMu.Unlock()

	a.stopVoiceVerificationAudio()
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	if err := a.waitForNativeModuleAudioStop(remaining); err != nil {
		a.moduleVoiceMu.Lock()
		a.moduleVoiceRouteLastError = err.Error()
		a.moduleVoiceMu.Unlock()
		return err
	}
	if err := a.stopModuleVoiceRouteConfirmed(false); err != nil {
		a.moduleVoiceMu.Lock()
		a.moduleVoiceRouteLastError = err.Error()
		a.moduleVoiceMu.Unlock()
		return err
	}
	a.moduleVoiceMu.Lock()
	a.moduleVoiceTeardownInProgress = false
	a.moduleVoiceMu.Unlock()
	return nil
}

func (a *app) commitModuleVoiceRouteOwnership(ownership moduleVoiceRouteOwnership) {
	if !ownership.created {
		return
	}
	a.moduleVoiceMu.Lock()
	defer a.moduleVoiceMu.Unlock()
	if a.moduleVoiceRoute && a.moduleVoiceRoutePendingOwner == ownership.generation {
		a.moduleVoiceRouteOwnerSession = ownership.sessionID
		a.moduleVoiceRouteOwnerGeneration = ownership.generation
		a.moduleVoiceRoutePendingOwner = 0
		a.moduleVoiceRouteAttemptedSession = ""
		a.moduleVoiceRouteLastError = ""
	}
}

func isRecoverableModuleIOError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "module_io_unavailable") ||
		strings.Contains(message, "coreaudio device start failed") ||
		strings.Contains(message, "0x10004003")
}

func (a *app) reserveModuleVoiceRecovery(sessionID string, runErr error) (uint64, bool) {
	if !a.moduleVoiceAutoStart || !isRecoverableModuleIOError(runErr) {
		return 0, false
	}
	a.moduleVoiceMu.Lock()
	defer a.moduleVoiceMu.Unlock()
	if a.moduleVoiceRecoverySession == sessionID || a.moduleVoiceRecoveryInProgress {
		return 0, false
	}
	a.moduleVoiceRecoveryGeneration++
	generation := a.moduleVoiceRecoveryGeneration
	a.moduleVoiceRecoverySession = sessionID
	a.moduleVoiceRecoveryInProgress = true
	a.moduleVoiceRecoveryActive = generation
	return generation, true
}

func (a *app) completeReservedModuleVoiceRecovery(sessionID string, generation uint64) bool {
	a.moduleVoiceTeardownMu.Lock()
	defer a.moduleVoiceTeardownMu.Unlock()
	a.moduleVoiceMu.Lock()
	a.moduleVoiceTeardownInProgress = true
	a.moduleVoiceTransitionGeneration++
	active := a.moduleVoiceRoute
	stopper := a.moduleVoiceRouteStop
	a.moduleVoiceMu.Unlock()

	var recoveryErr error
	if active {
		if waitErr := a.waitForNativeModuleAudioStop(2 * time.Second); waitErr != nil {
			recoveryErr = waitErr
		} else {
			if stopper == nil {
				stopper = func(directory string) error { return a.stopModuleVoiceRoute(directory) }
			}
			recoveryErr = stopper(a.runtimeDirectory())
		}
	}

	a.moduleVoiceMu.Lock()
	ownsRecovery := a.moduleVoiceRecoveryInProgress && a.moduleVoiceRecoveryActive == generation
	validRecovery := ownsRecovery && a.moduleVoiceRecoveryGeneration == generation && a.moduleVoiceRecoverySession == sessionID
	if recoveryErr == nil && ownsRecovery {
		// A replacement route cannot start while recovery is in progress, so the
		// route stopped by this owner is still the old route even if invalidated.
		a.clearStoppedModuleVoiceRouteLocked()
	}
	if recoveryErr == nil && validRecovery {
		a.moduleVoiceRouteAttemptedSession = ""
		a.moduleVoiceRouteLastError = ""
	}
	if ownsRecovery {
		a.moduleVoiceRecoveryInProgress = false
		a.moduleVoiceRecoveryActive = 0
	}
	if recoveryErr == nil {
		a.moduleVoiceTeardownInProgress = false
	}
	a.moduleVoiceMu.Unlock()

	if recoveryErr != nil && validRecovery {
		a.moduleVoiceMu.Lock()
		a.moduleVoiceRecoveryExhausted = sessionID
		a.moduleVoiceMu.Unlock()
		a.agentAudioMu.Lock()
		a.agentAudio.CaptureError = "模块音频自动恢复失败，需要授权重启 CoreAudio：" + recoveryErr.Error()
		a.agentAudioMu.Unlock()
	}
	return recoveryErr == nil
}

func (a *app) startVoiceVerificationAudio(parent context.Context, sessionID string) {
	transport := a.agentAudioSnapshot()
	if !transport.PCMTransportAvailable || strings.TrimSpace(transport.SocketPath) == "" {
		a.stopVoiceVerificationAudio()
		a.agentAudioMu.Lock()
		a.agentAudio.CaptureError = "agent_pcm_transport_unavailable"
		a.agentAudioMu.Unlock()
		return
	}

	a.agentPCMMu.Lock()
	if a.agentPCMCancel != nil && a.agentPCMSessionID == sessionID {
		a.agentPCMMu.Unlock()
		return
	}
	if a.agentPCMCancel != nil {
		a.agentPCMCancel()
	}
	a.agentPCMGeneration++
	generation := a.agentPCMGeneration
	ctx, cancel := context.WithCancel(parent)
	a.agentPCMCancel = cancel
	a.agentPCMSessionID = sessionID
	runner := a.agentPCMRunner
	if runner == nil {
		runner = runAgentPCMClient
	}
	// Commit the runtime state while holding the same lifecycle lock that
	// stop/completion use, so an old start cannot resurrect capture after stop.
	a.agentAudioMu.Lock()
	if a.agentAudio.CaptureSessionID != sessionID {
		a.clearCurrentTranscriptLocked()
	}
	a.agentAudio.CaptureActive = true
	a.agentAudio.CaptureSessionID = sessionID
	a.agentAudio.CaptureStartedAt = time.Now()
	a.agentAudio.FramesReceived = 0
	a.agentAudio.NonzeroFrames = 0
	a.agentAudio.BytesReceived = 0
	a.agentAudio.PeakSample = 0
	a.agentAudio.CaptureError = ""
	a.agentAudioMu.Unlock()
	a.agentPCMMu.Unlock()

	go func() {
		err := runner(ctx, transport.SocketPath, func(frame []byte) {
			a.recordAgentPCMFrame(generation, frame)
		})
		a.agentPCMMu.Lock()
		if a.agentPCMGeneration != generation {
			a.agentPCMMu.Unlock()
			return
		}
		a.agentPCMCancel = nil
		a.agentPCMSessionID = ""
		captureError := ""
		if err != nil && !errors.Is(err, context.Canceled) {
			captureError = err.Error()
		}
		recoveryGeneration, recoveryReserved := a.reserveModuleVoiceRecovery(sessionID, err)
		a.agentAudioMu.Lock()
		if completed := a.completedCaptureLocked(captureError, time.Now()); completed != nil {
			a.setLastCaptureLocked(completed, verificationResultRetention)
		}
		a.agentAudio.CaptureActive = false
		a.agentAudio.CaptureSessionID = ""
		a.agentAudio.CaptureStartedAt = time.Time{}
		a.agentAudio.FramesReceived = 0
		a.agentAudio.NonzeroFrames = 0
		a.agentAudio.BytesReceived = 0
		a.agentAudio.PeakSample = 0
		a.agentAudio.CaptureError = captureError
		a.clearCurrentTranscriptLocked()
		a.agentAudioMu.Unlock()
		a.agentPCMMu.Unlock()
		if recoveryReserved {
			a.completeReservedModuleVoiceRecovery(sessionID, recoveryGeneration)
		} else if err != nil && !errors.Is(err, context.Canceled) && isRecoverableModuleIOError(err) {
			a.moduleVoiceMu.Lock()
			a.moduleVoiceRecoveryExhausted = sessionID
			a.moduleVoiceMu.Unlock()
			a.agentAudioMu.Lock()
			a.agentAudio.CaptureError = errModuleVoiceRecoveryExhausted.Error()
			a.agentAudioMu.Unlock()
		}
	}()
}

func (a *app) stopVoiceVerificationAudio() {
	a.agentPCMMu.Lock()
	a.agentPCMGeneration++
	cancel := a.agentPCMCancel
	a.agentPCMCancel = nil
	a.agentPCMSessionID = ""
	a.agentAudioMu.Lock()
	if completed := a.completedCaptureLocked("", time.Now()); completed != nil {
		a.setLastCaptureLocked(completed, verificationResultRetention)
	}
	a.agentAudio.CaptureActive = false
	a.agentAudio.CaptureSessionID = ""
	a.agentAudio.CaptureStartedAt = time.Time{}
	a.agentAudio.FramesReceived = 0
	a.agentAudio.NonzeroFrames = 0
	a.agentAudio.BytesReceived = 0
	a.agentAudio.PeakSample = 0
	a.agentAudio.CaptureError = ""
	a.clearCurrentTranscriptLocked()
	a.agentAudioMu.Unlock()
	a.agentPCMMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *app) recordAgentPCMFrame(generation uint64, frame []byte) {
	if len(frame) != agentPCMFrameBytes {
		return
	}
	a.agentPCMMu.Lock()
	defer a.agentPCMMu.Unlock()
	if a.agentPCMGeneration != generation || a.agentPCMCancel == nil {
		return
	}
	nonzero := false
	peak := 0
	for offset := 0; offset+1 < len(frame); offset += 2 {
		sample := int(int16(binary.LittleEndian.Uint16(frame[offset : offset+2])))
		if sample != 0 {
			nonzero = true
		}
		absolute := sample
		if absolute < 0 {
			absolute = -absolute
		}
		if absolute > peak {
			peak = absolute
		}
	}
	a.agentAudioMu.Lock()
	a.agentAudio.FramesReceived++
	a.agentAudio.BytesReceived += uint64(len(frame))
	if nonzero {
		a.agentAudio.NonzeroFrames++
	}
	if peak > a.agentAudio.PeakSample {
		a.agentAudio.PeakSample = peak
	}
	a.agentAudioMu.Unlock()
}
