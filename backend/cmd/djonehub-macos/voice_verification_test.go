package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVoiceVerificationDisarmAndAgentAudioStopDoNotRestartCapture(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(*app)
	}{
		{name: "disarm", stop: func(instance *app) { instance.disarmVoiceVerification() }},
		{name: "agent audio stop", stop: func(instance *app) {
			instance.agentAudioStop(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/agent/audio/stop", nil))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			starts := make(chan struct{}, 2)
			instance := &app{}
			instance.agentAudio = agentAudioState{Compiled: true, TransportReady: true, SocketPath: "/private/tmp/agent.sock"}
			instance.agentPCMRunner = func(ctx context.Context, _ string, _ func([]byte)) error {
				starts <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}
			instance.voiceVerification = &voiceVerificationWindow{Answered: true, Consumed: true, AudioAuthorized: true, SessionID: "session-1"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			call := callStateInfo{State: "active", Active: true, SessionID: "session-1"}
			instance.reconcileVoiceVerificationAudio(ctx, call)
			<-starts
			test.stop(instance)
			instance.reconcileVoiceVerificationAudio(ctx, call)
			select {
			case <-starts:
				t.Fatal("capture restarted after explicit stop")
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestVoiceVerificationDisarmAndAgentAudioStopCancelCapture(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(*app)
	}{
		{name: "disarm", stop: func(instance *app) { instance.disarmVoiceVerification() }},
		{name: "rearm", stop: func(instance *app) { instance.armVoiceVerification(time.Minute, []string{"10000000000"}) }},
		{name: "agent audio stop", stop: func(instance *app) {
			instance.agentAudioStop(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/agent/audio/stop", nil))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			instance.startVoiceVerificationAudio(ctx, "session-1")
			<-started
			test.stop(instance)
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("capture was not cancelled")
			}
		})
	}
}

func TestVoiceVerificationStopsPCMWhenAuthorizedSessionChanges(t *testing.T) {
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
	instance.voiceVerification = &voiceVerificationWindow{Answered: true, Consumed: true, AudioAuthorized: true, SessionID: "session-1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance.reconcileVoiceVerificationAudio(ctx, callStateInfo{State: "active", Active: true, SessionID: "session-1"})
	<-started
	instance.reconcileVoiceVerificationAudio(ctx, callStateInfo{State: "active", Active: true, SessionID: "session-2"})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("PCM client remained active after call session changed")
	}
}

func TestVoiceVerificationRejectsLateFramesAndClearsSessionMetrics(t *testing.T) {
	callbacks := make(chan func([]byte), 2)
	instance := &app{}
	instance.agentAudio = agentAudioState{Compiled: true, TransportReady: true, SocketPath: "/private/tmp/agent.sock"}
	instance.agentPCMRunner = func(ctx context.Context, _ string, onPCM func([]byte)) error {
		callbacks <- onPCM
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance.startVoiceVerificationAudio(ctx, "session-1")
	oldCallback := <-callbacks
	instance.startVoiceVerificationAudio(ctx, "session-2")
	newCallback := <-callbacks
	oldFrame := make([]byte, 320)
	oldFrame[0] = 1
	oldCallback(oldFrame)
	if state := instance.agentAudioSnapshot(); state.FramesReceived != 0 {
		t.Fatalf("late frame from old session was counted: %+v", state)
	}
	newFrame := make([]byte, 320)
	newFrame[0] = 2
	newCallback(newFrame)
	if state := instance.agentAudioSnapshot(); state.FramesReceived != 1 || state.CaptureSessionID != "session-2" {
		t.Fatalf("new session frame was not counted: %+v", state)
	}
	instance.stopVoiceVerificationAudio()
	state := instance.agentAudioSnapshot()
	if state.CaptureSessionID != "" || state.FramesReceived != 0 || state.NonzeroFrames != 0 || state.BytesReceived != 0 || state.PeakSample != 0 || state.CaptureError != "" || state.LastCapture == nil || state.LastCapture.SessionID != "session-2" || state.LastCapture.FramesReceived != 1 || state.LastCapture.NonzeroFrames != 1 {
		t.Fatalf("capture identity or metrics survived stop incorrectly: %+v", state)
	}
}

func TestVoiceVerificationStartsPCMForAnsweredActiveCallAndStopsOnIdle(t *testing.T) {
	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	instance := &app{}
	instance.agentAudio = agentAudioState{Compiled: true, TransportReady: true, SocketPath: "/private/tmp/agent.sock"}
	instance.agentPCMRunner = func(ctx context.Context, _ string, onPCM func([]byte)) error {
		started <- struct{}{}
		pcm := make([]byte, 320)
		pcm[0], pcm[1] = 1, 0
		onPCM(pcm)
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	}
	instance.voiceVerification = &voiceVerificationWindow{Answered: true, Consumed: true, AudioAuthorized: true, SessionID: "session-1"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	instance.reconcileVoiceVerificationAudio(ctx, callStateInfo{State: "active", Active: true, SessionID: "session-1"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("PCM client was not started")
	}
	deadline := time.Now().Add(time.Second)
	for {
		state := instance.agentAudioSnapshot()
		if state.CaptureActive && state.FramesReceived == 1 && state.NonzeroFrames == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("PCM metrics not updated: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}

	instance.reconcileVoiceVerificationAudio(ctx, callStateInfo{State: "idle"})
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("PCM client was not stopped")
	}
	if state := instance.agentAudioSnapshot(); state.CaptureActive {
		t.Fatalf("capture remained active: %+v", state)
	}
}

func TestVoiceVerificationDisarmClearsSensitiveWindowMetadata(t *testing.T) {
	instance := &app{}
	instance.voiceVerification = &voiceVerificationWindow{
		Active:          true,
		ExpiresAt:       time.Now().Add(time.Minute),
		AllowedNumbers:  []string{"10000000000"},
		Answered:        true,
		SessionID:       "session-sensitive",
		Consumed:        true,
		AudioAuthorized: true,
		LastError:       "sensitive failure",
		answering:       true,
		attemptNumber:   "10000000000",
		attemptSession:  "session-sensitive",
		attemptCallID:   7,
	}

	instance.disarmVoiceVerification()
	window := instance.voiceVerificationSnapshot()
	if window.Active || !window.ExpiresAt.IsZero() || len(window.AllowedNumbers) != 0 || window.Answered || window.SessionID != "" || window.Consumed || window.AudioAuthorized || window.LastError != "" || window.answering || window.attemptNumber != "" || window.attemptSession != "" || window.attemptCallID != 0 {
		t.Fatalf("disarm retained sensitive metadata: %+v", window)
	}
}

func TestVoiceVerificationExpiryClearsSensitiveWindowMetadata(t *testing.T) {
	instance := &app{}
	instance.voiceVerification = &voiceVerificationWindow{
		Active:          true,
		ExpiresAt:       time.Now().Add(-time.Second),
		AllowedNumbers:  []string{"10000000000"},
		SessionID:       "session-sensitive",
		Answered:        true,
		Consumed:        true,
		AudioAuthorized: true,
		LastError:       "sensitive failure",
		answering:       true,
		attemptNumber:   "10000000000",
		attemptSession:  "session-sensitive",
		attemptCallID:   7,
	}

	window := instance.voiceVerificationSnapshot()
	if window.Active || !window.ExpiresAt.IsZero() || len(window.AllowedNumbers) != 0 || window.SessionID != "" || window.Answered || window.Consumed || window.AudioAuthorized || window.LastError != "" || window.answering || window.attemptNumber != "" || window.attemptSession != "" || window.attemptCallID != 0 {
		t.Fatalf("expiry retained sensitive metadata: %+v", window)
	}
}

func TestVoiceVerificationAutomationProactivelyClearsExpiredSensitiveMetadata(t *testing.T) {
	instance := &app{}
	instance.voiceVerification = &voiceVerificationWindow{
		Active:          true,
		ExpiresAt:       time.Now().Add(50 * time.Millisecond),
		AllowedNumbers:  []string{"10000000000"},
		SessionID:       "session-sensitive",
		Answered:        true,
		Consumed:        true,
		AudioAuthorized: true,
		LastError:       "sensitive failure",
		answering:       true,
		attemptNumber:   "10000000000",
		attemptSession:  "session-sensitive",
		attemptCallID:   7,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		instance.runVoiceVerificationAutomation(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		instance.voiceVerificationMu.Lock()
		window := *instance.voiceVerification
		instance.voiceVerificationMu.Unlock()
		if !window.Active && window.ExpiresAt.IsZero() && len(window.AllowedNumbers) == 0 && window.SessionID == "" && !window.Answered && !window.Consumed && !window.AudioAuthorized && window.LastError == "" && !window.answering && window.attemptNumber == "" && window.attemptSession == "" && window.attemptCallID == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	instance.voiceVerificationMu.Lock()
	window := *instance.voiceVerification
	instance.voiceVerificationMu.Unlock()
	t.Fatalf("automation did not proactively clear expired sensitive metadata: %+v", window)
}

func TestVoiceVerificationInactiveJSONOmitsExpiredMetadata(t *testing.T) {
	window := voiceVerificationWindow{AllowedNumbers: nil}
	encoded, err := json.Marshal(window)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, `"expires_at"`) || strings.Contains(text, `"session_id"`) || strings.Contains(text, `"last_error"`) {
		t.Fatalf("inactive status serialized cleared metadata: %s", text)
	}
	if !strings.Contains(text, `"allowed_numbers":[]`) {
		t.Fatalf("inactive status must retain a stable empty allowed_numbers array: %s", text)
	}
}

func TestVoiceVerificationWindowIsSingleUseAndScoped(t *testing.T) {
	instance := &app{}
	window := instance.armVoiceVerification(5*time.Minute, []string{"10000000000"})
	if !window.Active || len(window.AllowedNumbers) != 1 {
		t.Fatalf("unexpected window: %+v", window)
	}
	if !instance.voiceVerificationNumberAllowed("10000000000") {
		t.Fatal("allowed number was rejected")
	}
	if instance.voiceVerificationNumberAllowed("10000000001") {
		t.Fatal("unlisted number was allowed")
	}
	instance.updateVoiceVerification(func(current *voiceVerificationWindow) {
		current.Active = false
		current.Answered = true
		current.Consumed = true
	})
	if got := instance.voiceVerificationSnapshot(); got.Active || !got.Answered || !got.Consumed {
		t.Fatalf("window was not consumed: %+v", got)
	}
}

type testIncomingCall struct {
	mu        sync.Mutex
	number    string
	sessionID string
	callID    int
}

func (call *testIncomingCall) install(instance *app) {
	instance.callCommand = func(command, _ string, _ time.Duration, beforeWrite func() error) (string, error) {
		if beforeWrite != nil {
			if err := beforeWrite(); err != nil {
				return "", err
			}
		}
		if command != "AT+CLCC" {
			return "OK", nil
		}
		call.mu.Lock()
		defer call.mu.Unlock()
		if call.callID == 0 {
			call.callID = 1
		}
		instance.callSessionMu.Lock()
		instance.callSession = &callSession{id: call.sessionID, number: call.number, direction: "in"}
		instance.callSessionMu.Unlock()
		return fmt.Sprintf("+CLCC: %d,1,4,0,0,\"%s\",129\r\nOK", call.callID, call.number), nil
	}
}

func (call *testIncomingCall) replace(number, sessionID string) {
	call.mu.Lock()
	call.number = number
	call.sessionID = sessionID
	call.mu.Unlock()
}

func (call *testIncomingCall) replaceCallID(callID int) {
	call.mu.Lock()
	call.callID = callID
	call.mu.Unlock()
}

func TestVoiceVerificationPrewarmsModuleRouteBeforeAnswerAndConsumesOnce(t *testing.T) {
	events := make(chan string, 4)
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStart: func(string) error {
			events <- "route"
			return nil
		},
		voiceVerificationAnswer: func() error {
			events <- "answer"
			return nil
		},
	}
	current := &testIncomingCall{number: "10000000000", sessionID: "incoming-session"}
	current.install(instance)
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	call := callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 1}
	if !instance.handleVoiceVerificationIncoming(call) {
		t.Fatal("authorized incoming call was not handled")
	}
	for _, want := range []string{"route", "answer"} {
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("event=%q want=%q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
	window := instance.voiceVerificationSnapshot()
	if window.Active || !window.Answered || !window.Consumed || !window.AudioAuthorized || window.SessionID != "incoming-session" {
		t.Fatalf("window not consumed after prewarmed answer: %+v", window)
	}
	instance.handleVoiceVerificationIncoming(call)
	select {
	case event := <-events:
		t.Fatalf("single-use window performed extra action: %s", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestVoiceVerificationInvalidatesBlockedPrewarm(t *testing.T) {
	for _, test := range []struct {
		name       string
		invalidate func(*app, *testIncomingCall)
		assert     func(*testing.T, voiceVerificationWindow)
	}{
		{
			name: "disarm",
			invalidate: func(instance *app, _ *testIncomingCall) {
				instance.disarmVoiceVerification()
			},
			assert: func(t *testing.T, window voiceVerificationWindow) {
				if window.Active || window.Answered || window.AudioAuthorized {
					t.Fatalf("disarmed window was changed by stale attempt: %+v", window)
				}
			},
		},
		{
			name: "rearm",
			invalidate: func(instance *app, _ *testIncomingCall) {
				instance.armVoiceVerification(time.Minute, []string{"10000000001"})
			},
			assert: func(t *testing.T, window voiceVerificationWindow) {
				if !window.Active || window.Consumed || window.Answered || len(window.AllowedNumbers) != 1 || window.AllowedNumbers[0] != "10000000001" || window.LastError != "" {
					t.Fatalf("stale attempt polluted replacement window: %+v", window)
				}
			},
		},
		{
			name: "expiry",
			invalidate: func(instance *app, _ *testIncomingCall) {
				instance.updateVoiceVerification(func(window *voiceVerificationWindow) {
					window.ExpiresAt = time.Now().Add(-time.Second)
				})
				_ = instance.voiceVerificationSnapshot()
			},
			assert: func(t *testing.T, window voiceVerificationWindow) {
				if window.Active || window.Answered || window.AudioAuthorized {
					t.Fatalf("expired window was changed by stale attempt: %+v", window)
				}
			},
		},
		{
			name: "incoming replacement",
			invalidate: func(_ *app, current *testIncomingCall) {
				current.replace("10000000001", "replacement-session")
			},
			assert: func(t *testing.T, window voiceVerificationWindow) {
				if window.Active || !window.Consumed || window.Answered || window.AudioAuthorized || window.LastError == "" {
					t.Fatalf("replaced incoming call did not terminate attempt safely: %+v", window)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			var answers atomic.Int32
			var stops atomic.Int32
			instance := &app{
				moduleVoiceAutoStart: true,
				moduleVoiceRouteStart: func(string) error {
					close(started)
					<-release
					return nil
				},
				moduleVoiceRouteStop: func(string) error {
					stops.Add(1)
					return nil
				},
				voiceVerificationAnswer: func() error {
					answers.Add(1)
					return nil
				},
			}
			current := &testIncomingCall{number: "10000000000", sessionID: "incoming-session"}
			current.install(instance)
			instance.armVoiceVerification(time.Minute, []string{"10000000000"})
			done := make(chan struct{})
			go func() {
				instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 1})
				close(done)
			}()
			<-started
			test.invalidate(instance, current)
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("blocked prewarm did not finish")
			}
			if answers.Load() != 0 {
				t.Fatalf("stale attempt answered %d calls", answers.Load())
			}
			if stops.Load() != 1 {
				t.Fatalf("owned prewarm route stops=%d want=1", stops.Load())
			}
			test.assert(t, instance.voiceVerificationSnapshot())
		})
	}
}

func TestVoiceVerificationRejectsReplacedIncomingCallIDAfterPrewarm(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var answers atomic.Int32
	var stops atomic.Int32
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStart: func(string) error {
			close(started)
			<-release
			return nil
		},
		moduleVoiceRouteStop: func(string) error {
			stops.Add(1)
			return nil
		},
		voiceVerificationAnswer: func() error {
			answers.Add(1)
			return nil
		},
	}
	current := &testIncomingCall{number: "10000000000", sessionID: "incoming-session", callID: 7}
	current.install(instance)
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	done := make(chan struct{})
	go func() {
		instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 7})
		close(done)
	}()
	<-started
	current.replaceCallID(8)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prewarm did not finish after call ID replacement")
	}
	window := instance.voiceVerificationSnapshot()
	if answers.Load() != 0 || stops.Load() != 1 || window.Active || !window.Consumed || window.LastError == "" {
		t.Fatalf("replaced call ID was not rejected: window=%+v answers=%d stops=%d", window, answers.Load(), stops.Load())
	}
}

func TestVoiceVerificationPrewarmFailureConsumesWindowAndRollsBackOnce(t *testing.T) {
	var starts atomic.Int32
	var stops atomic.Int32
	var answers atomic.Int32
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStart: func(string) error {
			starts.Add(1)
			return errors.New("partial route failure")
		},
		moduleVoiceRouteStop: func(string) error {
			stops.Add(1)
			return nil
		},
		voiceVerificationAnswer: func() error {
			answers.Add(1)
			return nil
		},
	}
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	call := callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 1}
	instance.handleVoiceVerificationIncoming(call)
	instance.handleVoiceVerificationIncoming(call)
	window := instance.voiceVerificationSnapshot()
	if window.Active || !window.Consumed || window.Answered || window.LastError == "" {
		t.Fatalf("prewarm failure did not consume window: %+v", window)
	}
	if starts.Load() != 1 || stops.Load() != 1 || answers.Load() != 0 {
		t.Fatalf("starts=%d stops=%d answers=%d want 1,1,0", starts.Load(), stops.Load(), answers.Load())
	}
}

func TestVoiceVerificationRollsBackStarterThatFinishesAfterTeardown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var stops atomic.Int32
	var answers atomic.Int32
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStart: func(string) error {
			close(started)
			<-release
			return nil
		},
		moduleVoiceRouteStop: func(string) error {
			stops.Add(1)
			return nil
		},
		voiceVerificationAnswer: func() error {
			answers.Add(1)
			return nil
		},
	}
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	done := make(chan struct{})
	go func() {
		instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 1})
		close(done)
	}()
	<-started
	if err := instance.stopModuleVoiceRouteSafely(time.Second); err != nil {
		t.Fatalf("concurrent teardown failed: %v", err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale starter did not finish")
	}
	window := instance.voiceVerificationSnapshot()
	if answers.Load() != 0 || stops.Load() != 1 || window.Active || !window.Consumed || window.LastError == "" {
		t.Fatalf("late starter side effect was not rolled back: window=%+v answers=%d stops=%d", window, answers.Load(), stops.Load())
	}
}

func TestVoiceVerificationRearmWaitsForStalePrewarmOwnership(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var starts atomic.Int32
	var stops atomic.Int32
	var answers atomic.Int32
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStart: func(string) error {
			if starts.Add(1) == 1 {
				close(firstStarted)
				<-releaseFirst
			}
			return nil
		},
		moduleVoiceRouteStop: func(string) error {
			stops.Add(1)
			return nil
		},
		voiceVerificationAnswer: func() error {
			answers.Add(1)
			return nil
		},
	}
	current := &testIncomingCall{number: "10000000000", sessionID: "old-session"}
	current.install(instance)
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	oldDone := make(chan struct{})
	go func() {
		instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "old-session", moduleCallID: 1})
		close(oldDone)
	}()
	<-firstStarted

	current.replace("10000000001", "new-session")
	instance.armVoiceVerification(time.Minute, []string{"10000000001"})
	instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000001", SessionID: "new-session", moduleCallID: 1})
	window := instance.voiceVerificationSnapshot()
	if !window.Active || window.Consumed || window.LastError != "" || starts.Load() != 1 || answers.Load() != 0 {
		t.Fatalf("replacement window was polluted while stale prewarm owned transition: window=%+v starts=%d answers=%d", window, starts.Load(), answers.Load())
	}

	close(releaseFirst)
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("stale prewarm did not release ownership")
	}
	instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000001", SessionID: "new-session", moduleCallID: 1})
	window = instance.voiceVerificationSnapshot()
	if window.Active || !window.Answered || !window.Consumed || starts.Load() != 2 || stops.Load() != 1 || answers.Load() != 1 {
		t.Fatalf("replacement did not proceed after stale cleanup: window=%+v starts=%d stops=%d answers=%d", window, starts.Load(), stops.Load(), answers.Load())
	}
}

func TestVoiceVerificationAnswerFailureConsumesWindowAndStopsOwnedRouteOnce(t *testing.T) {
	var starts atomic.Int32
	var stops atomic.Int32
	var answers atomic.Int32
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStart: func(string) error {
			starts.Add(1)
			return nil
		},
		moduleVoiceRouteStop: func(string) error {
			stops.Add(1)
			return nil
		},
		voiceVerificationAnswer: func() error {
			answers.Add(1)
			return errors.New("ATA failed")
		},
	}
	current := &testIncomingCall{number: "10000000000", sessionID: "incoming-session"}
	current.install(instance)
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	call := callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 1}
	instance.handleVoiceVerificationIncoming(call)
	instance.handleVoiceVerificationIncoming(call)
	window := instance.voiceVerificationSnapshot()
	if window.Active || !window.Consumed || window.Answered || window.AudioAuthorized || window.LastError == "" {
		t.Fatalf("answer failure did not consume window: %+v", window)
	}
	if starts.Load() != 1 || stops.Load() != 1 || answers.Load() != 1 {
		t.Fatalf("starts=%d stops=%d answers=%d want 1,1,1", starts.Load(), stops.Load(), answers.Load())
	}
}

func TestVoiceVerificationAnswerFailureDoesNotStopPreexistingRoute(t *testing.T) {
	var stops atomic.Int32
	instance := &app{
		moduleVoiceAutoStart: true,
		moduleVoiceRouteStop: func(string) error {
			stops.Add(1)
			return nil
		},
		voiceVerificationAnswer: func() error { return errors.New("ATA failed") },
	}
	instance.moduleVoiceRoute = true
	current := &testIncomingCall{number: "10000000000", sessionID: "incoming-session"}
	current.install(instance)
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Incoming: true, Number: "10000000000", SessionID: "incoming-session", moduleCallID: 1})
	if stops.Load() != 0 {
		t.Fatalf("preexisting route was stopped %d times", stops.Load())
	}
	instance.moduleVoiceMu.Lock()
	routeActive := instance.moduleVoiceRoute
	instance.moduleVoiceMu.Unlock()
	if !routeActive {
		t.Fatal("preexisting route was cleared")
	}
}

func TestVoiceVerificationWindowExpires(t *testing.T) {
	instance := &app{}
	instance.armVoiceVerification(30*time.Second, []string{"10000000000"})
	instance.updateVoiceVerification(func(current *voiceVerificationWindow) {
		current.ExpiresAt = time.Now().Add(-time.Second)
	})
	if got := instance.voiceVerificationSnapshot(); got.Active {
		t.Fatalf("expired window remained active: %+v", got)
	}
}

func TestVoiceVerificationDefersAllTransientPrewarmStates(t *testing.T) {
	for _, state := range []struct {
		name string
		set  func(*app)
	}{
		{"start", func(a *app) { a.moduleVoiceRouteStarting = true }},
		{"recovery", func(a *app) { a.moduleVoiceRecoveryInProgress = true }},
		{"teardown", func(a *app) { a.moduleVoiceTeardownInProgress = true }},
	} {
		t.Run(state.name, func(t *testing.T) {
			instance := &app{moduleVoiceAutoStart: true}
			instance.moduleVoiceMu.Lock()
			state.set(instance)
			instance.moduleVoiceMu.Unlock()
			instance.armVoiceVerification(time.Minute, []string{"10000000000"})
			instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Number: "10000000000", SessionID: "s"})
			window := instance.voiceVerificationSnapshot()
			if !window.Active || window.Consumed || window.LastError != "" {
				t.Fatalf("transient state consumed window: %+v", window)
			}
		})
	}
}

func TestVoiceVerificationMatchesNormalizedNumbersAndRejectsUnknownCallID(t *testing.T) {
	instance := &app{}
	instance.armVoiceVerification(time.Minute, []string{"+86 187-4258-9885"})
	if !instance.voiceVerificationNumberAllowed("+86 (187) 4258 9885") {
		t.Fatal("normalized allowed number rejected")
	}
	attempt, _, handled := instance.reserveVoiceVerificationAttempt(callStateInfo{State: "incoming", Number: "+86 (187) 4258 9885", SessionID: "s", moduleCallID: 0})
	if !handled || attempt.callID != 0 {
		t.Fatalf("unexpected attempt: %+v handled=%v", attempt, handled)
	}
	instance.voiceVerificationMu.Lock()
	valid := instance.voiceVerificationAttemptValidLocked(attempt, callStateInfo{State: "incoming", Number: "8610000000000", SessionID: "s", moduleCallID: 7})
	instance.voiceVerificationMu.Unlock()
	if valid {
		t.Fatal("callID=0 was treated as a wildcard")
	}
}

func TestVoiceVerificationReportsRollbackFailureOnIdentityChange(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	instance := &app{moduleVoiceAutoStart: true, moduleVoiceRouteStart: func(string) error { close(started); <-release; return nil }, moduleVoiceRouteStop: func(string) error { return errors.New("rollback failed") }}
	current := &testIncomingCall{number: "10000000000", sessionID: "s"}
	current.install(instance)
	instance.armVoiceVerification(time.Minute, []string{"10000000000"})
	done := make(chan struct{})
	go func() {
		instance.handleVoiceVerificationIncoming(callStateInfo{State: "incoming", Number: "10000000000", SessionID: "s"})
		close(done)
	}()
	<-started
	current.replace("10000000001", "replacement")
	close(release)
	<-done
	if got := instance.voiceVerificationSnapshot().LastError; !strings.Contains(got, "rollback failed") {
		t.Fatalf("rollback error was swallowed: %q", got)
	}
}
