package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	modemruntime "github.com/iniwex5/vohive/internal/modem"
)

func TestCallModeTargetPreservesIdentityAndUnrelatedFlags(t *testing.T) {
	original, err := parseUSBComposition("AT+QCFG=\"USBCFG\"\r\n+QCFG: \"usbcfg\",0x2CA3,0x4006,1,1,1,1,1,0,1\r\nOK")
	if err != nil {
		t.Fatal(err)
	}
	target, err := original.callModeTarget()
	if err != nil {
		t.Fatal(err)
	}
	if target.VendorID != original.VendorID || target.ProductID != original.ProductID {
		t.Fatalf("identity changed: original=%#v target=%#v", original, target)
	}
	wantFlags := []int{1, 1, 1, 1, 1, 1, 1}
	if !reflect.DeepEqual(target.Flags, wantFlags) {
		t.Fatalf("flags = %v, want %v", target.Flags, wantFlags)
	}
	if !reflect.DeepEqual(original.Flags, []int{1, 1, 1, 1, 1, 0, 1}) {
		t.Fatalf("target mutated original flags: %v", original.Flags)
	}
	if got := target.command(); got != `AT+QCFG="USBCFG",0x2CA3,0x4006,1,1,1,1,1,1,1` {
		t.Fatalf("command = %q", got)
	}
}

func TestCallModeTargetRejectsUnknownComposition(t *testing.T) {
	for _, composition := range []usbComposition{
		{VendorID: 0, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 0, 1}},
		{VendorID: 0x2ca3, ProductID: 0x10000, Flags: []int{1, 1, 1, 1, 1, 0, 1}},
		{VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1}},
		{VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 2, 1}},
	} {
		if _, err := composition.callModeTarget(); err == nil {
			t.Fatalf("callModeTarget(%#v) unexpectedly succeeded", composition)
		}
	}
}

func TestCallModeVoiceConfigurationParsing(t *testing.T) {
	ims, capability, err := parseCallModeIMSConfiguration("AT+QCFG=\"ims\"\r\n+QCFG: \"ims\",0,1\r\nOK")
	if err != nil || ims != 0 || capability != 1 {
		t.Fatalf("IMS parse = %d,%d err=%v", ims, capability, err)
	}
	disabled, err := parseCallModeVoLTEDisabled("AT+QCFG=\"volte_disable\"\r\n+QCFG: \"volte/disable\",0\r\nOK")
	if err != nil || disabled != 0 {
		t.Fatalf("VoLTE parse = %d err=%v", disabled, err)
	}
	ready := callModeVoiceConfiguration{IMS: 1, VoLTECapability: 1, VoLTEDisabled: 0}
	if !ready.ready() {
		t.Fatal("valid IMS/VoLTE configuration was not ready")
	}
	for _, invalid := range []string{"+QCFG: \"ims\",2,1", "+QCFG: \"ims\",1,9", "ERROR"} {
		if _, _, err := parseCallModeIMSConfiguration(invalid); err == nil {
			t.Fatalf("invalid IMS response accepted: %q", invalid)
		}
	}
}

func TestCallModeATResponseErrorDetectionUsesWholeLines(t *testing.T) {
	for _, response := range []string{"ERROR\r\n", "\r\n+CME ERROR: 50\r\n", "AT+QCFG\r\n+CMS ERROR: 302\r\n"} {
		if !callModeATResponseIsError(response) {
			t.Fatalf("error response not detected: %q", response)
		}
	}
	for _, response := range []string{"OK\r\n", "NO ERROR HERE\r\n", "+QCFG: \"error\",1\r\nOK\r\n"} {
		if callModeATResponseIsError(response) {
			t.Fatalf("non-error response rejected: %q", response)
		}
	}
}

func TestCallModeATResponseSuccessRequiresStandaloneOKAndNoError(t *testing.T) {
	for _, response := range []string{"OK\r\n", "AT+QCFG\r\nOK\r\n", "+QCFG: \"usbcfg\",1\r\nOK"} {
		if !callModeATResponseSucceeded(response) {
			t.Fatalf("successful response rejected: %q", response)
		}
	}
	for _, response := range []string{"", "NO ERROR HERE", "ERROR\r\n", "ERROR\r\nOK\r\n"} {
		if callModeATResponseSucceeded(response) {
			t.Fatalf("unsuccessful response accepted: %q", response)
		}
	}
}

func TestCallModeATAcceptanceMatchesTransportContract(t *testing.T) {
	direct := &app{}
	if !direct.callModeATCommandAccepted("AT+QCFG\r\nOK\r\n", nil) {
		t.Fatal("direct USB terminal OK was rejected")
	}
	if direct.callModeATCommandAccepted("", nil) {
		t.Fatal("direct USB response without terminal OK was accepted")
	}
	managed := &app{modem: &modemruntime.Manager{}}
	if !managed.callModeATCommandAccepted("", nil) {
		t.Fatal("manager success contract was rejected after it stripped terminal OK")
	}
	if managed.callModeATCommandAccepted("ERROR", nil) {
		t.Fatal("manager ERROR response was accepted")
	}
}

func TestLegacyQADBKeyDerivationMatchesPublishedVectors(t *testing.T) {
	vectors := map[string]string{
		"12345678": "0jXKXQwSwMxYoeg",
		"15478726": "n9Qq0s1x4LtgAvt",
		"31711264": "SV3LHz1ynUZZmYU",
	}
	for challenge, expected := range vectors {
		actual, err := legacyQADBUnlockPassword(challenge)
		if err != nil {
			t.Fatalf("legacyQADBUnlockPassword(%q): %v", challenge, err)
		}
		if actual != expected {
			t.Fatalf("legacyQADBUnlockPassword(%q) = %q, want %q", challenge, actual, expected)
		}
	}
}

func TestLegacyQADBKeyChallengeParsingIsConservative(t *testing.T) {
	challenge, err := parseLegacyQADBKeyChallenge("AT+QADBKEY?\r\n+QADBKEY: 12345678\r\nOK")
	if err != nil || challenge != "12345678" {
		t.Fatalf("challenge = %q, err=%v", challenge, err)
	}
	for _, response := range []string{
		"AT+QADBKEY?\r\nERROR\r\n",
		"+QADBKEY: P1KR27_13_27_17\r\nOK\r\n",
		"+QADBKEY: 12345678\r\n",
		"+QADBKEY: 12345678\r\n+QADBKEY: 87654321\r\nOK\r\n",
	} {
		if _, err := parseLegacyQADBKeyChallenge(response); err == nil {
			t.Fatalf("unsupported response accepted: %q", response)
		}
	}
	managedChallenge, err := parseLegacyQADBKeyChallengeResponse("+QADBKEY: 12345678", false)
	if err != nil || managedChallenge != "12345678" {
		t.Fatalf("manager response challenge = %q, err=%v", managedChallenge, err)
	}
	if _, err := legacyQADBUnlockPassword("1234"); err == nil {
		t.Fatal("short QADBKEY challenge unexpectedly accepted")
	}
	if _, err := legacyQADBUnlockPassword("12345678\nAT+CFUN=1,1"); err == nil {
		t.Fatal("multiline QADBKEY challenge unexpectedly accepted")
	}
}

func TestUSBReadBackBeforeRebootAllowsOnlyPendingIntendedFlags(t *testing.T) {
	original := usbComposition{VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 0, 0}}
	target := usbComposition{VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 1, 1}}
	for _, actual := range []usbComposition{
		original,
		target,
		{VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{1, 1, 1, 1, 1, 0, 1}},
	} {
		if err := validateUSBReadBackBeforeReboot(original, target, actual); err != nil {
			t.Fatalf("safe pending read-back rejected: %#v: %v", actual, err)
		}
	}
	unexpectedFlag := usbComposition{VendorID: 0x2ca3, ProductID: 0x4006, Flags: []int{0, 1, 1, 1, 1, 0, 1}}
	if err := validateUSBReadBackBeforeReboot(original, target, unexpectedFlag); err == nil {
		t.Fatal("unexpected unrelated flag change accepted")
	}
	unexpectedIdentity := target
	unexpectedIdentity.ProductID = 0x0125
	if err := validateUSBReadBackBeforeReboot(original, target, unexpectedIdentity); err == nil {
		t.Fatal("unexpected USB identity change accepted")
	}
}

func TestSensitiveATFailureNeverIncludesAuthorizationPassword(t *testing.T) {
	password := "0jXKXQwSwMxYoeg"
	detail := callModeSensitiveATFailureDetail(nil, "AT+QADBKEY=\""+password+"\"\r\nERROR\r\n")
	if strings.Contains(detail, password) {
		t.Fatalf("sensitive password leaked in detail: %q", detail)
	}
	unknownDetail := callModeSensitiveATFailureDetail(fmt.Errorf("命令执行超时"), "")
	if !strings.Contains(unknownDetail, "可能已经接受持久授权") {
		t.Fatalf("ambiguous authorization result was not disclosed: %q", unknownDetail)
	}
}

func TestCallModeFailurePreservesAcceptedAuthorizationDisclosure(t *testing.T) {
	application := &app{dataDir: t.TempDir()}
	application.callMode = newCallModeStatus("enabling_usb", "正在开启接口")
	application.callMode.ADBAuthorizationAccepted = true
	application.failCallMode("模块拒绝新的 USB 配置", "模块返回 ERROR")
	status := application.callModeSnapshot()
	if !status.ADBAuthorizationAccepted || !strings.Contains(status.Detail, "持久授权") {
		t.Fatalf("accepted authorization disclosure lost: %#v", status)
	}
}

func TestModuleVoiceRelayURLsKeepImmutableOriginal(t *testing.T) {
	urls := moduleVoiceURLs("relay", "manifest.json")
	if len(urls) != 2 {
		t.Fatalf("relay URL count = %d", len(urls))
	}
	for _, url := range urls {
		if !strings.Contains(url, moduleVoiceCommit) || !strings.HasSuffix(url, "/manifest.json") {
			t.Fatalf("relay URL is not commit-pinned: %s", url)
		}
	}
	direct := moduleVoiceURLs("github", "manifest.json")
	if len(direct) != 1 || direct[0] != moduleVoiceBaseURL+"manifest.json" {
		t.Fatalf("direct URLs = %v", direct)
	}
}

func TestVerifyModuleVoiceFileChecksHashAndRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	payload := []byte("voice-runtime-test")
	hash := sha256.Sum256(payload)
	expected := moduleVoiceFile{
		Name:   "component.bin",
		Size:   int64(len(payload)),
		SHA256: fmt.Sprintf("%x", hash[:]),
		Mode:   0o644,
	}
	if err := os.WriteFile(filepath.Join(directory, expected.Name), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyModuleVoiceFile(directory, expected); err != nil {
		t.Fatalf("valid file rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, expected.Name), []byte("voice-runtime-TEST"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyModuleVoiceFile(directory, expected); err == nil {
		t.Fatal("bad hash unexpectedly accepted")
	}
	if err := os.Remove(filepath.Join(directory, expected.Name)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target.bin")
	if err := os.WriteFile(target, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, expected.Name)); err != nil {
		t.Fatal(err)
	}
	if err := verifyModuleVoiceFile(directory, expected); err == nil {
		t.Fatal("symlink unexpectedly accepted")
	}
}

func TestRuntimeDownloadUsesTemporaryFileAndVerifiesHash(t *testing.T) {
	payload := []byte("downloaded voice runtime")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	hash := sha256.Sum256(payload)
	expected := moduleVoiceFile{
		Name:   "helper.bin",
		Size:   int64(len(payload)),
		SHA256: fmt.Sprintf("%x", hash[:]),
		Mode:   0o755,
	}
	directory := t.TempDir()
	application := &app{}
	if err := application.downloadModuleVoiceFileWithClient(
		server.Client(), server.URL, "github", directory, expected, 0,
	); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if err := verifyModuleVoiceFile(directory, expected); err != nil {
		t.Fatalf("downloaded file failed verification: %v", err)
	}
	info, err := os.Stat(filepath.Join(directory, expected.Name))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != expected.Name {
		t.Fatalf("unexpected download artifacts: %v", entries)
	}
}

func TestRuntimeDownloadRejectsWrongHashWithoutTarget(t *testing.T) {
	payload := []byte("unexpected content")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	directory := t.TempDir()
	expected := moduleVoiceFile{
		Name:   "component.bin",
		Size:   int64(len(payload)),
		SHA256: strings.Repeat("0", 64),
		Mode:   0o644,
	}
	application := &app{}
	if err := application.downloadModuleVoiceFileWithClient(
		server.Client(), server.URL, "relay", directory, expected, 0,
	); err == nil {
		t.Fatal("wrong hash unexpectedly accepted")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed download left files behind: %v", entries)
	}
}

func TestValidVoiceNumber(t *testing.T) {
	for _, number := range []string{"10086", "+8613800138000", "*100#"} {
		if !validVoiceNumber(number) {
			t.Fatalf("valid number rejected: %q", number)
		}
	}
	for _, number := range []string{"", "86+138", "123;ATH", "123 456", strings.Repeat("1", 33)} {
		if validVoiceNumber(number) {
			t.Fatalf("invalid number accepted: %q", number)
		}
	}
}
