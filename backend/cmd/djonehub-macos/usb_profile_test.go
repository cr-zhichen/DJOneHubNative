package main

import (
	"path/filepath"
	"testing"
)

func TestParseUSBConfigPreservesEveryNonUACField(t *testing.T) {
	config, err := parseUSBConfig(`AT+QCFG="usbcfg"
+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,1
OK`)
	if err != nil {
		t.Fatal(err)
	}
	if !config.uacEnabled() {
		t.Fatal("expected UAC enabled")
	}
	want := `AT+QCFG="usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,0`
	if got := config.withUAC(false); got != want {
		t.Fatalf("mobile command = %q, want %q", got, want)
	}
}

func TestParseUSBConfigRejectsUnknownLayout(t *testing.T) {
	for _, response := range []string{
		`+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1`,
		`+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,2`,
		`ERROR`,
	} {
		if _, err := parseUSBConfig(response); err == nil {
			t.Fatalf("parseUSBConfig(%q) unexpectedly succeeded", response)
		}
	}
}

func TestUSBProfileIntentDefaultsToNoAutomaticRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usb-profile-intent.json")
	a := &app{usbProfileIntentPath: path}
	if err := a.loadUSBProfileIntentLocked(); err != nil {
		t.Fatalf("load blank intent: %v", err)
	}
	if a.usbProfileMobileArmed {
		t.Fatal("a fresh installation must not automatically change a non-UAC module")
	}
	a.usbProfileMobileArmed = true
	if err := a.persistUSBProfileIntentLocked(); err != nil {
		t.Fatalf("persist mobile intent: %v", err)
	}
	reloaded := &app{usbProfileIntentPath: path}
	if err := reloaded.loadUSBProfileIntentLocked(); err != nil {
		t.Fatalf("reload intent: %v", err)
	}
	if !reloaded.usbProfileMobileArmed {
		t.Fatal("an explicit iPhone/iPad selection must survive the reconnect")
	}
}
