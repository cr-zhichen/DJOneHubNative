package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCallModeBackup() callModeUSBBackup {
	composition := usbComposition{
		VendorID:  0x2ca3,
		ProductID: 0x4006,
		Flags:     []int{1, 1, 1, 1, 1, 0, 1},
	}
	voice := callModeVoiceConfiguration{IMS: 0, VoLTECapability: 1, VoLTEDisabled: 0}
	return callModeUSBBackup{
		SchemaVersion: callModeBackupSchemaVersion,
		Kind:          callModeBackupKind,
		Reason:        callModeBackupReasonBeforeCallMode,
		SavedAt:       time.Date(2026, 8, 14, 12, 34, 56, 123456789, time.Local),
		Module: callModeModuleIdentity{
			IMEI:     "860000000000001",
			Firmware: "QDC507GLEFM21_01.001.02.001",
		},
		USB:            composition,
		Voice:          &voice,
		RestoreCommand: composition.command(),
	}
}

func TestCallModeBackupRoundTripIsPrivateAndRestorable(t *testing.T) {
	directory := t.TempDir()
	backup := testCallModeBackup()
	path, err := writeCallModeUSBBackup(directory, backup)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != directory || !validCallModeBackupID(filepath.Base(path)) {
		t.Fatalf("unexpected backup path: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
	payload, _, err := readCallModeBackupFile(directory, filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCallModeUSBBackup(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCallModeBackupForModule(decoded, backup.Module, usbComposition{
		VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 1, 1},
	}); err != nil {
		t.Fatalf("matching backup rejected: %v", err)
	}
	summary := callModeBackupSummary(filepath.Base(path), info, payload)
	if !summary.Valid || !summary.Restorable || summary.ModuleIMEI != backup.Module.IMEI {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if !summary.VoiceIncluded || summary.IMSConfigured {
		t.Fatalf("voice configuration summary lost: %#v", summary)
	}
}

func TestVersionTwoCallModeBackupRestoresUSBOnly(t *testing.T) {
	backup := testCallModeBackup()
	backup.SchemaVersion = callModeBackupPreviousSchemaVersion
	backup.Voice = nil
	if err := validateCallModeUSBBackup(backup, true); err != nil {
		t.Fatalf("version 2 backup rejected: %v", err)
	}
	payload, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	id := "usb-before-call-mode-v2.json"
	path := filepath.Join(directory, id)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	summary := callModeBackupSummary(id, info, payload)
	if !summary.Valid || !summary.Restorable || summary.VoiceIncluded || !strings.Contains(summary.Detail, "IMS/VoLTE") {
		t.Fatalf("unexpected version 2 summary: %#v", summary)
	}
}

func TestCallModeBackupRejectsTamperedCommandAndWrongModule(t *testing.T) {
	backup := testCallModeBackup()
	backup.RestoreCommand = `AT+QCFG="USBCFG",0x2CA3,0x4006,0,0,0,0,0,0,0`
	if err := validateCallModeUSBBackup(backup, true); err == nil {
		t.Fatal("tampered restore command unexpectedly accepted")
	}
	backup = testCallModeBackup()
	wrongIdentity := backup.Module
	wrongIdentity.IMEI = "860000000000002"
	if err := validateCallModeBackupForModule(backup, wrongIdentity, backup.USB); err == nil {
		t.Fatal("backup for another IMEI unexpectedly accepted")
	}
	wrongUSB := backup.USB
	wrongUSB.ProductID = 0x0125
	if err := validateCallModeBackupForModule(backup, backup.Module, wrongUSB); err == nil {
		t.Fatal("backup for another USB identity unexpectedly accepted")
	}
}

func TestLegacyCallModeBackupCanBeExportedButNotRestored(t *testing.T) {
	directory := t.TempDir()
	composition := usbComposition{
		VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 0, 1},
	}
	legacy := callModeUSBBackup{
		SavedAt:        time.Date(2026, 8, 14, 10, 0, 0, 0, time.Local),
		USB:            composition,
		RestoreCommand: composition.command(),
	}
	payload, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	id := "usb-before-call-mode-20260814-100000.json"
	if err := os.WriteFile(filepath.Join(directory, id), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(directory, id))
	summary := callModeBackupSummary(id, info, payload)
	if !summary.Valid || summary.Restorable {
		t.Fatalf("legacy summary = %#v", summary)
	}
	if err := validateCallModeUSBBackup(legacy, true); err == nil {
		t.Fatal("legacy backup unexpectedly restorable")
	}
}

func TestCallModeBackupIDRejectsTraversalAndSymlink(t *testing.T) {
	for _, id := range []string{"../usb-before-call-mode-a.json", "/tmp/usb-before-call-mode-a.json", "other.json", "usb-before-call-mode-a.json/child"} {
		if validCallModeBackupID(id) {
			t.Fatalf("invalid backup ID accepted: %q", id)
		}
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "usb-before-call-mode-symlink.json"
	if err := os.Symlink(target, filepath.Join(directory, id)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readCallModeBackupFile(directory, id); err == nil {
		t.Fatal("symlink backup unexpectedly accepted")
	}
}

func TestCallModeBackupExportReturnsOriginalJSON(t *testing.T) {
	application := &app{dataDir: t.TempDir()}
	backup := testCallModeBackup()
	path, err := writeCallModeUSBBackup(application.callModeBackupDirectory(), backup)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/call-mode/backups/export?id="+filepath.Base(path), nil)
	recorder := httptest.NewRecorder()
	application.callModeBackupExportAPI(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != string(want) {
		t.Fatal("exported payload changed")
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, filepath.Base(path)) {
		t.Fatalf("content disposition = %q", disposition)
	}
}

func TestCallModeBackupListKeepsInvalidFilesVisibleAndSortsValidBackups(t *testing.T) {
	application := &app{dataDir: t.TempDir()}
	older := testCallModeBackup()
	older.SavedAt = older.SavedAt.Add(-time.Hour)
	newer := testCallModeBackup()
	newer.Reason = callModeBackupReasonBeforeRestore
	if _, err := writeCallModeUSBBackup(application.callModeBackupDirectory(), older); err != nil {
		t.Fatal(err)
	}
	newerPath, err := writeCallModeUSBBackup(application.callModeBackupDirectory(), newer)
	if err != nil {
		t.Fatal(err)
	}
	invalidID := "usb-before-call-mode-invalid.json"
	if err := os.WriteFile(filepath.Join(application.callModeBackupDirectory(), invalidID), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	backups, err := application.listCallModeUSBBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 3 {
		t.Fatalf("backup count = %d, want 3", len(backups))
	}
	var newerIndex, olderIndex = -1, -1
	var invalidFound bool
	for index, backup := range backups {
		switch {
		case backup.ID == filepath.Base(newerPath):
			newerIndex = index
		case backup.ID == invalidID:
			invalidFound = !backup.Valid && backup.Detail != ""
		case backup.Valid && backup.Reason == callModeBackupReasonBeforeCallMode:
			olderIndex = index
		}
	}
	if newerIndex < 0 || olderIndex < 0 || newerIndex >= olderIndex {
		t.Fatalf("valid backups not sorted newest first: %#v", backups)
	}
	if !invalidFound {
		t.Fatalf("invalid backup was hidden or marked valid: %#v", backups)
	}
}
