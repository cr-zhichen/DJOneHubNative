package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The Skill is embedded so a copied CLI never depends on the application
// bundle remaining at its original path.
//
//go:embed bundled_skill/djonehub
var bundledSkillFS embed.FS

const (
	bundledSkillRoot = "bundled_skill/djonehub"
	skillMarkerName  = ".djonehub-managed.json"
)

type skillDestination struct {
	Target string
	Path   string
}

type skillMarker struct {
	Product     string `json:"product"`
	Skill       string `json:"skill"`
	CLIVersion  string `json:"cli_version"`
	InstalledAt string `json:"installed_at"`
}

type skillStatusItem struct {
	Target    string `json:"target"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Managed   bool   `json:"managed"`
	Version   string `json:"version,omitempty"`
	Conflict  string `json:"conflict,omitempty"`
}

func installBundledSkill(target string) (any, *commandError) {
	destinations, err := resolveSkillDestinations(target)
	if err != nil {
		return nil, err
	}
	installedPaths := make([]string, 0, len(destinations))
	items := make([]skillStatusItem, 0, len(destinations))
	for _, destination := range destinations {
		if installErr := installSkillAt(destination); installErr != nil {
			return nil, &commandError{
				Code: "skill_install_failed", Message: installErr.Error(),
				Details: map[string]string{"target": destination.Target, "path": destination.Path}, ExitCode: 6,
			}
		}
		installedPaths = append(installedPaths, filepath.Join(destination.Path, "SKILL.md"))
		items = append(items, inspectSkillDestination(destination))
	}
	return map[string]any{
		"skill": "djonehub", "installed_paths": installedPaths, "installations": items,
		"reload_hint": "请立即读取返回路径中的每个 SKILL.md；部分 Agent 可能要到下一个任务才会自动发现新 Skill。",
	}, nil
}

func bundledSkillStatus(target string) (any, *commandError) {
	destinations, err := resolveSkillDestinations(target)
	if err != nil {
		return nil, err
	}
	items := make([]skillStatusItem, 0, len(destinations))
	for _, destination := range destinations {
		items = append(items, inspectSkillDestination(destination))
	}
	return map[string]any{"skill": "djonehub", "installations": items}, nil
}

func uninstallBundledSkill(target string) (any, *commandError) {
	destinations, err := resolveSkillDestinations(target)
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		status := inspectSkillDestination(destination)
		if !status.Installed {
			continue
		}
		if !status.Managed {
			return nil, &commandError{
				Code: "skill_conflict", Message: "refusing to remove an unmanaged Skill directory",
				Details: map[string]string{"target": destination.Target, "path": destination.Path}, ExitCode: 6,
			}
		}
		if info, statErr := os.Lstat(destination.Path); statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, &commandError{Code: "skill_uninstall_failed", Message: "managed Skill path changed during uninstall", ExitCode: 6}
		}
		if removeErr := os.RemoveAll(destination.Path); removeErr != nil {
			return nil, &commandError{Code: "skill_uninstall_failed", Message: removeErr.Error(), ExitCode: 6}
		}
		removed = append(removed, destination.Path)
	}
	return map[string]any{"skill": "djonehub", "removed_paths": removed}, nil
}

func resolveSkillDestinations(target string) ([]skillDestination, *commandError) {
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		target = "auto"
	}
	home := skillHomeDirectory()
	if home == "" {
		return nil, &commandError{Code: "home_unavailable", Message: "cannot locate the user home directory", ExitCode: 6}
	}
	all := map[string]skillDestination{
		"codex": {
			Target: "codex",
			Path:   filepath.Join(valueOrDefault("CODEX_HOME", filepath.Join(home, ".codex")), "skills", "djonehub"),
		},
		"claude": {
			Target: "claude",
			Path:   filepath.Join(valueOrDefault("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "skills", "djonehub"),
		},
		"agents": {Target: "agents", Path: filepath.Join(home, ".agents", "skills", "djonehub")},
	}
	if target != "auto" {
		destination, ok := all[target]
		if !ok {
			return nil, usageError("--target must be auto, codex, claude, or agents")
		}
		return []skillDestination{destination}, nil
	}

	destinations := make([]skillDestination, 0, 3)
	if os.Getenv("CODEX_HOME") != "" || directoryExists(filepath.Join(home, ".codex")) {
		destinations = append(destinations, all["codex"])
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" || directoryExists(filepath.Join(home, ".claude")) {
		destinations = append(destinations, all["claude"])
	}
	if directoryExists(filepath.Join(home, ".agents")) {
		destinations = append(destinations, all["agents"])
	}
	if len(destinations) == 0 {
		destinations = append(destinations, all["agents"])
	}
	sort.Slice(destinations, func(i, j int) bool { return destinations[i].Target < destinations[j].Target })
	return deduplicateDestinations(destinations), nil
}

func skillHomeDirectory() string {
	if override := strings.TrimSpace(os.Getenv("DJONEHUB_SKILL_HOME")); override != "" {
		return override
	}
	home, _ := os.UserHomeDir()
	return home
}

func valueOrDefault(environment, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(environment)); value != "" {
		return value
	}
	return fallback
}

func deduplicateDestinations(input []skillDestination) []skillDestination {
	seen := make(map[string]bool)
	result := make([]skillDestination, 0, len(input))
	for _, destination := range input {
		cleaned := filepath.Clean(destination.Path)
		if seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		destination.Path = cleaned
		result = append(result, destination)
	}
	return result
}

func installSkillAt(destination skillDestination) error {
	status := inspectSkillDestination(destination)
	if status.Installed && !status.Managed {
		return fmt.Errorf("%s already exists and is not managed by DJOneHub", destination.Path)
	}
	parent := filepath.Dir(destination.Path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".djonehub-skill-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := copyEmbeddedSkill(staging); err != nil {
		return err
	}
	marker := skillMarker{
		Product: "DJOneHub", Skill: "djonehub", CLIVersion: version,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	markerData, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, skillMarkerName), markerData, 0o644); err != nil {
		return err
	}

	backup := ""
	if status.Installed {
		backup = destination.Path + ".backup-" + fmt.Sprint(time.Now().UnixNano())
		if err := os.Rename(destination.Path, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, destination.Path); err != nil {
		if backup != "" {
			_ = os.Rename(backup, destination.Path)
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func copyEmbeddedSkill(destination string) error {
	return fs.WalkDir(bundledSkillFS, bundledSkillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(bundledSkillRoot, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("invalid embedded Skill path")
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := bundledSkillFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func inspectSkillDestination(destination skillDestination) skillStatusItem {
	status := skillStatusItem{Target: destination.Target, Path: destination.Path}
	info, err := os.Lstat(destination.Path)
	if errors.Is(err, os.ErrNotExist) {
		return status
	}
	status.Installed = err == nil
	if err != nil {
		status.Conflict = err.Error()
		return status
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		status.Conflict = "path is not a regular directory"
		return status
	}
	markerData, err := os.ReadFile(filepath.Join(destination.Path, skillMarkerName))
	if err != nil {
		status.Conflict = "DJOneHub management marker is missing"
		return status
	}
	var marker skillMarker
	if err := json.Unmarshal(markerData, &marker); err != nil || marker.Product != "DJOneHub" || marker.Skill != "djonehub" {
		status.Conflict = "DJOneHub management marker is invalid"
		return status
	}
	status.Managed = true
	status.Version = marker.CLIVersion
	return status
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
