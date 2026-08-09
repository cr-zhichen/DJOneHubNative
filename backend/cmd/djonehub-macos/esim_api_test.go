package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iniwex5/vohive/internal/esim"
)

func TestESIMOperationLifecycle(t *testing.T) {
	a := &app{}
	operation, started := a.beginESIMOperation("download_profile", "准备下载", "")
	if !started || operation == nil || operation.State != esimOperationStateQueued {
		t.Fatalf("begin operation = %#v started=%v", operation, started)
	}
	if _, secondStarted := a.beginESIMOperation("switch_profile", "切换", "8986"); secondStarted {
		t.Fatal("second operation started while first is active")
	}

	a.updateESIMOperation(operation.ID, esimOperationStateRun, "install", "正在写入", 80)
	running := a.currentESIMOperation()
	if running == nil || running.State != esimOperationStateRun || running.Step != "install" || running.Progress != 80 {
		t.Fatalf("running operation = %#v", running)
	}

	a.finishESIMOperation(operation.ID, "完成", &esimOperationResult{Warning: "通知待确认", WarningCode: "notify_pending"}, 2)
	finished := a.currentESIMOperation()
	if finished == nil || finished.State != esimOperationStateWarn || finished.FinishedAt == nil || finished.RefreshAfterSeconds != 2 {
		t.Fatalf("finished operation = %#v", finished)
	}
	if _, nextStarted := a.beginESIMOperation("switch_profile", "切换", "8986"); !nextStarted {
		t.Fatal("new operation did not start after terminal state")
	}
}

func TestCurrentESIMOperationReturnsDeepCopy(t *testing.T) {
	a := &app{}
	operation, _ := a.beginESIMOperation("download_profile", "准备下载", "")
	a.finishESIMOperation(operation.ID, "完成", &esimOperationResult{
		SpaceDelta: &esim.SpaceDelta{Direction: esim.SpaceDeltaDirectionConsumed, Bytes: 42},
	}, 0)

	copy1 := a.currentESIMOperation()
	copy1.Message = "mutated"
	copy1.Result.SpaceDelta.Bytes = 99
	copy2 := a.currentESIMOperation()
	if copy2.Message == "mutated" || copy2.Result.SpaceDelta.Bytes != 42 {
		t.Fatalf("operation store leaked mutable pointer: %#v", copy2)
	}
}

func TestWriteCodedError(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeCodedError(recorder, http.StatusConflict, "esim_operation_busy", "正在进行", true)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d", recorder.Code)
	}
	var payload codedErrorPayload
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "esim_operation_busy" || payload.Error != "正在进行" || !payload.Recoverable {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestClassifyDownloadOperationErrorBusy(t *testing.T) {
	code, message, recoverable := classifyDownloadOperationError(errors.Join(errors.New("wrapped"), esim.ErrOperationInProgress))
	if code != "esim_operation_busy" || message == "" || !recoverable {
		t.Fatalf("classification=%q %q %v", code, message, recoverable)
	}
}

func TestClassifyDownloadOperationErrorDoesNotExposeRawActivationData(t *testing.T) {
	const secret = "MATCHING-ID-SECRET"
	err := esim.NewDownloadProfileError(errors.New("request failed for " + secret))
	code, message, recoverable := classifyDownloadOperationError(err)
	if code != esim.DownloadErrorGeneric || !recoverable {
		t.Fatalf("classification=%q %q %v", code, message, recoverable)
	}
	if strings.Contains(message, secret) {
		t.Fatalf("message exposed activation data: %q", message)
	}
}

func TestPhysicalSIMClassificationRequiresATRejection(t *testing.T) {
	if !isPhysicalSIMESIMProbeError(fmt.Errorf("%w: AT+CCHO=... ERROR", esim.ErrEUICCNotFound)) {
		t.Fatal("AT AID rejection should be classified as a physical SIM")
	}
	if isPhysicalSIMESIMProbeError(esim.ErrEUICCNotFound) {
		t.Fatal("bare discovery failure must not be classified as a physical SIM")
	}
	if isPhysicalSIMESIMProbeError(fmt.Errorf("%w: QMI transport timeout", esim.ErrEUICCNotFound)) {
		t.Fatal("transport failure must not be classified as a physical SIM")
	}
}

func TestPhonebookStorageSupported(t *testing.T) {
	response := `+CPBS: ("SM","ME","ON")`
	if !phonebookStorageSupported(response, "ME") {
		t.Fatal("ME storage was not detected")
	}
	if phonebookStorageSupported(response, "FD") {
		t.Fatal("unsupported FD storage was detected")
	}
}

func TestEncodeModuleProfileNoteCountsUnicodeCharacters(t *testing.T) {
	note := moduleProfileNote{ICCID: "8901000000000000000", Label: strings.Repeat("卡", 48)}
	if _, err := encodeModuleProfileNote(note); err != nil {
		t.Fatalf("48 Unicode characters should be accepted: %v", err)
	}
	note.Label += "卡"
	if _, err := encodeModuleProfileNote(note); err == nil {
		t.Fatal("49 Unicode characters should be rejected")
	}
}

func TestSaveESIMNoteCountsUnicodeCharacters(t *testing.T) {
	a := &app{profileNotesPath: t.TempDir() + "/notes.json"}

	save := func(label string) *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string]string{
			"iccid": "8901000000000000000",
			"label": label,
			"phone": "",
			"tags":  "",
		})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		a.saveESIMNote(recorder, httptest.NewRequest(http.MethodPut, "/api/esim/notes", strings.NewReader(string(body))))
		return recorder
	}

	if recorder := save(strings.Repeat("卡", 80)); recorder.Code != http.StatusOK {
		t.Fatalf("80 Unicode characters status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder := save(strings.Repeat("卡", 81)); recorder.Code != http.StatusBadRequest {
		t.Fatalf("81 Unicode characters status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
