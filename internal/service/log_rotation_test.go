package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"auto_reset_remaining/internal/config"
)

func TestLogRotatorRotateWithKey(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "logs")
	archiveDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "query.jsonl"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rotator := NewLogRotator(config.Config{
		QueryLogDir:           sourceDir,
		LogRotationEnabled:    true,
		LogRotationArchiveDir: archiveDir,
		LogRotationKey:        "secret",
	}, NewQueryLogger(sourceDir), nil)
	rotator.now = func() time.Time {
		return time.Date(2026, 5, 23, 2, 0, 0, 0, time.Local)
	}

	if _, err := rotator.RotateWithKey("wrong"); !errors.Is(err, ErrLogRotationUnauthorized) {
		t.Fatalf("RotateWithKey(wrong) error = %v, want unauthorized", err)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "query.jsonl")); err != nil {
		t.Fatalf("source log should remain after failed auth: %v", err)
	}

	result, err := rotator.RotateWithKey("secret")
	if err != nil {
		t.Fatalf("RotateWithKey(secret) error = %v", err)
	}
	if got := len(result.Files); got != 1 {
		t.Fatalf("rotated files = %d, want 1", got)
	}
}

func TestLogRotatorDisabled(t *testing.T) {
	rotator := NewLogRotator(config.Config{}, NewQueryLogger(t.TempDir()), nil)
	if _, err := rotator.RotateWithKey("secret"); !errors.Is(err, ErrLogRotationDisabled) {
		t.Fatalf("RotateWithKey() error = %v, want disabled", err)
	}
}

func TestNextDailyRotation(t *testing.T) {
	loc := time.FixedZone("test", 8*60*60)
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before 2am",
			now:  time.Date(2026, 5, 23, 1, 30, 0, 0, loc),
			want: time.Date(2026, 5, 23, 2, 0, 0, 0, loc),
		},
		{
			name: "at 2am",
			now:  time.Date(2026, 5, 23, 2, 0, 0, 0, loc),
			want: time.Date(2026, 5, 24, 2, 0, 0, 0, loc),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextDailyRotation(tc.now); !got.Equal(tc.want) {
				t.Fatalf("nextDailyRotation() = %s, want %s", got, tc.want)
			}
		})
	}
}
