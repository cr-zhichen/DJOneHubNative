package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBackendDefaultSupportPathsUseFlameNamespace(t *testing.T) {
	t.Setenv("HOME", "/Users/flame-test")
	t.Setenv("DJONEHUB_SUPPORT_DIR", "")
	t.Setenv(cliConfigEnvironment, "")

	support := filepath.Join("/Users/flame-test", "Library", "Application Support", "FlameDJHub")
	if got := defaultAppSupportDirectory(); got != support {
		t.Fatalf("defaultAppSupportDirectory() = %q, want %q", got, support)
	}
	if got := cliAccessConfigPath(); got != filepath.Join(support, "cli-access.json") {
		t.Fatalf("cliAccessConfigPath() = %q", got)
	}
}

func TestPrepareAppSupportDirectoryCopiesPersistentLegacyData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DJONEHUB_SUPPORT_DIR", "")

	legacy := filepath.Join(home, "Library", "Application Support", "DJOneHubNative")
	if err := os.MkdirAll(filepath.Join(legacy, "module-backups", ".state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "sms-archive.json"), []byte(`{"messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "module-backups", "backup.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "module-backups", ".state", "settings.json"), []byte(`{"hidden":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nested.sock", "nested.LOG", "nested.pid", "nested.tmp"} {
		if err := os.WriteFile(filepath.Join(legacy, "module-backups", name), []byte("transient"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacy, "djonehub.sock"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacy, "sms-archive.json"), filepath.Join(legacy, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacy, "module-backups"), filepath.Join(legacy, "linked-directory")); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, "Library", "Application Support", "FlameDJHub")
	if err := os.MkdirAll(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(want, "sms-archive.json"), []byte("destination-old"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := prepareAppSupportDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("prepareAppSupportDirectory() = %q, want %q", got, want)
	}
	for _, relative := range []string{
		filepath.Join("module-backups", "backup.json"),
		filepath.Join("module-backups", ".state", "settings.json"),
	} {
		if _, err := os.Stat(filepath.Join(want, relative)); err != nil {
			t.Fatalf("expected migrated %s: %v", relative, err)
		}
	}
	if content, err := os.ReadFile(filepath.Join(want, "sms-archive.json")); err != nil {
		t.Fatal(err)
	} else if string(content) != "destination-old" {
		t.Fatalf("existing destination was overwritten: %q", content)
	}
	for _, relative := range []string{
		"djonehub.sock",
		"linked.json",
		"linked-directory",
		filepath.Join("module-backups", "nested.sock"),
		filepath.Join("module-backups", "nested.LOG"),
		filepath.Join("module-backups", "nested.pid"),
		filepath.Join("module-backups", "nested.tmp"),
	} {
		if _, err := os.Lstat(filepath.Join(want, relative)); !os.IsNotExist(err) {
			t.Fatalf("runtime file or symlink must not migrate: %s, err=%v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(legacy, "module-backups", "backup.json")); err != nil {
		t.Fatalf("legacy data must remain: %v", err)
	}
}

func TestCopySupportFilePublishesOnlyAfterCopyCompletes(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.fifo")
	destination := filepath.Join(directory, "archive.json")
	if err := syscall.Mkfifo(source, 0o600); err != nil {
		t.Fatal(err)
	}

	copyDone := make(chan error, 1)
	go func() {
		copyDone <- copySupportFile(source, destination, 0o600)
	}()

	writer, err := os.OpenFile(source, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	publishedEarly := false
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(destination); err == nil {
			publishedEarly = true
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := writer.Write([]byte("persistent-data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-copyDone; err != nil {
		t.Fatal(err)
	}
	if publishedEarly {
		t.Fatal("destination became visible before the source copy completed")
	}
	if got, err := os.ReadFile(destination); err != nil {
		t.Fatal(err)
	} else if string(got) != "persistent-data" {
		t.Fatalf("destination content = %q", got)
	}
}

func TestCopySupportFileCleansTemporaryFileAfterFailure(t *testing.T) {
	directory := t.TempDir()
	sourceDirectory := filepath.Join(directory, "source-directory")
	destination := filepath.Join(directory, "archive.json")
	if err := os.Mkdir(sourceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := copySupportFile(sourceDirectory, destination, 0o600); err == nil {
		t.Fatal("copySupportFile() error = nil, want source read failure")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed copy published destination, err=%v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".archive.json.tmp-") {
			t.Fatalf("failed copy left temporary file %q", entry.Name())
		}
	}
}
