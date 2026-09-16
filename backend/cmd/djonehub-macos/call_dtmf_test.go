package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/iniwex5/vohive/internal/callnumber"
)

type callTestTransport struct {
	mu          sync.Mutex
	state       string
	commands    []string
	toneTimes   []time.Time
	toneError   error
	hangupError error
	beforeTone  func()
}

func (f *callTestTransport) command(command, redacted string, _ time.Duration, beforeWrite func() error) (string, error) {
	if strings.HasPrefix(command, "AT+VTS=") {
		if redacted != "AT+VTS=<redacted>" {
			return "", errors.New("tone was not redacted")
		}
		if f.beforeTone != nil {
			f.beforeTone()
		}
	}
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return "", err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	switch {
	case command == "AT+CLCC":
		return f.state, nil
	case command == "ATH":
		return "OK", f.hangupError
	case strings.HasPrefix(command, "AT+VTS="):
		f.toneTimes = append(f.toneTimes, time.Now())
		return "OK", f.toneError
	default:
		return "OK", nil
	}
}

func (f *callTestTransport) setState(id, direction, state int, number string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = fmt.Sprintf("+CLCC: %d,%d,%d,0,0,%q,129\r\nOK", id, direction, state, number)
}

func (f *callTestTransport) sentTones() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var tones []string
	for _, command := range f.commands {
		if strings.HasPrefix(command, "AT+VTS=") {
			tones = append(tones, command)
		}
	}
	return tones
}

func newCallTestApp(t *testing.T) (*app, *callTestTransport) {
	t.Helper()
	transport := &callTestTransport{state: "OK"}
	a := &app{callCommand: transport.command, callLog: newCallLog(filepath.Join(t.TempDir(), "calls.json"))}
	return a, transport
}

func placeTestCall(t *testing.T, a *app, value string) {
	t.Helper()
	plan, err := callnumber.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	a.callControlMu.Lock()
	err = a.placeCall(plan)
	a.callControlMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func performDTMF(a *app, sessionID, tone string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"session_id": sessionID, "tone": tone})
	request := httptest.NewRequest(http.MethodPost, "/api/call/dtmf", bytes.NewReader(body))
	response := httptest.NewRecorder()
	a.callDTMF(response, request)
	return response
}

func TestPostDialWaitsForActivePausesInOrderAndNeverReplays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 2, "+8613800138000")
		placeTestCall(t, a, "+8613800138000,,12,#")
		time.Sleep(time.Second)
		synctest.Wait()
		if got := transport.sentTones(); len(got) != 0 {
			t.Fatalf("sent before connection: %v", got)
		}
		transport.setState(1, 0, 0, "+8613800138000")
		a.currentCallState()
		synctest.Wait()
		time.Sleep(4*time.Second - time.Millisecond)
		synctest.Wait()
		if got := transport.sentTones(); len(got) != 0 {
			t.Fatalf("consecutive pauses did not add up: %v", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if got := transport.sentTones(); !reflect.DeepEqual(got, []string{`AT+VTS="1",1`, `AT+VTS="2",1`}) {
			t.Fatalf("first extension segment = %v", got)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		for range 5 {
			a.currentCallState()
		}
		if got := transport.sentTones(); !reflect.DeepEqual(got, []string{`AT+VTS="1",1`, `AT+VTS="2",1`, `AT+VTS="#",1`}) {
			t.Fatalf("extension duplicated or reordered: %v", got)
		}
		if state := a.currentCallState(); state.PostDialState != "completed" || !state.DTMFAvailable {
			t.Fatalf("completion state = %+v", state)
		}
		transport.mu.Lock()
		first := transport.commands[0]
		transport.mu.Unlock()
		if first != "ATD+8613800138000;" {
			t.Fatalf("ATD included post-dial input: %s", first)
		}
		a.endCall()
		stored := newCallLog(a.callLog.path).snapshot()
		if len(stored) != 1 || stored[0].Number != "+8613800138000" || stored[0].DialString != "+8613800138000,,12,#" {
			t.Fatalf("CLCC overwrote callback input: %+v", stored)
		}
	})
}

func TestPostDialMonitorWorksWithoutClientPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		placeTestCall(t, a, "10086,1")
		synctest.Wait()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if got := transport.sentTones(); !reflect.DeepEqual(got, []string{`AT+VTS="1",1`}) {
			t.Fatalf("CLI post-dial did not run autonomously: %v", got)
		}
	})
}

func TestFirstCLCCMayArriveLateWithInternationalNumber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		placeTestCall(t, a, "13800138000,1")
		initial := a.currentCallState()
		if initial.State != "dialing" || initial.DTMFAvailable {
			t.Fatalf("empty first CLCC discarded accepted call: %+v", initial)
		}
		time.Sleep(time.Second)
		transport.setState(1, 0, 0, "+8613800138000")
		active := a.currentCallState()
		if active.SessionID != initial.SessionID || active.PostDialState != "sending" {
			t.Fatalf("normalized first CLCC replaced original call: %+v", active)
		}
		synctest.Wait()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := transport.sentTones(); !reflect.DeepEqual(got, []string{`AT+VTS="1",1`}) {
			t.Fatalf("extension lost after number normalization: %v", got)
		}
		a.endCall()
		if record := a.callLog.snapshot()[0]; record.DialString != "13800138000,1" {
			t.Fatalf("original callback number was not retained: %+v", record)
		}
	})
}

func TestFirstCLCCGraceExpiresAndNeverSendsUnconfirmedTones(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		placeTestCall(t, a, "10086,1")
		synctest.Wait()
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if state := a.currentCallState(); state.State != "idle" || len(transport.sentTones()) != 0 {
			t.Fatalf("unconfirmed call outlived initial grace: %+v", state)
		}
	})
}

func TestUnknownOrHeldStateCancelsAutomaticSuffix(t *testing.T) {
	for _, response := range []string{"ERROR", `+CLCC: 1,0,1,0,0,"10086",129` + "\r\nOK"} {
		t.Run(response, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a, transport := newCallTestApp(t)
				defer a.endCall()
				transport.setState(1, 0, 0, "10086")
				placeTestCall(t, a, "10086,,123")
				a.currentCallState()
				synctest.Wait()
				transport.mu.Lock()
				transport.state = response
				transport.mu.Unlock()
				a.currentCallState()
				transport.setState(1, 0, 0, "10086")
				state := a.currentCallState()
				time.Sleep(10 * time.Second)
				synctest.Wait()
				if state.PostDialState != "canceled" || !state.DTMFAvailable || len(transport.sentTones()) != 0 {
					t.Fatalf("unsafe automatic resume: %+v", state)
				}
			})
		})
	}
}

func TestCLCCIgnoresFirmwareResidueBeforeChoosingToneTarget(t *testing.T) {
	live := `+CLCC: 1,0,0,0,0,"10086",129`
	for _, ignored := range []string{
		`+CLCC: 9,1,0,0,0,"",129`,
		`+CLCC: 9,1,6,0,0,"10010",129`,
		`+CLCC: 9,1,0,1,0,"10010",129`,
	} {
		for _, rows := range []string{live + "\r\n" + ignored, ignored + "\r\n" + live} {
			t.Run(rows, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					a, transport := newCallTestApp(t)
					defer a.endCall()
					transport.state = rows + "\r\nOK"
					placeTestCall(t, a, "10086,1")
					state := a.currentCallState()
					if state.PostDialState != "sending" || state.Number != "10086" {
						t.Fatalf("residue selected instead of live call: %+v", state)
					}
					synctest.Wait()
					time.Sleep(2 * time.Second)
					synctest.Wait()
					current := a.currentCallState()
					if current.SessionID != state.SessionID || !current.DTMFAvailable || len(transport.sentTones()) != 1 {
						t.Fatalf("residue blocked or replaced tone target: %+v", current)
					}
				})
			})
		}
	}
}

func TestMultipleRealCallsNeverReceiveAutomaticOrManualTones(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.state = "+CLCC: 1,0,0,0,0,\"10086\",129\r\n+CLCC: 2,1,5,0,0,\"10010\",129\r\nOK"
		placeTestCall(t, a, "10086,1")
		state := a.currentCallState()
		if state.DTMFAvailable || state.PostDialState != "canceled" {
			t.Fatalf("ambiguous call permits DTMF: %+v", state)
		}
		if result := performDTMF(a, state.SessionID, "1"); result.Code != http.StatusConflict {
			t.Fatalf("manual key accepted with two calls: %d", result.Code)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if got := transport.sentTones(); len(got) != 0 {
			t.Fatalf("sent to ambiguous target: %v", got)
		}
	})
}

func TestRepeatedTonesHaveCancelableSpacing(t *testing.T) {
	for _, hangupDuringGap := range []bool{false, true} {
		t.Run(fmt.Sprint(hangupDuringGap), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a, transport := newCallTestApp(t)
				defer a.endCall()
				transport.setState(1, 0, 0, "10086")
				placeTestCall(t, a, "10086,11")
				a.currentCallState()
				synctest.Wait()
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if len(transport.sentTones()) != 1 {
					t.Fatal("repeated digits had no intervening gap")
				}
				if hangupDuringGap {
					started := time.Now()
					response := httptest.NewRecorder()
					a.callHangup(response, httptest.NewRequest(http.MethodPost, "/api/call/hangup", nil))
					if response.Code != http.StatusOK || time.Since(started) != 0 {
						t.Fatal("hangup waited for inter-tone spacing")
					}
				}
				time.Sleep(100 * time.Millisecond)
				synctest.Wait()
				if hangupDuringGap {
					if len(transport.sentTones()) != 1 {
						t.Fatal("second repeated digit escaped hangup")
					}
					return
				}
				transport.mu.Lock()
				defer transport.mu.Unlock()
				if len(transport.toneTimes) != 2 || transport.toneTimes[1].Sub(transport.toneTimes[0]) < 100*time.Millisecond {
					t.Fatalf("repeated digit timing = %v", transport.toneTimes)
				}
			})
		})
	}
}

func TestHangupCancelsCommaPauseWithoutWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		placeTestCall(t, a, "10086,,,,123")
		a.currentCallState()
		synctest.Wait()
		started := time.Now()
		response := httptest.NewRecorder()
		a.callHangup(response, httptest.NewRequest(http.MethodPost, "/api/call/hangup", nil))
		if response.Code != http.StatusOK || time.Since(started) != 0 {
			t.Fatalf("hangup waited for a pause: status=%d delay=%s", response.Code, time.Since(started))
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if got := transport.sentTones(); len(got) != 0 {
			t.Fatalf("sent after hangup: %v", got)
		}
	})
}

func TestDTMFQueuedRequestsKeepClickOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		id := a.currentCallState().SessionID
		entered, release := make(chan struct{}), make(chan struct{})
		var first sync.Once
		transport.beforeTone = func() { first.Do(func() { close(entered); <-release }) }
		results := make(chan *httptest.ResponseRecorder, 3)
		go func() { results <- performDTMF(a, id, "1") }()
		<-entered
		go func() { results <- performDTMF(a, id, "*") }()
		synctest.Wait()
		go func() { results <- performDTMF(a, id, "#") }()
		synctest.Wait()
		close(release)
		for range 3 {
			if result := <-results; result.Code != http.StatusOK {
				t.Fatalf("tone failed: %s", result.Body)
			}
		}
		if got := transport.sentTones(); !reflect.DeepEqual(got, []string{`AT+VTS="1",1`, `AT+VTS="*",1`, `AT+VTS="#",1`}) {
			t.Fatalf("queued keys reordered: %v", got)
		}
		a.endCall()
		if history := a.callLog.snapshot(); history[0].DialString != "" || history[0].Number != "10086" {
			t.Fatalf("manual input leaked into history: %+v", history)
		}
	})
}

func TestDTMFCanceledInTransportQueueCannotReachNextCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		oldID := a.currentCallState().SessionID
		entered, release := make(chan struct{}), make(chan struct{})
		transport.beforeTone = func() { close(entered); <-release }
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() { result <- performDTMF(a, oldID, "9") }()
		<-entered
		a.onURCHangup()
		a.beginCall("out", "10010")
		close(release)
		if response := <-result; response.Code == http.StatusOK {
			t.Fatal("canceled request reported success")
		}
		synctest.Wait()
		if got := transport.sentTones(); len(got) != 0 {
			t.Fatalf("stale key escaped transport queue: %v", got)
		}
	})
}

func TestDTMFDetectsReplacementUsingAllCLCCIdentityFields(t *testing.T) {
	for _, replacement := range []struct {
		name          string
		id, direction int
		number        string
	}{
		{"number", 1, 0, "10010"}, {"index", 2, 0, "10086"}, {"direction", 1, 1, "10086"},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			a, transport := newCallTestApp(t)
			defer a.endCall()
			transport.setState(1, 0, 0, "10086")
			oldID := a.currentCallState().SessionID
			transport.setState(replacement.id, replacement.direction, 0, replacement.number)
			if response := performDTMF(a, oldID, "1"); response.Code == http.StatusOK {
				t.Fatal("old call accepted a key after replacement")
			}
			if current := a.currentCallState(); current.SessionID == oldID || len(transport.sentTones()) != 0 {
				t.Fatalf("replacement inherited old call: %+v", current)
			}
		})
	}
}

func TestPostDialTimeoutStopsSuffixAndRedactsResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		transport.toneError = errors.New(`timeout echoed AT+VTS="1" secret-input`)
		placeTestCall(t, a, "10086,12#")
		a.currentCallState()
		synctest.Wait()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		state := a.currentCallState()
		if state.PostDialState != "failed" || strings.Contains(state.PostDialError, "secret-input") || strings.Contains(state.PostDialError, "AT+VTS") {
			t.Fatalf("failure not safely surfaced: %+v", state)
		}
		if len(transport.sentTones()) != 1 {
			t.Fatalf("uncertain key was retried or suffix continued: %v", transport.sentTones())
		}
	})
}

func TestHangupFailureAllowsOnlyNewManualInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		transport.hangupError = errors.New("unconfirmed ATH")
		placeTestCall(t, a, "10086,123")
		a.currentCallState()
		synctest.Wait()
		response := httptest.NewRecorder()
		a.callHangup(response, httptest.NewRequest(http.MethodPost, "/api/call/hangup", nil))
		if response.Code != http.StatusBadGateway {
			t.Fatalf("hangup result = %d", response.Code)
		}
		state := a.currentCallState()
		if !state.DTMFAvailable || state.PostDialState != "canceled" {
			t.Fatalf("manual keys not recovered: %+v", state)
		}
		if result := performDTMF(a, state.SessionID, "#"); result.Code != http.StatusOK {
			t.Fatalf("new explicit key failed: %s", result.Body)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if got := transport.sentTones(); !reflect.DeepEqual(got, []string{`AT+VTS="#",1`}) {
			t.Fatalf("canceled suffix restarted: %v", got)
		}
	})
}

func TestHangupCancelsSessionCreatedWhileWaitingForDialLock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, transport := newCallTestApp(t)
		defer a.endCall()
		transport.setState(1, 0, 0, "10086")
		transport.hangupError = errors.New("unconfirmed ATH")
		a.beginCall("out", "10010")
		oldTones := a.callSession.toneCtx
		a.callControlMu.Lock()
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			response := httptest.NewRecorder()
			a.callHangup(response, httptest.NewRequest(http.MethodPost, "/api/call/hangup", nil))
			result <- response
		}()
		<-oldTones.Done()
		a.endCall()
		plan, _ := callnumber.Parse("10086,123")
		if err := a.placeCall(plan); err != nil {
			t.Fatal(err)
		}
		a.callControlMu.Unlock()
		if response := <-result; response.Code != http.StatusBadGateway {
			t.Fatalf("hangup result = %d", response.Code)
		}
		time.Sleep(5 * time.Second)
		synctest.Wait()
		state := a.currentCallState()
		if state.PostDialState != "canceled" || !state.DTMFAvailable || len(transport.sentTones()) != 0 {
			t.Fatalf("late-created session escaped hangup: %+v", state)
		}
	})
}

func TestDTMFRejectsInvalidInputAndStaleSession(t *testing.T) {
	a, transport := newCallTestApp(t)
	defer a.endCall()
	transport.setState(1, 0, 0, "10086")
	id := a.currentCallState().SessionID
	for _, tone := range []string{"", "12", ",", "+", "A", "1\rATH", "１"} {
		if result := performDTMF(a, id, tone); result.Code != http.StatusBadRequest {
			t.Errorf("accepted tone %q: %d", tone, result.Code)
		}
	}
	if result := performDTMF(a, "previous-session", "1"); result.Code != http.StatusConflict {
		t.Fatalf("stale session result = %d", result.Code)
	}
	if len(transport.sentTones()) != 0 {
		t.Fatal("rejected input reached transport")
	}
}

func TestCallHistoryLoadsLegacyRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	if err := os.WriteFile(path, []byte(`[{"id":"old-record","direction":"out","number":"10086","answered":true,"duration":3}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	items := newCallLog(path).snapshot()
	if len(items) != 1 || items[0].Number != "10086" || items[0].DialString != "" {
		t.Fatalf("legacy history lost: %+v", items)
	}
}

func TestDTMFRouteRemainsAppOnly(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "cli-access.json")
	writeTestCLIConfig(t, configPath, 0o600, cliAccessConfig{
		Version: 1, Enabled: true, Token: "0123456789abcdef0123456789abcdef",
		Scopes: []string{"call.dial", "call.audio", "module.admin"},
	})
	t.Setenv(appTokenEnvironment, "native-app-test-token-0123456789")
	t.Setenv(cliConfigEnvironment, configPath)
	a := &app{}
	handler := a.agentAccessMiddleware(http.HandlerFunc(a.callDTMF))
	request := httptest.NewRequest(http.MethodPost, "/api/call/dtmf", strings.NewReader(`{"session_id":"old","tone":"1"}`))
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("CLI gained implicit DTMF permission: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/call/dtmf", strings.NewReader(`{"session_id":"old","tone":"1"}`))
	request.Header.Set(appTokenHeader, "native-app-test-token-0123456789")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request.WithContext(context.Background()))
	if response.Code != http.StatusConflict {
		t.Fatalf("native app did not reach session validation: %d", response.Code)
	}
}
