package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseGPSEngineState(t *testing.T) {
	for _, test := range []struct {
		response string
		enabled  bool
	}{
		{response: "AT+QGPS?\r\n+QGPS: 0\r\nOK", enabled: false},
		{response: "+QGPS: 1", enabled: true},
	} {
		enabled, err := parseGPSEngineState(test.response)
		if err != nil || enabled != test.enabled {
			t.Fatalf("parseGPSEngineState(%q) = %v, %v", test.response, enabled, err)
		}
	}
	if _, err := parseGPSEngineState("+CME ERROR: 4"); !errors.Is(err, errGPSUnsupported) {
		t.Fatalf("unsupported result = %v, want errGPSUnsupported", err)
	}
	for _, response := range []string{"ERROR", "+CME ERROR: 515", "+CMS ERROR: 500"} {
		if _, err := parseGPSEngineState(response); err == nil || errors.Is(err, errGPSUnsupported) {
			t.Fatalf("transient result for %q = %v, want a retryable error", response, err)
		}
	}
}

func TestParseGPSLocationModeTwo(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	fix, err := parseGPSLocation(
		"AT+QGPSLOC=2\r\n+QGPSLOC: 020000.0,31.23040,121.47370,1.2,12.5,2,0.0,0.0,0.0,160826,09\r\nOK",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fix.Latitude != 31.23040 || fix.Longitude != 121.47370 || fix.Altitude != 12.5 ||
		fix.HDOP != 1.2 || fix.FixType != 2 || fix.Satellites != 9 || !fix.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected fix: %+v", fix)
	}
}

func TestParseGPSLocationRejectsInvalidCoordinates(t *testing.T) {
	_, err := parseGPSLocation(
		"+QGPSLOC: 020000.0,91.00000,121.47370,1.2,12.5,2,0.0,0.0,0.0,160826,09",
		time.Now(),
	)
	if err == nil {
		t.Fatal("out-of-range latitude was accepted")
	}
}

func TestGPSStartTreatsNotFixedAsSearching(t *testing.T) {
	application := &app{}
	application.gpsCommand = func(command string, _ time.Duration) (string, error) {
		switch command {
		case "AT+QGPS=1":
			return "OK", nil
		case "AT+QGPSLOC=2":
			return "", errors.New("设备返回错误: +CME ERROR: 516")
		default:
			t.Fatalf("unexpected command: %s", command)
			return "", nil
		}
	}

	recorder := httptest.NewRecorder()
	application.gpsStartAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/gps/start", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var status gpsStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.State != gpsStateSearching || status.Latitude != nil {
		t.Fatalf("status = %+v, want enabled searching without a fix", status)
	}
}

func TestGPSStartPreservesUnsupportedProbeState(t *testing.T) {
	application := &app{}
	application.gpsCommand = func(command string, _ time.Duration) (string, error) {
		switch command {
		case "AT+QGPS=1", "AT+QGPS?":
			return "", errors.New("设备返回错误: +CME ERROR: 4")
		default:
			t.Fatalf("unexpected command: %s", command)
			return "", nil
		}
	}

	recorder := httptest.NewRecorder()
	application.gpsStartAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/gps/start", nil))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status := application.gpsSnapshot(time.Now())
	if status.Supported == nil || *status.Supported || status.State != gpsStateUnsupported {
		t.Fatalf("status = %+v, want unsupported probe result", status)
	}
}

func TestGPSStatusRetriesTransientCMEError(t *testing.T) {
	application := &app{}
	commandCount := 0
	application.gpsCommand = func(command string, _ time.Duration) (string, error) {
		commandCount++
		if command != "AT+QGPS?" {
			t.Fatalf("unexpected command: %s", command)
		}
		if commandCount == 1 {
			return "", errors.New("设备返回错误: +CME ERROR: 515")
		}
		return "+QGPS: 0\r\nOK", nil
	}

	first := httptest.NewRecorder()
	application.gpsStatusAPI(first, httptest.NewRequest(http.MethodGet, "/api/gps/status", nil))
	if status := application.gpsSnapshot(time.Now()); status.State != gpsStateError || status.Supported != nil {
		t.Fatalf("first status = %+v, want retryable error", status)
	}

	second := httptest.NewRecorder()
	application.gpsStatusAPI(second, httptest.NewRequest(http.MethodGet, "/api/gps/status", nil))
	status := application.gpsSnapshot(time.Now())
	if commandCount != 2 || status.State != gpsStateDisabled || status.Supported == nil || !*status.Supported {
		t.Fatalf("commands = %d, second status = %+v, want supported disabled", commandCount, status)
	}
}

func TestGPSStartConflictsWithModuleConfiguration(t *testing.T) {
	application := &app{}
	application.moduleOperationMu.Lock()
	defer application.moduleOperationMu.Unlock()
	application.gpsCommand = func(command string, _ time.Duration) (string, error) {
		t.Fatalf("GPS command %q ran during a module configuration transaction", command)
		return "", nil
	}

	recorder := httptest.NewRecorder()
	application.gpsStartAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/gps/start", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestGPSStartPreservesUnavailableProbeState(t *testing.T) {
	application := &app{}
	application.gpsCommand = func(_ string, _ time.Duration) (string, error) {
		return "", errors.New("AT serial port is unavailable")
	}

	recorder := httptest.NewRecorder()
	application.gpsStartAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/gps/start", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status := application.gpsSnapshot(time.Now())
	if status.State != gpsStateUnavailable {
		t.Fatalf("state = %q, want %q", status.State, gpsStateUnavailable)
	}
}

func TestGPSStatusRetriesErrorState(t *testing.T) {
	application := &app{gps: gpsRuntimeStatus{
		Initialized: true,
		State:       gpsStateError,
		LastError:   "temporary parse error",
	}}
	commandCount := 0
	application.gpsCommand = func(command string, _ time.Duration) (string, error) {
		commandCount++
		if command != "AT+QGPS?" {
			t.Fatalf("unexpected command: %s", command)
		}
		return "+QGPS: 0\r\nOK", nil
	}

	recorder := httptest.NewRecorder()
	application.gpsStatusAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/gps/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status := application.gpsSnapshot(time.Now())
	if commandCount != 1 || status.State != gpsStateDisabled || status.LastError != "" {
		t.Fatalf("commands = %d, status = %+v, want a successful disabled retry", commandCount, status)
	}
}

func TestGPSStopClearsCachedFix(t *testing.T) {
	application := &app{}
	application.gps = gpsRuntimeStatus{
		Initialized: true,
		Enabled:     true,
		State:       gpsStateFixed,
		Fix: &gpsFix{
			Latitude: 31.2304, Longitude: 121.4737, UpdatedAt: time.Now(),
		},
	}
	application.gpsCommand = func(command string, _ time.Duration) (string, error) {
		if command != "AT+QGPSEND" {
			t.Fatalf("unexpected command: %s", command)
		}
		return "OK", nil
	}

	recorder := httptest.NewRecorder()
	application.gpsStopAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/gps/stop", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status := application.gpsSnapshot(time.Now())
	if status.Enabled || status.State != gpsStateDisabled || status.Latitude != nil {
		t.Fatalf("status = %+v, want disabled without cached coordinates", status)
	}
}

func TestGPSSnapshotMarksOldFixStale(t *testing.T) {
	supported := true
	application := &app{gps: gpsRuntimeStatus{
		Initialized: true,
		Supported:   &supported,
		Enabled:     true,
		State:       gpsStateFixed,
		Fix: &gpsFix{
			Latitude: 31.2304, Longitude: 121.4737,
			UpdatedAt: time.Now().Add(-31 * time.Second),
		},
	}}
	if status := application.gpsSnapshot(time.Now()); status.State != gpsStateStale {
		t.Fatalf("state = %q, want %q", status.State, gpsStateStale)
	}
}
