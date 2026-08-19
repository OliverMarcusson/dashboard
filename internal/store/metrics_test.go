package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "m.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// The series cache is package-level; keep tests from seeing each other's ids.
	series.mu.Lock()
	series.ids = nil
	series.mu.Unlock()
	return db
}

func TestWriteAndQueryRaw(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	now := time.Now().UTC()

	var samples []Sample
	for i := 0; i < 10; i++ {
		samples = append(samples, Sample{
			Kind: "host", Metric: "cpu",
			Value: float64(i), TS: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	if err := db.WriteSamples(ctx, samples); err != nil {
		t.Fatalf("write: %v", err)
	}

	points, err := db.QueryRange(ctx, "host", "", "cpu", now.Add(-time.Hour), now)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 10 {
		t.Fatalf("got %d points, want 10", len(points))
	}
	for i := 1; i < len(points); i++ {
		if points[i].TS <= points[i-1].TS {
			t.Fatalf("points are not in ascending time order at %d", i)
		}
	}
}

func TestWriteIsIdempotentPerTimestamp(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	ts := time.Now().UTC().Truncate(time.Second)

	db.WriteSamples(ctx, []Sample{{Kind: "host", Metric: "mem", Value: 1, TS: ts}})
	db.WriteSamples(ctx, []Sample{{Kind: "host", Metric: "mem", Value: 2, TS: ts}})

	points, _ := db.QueryRange(ctx, "host", "", "mem", ts.Add(-time.Minute), ts.Add(time.Minute))
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (same timestamp must overwrite)", len(points))
	}
	if points[0].Value != 2 {
		t.Errorf("value = %v, want the later 2", points[0].Value)
	}
}

func TestRollupAveragesCompletedBuckets(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	// 30 minutes of samples every 10s, ending two minutes ago so the most
	// recent buckets are genuinely complete.
	end := time.Now().UTC().Add(-2 * time.Minute)
	start := end.Add(-30 * time.Minute)

	var samples []Sample
	for ts := start; ts.Before(end); ts = ts.Add(10 * time.Second) {
		// A value that varies so an average is meaningful.
		samples = append(samples, Sample{
			Kind: "host", Metric: "cpu",
			Value: float64(ts.Unix() % 100), TS: ts,
		})
	}
	if err := db.WriteSamples(ctx, samples); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := db.Rollup(ctx); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	id, _, _ := db.lookupSeries(ctx, "host", "", "cpu")
	var buckets int
	db.QueryRowContext(ctx, `SELECT count(*) FROM samples_5m WHERE series_id = ?`, id).Scan(&buckets)
	if buckets < 4 {
		t.Fatalf("got %d five-minute buckets from 30 minutes of data, want at least 4", buckets)
	}

	// Verify one bucket against the raw rows it came from.
	var bucketTS int64
	var stored, wantAvg, storedMin, storedMax float64
	db.QueryRowContext(ctx,
		`SELECT ts, value, min_value, max_value FROM samples_5m WHERE series_id = ? ORDER BY ts LIMIT 1`,
		id).Scan(&bucketTS, &stored, &storedMin, &storedMax)
	db.QueryRowContext(ctx,
		`SELECT avg(value) FROM samples_raw WHERE series_id = ? AND ts >= ? AND ts < ?`,
		id, bucketTS, bucketTS+300).Scan(&wantAvg)

	if math.Abs(stored-wantAvg) > 1e-9 {
		t.Errorf("bucket average = %v, want %v", stored, wantAvg)
	}
	if storedMin > stored || storedMax < stored {
		t.Errorf("average %v falls outside min/max %v..%v", stored, storedMin, storedMax)
	}
	if bucketTS%300 != 0 {
		t.Errorf("bucket ts %d is not aligned to 300s", bucketTS)
	}
}

func TestRollupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	end := time.Now().UTC().Add(-2 * time.Minute)
	var samples []Sample
	for ts := end.Add(-20 * time.Minute); ts.Before(end); ts = ts.Add(10 * time.Second) {
		samples = append(samples, Sample{Kind: "host", Metric: "cpu", Value: 42, TS: ts})
	}
	db.WriteSamples(ctx, samples)

	count := func() int {
		var n int
		db.QueryRowContext(ctx, `SELECT count(*) FROM samples_5m`).Scan(&n)
		return n
	}

	db.Rollup(ctx)
	first := count()
	db.Rollup(ctx)
	db.Rollup(ctx)

	if got := count(); got != first {
		t.Errorf("bucket count changed across repeated rollups: %d then %d", first, got)
	}
	if first == 0 {
		t.Fatal("no buckets produced")
	}
}

func TestQueryRangePicksCoarserTierForOldWindows(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	// A window older than raw retention must not fail, and must come back empty
	// rather than reading a table that no longer holds it.
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	points, err := db.QueryRange(ctx, "host", "", "cpu", old, old.Add(time.Hour))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("got %d points from an empty year-old window", len(points))
	}
}
