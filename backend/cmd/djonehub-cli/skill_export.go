package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const skillExportMarkerName = ".djonehub-source.json"

var errSkillExportConflict = errors.New("skill export destination is not a verified DJOneHub source")

type skillExportMarker struct {
	Product       string `json:"product"`
	Skill         string `json:"skill"`
	CLIVersion    string `json:"cli_version"`
	ContentSHA256 string `json:"content_sha256"`
	ExportedAt    string `json:"exported_at"`
}

type skillExportFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type skillExportResult struct {
	Skill                 string            `json:"skill"`
	Version               string            `json:"version"`
	SourceDirectory       string            `json:"source_directory"`
	Entrypoint            string            `json:"entrypoint"`
	EntrypointContent     string            `json:"entrypoint_content"`
	Files                 []skillExportFile `json:"files"`
	ContentSHA256         string            `json:"content_sha256"`
	InstallResponsibility string            `json:"install_responsibility"`
	InstallHint           string            `json:"install_hint"`
}

func exportBundledSkill() (skillExportResult, *commandError) {
	destination, err := skillExportDestination()
	if err != nil {
		return skillExportResult{}, &commandError{
			Code: "skill_export_failed", Message: err.Error(), ExitCode: 6,
		}
	}
	result, err := exportSkillAt(destination)
	if err == nil {
		return result, nil
	}
	code := "skill_export_failed"
	if errors.Is(err, errSkillExportConflict) {
		code = "skill_export_conflict"
	}
	return skillExportResult{}, &commandError{
		Code: code, Message: err.Error(), Details: map[string]string{"path": destination}, ExitCode: 6,
	}
}

func skillExportDestination() (string, error) {
	supportDirectory := strings.TrimSpace(defaultSupportDirectory())
	if supportDirectory == "" {
		return "", errors.New("cannot locate DJOneHub’s application support directory")
	}
	return filepath.Join(supportDirectory, "skill-source", "djonehub"), nil
}

func exportSkillAt(destination string) (skillExportResult, error) {
	existing, marker, err := inspectSkillExport(destination)
	if err != nil {
		return skillExportResult{}, err
	}
	var existingFiles []skillExportFile
	if existing {
		files, contentHash, describeErr := describeSkillFiles(destination)
		if describeErr != nil || !strings.EqualFold(contentHash, marker.ContentSHA256) {
			return skillExportResult{}, fmt.Errorf("%w: exported Skill contents were modified", errSkillExportConflict)
		}
		existingFiles = files
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return skillExportResult{}, err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return skillExportResult{}, err
	}
	staging, err := os.MkdirTemp(parent, ".djonehub-source-")
	if err != nil {
		return skillExportResult{}, err
	}
	defer os.RemoveAll(staging)
	if err := copyEmbeddedSkill(staging); err != nil {
		return skillExportResult{}, err
	}
	files, contentHash, err := describeSkillFiles(staging)
	if err != nil {
		return skillExportResult{}, err
	}
	if existing && marker.CLIVersion == version && strings.EqualFold(marker.ContentSHA256, contentHash) {
		return makeSkillExportResult(destination, existingFiles, contentHash)
	}
	marker = skillExportMarker{
		Product: "DJOneHub", Skill: "djonehub", CLIVersion: version,
		ContentSHA256: contentHash, ExportedAt: time.Now().UTC().Format(time.RFC3339),
	}
	markerData, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return skillExportResult{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, skillExportMarkerName), markerData, 0o600); err != nil {
		return skillExportResult{}, err
	}

	backup := ""
	if existing {
		backup = destination + ".backup-" + fmt.Sprint(time.Now().UnixNano())
		if err := os.Rename(destination, backup); err != nil {
			return skillExportResult{}, err
		}
	}
	if err := os.Rename(staging, destination); err != nil {
		if backup != "" {
			_ = os.Rename(backup, destination)
		}
		return skillExportResult{}, err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return makeSkillExportResult(destination, files, contentHash)
}

func inspectSkillExport(destination string) (bool, skillExportMarker, error) {
	var marker skillExportMarker
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return false, marker, nil
	}
	if err != nil {
		return false, marker, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, marker, fmt.Errorf("%w: destination is not a regular directory", errSkillExportConflict)
	}
	markerPath := filepath.Join(destination, skillExportMarkerName)
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm()&0o077 != 0 {
		return false, marker, fmt.Errorf("%w: management marker is missing or insecure", errSkillExportConflict)
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return false, marker, err
	}
	if json.Unmarshal(data, &marker) != nil || marker.Product != "DJOneHub" ||
		marker.Skill != "djonehub" || marker.ContentSHA256 == "" {
		return false, marker, fmt.Errorf("%w: management marker is invalid", errSkillExportConflict)
	}
	return true, marker, nil
}

func describeSkillFiles(root string) ([]skillExportFile, string, error) {
	files := make([]skillExportFile, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("invalid exported Skill path")
		}
		if relative == skillExportMarkerName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("exported Skill contains a non-regular file: %s", relative)
		}
		hash, err := hashFile(path)
		if err != nil {
			return err
		}
		files = append(files, skillExportFile{
			Path: filepath.ToSlash(relative), SHA256: hash, Bytes: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	hasEntrypoint := false
	for _, file := range files {
		if file.Path == "SKILL.md" {
			hasEntrypoint = true
			break
		}
	}
	if !hasEntrypoint {
		return nil, "", errors.New("embedded Skill entrypoint is missing")
	}
	aggregate := sha256.New()
	for _, file := range files {
		aggregate.Write([]byte(file.Path))
		aggregate.Write([]byte{0})
		digest, _ := hex.DecodeString(file.SHA256)
		aggregate.Write(digest)
	}
	return files, hex.EncodeToString(aggregate.Sum(nil)), nil
}

func makeSkillExportResult(destination string, files []skillExportFile, contentHash string) (skillExportResult, error) {
	entrypoint := filepath.Join(destination, "SKILL.md")
	content, err := os.ReadFile(entrypoint)
	if err != nil {
		return skillExportResult{}, err
	}
	return skillExportResult{
		Skill: "djonehub", Version: version,
		SourceDirectory: destination, Entrypoint: entrypoint, EntrypointContent: string(content),
		Files: files, ContentSHA256: contentHash, InstallResponsibility: "agent",
		InstallHint: "请使用当前 Agent 支持的方式从 source_directory 安装 Skill。如果不支持安装 Skill，请在本次任务中直接读取并遵循 entrypoint_content。",
	}, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
