package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultBalanceDashboardMaxPoints = 180
	defaultBalanceChangeLimit        = 40
	defaultBalanceBucketLimit        = 48
	defaultBalanceChangeEpsilon      = 0.000001
)

type BalanceDashboard struct {
	GeneratedAt time.Time           `json:"generated_at"`
	Status      string              `json:"status"`
	LogDir      string              `json:"log_dir"`
	Current     *BalancePoint       `json:"current,omitempty"`
	Summary     BalanceSummary      `json:"summary"`
	Series      []BalancePoint      `json:"series"`
	Changes     []BalanceChange     `json:"changes"`
	Hourly      []BalanceTimeBucket `json:"hourly"`
	Ignored     BalanceIgnored      `json:"ignored"`
}

type BalancePoint struct {
	Time       time.Time `json:"time"`
	Balance    float64   `json:"balance"`
	Delta      float64   `json:"delta"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

type BalanceSummary struct {
	SampleCount       int        `json:"sample_count"`
	ChangeEvents      int        `json:"change_events"`
	FirstTime         *time.Time `json:"first_time,omitempty"`
	LastTime          *time.Time `json:"last_time,omitempty"`
	FirstBalance      float64    `json:"first_balance"`
	LastBalance       float64    `json:"last_balance"`
	MinBalance        float64    `json:"min_balance"`
	MaxBalance        float64    `json:"max_balance"`
	TotalChange       float64    `json:"total_change"`
	TotalSpent        float64    `json:"total_spent"`
	TodayChange       float64    `json:"today_change"`
	TodaySpent        float64    `json:"today_spent"`
	WindowHours       float64    `json:"window_hours"`
	SpendPerHour      float64    `json:"spend_per_hour"`
	AverageDurationMS float64    `json:"average_duration_ms"`
}

type BalanceChange struct {
	Time        time.Time `json:"time"`
	FromBalance float64   `json:"from_balance"`
	ToBalance   float64   `json:"to_balance"`
	Delta       float64   `json:"delta"`
}

type BalanceTimeBucket struct {
	Time         time.Time `json:"time"`
	StartBalance float64   `json:"start_balance"`
	EndBalance   float64   `json:"end_balance"`
	Change       float64   `json:"change"`
	Samples      int       `json:"samples"`
}

type BalanceIgnored struct {
	Total          int `json:"total"`
	InvalidJSON    int `json:"invalid_json"`
	ErrorRecords   int `json:"error_records"`
	MissingBalance int `json:"missing_balance"`
	InvalidTime    int `json:"invalid_time"`
}

type balanceSample struct {
	Time       time.Time
	Balance    float64
	DurationMS int64
}

func (l *QueryLogger) BalanceDashboard(now time.Time, location *time.Location, epsilon float64) (BalanceDashboard, error) {
	if l == nil {
		return BalanceDashboard{}, errors.New("query logger is not configured")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if location == nil {
		location = time.Local
	}
	if epsilon <= 0 {
		epsilon = defaultBalanceChangeEpsilon
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	samples, ignored, err := readBalanceSamples(l.dir)
	if err != nil {
		return BalanceDashboard{}, err
	}
	return buildBalanceDashboard(samples, ignored, now, location, l.dir, epsilon), nil
}

func readBalanceSamples(dir string) ([]balanceSample, BalanceIgnored, error) {
	var ignored BalanceIgnored
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, ignored, errors.New("query log dir is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ignored, nil
		}
		return nil, ignored, err
	}

	var samples []balanceSample
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, ignored, err
		}
		if !info.Mode().IsRegular() || !strings.HasSuffix(strings.ToLower(info.Name()), ".jsonl") {
			continue
		}
		fileSamples, fileIgnored, err := readBalanceSampleFile(filepath.Join(dir, info.Name()))
		if err != nil {
			return nil, ignored, err
		}
		samples = append(samples, fileSamples...)
		ignored.add(fileIgnored)
	}

	sort.SliceStable(samples, func(i, j int) bool {
		return samples[i].Time.Before(samples[j].Time)
	})
	return samples, ignored, nil
}

func readBalanceSampleFile(path string) ([]balanceSample, BalanceIgnored, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, BalanceIgnored{}, err
	}
	defer file.Close()

	var samples []balanceSample
	var ignored BalanceIgnored
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry QueryLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			ignored.InvalidJSON++
			ignored.Total++
			continue
		}
		sample, ok, reason := balanceSampleFromLogEntry(entry)
		if !ok {
			switch reason {
			case "error":
				ignored.ErrorRecords++
			case "time":
				ignored.InvalidTime++
			default:
				ignored.MissingBalance++
			}
			ignored.Total++
			continue
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, ignored, err
	}
	return samples, ignored, nil
}

func balanceSampleFromLogEntry(entry QueryLogEntry) (balanceSample, bool, string) {
	if entry.Error != "" || strings.Contains(strings.ToLower(entry.Status), "error") {
		return balanceSample{}, false, "error"
	}
	if entry.Time.IsZero() {
		return balanceSample{}, false, "time"
	}
	if entry.Balance == nil || math.IsNaN(*entry.Balance) || math.IsInf(*entry.Balance, 0) {
		return balanceSample{}, false, "balance"
	}
	return balanceSample{
		Time:       entry.Time,
		Balance:    *entry.Balance,
		DurationMS: entry.DurationMS,
	}, true, ""
}

func buildBalanceDashboard(samples []balanceSample, ignored BalanceIgnored, now time.Time, location *time.Location, logDir string, epsilon float64) BalanceDashboard {
	dashboard := BalanceDashboard{
		GeneratedAt: now,
		Status:      "empty",
		LogDir:      logDir,
		Ignored:     ignored,
		Series:      []BalancePoint{},
		Changes:     []BalanceChange{},
		Hourly:      []BalanceTimeBucket{},
	}
	if len(samples) == 0 {
		return dashboard
	}

	dashboard.Status = "ok"
	dashboard.Series = aggregateBalanceSeries(samples, defaultBalanceDashboardMaxPoints, epsilon)
	dashboard.Changes = recentBalanceChanges(samples, defaultBalanceChangeLimit, epsilon)
	dashboard.Hourly = hourlyBalanceBuckets(samples, location, defaultBalanceBucketLimit)
	dashboard.Summary = summarizeBalanceSamples(samples, location, epsilon)
	last := samples[len(samples)-1]
	previous := last.Balance
	if len(samples) > 1 {
		previous = samples[len(samples)-2].Balance
	}
	current := BalancePoint{
		Time:       last.Time,
		Balance:    last.Balance,
		Delta:      last.Balance - previous,
		DurationMS: last.DurationMS,
	}
	dashboard.Current = &current
	return dashboard
}

func summarizeBalanceSamples(samples []balanceSample, location *time.Location, epsilon float64) BalanceSummary {
	first := samples[0]
	last := samples[len(samples)-1]
	minBalance := first.Balance
	maxBalance := first.Balance
	var totalDuration int64
	for _, sample := range samples {
		minBalance = math.Min(minBalance, sample.Balance)
		maxBalance = math.Max(maxBalance, sample.Balance)
		totalDuration += sample.DurationMS
	}

	todayFirst := first
	todayStart := 0
	lastLocal := last.Time.In(location)
	for idx, sample := range samples {
		local := sample.Time.In(location)
		if local.Year() == lastLocal.Year() && local.YearDay() == lastLocal.YearDay() {
			todayFirst = sample
			todayStart = idx
			break
		}
	}

	totalChange := last.Balance - first.Balance
	todayChange := last.Balance - todayFirst.Balance
	totalSpent := spentAcross(samples, 0)
	todaySpent := spentAcross(samples, todayStart)
	windowHours := last.Time.Sub(first.Time).Hours()
	spendPerHour := 0.0
	if windowHours > 0 {
		spendPerHour = totalSpent / windowHours
	}
	averageDuration := float64(totalDuration) / float64(len(samples))
	return BalanceSummary{
		SampleCount:       len(samples),
		ChangeEvents:      countBalanceChanges(samples, epsilon),
		FirstTime:         &first.Time,
		LastTime:          &last.Time,
		FirstBalance:      first.Balance,
		LastBalance:       last.Balance,
		MinBalance:        minBalance,
		MaxBalance:        maxBalance,
		TotalChange:       totalChange,
		TotalSpent:        totalSpent,
		TodayChange:       todayChange,
		TodaySpent:        todaySpent,
		WindowHours:       windowHours,
		SpendPerHour:      spendPerHour,
		AverageDurationMS: averageDuration,
	}
}

func aggregateBalanceSeries(samples []balanceSample, maxPoints int, epsilon float64) []BalancePoint {
	if len(samples) == 0 {
		return nil
	}
	if maxPoints <= 0 {
		maxPoints = defaultBalanceDashboardMaxPoints
	}
	compressed := make([]balanceSample, 0, len(samples))
	for i, sample := range samples {
		if i == 0 || i == len(samples)-1 || balanceChanged(compressed[len(compressed)-1].Balance, sample.Balance, epsilon) {
			compressed = append(compressed, sample)
		}
	}
	if len(compressed) > maxPoints {
		compressed = bucketBalanceSamples(compressed, maxPoints)
	}

	points := make([]BalancePoint, 0, len(compressed))
	for i, sample := range compressed {
		previous := sample.Balance
		if i > 0 {
			previous = compressed[i-1].Balance
		}
		points = append(points, BalancePoint{
			Time:       sample.Time,
			Balance:    sample.Balance,
			Delta:      sample.Balance - previous,
			DurationMS: sample.DurationMS,
		})
	}
	return points
}

func bucketBalanceSamples(samples []balanceSample, maxPoints int) []balanceSample {
	if len(samples) <= maxPoints {
		return samples
	}
	first := samples[0]
	last := samples[len(samples)-1]
	span := last.Time.Sub(first.Time)
	if span <= 0 {
		return append([]balanceSample(nil), samples[:maxPoints]...)
	}
	bucketWidth := span / time.Duration(maxPoints-1)
	if bucketWidth <= 0 {
		bucketWidth = time.Nanosecond
	}

	buckets := make([]balanceSample, maxPoints)
	seen := make([]bool, maxPoints)
	buckets[0] = first
	seen[0] = true
	for _, sample := range samples[1:] {
		idx := int(sample.Time.Sub(first.Time) / bucketWidth)
		if idx < 0 {
			idx = 0
		}
		if idx >= maxPoints {
			idx = maxPoints - 1
		}
		buckets[idx] = sample
		seen[idx] = true
	}

	result := make([]balanceSample, 0, maxPoints)
	for i, sample := range buckets {
		if seen[i] {
			result = append(result, sample)
		}
	}
	if !result[len(result)-1].Time.Equal(last.Time) {
		result = append(result, last)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Time.Before(result[j].Time)
	})
	return result
}

func recentBalanceChanges(samples []balanceSample, limit int, epsilon float64) []BalanceChange {
	var changes []BalanceChange
	for i := 1; i < len(samples); i++ {
		from := samples[i-1].Balance
		to := samples[i].Balance
		if !balanceChanged(from, to, epsilon) {
			continue
		}
		changes = append(changes, BalanceChange{
			Time:        samples[i].Time,
			FromBalance: from,
			ToBalance:   to,
			Delta:       to - from,
		})
	}
	if limit > 0 && len(changes) > limit {
		changes = changes[len(changes)-limit:]
	}
	return changes
}

func hourlyBalanceBuckets(samples []balanceSample, location *time.Location, limit int) []BalanceTimeBucket {
	if len(samples) == 0 {
		return nil
	}
	type bucketState struct {
		key   time.Time
		start balanceSample
		end   balanceSample
		count int
	}
	var buckets []bucketState
	byKey := make(map[time.Time]int)
	for _, sample := range samples {
		key := localHour(sample.Time, location)
		idx, ok := byKey[key]
		if !ok {
			byKey[key] = len(buckets)
			buckets = append(buckets, bucketState{key: key, start: sample, end: sample, count: 1})
			continue
		}
		bucket := &buckets[idx]
		if sample.Time.Before(bucket.start.Time) {
			bucket.start = sample
		}
		if sample.Time.After(bucket.end.Time) {
			bucket.end = sample
		}
		bucket.count++
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		return buckets[i].key.Before(buckets[j].key)
	})
	if limit > 0 && len(buckets) > limit {
		buckets = buckets[len(buckets)-limit:]
	}

	result := make([]BalanceTimeBucket, 0, len(buckets))
	for _, bucket := range buckets {
		result = append(result, BalanceTimeBucket{
			Time:         bucket.key,
			StartBalance: bucket.start.Balance,
			EndBalance:   bucket.end.Balance,
			Change:       bucket.end.Balance - bucket.start.Balance,
			Samples:      bucket.count,
		})
	}
	return result
}

func countBalanceChanges(samples []balanceSample, epsilon float64) int {
	changes := 0
	for i := 1; i < len(samples); i++ {
		if balanceChanged(samples[i-1].Balance, samples[i].Balance, epsilon) {
			changes++
		}
	}
	return changes
}

func positiveSpend(change float64) float64 {
	if change < 0 {
		return -change
	}
	return 0
}

func spentAcross(samples []balanceSample, start int) float64 {
	if start < 0 {
		start = 0
	}
	if start >= len(samples) {
		return 0
	}
	spent := 0.0
	for i := start + 1; i < len(samples); i++ {
		spent += positiveSpend(samples[i].Balance - samples[i-1].Balance)
	}
	return spent
}

func localHour(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, location)
}

func (i *BalanceIgnored) add(other BalanceIgnored) {
	i.Total += other.Total
	i.InvalidJSON += other.InvalidJSON
	i.ErrorRecords += other.ErrorRecords
	i.MissingBalance += other.MissingBalance
	i.InvalidTime += other.InvalidTime
}
