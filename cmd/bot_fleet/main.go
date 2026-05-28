package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/iicpc/platform/internal/botfleet"
	kafkapub "github.com/iicpc/platform/internal/kafka"
	"github.com/iicpc/platform/internal/telemetry"
)

func main() {
	var (
		target       string
		bots         int
		ops          int
		durationSec  int
		runID        string
		contestantID string
		symbol       string
		ringSize     int
		kafkaBrokers string
		kafkaTopic   string
	)

	flag.StringVar(&target, "target", "127.0.0.1:50051", "mock matcher gRPC address host:port")
	flag.IntVar(&bots, "bots", 50, "number of concurrent bots")
	flag.IntVar(&ops, "ops", 200, "orders per second per bot")
	flag.IntVar(&durationSec, "duration", 10, "run duration in seconds")
	flag.StringVar(&runID, "run-id", "run-local", "test run id")
	flag.StringVar(&contestantID, "contestant-id", "local", "contestant id")
	flag.StringVar(&symbol, "symbol", "AAPL", "symbol")
	flag.IntVar(&ringSize, "ring", 1_000_000, "ring buffer capacity (metrics)")
	flag.StringVar(&kafkaBrokers, "kafka", "", "kafka/redpanda brokers (csv), e.g. localhost:9092 (optional)")
	flag.StringVar(&kafkaTopic, "kafka-topic", "metrics", "kafka topic (default: metrics)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rb := telemetry.NewRingBuffer(uint64(ringSize))

	go consumeMetrics(ctx, rb)

	cfg := botfleet.Config{
		TargetAddr:   target,
		BotCount:     bots,
		OrdersPerSec: ops,
		Duration:     time.Duration(durationSec) * time.Second,
		TestRunID:    runID,
		ContestantID: contestantID,
		Symbol:       symbol,
		KafkaBrokers: kafkapub.BrokersFromCSV(kafkaBrokers),
		KafkaTopic:   kafkaTopic,
	}

	fmt.Printf("Bot fleet starting: target=%s bots=%d ops/bot=%d duration=%ds\n", target, bots, ops, durationSec)
	if err := botfleet.Run(ctx, cfg, rb); err != nil {
		fmt.Printf("bot fleet error: %v\n", err)
		os.Exit(1)
	}

	// Let the consumer print the final batch.
	time.Sleep(250 * time.Millisecond)
	fmt.Println("Bot fleet finished.")
}

func consumeMetrics(ctx context.Context, rb *telemetry.RingBuffer) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	batch := make([]telemetry.Metric, 8192)
	var stats telemetry.Stats

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats.Reset()
			var drained uint64

			for {
				n := rb.PopInto(batch)
				if n == 0 {
					break
				}
				drained += uint64(n)
				for i := 0; i < n; i++ {
					m := batch[i]
					if m.RecvTimeNs >= m.SentTimeNs {
						stats.AddLatency(m.RecvTimeNs - m.SentTimeNs)
					}
				}
			}

			if drained == 0 {
				fmt.Println("metrics: drained=0")
				continue
			}

			fmt.Printf(
				"metrics: drained=%d e2e(us) min=%d avg=%d max=%d\n",
				drained,
				stats.MinLatencyNs/1000,
				stats.AvgLatencyNs()/1000,
				stats.MaxLatencyNs/1000,
			)
		}
	}
}
