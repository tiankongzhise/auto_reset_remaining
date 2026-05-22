package service

import (
	"encoding/json"
	"os"
	"path/filepath"
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
