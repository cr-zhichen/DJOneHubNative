package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/callnumber"
)

const (
	callPauseDuration = 2 * time.Second
	callToneGap       = 100 * time.Millisecond
)

var (
	errCallChanged     = errors.New("通话已结束或发生变化，未继续发送按键")
	errToneUnconfirmed = errors.New("模块未确认按键发送结果，已停止后续按键；请根据对方提示确认后再操作")
)

type callToneJob struct {
	ctx       context.Context
	sequence  string
	automatic bool
	done      chan error
}

func (a *app) newCallSession(direction, number string) *callSession {
	ctx, cancel := context.WithCancel(context.Background())
	toneCtx, toneCancel := context.WithCancel(ctx)
	session := &callSession{
		id: rand.Text(), direction: direction, number: number, startedAt: time.Now(),
		ctx: ctx, cancel: cancel, toneCtx: toneCtx, toneCancel: toneCancel, tones: make(chan callToneJob, 32),
	}
	go a.runCallToneQueue(session)
	return session
}

func (a *app) runCallCommand(command, redacted string, timeout time.Duration) (string, error) {
	return a.runCheckedCallCommand(command, redacted, timeout, nil)
}

func (a *app) runCheckedCallCommand(command, redacted string, timeout time.Duration, beforeWrite func() error) (string, error) {
	if a.callCommand != nil {
		return a.callCommand(command, redacted, timeout, beforeWrite)
	}
	if redacted != "" {
		return a.runSensitiveATCommandChecked(command, redacted, timeout, beforeWrite)
	}
	return a.runATCommand(command, timeout)
}

// placeCall runs with callControlMu held. Reserve the session before ATD so a
// CONNECT/NO CARRIER received during the command belongs to this call.
func (a *app) placeCall(plan callnumber.DialString) error {
	a.callSessionMu.Lock()
	if a.callSession != nil {
		a.callSessionMu.Unlock()
		return errors.New("通话状态已改变，请刷新后再拨号")
	}
	session := a.newCallSession("out", plan.Number)
	session.dialPending = true
	if plan.PostDial != "" {
		session.dialString = plan.Original
		session.postDial = plan.PostDial
		session.postDialState = "pending"
	}
	a.callSession = session
	a.callSessionMu.Unlock()

	response, err := a.runCallCommand("ATD"+plan.Number+";", "ATD<redacted>;", 10*time.Second)
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	if !a.callModeATCommandAccepted(response, err) {
		// ATD may have reached the module before a timeout. Retain the session
		// for reconciliation, but never send or retry its extension blindly.
		a.cancelPostDialLocked(session, "拨号结果未确认，已取消自动分机；请刷新通话状态")
		return errors.New("模块未确认拨号结果，请刷新通话状态后再操作")
	}
	if a.callSession == session {
		session.dialPending = false
		session.firstCLCCUntil = time.Now().Add(3 * time.Second)
		if session.postDial != "" {
			go a.monitorPostDial(session)
		}
	}
	return nil
}

// A terminal ATD OK can precede the first CLCC entry. Retain only this freshly
// accepted outbound call briefly; no tone is allowed until a real active row.
func (a *app) pendingOutgoingCallState() (callStateInfo, bool) {
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	session := a.callSession
	if session != nil && session.direction == "out" && session.moduleCallID == 0 && time.Now().Before(session.firstCLCCUntil) {
		return callStateInfo{State: "dialing", Number: session.number}, true
	}
	return callStateInfo{}, false
}

func normalizedCallNumber(number string) string {
	return strings.Map(func(value rune) rune {
		switch value {
		case '+', ' ', '-', '(', ')':
			return -1
		default:
			return value
		}
	}, number)
}

// CLCC IDs can be reused after a call ends, so also compare direction and main
// number. An observed replacement must never inherit the old extension queue.
func (a *app) reconcileCallIdentity(info callStateInfo) {
	if info.moduleCallID == 0 {
		return
	}
	a.callSessionMu.Lock()
	session := a.callSession
	// The first CLCC row may normalize a domestic number to international
	// format. Bind that modem identity first; compare numbers on later rows.
	replaced := session != nil && (session.direction != info.direction ||
		(session.moduleCallID != 0 && session.moduleCallID != info.moduleCallID) ||
		(session.moduleCallID != 0 && session.number != "" && info.Number != "" && normalizedCallNumber(session.number) != normalizedCallNumber(info.Number)))
	a.callSessionMu.Unlock()
	if replaced {
		a.endCallIfCurrent(session)
	}
	a.beginCall(info.direction, info.Number)
	a.callSessionMu.Lock()
	if a.callSession != nil {
		a.callSession.moduleCallID = info.moduleCallID
	}
	a.callSessionMu.Unlock()
}

// The CLI can dial without a UI status poll. Keep observing until the saved
// suffix finishes, including disconnects that happen during comma pauses.
func (a *app) monitorPostDial(session *callSession) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-session.ctx.Done():
			return
		case <-ticker.C:
			a.callSessionMu.Lock()
			pending := a.callSession == session && (session.postDialState == "pending" || session.postDialState == "sending")
			a.callSessionMu.Unlock()
			if !pending {
				return
			}
			a.currentCallState()
		}
	}
}

func (a *app) updateCallToneState(info callStateInfo) {
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	session := a.callSession
	if session == nil {
		return
	}
	session.active = info.State == "active" && info.singleActive
	if info.State == "active" {
		session.answered = true
	}
	if session.active && !session.dialPending && !session.tonesBlocked && session.postDialState == "pending" {
		ctx, cancel := context.WithCancel(session.toneCtx)
		session.postDialCancel = cancel
		session.postDialState = "sending"
		session.tones <- callToneJob{ctx: ctx, sequence: session.postDial, automatic: true}
	}
	if !session.active && (session.postDialState == "sending" || info.State == "unknown" || info.State == "held" || info.multipleCalls) {
		a.cancelPostDialLocked(session, "通话状态发生变化，已取消自动分机；接通后可使用按键盘")
	}
}

func (a *app) cancelPostDialLocked(session *callSession, message string) {
	if session.postDialState != "pending" && session.postDialState != "sending" {
		return
	}
	if session.postDialCancel != nil {
		session.postDialCancel()
	}
	session.postDialState = "canceled"
	session.postDialError = message
}

func (a *app) cancelCallTones() {
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	if session := a.callSession; session != nil {
		session.tonesBlocked = true
		session.toneCancel()
		a.cancelPostDialLocked(session, "已取消自动分机")
	}
}

func (a *app) resumeManualCallTones() {
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	if session := a.callSession; session != nil && session.tonesBlocked {
		session.toneCtx, session.toneCancel = context.WithCancel(session.ctx)
		session.tonesBlocked = false
	}
}

func (a *app) withCallSession(info callStateInfo) callStateInfo {
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	if session := a.callSession; session != nil {
		info.SessionID = session.id
		info.PostDialState = session.postDialState
		info.PostDialError = session.postDialError
		info.DTMFAvailable = session.active && !session.tonesBlocked && session.postDialState != "sending" && session.postDialState != "pending"
	}
	return info
}

func (a *app) runCallToneQueue(session *callSession) {
	var lastToneAt time.Time
	for {
		select {
		case <-session.ctx.Done():
			return
		case job := <-session.tones:
			err := a.runCallToneSequence(session, job, &lastToneAt)
			a.callSessionMu.Lock()
			if job.automatic && a.callSession == session && session.postDialState == "sending" {
				session.postDialState = "completed"
				if err != nil {
					session.postDialState = "failed"
					session.postDialError = err.Error()
				}
			}
			if err != nil && job.ctx.Err() == nil {
				// Stop already queued input after an uncertain result. New input
				// requires a fresh explicit action; no failed digit is retried.
				for len(session.tones) > 0 {
					pending := <-session.tones
					if pending.done != nil {
						pending.done <- err
					}
				}
			}
			a.callSessionMu.Unlock()
			if job.done != nil {
				job.done <- err
			}
		}
	}
}

func (a *app) runCallToneSequence(session *callSession, job callToneJob, lastToneAt *time.Time) error {
	for _, key := range job.sequence {
		if job.ctx.Err() != nil || session.ctx.Err() != nil {
			return errCallChanged
		}
		if key == ',' {
			if err := waitCallTonePause(job.ctx, session.ctx, callPauseDuration); err != nil {
				return err
			}
			continue
		}
		if err := waitCallTonePause(job.ctx, session.ctx, time.Until(lastToneAt.Add(callToneGap))); err != nil {
			return err
		}
		if err := a.sendCallTone(session, job.ctx, key); err != nil {
			return err
		}
		*lastToneAt = time.Now()
	}
	return nil
}

func waitCallTonePause(ctx, sessionCtx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return errCallChanged
	case <-sessionCtx.Done():
		return errCallChanged
	}
}

func (a *app) checkCallToneSession(session *callSession, ctx context.Context) error {
	a.callSessionMu.Lock()
	defer a.callSessionMu.Unlock()
	if a.callSession != session || !session.active || session.tonesBlocked || session.ctx.Err() != nil || ctx.Err() != nil {
		return errCallChanged
	}
	return nil
}

func (a *app) sendCallTone(session *callSession, ctx context.Context, key rune) error {
	a.callControlMu.Lock()
	defer a.callControlMu.Unlock()
	if err := a.checkCallToneSession(session, ctx); err != nil {
		return err
	}
	// A fresh CLCC prevents tones going to a held, replaced or second call.
	a.queryCallState()
	guard := func() error { return a.checkCallToneSession(session, ctx) }
	if err := guard(); err != nil {
		return err
	}
	response, err := a.runCheckedCallCommand(`AT+VTS="`+string(key)+`",1`, "AT+VTS=<redacted>", 3*time.Second, guard)
	if errors.Is(err, errCallChanged) {
		return errCallChanged
	}
	if !a.callModeATCommandAccepted(response, err) {
		// Errors can include the module's command echo. Do not expose it to
		// logs, UI or status: manual tones may contain PINs or account numbers.
		return errToneUnconfirmed
	}
	return nil
}

// POST /api/call/dtmf is app-only: it is intentionally absent from the CLI
// scope map. A session ID is mandatory, and one request sends exactly one key.
func (a *app) callDTMF(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Tone      string `json:"tone"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.SessionID == "" || len(body.Tone) != 1 || !strings.ContainsAny(body.Tone, "0123456789*#") {
		writeError(w, http.StatusBadRequest, "需要当前通话标识和一个按键（0–9、* 或 #）")
		return
	}
	a.callSessionMu.Lock()
	session := a.callSession
	if session == nil || session.id != body.SessionID || !session.active || session.tonesBlocked || session.postDialState == "pending" || session.postDialState == "sending" {
		a.callSessionMu.Unlock()
		writeCodedError(w, http.StatusConflict, "dtmf_unavailable", "当前通话不能发送按键，请等待接通或自动分机结束", false)
		return
	}
	ctx, cancel := context.WithCancel(session.toneCtx)
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	defer cancel()
	done := make(chan error, 1)
	select {
	case session.tones <- callToneJob{ctx: ctx, sequence: body.Tone, done: done}:
		a.callSessionMu.Unlock()
	default:
		a.callSessionMu.Unlock()
		writeCodedError(w, http.StatusTooManyRequests, "dtmf_queue_full", "待发送按键过多，请等待后再操作", false)
		return
	}
	select {
	case err := <-done:
		if err != nil {
			writeCodedError(w, http.StatusBadGateway, "dtmf_unconfirmed", err.Error(), false)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"accepted": true})
	case <-ctx.Done():
		writeCodedError(w, http.StatusConflict, "call_changed", errCallChanged.Error(), false)
	}
}
