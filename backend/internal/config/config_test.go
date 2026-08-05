package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestGetConfigPathReturnsInitializedPath(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: 7575
`)
	if err := InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	if got := GetConfigPath(); got != path {
		t.Fatalf("GetConfigPath() = %q, want %q", got, path)
	}
}

func TestLoadAcceptsConfigWithoutRemovedKeys(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: 7575
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
}

func TestDeviceFilePersistsQMIProxyFields(t *testing.T) {
	path := writeTempConfig(t, `
server:
  port: 7575
devices: []
`)

	err := AddDeviceInFile(path, DeviceConfig{
		ID:                 "dev-qmi",
		Interface:          "wwan0",
		ControlDevice:      "/dev/cdc-wdm0",
		QMIUseProxy:        true,
		QMIProxyPath:       "custom-qmi-proxy",
		QMIProxyExecutable: "/opt/vohive/bin/qmi-proxy",
	})
	if err != nil {
		t.Fatalf("AddDeviceInFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("devices=%d want 1", len(cfg.Devices))
	}
	dev := cfg.Devices[0]
	if !dev.QMIUseProxy {
		t.Fatal("QMIUseProxy=false, want true")
	}
	if dev.QMIProxyPath != "custom-qmi-proxy" {
		t.Fatalf("QMIProxyPath=%q, want custom-qmi-proxy", dev.QMIProxyPath)
	}
	if dev.QMIProxyExecutable != "/opt/vohive/bin/qmi-proxy" {
		t.Fatalf("QMIProxyExecutable=%q, want /opt/vohive/bin/qmi-proxy", dev.QMIProxyExecutable)
	}
}

func TestLoadDecodesDeviceESIMSwitchFlags(t *testing.T) {
	path := writeTempConfig(t, `
devices:
  - id: dev-esim
    esim_switch:
      use_refresh_true: true
      event_gated_converge: true
      radio_cycle: true
      reinit_window_ms: 12000
      nas_attach_timeout_ms: 45000
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("devices=%d want 1", len(cfg.Devices))
	}

	got := cfg.Devices[0].ESIMSwitch
	if !got.UseRefreshTrue {
		t.Fatal("UseRefreshTrue=false, want true")
	}
	if !got.EventGatedConverge {
		t.Fatal("EventGatedConverge=false, want true")
	}
	if !got.RadioCycle {
		t.Fatal("RadioCycle=false, want true")
	}
	if got.ReinitWindowMS != 12000 {
		t.Fatalf("ReinitWindowMS=%d, want 12000", got.ReinitWindowMS)
	}
	if got.NASAttachTimeoutMS != 45000 {
		t.Fatalf("NASAttachTimeoutMS=%d, want 45000", got.NASAttachTimeoutMS)
	}
}

func TestUpdateNotificationInFilePersistsQQ(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  enabled: false
feishu:
  enabled: false
qq:
  enabled: false
webhook:
  enabled: false
`)

	err := UpdateNotificationInFile(path,
		TelegramConfig{},
		FeishuConfig{},
		QQConfig{
			Enabled:   true,
			AppID:     "app-id",
			AppSecret: "secret",
			GroupIDs:  "G123",
			DirectIDs: "U456",
		},
		WebhookConfig{
			Enabled:      true,
			URLs:         []string{"https://example.com/webhook"},
			TextTemplate: "{{device_label}} {{text}}",
		},
		BarkConfig{},
		EmailConfig{},
		PushplusConfig{},
	)
	if err != nil {
		t.Fatalf("UpdateNotificationInFile() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"qq:",
		"enabled: true",
		"app_id: app-id",
		"app_secret: secret",
		"group_ids: G123",
		"direct_ids: U456",
		"text_template:",
		"{{device_label}}",
		"{{text}}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected config to contain %q, got:\n%s", want, text)
		}
	}
}
