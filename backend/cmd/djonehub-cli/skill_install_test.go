package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledSkillInstallUpgradeAndUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DJONEHUB_SKILL_HOME", home)

	data, err := installBundledSkill("agents")
	if err != nil {
		t.Fatalf("install: %+v", err)
	}
	if data == nil {
		t.Fatal("install returned no data")
	}
	destination := filepath.Join(home, ".agents", "skills", "djonehub")
	if _, statErr := os.Stat(filepath.Join(destination, "SKILL.md")); statErr != nil {
		t.Fatal(statErr)
	}
	status := inspectSkillDestination(skillDestination{Target: "agents", Path: destination})
	if !status.Installed || !status.Managed {
		t.Fatalf("status=%+v", status)
	}

	if _, upgradeErr := installBundledSkill("agents"); upgradeErr != nil {
		t.Fatalf("upgrade: %+v", upgradeErr)
	}
	if _, uninstallErr := uninstallBundledSkill("agents"); uninstallErr != nil {
		t.Fatalf("uninstall: %+v", uninstallErr)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination still exists: %v", statErr)
	}
}

func TestBundledSkillRefusesUnmanagedDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DJONEHUB_SKILL_HOME", home)
	destination := filepath.Join(home, ".codex", "skills", "djonehub")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), []byte("user-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installBundledSkill("codex"); err == nil || err.Code != "skill_install_failed" {
		t.Fatalf("expected conflict, got %+v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(destination, "SKILL.md"))
	if readErr != nil || string(data) != "user-owned" {
		t.Fatalf("unmanaged Skill changed: %q %v", data, readErr)
	}
}
