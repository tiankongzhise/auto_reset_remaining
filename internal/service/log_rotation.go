package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"auto_reset_remaining/internal/config"
)

var (
	ErrLogRotationDisabled     = errors.New("log rotation disabled")
	ErrLogRotationUnauthorized = errors.New("invalid log rotation key")
)

type LogRotator struct {
	queries    *QueryLogger
	archiveDir string
	key        string
	enabled    bool
	logger     *log.Logger
	now        func() time.Time

	mu sync.Mutex
}

func NewLogRotator(cfg config.Config, queries *QueryLogger, logger *log.Logger) *LogRotator {
	if logger == nil {
		logger = log.Default()
	}
	return &LogRotator{
		queries:    queries,
		archiveDir: strings.TrimSpace(cfg.LogRotationArchiveDir),
		key:        strings.TrimSpace(cfg.LogRotationKey),
		enabled:    cfg.LogRotationEnabled,
		logger:     logger,
		now:        time.Now,
	}
}

func (r *LogRotator) Enabled() bool {
	return r != nil && r.enabled
}

func (r *LogRotator) RotateWithKey(key string) (LogRotationResult, error) {
	if r == nil {
		return LogRotationResult{}, ErrLogRotationDisabled
	}
	if !r.enabled {
		return LogRotationResult{}, ErrLogRotationDisabled
	}
	if !r.validKey(key) {
		return LogRotationResult{}, ErrLogRotationUnauthorized
	}
	return r.RotateNow()
}

func (r *LogRotator) RotateNow() (LogRotationResult, error) {
	if r == nil || !r.enabled {
		return LogRotationResult{}, ErrLogRotationDisabled
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queries.RotateTo(r.archiveDir, r.now())
}

func (r *LogRotator) RunDaily(ctx context.Context) {
	if r == nil || !r.enabled {
		return
	}
	for {
		next := nextDailyRotation(r.now())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			result, err := r.RotateNow()
			if err != nil {
				r.logger.Printf("log rotation skipped: %v", err)
				continue
			}
			if result.SkippedReason != "" {
				r.logger.Printf("log rotation skipped: %s", result.SkippedReason)
				continue
			}
			r.logger.Printf("log rotation completed archive_dir=%s files=%d bytes=%d", result.ArchiveDir, len(result.Files), result.TotalBytes)
		}
	}
}

func (r *LogRotator) validKey(key string) bool {
	if strings.TrimSpace(r.key) == "" {
		return false
	}
	provided := strings.TrimSpace(key)
	if len(provided) != len(r.key) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(r.key)) == 1
}

func nextDailyRotation(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
