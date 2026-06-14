package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	telemetrypb "github.com/iicpc/platform/gen/go/iicpc/telemetry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Timescale struct {
	pool *pgxpool.Pool
}

func NewTimescale(pool *pgxpool.Pool) *Timescale {
	return &Timescale{pool: pool}
}

// InsertBatch writes a batch of raw metric records to the hypertable.
// It drains ALL queued results even if individual rows fail, collecting
// errors and returning them as a single combined error at the end.
// This ensures partial batches are not silently dropped.
func (t *Timescale) InsertBatch(ctx context.Context, recs []*telemetrypb.MetricRecord) error {
	if len(recs) == 0 {
		return nil
	}

	b := &pgx.Batch{}
	now := time.Now().UTC()
	for _, r := range recs {
		latNs := int64(0)
		if r.GetRecvTimeNs() >= r.GetSentTimeNs() {
			latNs = int64(r.GetRecvTimeNs() - r.GetSentTimeNs())
		}
		b.Queue(
			`INSERT INTO metric_records
			 (time, test_run_id, contestant_id, client_id, order_id,
			  sent_time_ns, recv_time_ns, engine_time_ns, is_correct, error_code, latency_ns)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			 ON CONFLICT DO NOTHING`,
			now,
			r.GetTestRunId(),
			r.GetContestantId(),
			r.GetClientId(),
			r.GetOrderId(),
			int64(r.GetSentTimeNs()),
			int64(r.GetRecvTimeNs()),
			int64(r.GetEngineTimeNs()),
			r.GetIsCorrect(),
			r.GetErrorCode(),
			latNs,
		)
	}

	br := t.pool.SendBatch(ctx, b)
	defer br.Close()

	// Drain ALL results. Collect errors without short-circuiting so that
	// successful rows in the batch are still committed by the server.
	var errs []error
	for i := range recs {
		if _, err := br.Exec(); err != nil {
			errs = append(errs, fmt.Errorf("record[%d] (run=%s order=%s): %w",
				i, recs[i].GetTestRunId(), recs[i].GetOrderId(), err))
		}
	}
	return errors.Join(errs...)
}

// ContestantStats holds aggregated per-window stats for one contestant.
type ContestantStats struct {
	ContestantID string    `json:"contestant_id"`
	WindowStart  time.Time `json:"window_start"`
	Count        int64     `json:"count"`
	CorrectCount int64     `json:"correct_count"`
	CorrectRatio float64   `json:"correct_ratio"`
	P50Us        float64   `json:"p50_us"`
	P90Us        float64   `json:"p90_us"`
	P99Us        float64   `json:"p99_us"`
	AvgUs        float64   `json:"avg_us"`
	MinUs        float64   `json:"min_us"`
	MaxUs        float64   `json:"max_us"`
}

// QueryHistory returns per-minute aggregated stats for a contestant over the
// last `hours` hours, ordered newest-first. Max 1440 rows (24h at 1-min buckets).
func (t *Timescale) QueryHistory(ctx context.Context, contestantID string, hours int) ([]ContestantStats, error) {
	if hours <= 0 {
		hours = 1
	}
	if hours > 168 {
		hours = 168
	}

	rows, err := t.pool.Query(ctx, `
		SELECT
			time_bucket('1 minute', time)                                                AS window_start,
			COUNT(*)                                                                     AS count,
			SUM(CASE WHEN is_correct THEN 1 ELSE 0 END)                                 AS correct_count,
			ROUND(AVG(CASE WHEN is_correct THEN 1.0 ELSE 0.0 END)::numeric, 4)          AS correct_ratio,
			ROUND((percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ns)/1000)::numeric, 2) AS p50_us,
			ROUND((percentile_cont(0.90) WITHIN GROUP (ORDER BY latency_ns)/1000)::numeric, 2) AS p90_us,
			ROUND((percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ns)/1000)::numeric, 2) AS p99_us,
			ROUND((AVG(latency_ns)/1000)::numeric, 2)                                   AS avg_us,
			ROUND((MIN(latency_ns)/1000)::numeric, 2)                                   AS min_us,
			ROUND((MAX(latency_ns)/1000)::numeric, 2)                                   AS max_us
		FROM metric_records
		WHERE contestant_id = $1
		  AND time >= NOW() - make_interval(hours => $2)
		GROUP BY window_start
		ORDER BY window_start DESC
		LIMIT 1440
	`, contestantID, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ContestantStats
	for rows.Next() {
		var s ContestantStats
		s.ContestantID = contestantID
		if err := rows.Scan(
			&s.WindowStart,
			&s.Count, &s.CorrectCount, &s.CorrectRatio,
			&s.P50Us, &s.P90Us, &s.P99Us,
			&s.AvgUs, &s.MinUs, &s.MaxUs,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// QueryRunStats returns aggregate stats for a specific test_run_id.
func (t *Timescale) QueryRunStats(ctx context.Context, testRunID string) (*ContestantStats, error) {
	row := t.pool.QueryRow(ctx, `
		SELECT
			contestant_id,
			MIN(time)                                                                    AS window_start,
			COUNT(*)                                                                     AS count,
			SUM(CASE WHEN is_correct THEN 1 ELSE 0 END)                                 AS correct_count,
			ROUND(AVG(CASE WHEN is_correct THEN 1.0 ELSE 0.0 END)::numeric, 4)          AS correct_ratio,
			ROUND((percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ns)/1000)::numeric, 2) AS p50_us,
			ROUND((percentile_cont(0.90) WITHIN GROUP (ORDER BY latency_ns)/1000)::numeric, 2) AS p90_us,
			ROUND((percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ns)/1000)::numeric, 2) AS p99_us,
			ROUND((AVG(latency_ns)/1000)::numeric, 2)                                   AS avg_us,
			ROUND((MIN(latency_ns)/1000)::numeric, 2)                                   AS min_us,
			ROUND((MAX(latency_ns)/1000)::numeric, 2)                                   AS max_us
		FROM metric_records
		WHERE test_run_id = $1
		GROUP BY contestant_id
		LIMIT 1
	`, testRunID)

	var s ContestantStats
	if err := row.Scan(
		&s.ContestantID, &s.WindowStart,
		&s.Count, &s.CorrectCount, &s.CorrectRatio,
		&s.P50Us, &s.P90Us, &s.P99Us,
		&s.AvgUs, &s.MinUs, &s.MaxUs,
	); err != nil {
		return nil, err
	}
	return &s, nil
}
