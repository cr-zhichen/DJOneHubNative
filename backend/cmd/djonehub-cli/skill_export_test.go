package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledSkillExportIsAgentNeutralAndIdempotent(t *testing.T) {
	supportDirectory := t.TempDir()
	t.Setenv("DJONEHUB_SUPPORT_DIR", supportDirectory)

	first, commandErr := exportBundledSkill()
	if commandErr != nil {
		t.Fatalf("first export: %+v", commandErr)
	}
	expectedDirectory := filepath.Join(supportDirectory, "skill-source", "djonehub")
	if first.SourceDirectory != expectedDirectory || first.Entrypoint != filepath.Join(expectedDirectory, "SKILL.md") {
		t.Fatalf("paths=%+v", first)
	}
	if first.InstallResponsibility != "agent" || first.ContentSHA256 == "" || len(first.Files) == 0 {
		t.Fatalf("result=%+v", first)
	}
	if !strings.Contains(first.EntrypointContent, "name: djonehub") ||
		!strings.Contains(first.EntrypointContent, "## 每次任务开始时") ||
		strings.Contains(first.EntrypointContent, "## Start every task") {
		t.Fatalf("entrypoint content=%q", first.EntrypointContent)
	}
	if _, err := os.Stat(first.Entrypoint); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(first.SourceDirectory, "agents", "openai.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metadata), "安全读取设备状态") ||
		!strings.Contains(string(metadata), "$djonehub") {
		t.Fatalf("openai.yaml=%q", metadata)
	}

	second, commandErr := exportBundledSkill()
	if commandErr != nil {
		t.Fatalf("second export: %+v", commandErr)
	}
	if second.SourceDirectory != first.SourceDirectory || second.ContentSHA256 != first.ContentSHA256 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestBundledSkillExportRefusesUnmanagedDestination(t *testing.T) {
	supportDirectory := t.TempDir()
	t.Setenv("DJONEHUB_SUPPORT_DIR", supportDirectory)
	destination := filepath.Join(supportDirectory, "skill-source", "djonehub")
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(destination, "SKILL.md")
	if err := os.WriteFile(userFile, []byte("user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, commandErr := exportBundledSkill(); commandErr == nil || commandErr.Code != "skill_export_conflict" {
		t.Fatalf("expected conflict, got %+v", commandErr)
	}
	data, err := os.ReadFile(userFile)
	if err != nil || string(data) != "user-owned" {
		t.Fatalf("unmanaged source changed: %q %v", data, err)
	}
}

func TestAgentGuideDelegatesInstallationToAgent(t *testing.T) {
	result, commandErr := agentCommand(globalOptions{}, []string{"guide"})
	if commandErr != nil {
		t.Fatalf("guide: %+v", commandErr)
	}
	data, ok := result.Data.(map[string]string)
	if !ok {
		t.Fatalf("data=%T", result.Data)
	}
	if data["export_command"] != "djonehub agent skill export --format json" {
		t.Fatalf("guide=%v", data)
	}
	if strings.Contains(data["prompt"], "--target") || !strings.Contains(data["prompt"], "当前 Agent") {
		t.Fatalf("prompt=%q", data["prompt"])
	}
}
