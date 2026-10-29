package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathsUseFlameNamespace(t *testing.T) {
	t.Setenv("HOME", "/Users/flame-test")
	t.Setenv("DJONEHUB_SUPPORT_DIR", "")
	t.Setenv("DJONEHUB_SOCKET", "")
	t.Setenv("DJONEHUB_CLI_CONFIG", "")

	support := filepath.Join("/Users/flame-test", "Library", "Application Support", "FlameDJHub")
	if got := defaultSupportDirectory(); got != support {
		t.Fatalf("defaultSupportDirectory() = %q, want %q", got, support)
	}
	if got := defaultSocketPath(); got != filepath.Join(support, "flame-dj-hub.sock") {
		t.Fatalf("defaultSocketPath() = %q", got)
	}
	if got := defaultConfigPath(); got != filepath.Join(support, "cli-access.json") {
		t.Fatalf("defaultConfigPath() = %q", got)
	}
}

func TestLegacyEnvironmentOverridesRemainSupported(t *testing.T) {
	t.Setenv("DJONEHUB_SUPPORT_DIR", "/custom/flame")
	t.Setenv("DJONEHUB_SOCKET", "/custom/flame/custom.sock")
	t.Setenv("DJONEHUB_CLI_CONFIG", "/custom/flame/access.json")

	if got := defaultSupportDirectory(); got != "/custom/flame" {
		t.Fatalf("support override = %q", got)
	}
	if got := defaultSocketPath(); got != "/custom/flame/custom.sock" {
		t.Fatalf("socket override = %q", got)
	}
	if got := defaultConfigPath(); got != "/custom/flame/access.json" {
		t.Fatalf("config override = %q", got)
	}
}
