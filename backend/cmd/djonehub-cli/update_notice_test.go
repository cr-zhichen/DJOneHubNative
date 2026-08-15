package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteIncludesCLISyncWarning(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("DJONEHUB_SUPPORT_DIR", temp)
	writeRuntimeManifest(t, filepath.Join(temp, cliRuntimeManifestName), cliRuntimeManifest{
		Version: 1, AppVersion: "2.0.0", BundledCLIVersion: "2.0.0",
	})
	var stdout, stderr bytes.Buffer
	if exitCode := execute(context.Background(), []string{"version"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", exitCode, stderr.String())
	}
	var envelope outputEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) != 1 || envelope.Warnings[0].Code != "cli_sync_required" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestInspectCLIRuntimeWarnsForDifferentVersion(t *testing.T) {
	temp := t.TempDir()
	manifestPath := filepath.Join(temp, cliRuntimeManifestName)
	writeRuntimeManifest(t, manifestPath, cliRuntimeManifest{
		Version: 1, AppVersion: "2.0.0", BundledCLIVersion: "2.0.0",
	})
	warnings := inspectCLIRuntime(manifestPath, os.Args[0], "1.0.0")
	if len(warnings) != 1 || warnings[0].Code != "cli_sync_required" {
		t.Fatalf("warnings=%+v", warnings)
	}
	if warnings[0].Details["installed_version"] != "1.0.0" ||
		warnings[0].Details["bundled_version"] != "2.0.0" {
		t.Fatalf("details=%v", warnings[0].Details)
	}
}

func TestInspectCLIRuntimeAcceptsMatchingBinary(t *testing.T) {
	temp := t.TempDir()
	manifestPath := filepath.Join(temp, cliRuntimeManifestName)
	hash, err := sha256File(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeManifest(t, manifestPath, cliRuntimeManifest{
		Version: 1, AppVersion: "1.0.0", BundledCLIVersion: "1.0.0", BundledCLISHA256: hash,
	})
	if warnings := inspectCLIRuntime(manifestPath, os.Args[0], "1.0.0"); len(warnings) != 0 {
		t.Fatalf("warnings=%+v", warnings)
	}
}

func TestInspectCLIRuntimeRejectsInsecureManifest(t *testing.T) {
	temp := t.TempDir()
	manifestPath := filepath.Join(temp, cliRuntimeManifestName)
	writeRuntimeManifest(t, manifestPath, cliRuntimeManifest{
		Version: 1, AppVersion: "2.0.0", BundledCLIVersion: "2.0.0",
	})
	if err := os.Chmod(manifestPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if warnings := inspectCLIRuntime(manifestPath, os.Args[0], "1.0.0"); len(warnings) != 0 {
		t.Fatalf("warnings=%+v", warnings)
	}
}

func writeRuntimeManifest(t *testing.T, path string, manifest cliRuntimeManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
