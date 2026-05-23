package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueryLoggerRotateToArchivesLocalFiles(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "logs")
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(filepath.Join(sourceDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "query-2026-05-23.jsonl"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "app.log"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "nested", "ignored.log"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := NewQueryLogger(sourceDir).RotateTo(archiveRoot, time.Date(2026, 5, 23, 2, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("RotateTo() error = %v", err)
	}
	if result.SkippedReason != "" {
		t.Fatalf("RotateTo() skipped: %s", result.SkippedReason)
	}
	if got := len(result.Files); got != 2 {
		t.Fatalf("rotated files = %d, want 2", got)
	}
	for _, name := range []string{"query-2026-05-23.jsonl", "app.log"} {
		if _, err := os.Stat(filepath.Join(sourceDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists in source dir", name)
		}
		if _, err := os.Stat(filepath.Join(result.ArchiveDir, name)); err != nil {
			t.Fatalf("%s missing from archive: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "nested", "ignored.log")); err != nil {
		t.Fatalf("nested log should not be rotated: %v", err)
	}
}

func TestQueryLoggerRotateToCreatesMissingArchiveDir(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "logs")
	archiveRoot := filepath.Join(t.TempDir(), "missing", "archives")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "query.jsonl"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := NewQueryLogger(sourceDir).RotateTo(archiveRoot, time.Now())
	if err != nil {
		t.Fatalf("RotateTo() error = %v", err)
	}
	if result.SkippedReason != "" {
		t.Fatalf("RotateTo() skipped: %s", result.SkippedReason)
	}
	if _, err := os.Stat(result.ArchiveDir); err != nil {
		t.Fatalf("archive dir was not created: %v", err)
	}
}

func TestQueryLoggerRotateToSkipsWhenArchivePathCannotBeDirectory(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "logs")
	archiveRoot := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "query.jsonl"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archiveRoot, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := NewQueryLogger(sourceDir).RotateTo(archiveRoot, time.Now())
	if err != nil {
		t.Fatalf("RotateTo() error = %v", err)
	}
	if !strings.Contains(result.SkippedReason, "not a directory") {
		t.Fatalf("skipped reason = %q, want not a directory", result.SkippedReason)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "query.jsonl")); err != nil {
		t.Fatalf("source log should remain after skipped rotation: %v", err)
	}
}
