package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type QueryLogger struct {
	dir string
	mu  sync.Mutex
}

type QueryLogEntry struct {
	Time       time.Time `json:"time"`
	Status     string    `json:"status"`
	Balance    *float64  `json:"balance,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

type LogRotationResult struct {
	RotatedAt     time.Time        `json:"rotated_at"`
	SourceDir     string           `json:"source_dir"`
	ArchiveDir    string           `json:"archive_dir,omitempty"`
	Files         []RotatedLogFile `json:"files"`
	TotalBytes    int64            `json:"total_bytes"`
	SkippedReason string           `json:"skipped_reason,omitempty"`
}

type RotatedLogFile struct {
	Name        string `json:"name"`
	Bytes       int64  `json:"bytes"`
	ArchivePath string `json:"archive_path"`
}

func NewQueryLogger(dir string) *QueryLogger {
	return &QueryLogger{dir: dir}
}

func (l *QueryLogger) Log(entry QueryLogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	name := "query-" + entry.Time.Format("2006-01-02") + ".jsonl"
	file, err := os.OpenFile(filepath.Join(l.dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	return encoder.Encode(entry)
}

func (l *QueryLogger) RotateTo(archiveRoot string, rotatedAt time.Time) (LogRotationResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	sourceDir := strings.TrimSpace(l.dir)
	archiveRoot = strings.TrimSpace(archiveRoot)
	result := LogRotationResult{
		RotatedAt: rotatedAt,
		SourceDir: sourceDir,
	}
	if sourceDir == "" {
		return result, errors.New("query log dir is empty")
	}
	if archiveRoot == "" {
		return result, errors.New("log rotation archive dir is empty")
	}
	if sameFilesystemPath(sourceDir, archiveRoot) {
		return result, errors.New("log rotation archive dir must be different from query log dir")
	}
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		result.SkippedReason = "query log dir unavailable: " + err.Error()
		return result, nil
	}

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return result, err
	}

	type pendingFile struct {
		name string
		path string
		size int64
		mode os.FileMode
	}
	var pending []pendingFile
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		pending = append(pending, pendingFile{
			name: info.Name(),
			path: filepath.Join(sourceDir, info.Name()),
			size: info.Size(),
			mode: info.Mode().Perm(),
		})
	}
	if len(pending) == 0 {
		result.SkippedReason = "no log files to rotate"
		return result, nil
	}

	info, err := os.Stat(archiveRoot)
	if err == nil && !info.IsDir() {
		result.SkippedReason = "archive path is not a directory"
		return result, nil
	}
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result.SkippedReason = "archive dir unavailable: " + err.Error()
			return result, nil
		}
		if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
			result.SkippedReason = "archive dir unavailable: " + err.Error()
			return result, nil
		}
	}
	archiveDir, err := createArchiveDir(archiveRoot, rotatedAt)
	if err != nil {
		result.SkippedReason = "archive dir unavailable: " + err.Error()
		return result, nil
	}
	result.ArchiveDir = archiveDir

	for _, file := range pending {
		archivePath := filepath.Join(archiveDir, file.name)
		if err := moveRegularFile(file.path, archivePath, file.mode); err != nil {
			return result, fmt.Errorf("archive %s: %w", file.name, err)
		}
		result.Files = append(result.Files, RotatedLogFile{
			Name:        file.name,
			Bytes:       file.size,
			ArchivePath: archivePath,
		})
		result.TotalBytes += file.size
	}
	return result, nil
}

func createArchiveDir(root string, rotatedAt time.Time) (string, error) {
	name := rotatedAt.Format("2006-01-02_15-04-05")
	for i := 0; i < 1000; i++ {
		dirName := name
		if i > 0 {
			dirName = fmt.Sprintf("%s-%03d", name, i)
		}
		path := filepath.Join(root, dirName)
		if err := os.Mkdir(path, 0o755); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not create unique archive dir under %s", root)
}

func moveRegularFile(src, dst string, mode os.FileMode) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyRegularFile(src, dst, mode); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyRegularFile(src, dst string, mode os.FileMode) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		_ = destination.Close()
		if cleanup {
			_ = os.Remove(dst)
		}
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	if err := destination.Close(); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func sameFilesystemPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}
