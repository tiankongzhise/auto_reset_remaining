package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBalanceDashboardReadsLogsAndIgnoresErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "query-2026-05-28.jsonl")
	body := []byte(`{"time":"2026-05-28T02:03:24.9984377+08:00","status":"ok","balance":82.1455066,"duration_ms":1161}
{"time":"2026-05-28T06:29:29.36814338+08:00","status":"ok","balance":81.7417792,"duration_ms":1307}
{"time":"2026-05-28T13:55:16.800940214+08:00","status":"balance_error","duration_ms":331,"error":"usage API returned HTTP 401"}
{"time":"2026-05-28T22:55:57.916137761+08:00","status":"ok","balance":45.0348754,"duration_ms":424}
not-json
{"time":"2026-05-28T23:02:36.081107537+08:00","status":"ok","duration_ms":436}
`)
	if err := os.WriteFile(logPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	dashboard, err := NewQueryLogger(dir).BalanceDashboard(time.Date(2026, 5, 28, 23, 3, 0, 0, location), location, 0.000001)
	if err != nil {
		t.Fatalf("BalanceDashboard() error = %v", err)
	}
	if dashboard.Status != "ok" {
		t.Fatalf("status = %s, want ok", dashboard.Status)
	}
	if dashboard.Current == nil || dashboard.Current.Balance != 45.0348754 {
		t.Fatalf("current = %+v, want latest ok balance", dashboard.Current)
	}
	if dashboard.Summary.SampleCount != 3 {
		t.Fatalf("sample count = %d, want 3", dashboard.Summary.SampleCount)
	}
	if dashboard.Summary.ChangeEvents != 2 {
		t.Fatalf("change events = %d, want 2", dashboard.Summary.ChangeEvents)
	}
	if dashboard.Ignored.ErrorRecords != 1 || dashboard.Ignored.InvalidJSON != 1 || dashboard.Ignored.MissingBalance != 1 {
		t.Fatalf("ignored = %+v, want one error, invalid json, and missing balance", dashboard.Ignored)
	}
	if dashboard.Summary.TotalSpent <= 37 || dashboard.Summary.TotalSpent >= 38 {
		t.Fatalf("total spent = %.6f, want about 37.110631", dashboard.Summary.TotalSpent)
	}
	if len(dashboard.Hourly) != 3 {
		t.Fatalf("hourly buckets = %d, want 3", len(dashboard.Hourly))
	}
}

func TestBalanceDashboardMissingLogDirReturnsEmptyDashboard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	dashboard, err := NewQueryLogger(dir).BalanceDashboard(time.Now(), time.Local, 0.000001)
	if err != nil {
		t.Fatalf("BalanceDashboard() error = %v", err)
	}
	if dashboard.Status != "empty" || dashboard.Current != nil || dashboard.Summary.SampleCount != 0 {
		t.Fatalf("dashboard = %+v, want empty", dashboard)
	}
}

func TestBalanceDashboardSpentSumsOnlyDownwardMoves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"time":"2026-05-28T01:00:00+08:00","status":"ok","balance":10,"duration_ms":100}
{"time":"2026-05-28T02:00:00+08:00","status":"ok","balance":7,"duration_ms":100}
{"time":"2026-05-28T03:00:00+08:00","status":"ok","balance":9,"duration_ms":100}
{"time":"2026-05-28T04:00:00+08:00","status":"ok","balance":6,"duration_ms":100}
`)
	if err := os.WriteFile(filepath.Join(dir, "query.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	dashboard, err := NewQueryLogger(dir).BalanceDashboard(time.Now(), time.FixedZone("Asia/Shanghai", 8*60*60), 0.000001)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Summary.TotalChange != -4 {
		t.Fatalf("total change = %.2f, want -4", dashboard.Summary.TotalChange)
	}
	if dashboard.Summary.TotalSpent != 6 {
		t.Fatalf("total spent = %.2f, want 6", dashboard.Summary.TotalSpent)
	}
}

func TestBalanceDashboardJSONRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "query.jsonl"), []byte(`{"time":"2026-05-28T02:03:24+08:00","status":"ok","balance":82.1455066,"duration_ms":1161}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dashboard, err := NewQueryLogger(dir).BalanceDashboard(time.Now(), time.Local, 0.000001)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(dashboard)
	if err != nil {
		t.Fatalf("marshal dashboard: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty JSON")
	}
}
