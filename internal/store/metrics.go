package store

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Retention per tier. Roughly 26k rows per series once steady.
const (
	RetainRaw = 24 * time.Hour
	Retain5m  = 30 * 24 * time.Hour
	Retain1h  = 365 * 24 * time.Hour

	bucket5m = 300
	bucket1h = 3600
)

// Sample is one measurement.
type Sample struct {
	Kind    string
	Subject string
	Metric  string
	Value   float64
	TS      time.Time
}

// Point is one stored value at a timestamp.
type Point struct {
	TS    int64   `json:"ts"`
	Value float64 `json:"value"`
	Min   float64 `json:"min,omitempty"`
	Max   float64 `json:"max,omitempty"`
}

type seriesCache struct {
	mu  sync.RWMutex
	ids map[string]int64
}

func (c *seriesCache) get(key string) (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.ids[key]
	return id, ok
}

func (c *seriesCache) put(key string, id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ids == nil {
		c.ids = make(map[string]int64)
	}
	c.ids[key] = id
}

var series seriesCache

func seriesKey(kind, subject, metric string) string {
	return kind + "\x00" + subject + "\x00" + metric
}

// seriesID resolves a series, creating it on first sight. Results are cached
// so the hot path is a map lookup rather than a query per sample.
func (db *DB) seriesID(ctx context.Context, kind, subject, metric string) (int64, error) {
	key := seriesKey(kind, subject, metric)
	if id, ok := series.get(key); ok {
		return id, nil
	}

	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM series WHERE kind = ? AND subject = ? AND metric = ?`,
		kind, subject, metric).Scan(&id)
	if err != nil {
		res, err := db.ExecContext(ctx,
			`INSERT INTO series (kind, subject, metric) VALUES (?, ?, ?)
			 ON CONFLICT (kind, subject, metric) DO NOTHING`, kind, subject, metric)
		if err != nil {
			return 0, fmt.Errorf("create series %s/%s/%s: %w", kind, subject, metric, err)
		}
		if id, err = res.LastInsertId(); err != nil || id == 0 {
			if err := db.QueryRowContext(ctx,
				`SELECT id FROM series WHERE kind = ? AND subject = ? AND metric = ?`,
				kind, subject, metric).Scan(&id); err != nil {
				return 0, err
			}
		}
	}
	series.put(key, id)
	return id, nil
}

// lookupSeries finds a series without creating one. Reads must never write.
func (db *DB) lookupSeries(ctx context.Context, kind, subject, metric string) (int64, bool, error) {
	key := seriesKey(kind, subject, metric)
	if id, ok := series.get(key); ok {
		return id, true, nil
	}
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM series WHERE kind = ? AND subject = ? AND metric = ?`,
		kind, subject, metric).Scan(&id)
	if err != nil {
		return 0, false, nil
	}
	series.put(key, id)
	return id, true, nil
}

// WriteSamples persists a batch in one transaction.
func (db *DB) WriteSamples(ctx context.Context, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	// Series ids are resolved before the transaction opens. The pool holds a
	// single connection, so a lookup issued while a transaction owns it would
	// wait for a connection that only the transaction can release.
	ids := make([]int64, len(samples))
	for i, s := range samples {
		id, err := db.seriesID(ctx, s.Kind, s.Subject, s.Metric)
		if err != nil {
			return err
		}
		ids[i] = id
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO samples_raw (series_id, ts, value) VALUES (?, ?, ?)
		 ON CONFLICT (series_id, ts) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, s := range samples {
		if _, err := stmt.ExecContext(ctx, ids[i], s.TS.UTC().Unix(), s.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rollup folds raw samples into coarser tiers and prunes what has aged out.
// It is idempotent: re-running over the same window rewrites identical values.
func (db *DB) Rollup(ctx context.Context) error {
	now := time.Now().UTC().Unix()

	if err := db.rollupTier(ctx, "5m", "samples_raw", "samples_5m", bucket5m, now, false); err != nil {
		return err
	}
	if err := db.rollupTier(ctx, "1h", "samples_5m", "samples_1h", bucket1h, now, true); err != nil {
		return err
	}

	for _, p := range []struct {
		table  string
		retain time.Duration
	}{
		{"samples_raw", RetainRaw},
		{"samples_5m", Retain5m},
		{"samples_1h", Retain1h},
	} {
		cutoff := time.Now().UTC().Add(-p.retain).Unix()
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE ts < ?`, p.table), cutoff); err != nil {
			return fmt.Errorf("prune %s: %w", p.table, err)
		}
	}
	return nil
}

// rollupTier aggregates completed buckets from src into dst. Only buckets that
// have fully elapsed are written, so a bucket is never stored half-filled.
func (db *DB) rollupTier(ctx context.Context, tier, src, dst string, width, now int64, srcHasMinMax bool) error {
	var through int64
	db.QueryRowContext(ctx, `SELECT through_ts FROM rollup_state WHERE tier = ?`, tier).Scan(&through)

	// The most recent complete bucket.
	latest := (now/width)*width - width
	if latest <= through {
		return nil
	}

	minExpr, maxExpr := "min(value)", "max(value)"
	if srcHasMinMax {
		minExpr, maxExpr = "min(min_value)", "max(max_value)"
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (series_id, ts, value, min_value, max_value)
		SELECT series_id, (ts / %d) * %d AS bucket, avg(value), %s, %s
		  FROM %s
		 WHERE ts >= ? AND ts < ?
		 GROUP BY series_id, bucket
		ON CONFLICT (series_id, ts) DO UPDATE
		   SET value = excluded.value, min_value = excluded.min_value, max_value = excluded.max_value`,
		dst, width, width, minExpr, maxExpr, src)

	if _, err := db.ExecContext(ctx, query, through, latest+width); err != nil {
		return fmt.Errorf("rollup %s: %w", tier, err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO rollup_state (tier, through_ts) VALUES (?, ?)
		 ON CONFLICT (tier) DO UPDATE SET through_ts = excluded.through_ts`, tier, latest)
	return err
}

// QueryRange returns the series between from and to, automatically choosing
// the coarsest tier that still covers the window.
func (db *DB) QueryRange(ctx context.Context, kind, subject, metric string, from, to time.Time) ([]Point, error) {
	table := "samples_1h"
	switch age := time.Since(from); {
	case age <= RetainRaw:
		table = "samples_raw"
	case age <= Retain5m:
		table = "samples_5m"
	}

	id, ok, err := db.lookupSeries(ctx, kind, subject, metric)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []Point{}, nil
	}

	cols := "ts, value, 0, 0"
	if table != "samples_raw" {
		cols = "ts, value, min_value, max_value"
	}
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf(`SELECT %s FROM %s WHERE series_id = ? AND ts >= ? AND ts <= ? ORDER BY ts`, cols, table),
		id, from.UTC().Unix(), to.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.TS, &p.Value, &p.Min, &p.Max); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
