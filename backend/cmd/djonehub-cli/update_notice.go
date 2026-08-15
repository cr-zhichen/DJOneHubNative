package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const cliRuntimeManifestName = "cli-runtime.json"

type cliRuntimeManifest struct {
	Version           int    `json:"version"`
	AppVersion        string `json:"app_version"`
	BundledCLIVersion string `json:"bundled_cli_version"`
	BundledCLISHA256  string `json:"bundled_cli_sha256"`
	AppBundlePath     string `json:"app_bundle_path"`
}

func cliRuntimeWarnings() []warningPayload {
	supportDirectory := defaultSupportDirectory()
	if strings.TrimSpace(supportDirectory) == "" {
		return nil
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil
	}
	return inspectCLIRuntime(
		filepath.Join(supportDirectory, cliRuntimeManifestName), executablePath, version)
}

func inspectCLIRuntime(manifestPath, executablePath, installedVersion string) []warningPayload {
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return nil
	}
	var manifest cliRuntimeManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 ||
		strings.TrimSpace(manifest.BundledCLIVersion) == "" {
		return nil
	}

	versionDiffers := manifest.BundledCLIVersion != installedVersion
	hashDiffers := false
	if !versionDiffers && len(manifest.BundledCLISHA256) == sha256.Size*2 {
		if executableHash, hashErr := sha256File(executablePath); hashErr == nil {
			hashDiffers = !strings.EqualFold(executableHash, manifest.BundledCLISHA256)
		}
	}
	if !versionDiffers && !hashDiffers {
		return nil
	}

	details := map[string]any{
		"installed_version": installedVersion,
		"bundled_version":   manifest.BundledCLIVersion,
		"action":            "Open DJOneHub → AI 与 CLI and select Sync CLI.",
	}
	if strings.TrimSpace(manifest.AppVersion) != "" {
		details["app_version"] = manifest.AppVersion
	}
	return []warningPayload{{
		Code:    "cli_sync_required",
		Message: "this CLI does not match the current DJOneHub app; sync it from DJOneHub’s AI 与 CLI page",
		Details: details,
	}}
}

func sha256File(path string) (string, error) {
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
