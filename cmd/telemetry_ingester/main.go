package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/iicpc/platform/gen/go/iicpc/telemetry"
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

		// Scoring parameters (checklist §5).
		scoreLambda  = flag.Float64("score-lambda", ingest.DefaultLambda, "latency decay rate λ")
		scoreLTarget = flag.Float64("score-ltarget", ingest.DefaultLTarget, "target latency µs for S_L")
		scoreTTarget = flag.Float64("score-ttarget", ingest.DefaultTTarget, "target TPS for S_T")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: *redisAddr})
	defer rdb.Close()

	pool, err := pgxpool.New(ctx, *pgURL)
	if err != nil {
		fmt.Printf("pg connect error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()
	ts := store.NewTimescale(pool)

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  splitCSV(*kafkaBrokers),
		Topic:    *kafkaTopic,
		GroupID:  *groupID,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	defer reader.Close()

	agg := ingest.NewAggregatorWithParams(*window, ingest.ScoringParams{
		Lambda:  *scoreLambda,
		LTarget: *scoreLTarget,
		TTarget: *scoreTTarget,
	})

	ticker := time.NewTicker(*window)
	defer ticker.Stop()

	// Pending raw records for Timescale insert.
	var pending []*telemetry.MetricRecord
	pendingCap := 200_000

	fmt.Printf("telemetry_ingester started: kafka=%s topic=%s redis=%s pg=%s\n", *kafkaBrokers, *kafkaTopic, *redisAddr, *pgURL)

	for {
		select {
		case <-ctx.Done():
			flush(ctx, agg, rdb, ts, pending, *window, *topN)
			fmt.Println("telemetry_ingester stopped")
			return
		case <-ticker.C:
			pending = flush(ctx, agg, rdb, ts, pending, *window, *topN)
		default:
			msg, err := reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					continue
				}
				fmt.Printf("kafka read error: %v\n", err)
				time.Sleep(250 * time.Millisecond)
				continue
			}

			var rec telemetry.MetricRecord
			if err := proto.Unmarshal(msg.Value, &rec); err != nil {
				continue
			}

			latUs := uint64(1)
			if rec.RecvTimeNs >= rec.SentTimeNs {
				latUs = (rec.RecvTimeNs - rec.SentTimeNs) / 1000
				if latUs == 0 {
					latUs = 1
				}
			}

			agg.Add(rec.ContestantId, latUs, rec.IsCorrect)

			// keep raw for DB
			pending = append(pending, &rec)
			if len(pending) >= pendingCap {
				// best-effort backpressure: flush early
				pending = flush(ctx, agg, rdb, ts, pending, *window, *topN)
			}
		}
	}
}

func flush(
	ctx context.Context,
	agg *ingest.Aggregator,
	rdb *redis.Client,
	ts *store.Timescale,
	pending []*telemetry.MetricRecord,
	window time.Duration,
	topN int,
) []*telemetry.MetricRecord {
	// Flush aggregates to Redis leaderboard.
	windows := agg.FlushAndReset(window)
	if len(windows) > 0 {
		pipe := rdb.Pipeline()
		for _, w := range windows {
			pipe.ZAdd(ctx, "leaderboard", redis.Z{Score: w.Score, Member: w.ContestantID})
			pipe.Set(ctx, "leaderboard:"+w.ContestantID, w.JSON(), 0)
			pipe.Publish(ctx, "leaderboard_updates", w.ContestantID)
		}
		_, _ = pipe.Exec(ctx)

		// Optionally trim to top N.
		if topN > 0 {
			card, err := rdb.ZCard(ctx, "leaderboard").Result()
			if err == nil && card > int64(topN) {
				// Remove the lowest-ranked entries, keep only topN highest scores.
				_ = rdb.ZRemRangeByRank(ctx, "leaderboard", 0, card-int64(topN)-1).Err()
			}
		}
	}

	// Flush raw metrics to Timescale.
	if len(pending) > 0 {
		_ = ts.InsertBatch(ctx, pending) // best-effort MVP
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
