package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"time"

	telemetrypb "github.com/iicpc/platform/gen/go/iicpc/telemetry"
	"github.com/iicpc/platform/internal/ingest"
	"github.com/iicpc/platform/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

func main() {
	var (
		kafkaBrokers = flag.String("kafka", envOr("KAFKA_BROKERS", "localhost:9092"), "kafka brokers (csv)")
		kafkaTopic   = flag.String("topic", envOr("KAFKA_TOPIC", "metrics"), "kafka topic")
		groupID      = flag.String("group", envOr("KAFKA_GROUP", "telemetry_ingester"), "consumer group id")

		redisAddr = flag.String("redis", envOr("REDIS_ADDR", "localhost:6379"), "redis addr host:port")
		pgURL     = flag.String("pg", envOr("PG_URL", "postgres://postgres:postgres@localhost:5432/iicpc?sslmode=disable"), "timescaledb postgres url")

		window = flag.Duration("window", 1*time.Second, "aggregation window")
		topN   = flag.Int("top", 50, "leaderboard size (top N)")

		// Worker pool size for concurrent Kafka message decoding.
		workers = flag.Int("workers", 4, "number of concurrent kafka consumer workers")

		// Scoring parameters.
		scoreLambda  = flag.Float64("score-lambda", ingest.DefaultLambda, "latency decay rate λ")
		scoreLTarget = flag.Float64("score-ltarget", ingest.DefaultLTarget, "target latency µs for S_L")
		scoreTTarget = flag.Float64("score-ttarget", ingest.DefaultTTarget, "target TPS for S_T")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// ── Redis (with connection pool) ─────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:            *redisAddr,
		PoolSize:        10,
		MinIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
	})
	defer rdb.Close()

	// ── TimescaleDB (with connection pool) ───────────────────────────────────
	pgCfg, err := pgxpool.ParseConfig(*pgURL)
	if err != nil {
		fmt.Printf("pg config error: %v\n", err)
		os.Exit(1)
	}
	pgCfg.MaxConns = 10
	pgCfg.MinConns = 2
	pgCfg.MaxConnLifetime = 10 * time.Minute
	pgCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		fmt.Printf("pg connect error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()
	ts := store.NewTimescale(pool)

	// ── Kafka reader (batch-optimised config) ─────────────────────────────────
	// MinBytes=10KB + MaxWait=500ms lets Redpanda batch server-side before push.
	// This dramatically reduces per-message round trips under high throughput.
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        splitCSV(*kafkaBrokers),
		Topic:          *kafkaTopic,
		GroupID:        *groupID,
		MinBytes:       10e3,         // 10 KB
		MaxBytes:       10e6,         // 10 MB
		MaxWait:        500 * time.Millisecond,
		CommitInterval: 0,            // manual commit for reliability
		StartOffset:    kafkago.LastOffset,
	})
	defer reader.Close()

	agg := ingest.NewAggregatorWithParams(*window, ingest.ScoringParams{
		Lambda:  *scoreLambda,
		LTarget: *scoreLTarget,
		TTarget: *scoreTTarget,
	})

	// ── Concurrent worker pool ────────────────────────────────────────────────
	// Workers: fetch → decode → push to aggregator channel
	// Collector: drains channel, accumulates batch, flushes on tick or cap
	type decoded struct {
		rec *telemetrypb.MetricRecord
		msg kafkago.Message
	}

	msgCh := make(chan decoded, 4096) // buffered channel between workers and collector

	// Launch N worker goroutines that fetch and decode messages concurrently.
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				msg, err := reader.FetchMessage(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return // context cancelled — graceful shutdown
					}
					log.Printf("kafka fetch error: %v — retrying in 250ms", err)
					time.Sleep(250 * time.Millisecond)
					continue
				}

				var rec telemetrypb.MetricRecord
				if err := proto.Unmarshal(msg.Value, &rec); err != nil {
					// Commit bad messages so we don't get stuck.
					_ = reader.CommitMessages(ctx, msg)
					continue
				}

				select {
				case msgCh <- decoded{rec: &rec, msg: msg}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// ── Collector goroutine ───────────────────────────────────────────────────
	// Single-threaded collector for the aggregator (no lock contention).
	pendingCap := 200_000
	var pending []*telemetrypb.MetricRecord
	var toCommit []kafkago.Message

	ticker := time.NewTicker(*window)
	defer ticker.Stop()

	log.Printf("telemetry_ingester started: kafka=%s topic=%s workers=%d redis=%s pg=%s",
		*kafkaBrokers, *kafkaTopic, *workers, *redisAddr, *pgURL)

	for {
		select {
		case <-ctx.Done():
			// Final flush before shutdown.
			if len(toCommit) > 0 {
				_ = reader.CommitMessages(ctx, toCommit...)
			}
			flush(context.Background(), agg, rdb, ts, pending, *window, *topN)
			log.Println("telemetry_ingester stopped")
			// Wait for worker goroutines to exit.
			wg.Wait()
			return

		case <-ticker.C:
			// Commit consumed offsets as a batch.
			if len(toCommit) > 0 {
				if err := reader.CommitMessages(ctx, toCommit...); err != nil {
					log.Printf("kafka commit error: %v", err)
				}
				toCommit = toCommit[:0]
			}
			pending = flush(ctx, agg, rdb, ts, pending, *window, *topN)

		case d := <-msgCh:
			rec := d.rec
			toCommit = append(toCommit, d.msg)

			latUs := uint64(1)
			if rec.RecvTimeNs >= rec.SentTimeNs {
				latUs = (rec.RecvTimeNs - rec.SentTimeNs) / 1000
				if latUs == 0 {
					latUs = 1
				}
			}

			agg.Add(rec.ContestantId, latUs, rec.IsCorrect)
			pending = append(pending, rec)

			// Early flush if batch is full.
			if len(pending) >= pendingCap {
				if len(toCommit) > 0 {
					if err := reader.CommitMessages(ctx, toCommit...); err != nil {
						log.Printf("kafka commit error: %v", err)
					}
					toCommit = toCommit[:0]
				}
				pending = flush(ctx, agg, rdb, ts, pending, *window, *topN)
			}
		}
	}
}

// flush drains aggregated windows to Redis and raw records to TimescaleDB.
func flush(
	ctx context.Context,
	agg *ingest.Aggregator,
	rdb *redis.Client,
	ts *store.Timescale,
	pending []*telemetrypb.MetricRecord,
	window time.Duration,
	topN int,
) []*telemetrypb.MetricRecord {
	// Flush aggregates to Redis leaderboard.
	windows := agg.FlushAndReset(window)
	if len(windows) > 0 {
		pipe := rdb.Pipeline()
		for _, w := range windows {
			pipe.ZAdd(ctx, "leaderboard", redis.Z{Score: w.Score, Member: w.ContestantID})
			pipe.Set(ctx, "leaderboard:"+w.ContestantID, w.JSON(), 0)
			pipe.Publish(ctx, "leaderboard_updates", w.ContestantID)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("redis pipeline error: %v", err)
		}

		// Trim to top N.
		if topN > 0 {
			card, err := rdb.ZCard(ctx, "leaderboard").Result()
			if err == nil && card > int64(topN) {
				_ = rdb.ZRemRangeByRank(ctx, "leaderboard", 0, card-int64(topN)-1).Err()
			}
		}
	}

	// Batch-insert raw metrics into TimescaleDB.
	if len(pending) > 0 {
		if err := ts.InsertBatch(ctx, pending); err != nil {
			log.Printf("timescaledb insert error (batch of %d): %v", len(pending), err)
		}
	}

	return pending[:0]
}

func envOr(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			continue
		}
		cur += string(ch)
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) == 0 {
		out = append(out, "localhost:9092")
	}
	return out
}
