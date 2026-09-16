package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	gpsDefaultPollInterval = 15 * time.Second
	gpsStaleAfter          = 30 * time.Second

	gpsStateChecking    = "checking"
	gpsStateDisabled    = "disabled"
	gpsStateSearching   = "searching"
	gpsStateFixed       = "fixed"
	gpsStateStale       = "stale"
	gpsStateUnavailable = "unavailable"
	gpsStateUnsupported = "unsupported"
	gpsStateError       = "error"
)

var (
	errGPSNotFixed    = errors.New("GNSS position is not fixed")
	errGPSUnsupported = errors.New("module does not support GNSS commands")
)

type gpsFix struct {
	Latitude   float64
	Longitude  float64
	Altitude   float64
	HDOP       float64
	FixType    int
	Satellites int
	UTC        string
	Date       string
	UpdatedAt  time.Time
}

type gpsRuntimeStatus struct {
	Initialized bool
	Supported   *bool
	Enabled     bool
	State       string
	Fix         *gpsFix
	CheckedAt   time.Time
	LastError   string
}

type gpsStatusResponse struct {
	Supported   *bool      `json:"supported"`
	Enabled     bool       `json:"enabled"`
	State       string     `json:"state"`
	Latitude    *float64   `json:"latitude,omitempty"`
	Longitude   *float64   `json:"longitude,omitempty"`
	Altitude    *float64   `json:"altitude,omitempty"`
	HDOP        *float64   `json:"hdop,omitempty"`
	FixType     *int       `json:"fix_type,omitempty"`
	Satellites  *int       `json:"satellites,omitempty"`
	UTC         string     `json:"utc,omitempty"`
	Date        string     `json:"date,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	LastError   string     `json:"error,omitempty"`
	PollSeconds int        `json:"poll_interval_s"`
}

func (a *app) startGPSPoller(ctx context.Context) {
	interval := a.gpsPollInterval
	if interval <= 0 {
		interval = gpsDefaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !a.gpsPollingAllowed() {
				continue
			}
			if !a.moduleOperationMu.TryRLock() {
				continue
			}
			a.gpsRefreshMu.Lock()
			err := a.refreshGPSLocationLocked()
			if err != nil && !errors.Is(err, errGPSNotFixed) {
				// A stopped engine and a detached module both surface as a location
				// read failure. Re-read the engine state before the next cycle.
				_ = a.syncGPSEngineLocked(false)
			}
			a.gpsRefreshMu.Unlock()
			a.moduleOperationMu.RUnlock()
		}
	}
}

func (a *app) gpsPollingAllowed() bool {
	a.gpsMu.RLock()
	enabled := a.gps.Enabled
	a.gpsMu.RUnlock()
	if !enabled {
		return false
	}
	a.callModeMu.RLock()
	operationRunning := a.callModeOperation
	a.callModeMu.RUnlock()
	return !operationRunning
}

func (a *app) gpsStatusAPI(w http.ResponseWriter, _ *http.Request) {
	if a.gpsStateNeedsSync() {
		if a.moduleOperationMu.TryRLock() {
			a.gpsRefreshMu.Lock()
			if a.gpsStateNeedsSync() {
				_ = a.syncGPSEngineLocked(true)
			}
			a.gpsRefreshMu.Unlock()
			a.moduleOperationMu.RUnlock()
		}
	}
	writeJSON(w, http.StatusOK, a.gpsSnapshot(time.Now()))
}

func (a *app) gpsStartAPI(w http.ResponseWriter, _ *http.Request) {
	if !a.moduleOperationMu.TryRLock() {
		writeError(w, http.StatusConflict, "模块配置操作正在进行，请稍后再启用定位")
		return
	}
	defer a.moduleOperationMu.RUnlock()
	a.gpsRefreshMu.Lock()
	defer a.gpsRefreshMu.Unlock()

	response, commandErr := a.runGPSCommand("AT+QGPS=1", 8*time.Second)
	if !a.gpsCommandAccepted(response, commandErr) {
		// Starting an already-running engine can be reported as an error by
		// some firmware. Read back the real state before declaring failure.
		syncErr := a.syncGPSEngineLocked(false)
		if syncErr == nil && a.gpsEnabled() {
			_ = a.refreshGPSLocationLocked()
			writeJSON(w, http.StatusOK, a.gpsSnapshot(time.Now()))
			return
		}
		detail := gpsCommandFailureDetail(response, commandErr)
		statusCode := a.preserveGPSProbeFailure(syncErr, "启动模块定位失败："+detail)
		writeError(w, statusCode, "启动模块定位失败："+detail)
		return
	}

	a.setGPSEnabled(true)
	// A missing first fix is the normal search state and does not make a
	// successfully-started engine fail its API request.
	_ = a.refreshGPSLocationLocked()
	writeJSON(w, http.StatusOK, a.gpsSnapshot(time.Now()))
}

func (a *app) gpsStopAPI(w http.ResponseWriter, _ *http.Request) {
	if !a.moduleOperationMu.TryRLock() {
		writeError(w, http.StatusConflict, "模块配置操作正在进行，请稍后再关闭定位")
		return
	}
	defer a.moduleOperationMu.RUnlock()
	a.gpsRefreshMu.Lock()
	defer a.gpsRefreshMu.Unlock()

	response, commandErr := a.runGPSCommand("AT+QGPSEND", 8*time.Second)
	if !a.gpsCommandAccepted(response, commandErr) {
		// Stopping an engine that is already off is idempotent from the UI's
		// perspective. Confirm the module state before returning an error.
		syncErr := a.syncGPSEngineLocked(false)
		if syncErr == nil && !a.gpsEnabled() {
			writeJSON(w, http.StatusOK, a.gpsSnapshot(time.Now()))
			return
		}
		detail := gpsCommandFailureDetail(response, commandErr)
		statusCode := a.preserveGPSProbeFailure(syncErr, "关闭模块定位失败："+detail)
		writeError(w, statusCode, "关闭模块定位失败："+detail)
		return
	}

	a.setGPSEnabled(false)
	writeJSON(w, http.StatusOK, a.gpsSnapshot(time.Now()))
}

func (a *app) gpsStateNeedsSync() bool {
	a.gpsMu.RLock()
	defer a.gpsMu.RUnlock()
	return !a.gps.Initialized || a.gps.State == gpsStateUnavailable || a.gps.State == gpsStateError
}

func (a *app) syncGPSEngineLocked(refreshLocation bool) error {
	response, commandErr := a.runGPSCommand("AT+QGPS?", 5*time.Second)
	commandOutput := gpsCommandOutput(response, commandErr)
	now := time.Now()
	if commandErr != nil && !gpsATResponseIsError(commandOutput) {
		a.markGPSUnavailable(commandErr.Error())
		return commandErr
	}

	enabled, parseErr := parseGPSEngineState(commandOutput)
	if parseErr != nil {
		if errors.Is(parseErr, errGPSUnsupported) {
			supported := false
			a.gpsMu.Lock()
			a.gps = gpsRuntimeStatus{
				Initialized: true,
				Supported:   &supported,
				State:       gpsStateUnsupported,
				CheckedAt:   now,
				LastError:   "当前模块未确认支持 GNSS 定位",
			}
			a.gpsMu.Unlock()
			return parseErr
		}
		a.setGPSError("无法读取模块定位状态：" + parseErr.Error())
		return parseErr
	}

	supported := true
	a.gpsMu.Lock()
	a.gps.Initialized = true
	a.gps.Supported = &supported
	a.gps.Enabled = enabled
	a.gps.CheckedAt = now
	a.gps.LastError = ""
	if enabled {
		a.gps.State = gpsStateSearching
	} else {
		a.gps.State = gpsStateDisabled
		a.gps.Fix = nil
	}
	a.gpsMu.Unlock()

	if enabled && refreshLocation {
		return a.refreshGPSLocationLocked()
	}
	return nil
}

func (a *app) refreshGPSLocationLocked() error {
	if !a.gpsEnabled() {
		return nil
	}
	response, commandErr := a.runGPSCommand("AT+QGPSLOC=2", 8*time.Second)
	commandOutput := gpsCommandOutput(response, commandErr)
	now := time.Now()
	if gpsResponseIsNotFixed(commandOutput) {
		a.gpsMu.Lock()
		a.gps.Initialized = true
		a.gps.CheckedAt = now
		a.gps.LastError = ""
		if a.gps.Fix == nil || now.Sub(a.gps.Fix.UpdatedAt) > gpsStaleAfter {
			a.gps.State = gpsStateSearching
		}
		a.gpsMu.Unlock()
		return errGPSNotFixed
	}
	if commandErr != nil && !gpsATResponseIsError(commandOutput) {
		a.markGPSUnavailable(commandErr.Error())
		return commandErr
	}
	if gpsATResponseIsError(commandOutput) {
		detail := gpsCommandFailureDetail(response, commandErr)
		a.setGPSError("读取定位失败：" + detail)
		return errors.New(detail)
	}

	fix, parseErr := parseGPSLocation(commandOutput, now)
	if parseErr != nil {
		a.setGPSError("解析定位数据失败：" + parseErr.Error())
		return parseErr
	}
	supported := true
	a.gpsMu.Lock()
	a.gps.Initialized = true
	a.gps.Supported = &supported
	a.gps.Enabled = true
	a.gps.State = gpsStateFixed
	a.gps.Fix = &fix
	a.gps.CheckedAt = now
	a.gps.LastError = ""
	a.gpsMu.Unlock()
	return nil
}

func (a *app) runGPSCommand(command string, timeout time.Duration) (string, error) {
	if a.gpsCommand != nil {
		return a.gpsCommand(command, timeout)
	}
	return a.runATCommand(command, timeout)
}

func gpsCommandOutput(response string, err error) string {
	response = strings.TrimSpace(response)
	if err == nil {
		return response
	}
	detail := strings.TrimSpace(err.Error())
	if response == "" {
		return detail
	}
	return response + "\n" + detail
}

func (a *app) gpsCommandAccepted(response string, err error) bool {
	if err != nil || gpsATResponseIsError(response) {
		return false
	}
	// The managed serial transport strips the final OK after accepting a
	// command. Direct USB and test transports retain it.
	if a.modem != nil && a.gpsCommand == nil {
		return true
	}
	for _, line := range splitATLines(response) {
		if strings.EqualFold(line, "OK") {
			return true
		}
	}
	return false
}

func (a *app) gpsEnabled() bool {
	a.gpsMu.RLock()
	defer a.gpsMu.RUnlock()
	return a.gps.Enabled
}

func (a *app) setGPSEnabled(enabled bool) {
	supported := true
	a.gpsMu.Lock()
	a.gps.Initialized = true
	a.gps.Supported = &supported
	a.gps.Enabled = enabled
	a.gps.CheckedAt = time.Now()
	a.gps.LastError = ""
	if enabled {
		a.gps.State = gpsStateSearching
	} else {
		a.gps.State = gpsStateDisabled
		a.gps.Fix = nil
	}
	a.gpsMu.Unlock()
}

func (a *app) setGPSError(message string) {
	a.gpsMu.Lock()
	a.gps.Initialized = true
	a.gps.State = gpsStateError
	a.gps.CheckedAt = time.Now()
	a.gps.LastError = strings.TrimSpace(message)
	a.gpsMu.Unlock()
}

// A failed action is followed by an engine-state probe. Keep a more useful
// unsupported/unavailable result from that probe instead of replacing it with
// a generic action error.
func (a *app) preserveGPSProbeFailure(probeErr error, message string) int {
	if errors.Is(probeErr, errGPSUnsupported) {
		return http.StatusNotImplemented
	}
	a.gpsMu.RLock()
	state := a.gps.State
	a.gpsMu.RUnlock()
	if state == gpsStateUnavailable {
		return http.StatusServiceUnavailable
	}
	a.setGPSError(message)
	return http.StatusBadGateway
}

func (a *app) markGPSUnavailable(reason string) {
	a.gpsMu.Lock()
	a.gps.Initialized = true
	a.gps.State = gpsStateUnavailable
	a.gps.CheckedAt = time.Now()
	a.gps.LastError = strings.TrimSpace(reason)
	a.gpsMu.Unlock()
}

func (a *app) gpsSnapshot(now time.Time) gpsStatusResponse {
	a.gpsMu.RLock()
	runtime := a.gps
	if runtime.Fix != nil {
		copyFix := *runtime.Fix
		runtime.Fix = &copyFix
	}
	a.gpsMu.RUnlock()

	state := runtime.State
	if state == "" {
		state = gpsStateChecking
	}
	if runtime.Enabled && state != gpsStateUnavailable && state != gpsStateUnsupported && state != gpsStateError {
		if runtime.Fix == nil {
			state = gpsStateSearching
		} else if now.Sub(runtime.Fix.UpdatedAt) > gpsStaleAfter {
			state = gpsStateStale
		} else {
			state = gpsStateFixed
		}
	}

	interval := a.gpsPollInterval
	if interval <= 0 {
		interval = gpsDefaultPollInterval
	}
	result := gpsStatusResponse{
		Supported:   runtime.Supported,
		Enabled:     runtime.Enabled,
		State:       state,
		LastError:   runtime.LastError,
		PollSeconds: int(interval.Seconds()),
	}
	if !runtime.CheckedAt.IsZero() {
		checkedAt := runtime.CheckedAt
		result.CheckedAt = &checkedAt
	}
	if runtime.Fix != nil {
		result.Latitude = &runtime.Fix.Latitude
		result.Longitude = &runtime.Fix.Longitude
		result.Altitude = &runtime.Fix.Altitude
		result.HDOP = &runtime.Fix.HDOP
		result.FixType = &runtime.Fix.FixType
		result.Satellites = &runtime.Fix.Satellites
		result.UTC = runtime.Fix.UTC
		result.Date = runtime.Fix.Date
		updatedAt := runtime.Fix.UpdatedAt
		result.UpdatedAt = &updatedAt
	}
	return result
}

func parseGPSEngineState(response string) (bool, error) {
	if gpsResponseIsExplicitlyUnsupported(response) {
		return false, errGPSUnsupported
	}
	if gpsATResponseIsError(response) {
		return false, errors.New(gpsCommandFailureDetail(response, nil))
	}
	for _, line := range splitATLines(response) {
		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, "+QGPS:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(upper, "+QGPS:"))
		switch value {
		case "0":
			return false, nil
		case "1":
			return true, nil
		default:
			return false, fmt.Errorf("unexpected +QGPS state %q", value)
		}
	}
	return false, errors.New("response did not include +QGPS state")
}

func parseGPSLocation(response string, now time.Time) (gpsFix, error) {
	for _, line := range splitATLines(response) {
		if !strings.HasPrefix(strings.ToUpper(line), "+QGPSLOC:") {
			continue
		}
		payload := strings.TrimSpace(line[strings.Index(line, ":")+1:])
		fields := strings.Split(payload, ",")
		if len(fields) < 11 {
			return gpsFix{}, fmt.Errorf("+QGPSLOC returned %d fields, want at least 11", len(fields))
		}
		for index := range fields {
			fields[index] = strings.TrimSpace(fields[index])
		}
		latitude, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return gpsFix{}, fmt.Errorf("invalid latitude: %w", err)
		}
		longitude, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return gpsFix{}, fmt.Errorf("invalid longitude: %w", err)
		}
		hdop, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			return gpsFix{}, fmt.Errorf("invalid HDOP: %w", err)
		}
		altitude, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			return gpsFix{}, fmt.Errorf("invalid altitude: %w", err)
		}
		fixType, err := strconv.Atoi(fields[5])
		if err != nil {
			return gpsFix{}, fmt.Errorf("invalid fix type: %w", err)
		}
		satellites, err := strconv.Atoi(fields[10])
		if err != nil {
			return gpsFix{}, fmt.Errorf("invalid satellite count: %w", err)
		}
		if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
			return gpsFix{}, fmt.Errorf("latitude %.6f is out of range", latitude)
		}
		if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
			return gpsFix{}, fmt.Errorf("longitude %.6f is out of range", longitude)
		}
		if math.IsNaN(hdop) || math.IsInf(hdop, 0) || hdop < 0 {
			return gpsFix{}, fmt.Errorf("HDOP %.3f is invalid", hdop)
		}
		if math.IsNaN(altitude) || math.IsInf(altitude, 0) {
			return gpsFix{}, errors.New("altitude is invalid")
		}
		if fixType <= 0 || satellites < 0 {
			return gpsFix{}, errors.New("fix type or satellite count is invalid")
		}
		return gpsFix{
			Latitude:   latitude,
			Longitude:  longitude,
			Altitude:   altitude,
			HDOP:       hdop,
			FixType:    fixType,
			Satellites: satellites,
			UTC:        fields[0],
			Date:       fields[9],
			UpdatedAt:  now,
		}, nil
	}
	return gpsFix{}, errors.New("response did not include +QGPSLOC data")
}

func gpsATResponseIsError(response string) bool {
	for _, line := range splitATLines(response) {
		upper := strings.ToUpper(strings.TrimSpace(line))
		compact := strings.ReplaceAll(upper, " ", "")
		if upper == "ERROR" || strings.HasSuffix(upper, ": ERROR") ||
			strings.Contains(compact, "+CMEERROR:") || strings.Contains(compact, "+CMSERROR:") {
			return true
		}
	}
	return false
}

func gpsResponseIsNotFixed(response string) bool {
	return gpsResponseHasErrorCode(response, 516)
}

func gpsResponseIsExplicitlyUnsupported(response string) bool {
	return gpsResponseHasErrorCode(response, 4)
}

func gpsResponseHasErrorCode(response string, expected int) bool {
	for _, line := range splitATLines(response) {
		compact := strings.ToUpper(strings.ReplaceAll(line, " ", ""))
		for _, marker := range []string{"+CMEERROR:", "+CMSERROR:"} {
			markerIndex := strings.Index(compact, marker)
			if markerIndex < 0 {
				continue
			}
			value := compact[markerIndex+len(marker):]
			end := 0
			for end < len(value) && value[end] >= '0' && value[end] <= '9' {
				end++
			}
			if end == 0 {
				continue
			}
			code, err := strconv.Atoi(value[:end])
			if err == nil && code == expected {
				return true
			}
		}
	}
	return false
}

func gpsCommandFailureDetail(response string, err error) string {
	if err != nil {
		return err.Error()
	}
	for _, line := range splitATLines(response) {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if upper == "ERROR" || strings.HasSuffix(upper, ": ERROR") ||
			strings.Contains(upper, "+CME ERROR:") || strings.Contains(upper, "+CMS ERROR:") {
			return line
		}
	}
	return "模块未确认操作结果"
}
