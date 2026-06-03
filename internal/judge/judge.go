// Package judge orchestrates the full lifecycle of a contestant submission:
//
//  1. Accept a submission (image tag + contestant metadata).
//  2. Spin up a sandboxed container via internal/sandbox.
//  3. Wait for the gRPC port to be ready (with retries).
//  4. Run a single probe order to validate the engine implements TradingGateway.
//  5. Launch the full bot-fleet run.
//  6. Tear down the container (always, even on failure).
//  7. Persist the run result to Redis and publish a leaderboard update.
package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	tradingpb "github.com/iicpc/platform/gen/go/iicpc/trading"
	"github.com/iicpc/platform/internal/botfleet"
	kafkapub "github.com/iicpc/platform/internal/kafka"
	"github.com/iicpc/platform/internal/sandbox"
	"github.com/iicpc/platform/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// SubmissionStatus represents the lifecycle state of a submission run.
type SubmissionStatus string

const (
	StatusQueued    SubmissionStatus = "QUEUED"
	StatusRunning   SubmissionStatus = "RUNNING"
	StatusCompleted SubmissionStatus = "COMPLETED"
	StatusFailed    SubmissionStatus = "FAILED"
)

// Submission is the input record created when a contestant submits an image.
type Submission struct {
	ID           string    `json:"id"`
	ContestantID string    `json:"contestant_id"`
	ImageTag     string    `json:"image_tag"`
	SubmittedAt  time.Time `json:"submitted_at"`
}

// RunResult is the output record produced after a judge run completes.
type RunResult struct {
	RunID        string           `json:"run_id"`
	SubmissionID string           `json:"submission_id"`
	ContestantID string           `json:"contestant_id"`
	Status       SubmissionStatus `json:"status"`
	StartedAt    time.Time        `json:"started_at"`
	FinishedAt   time.Time        `json:"finished_at,omitempty"`

	// Aggregate metrics (populated on COMPLETED).
	TotalOrders  uint64  `json:"total_orders"`
	CorrectRatio float64 `json:"correct_ratio"`
	P50Us        uint64  `json:"p50_us"`
	P90Us        uint64  `json:"p90_us"`
	P99Us        uint64  `json:"p99_us"`
	Score        float64 `json:"score"`

	// Error message (populated on FAILED).
	Error string `json:"error,omitempty"`
}

// Config holds all tunable parameters for the judge engine.
type Config struct {
	SandboxMemory string
	SandboxCPUs   int
	SandboxCPUSet string
	SandboxPIDs   int64
	SandboxPort   int

	BotCount     int
	OrdersPerSec int
	Duration     time.Duration
	Symbol       string

	KafkaBrokers []string
	KafkaTopic   string

	RedisAddr string
}

func DefaultConfig() Config {
	return Config{
		SandboxMemory: "512m",
		SandboxCPUs:   1,
		SandboxPIDs:   50,
		SandboxPort:   50052,
		BotCount:      50,
		OrdersPerSec:  200,
		Duration:      60 * time.Second,
		Symbol:        "AAPL",
		KafkaTopic:    "metrics",
		RedisAddr:     "localhost:6379",
	}
}

// Engine is the judge engine. Safe for concurrent use.
type Engine struct {
	cfg    Config
	docker *sandbox.Client
	rdb    *redis.Client
}

// New creates a new Engine. Returns an error if the Docker daemon is unreachable.
func New(cfg Config, rdb *redis.Client) (*Engine, error) {
	dc, err := sandbox.New()
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dc.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("docker daemon unreachable: %w", err)
	}
	return &Engine{cfg: cfg, docker: dc, rdb: rdb}, nil
}

// PingDocker checks whether the Docker daemon is reachable.
func (e *Engine) PingDocker(ctx context.Context) error {
	return e.docker.Ping(ctx)
}

// Run executes a full judge cycle for the given submission.
// It blocks until the bot-fleet run completes or ctx is cancelled.
func (e *Engine) Run(ctx context.Context, sub Submission) (*RunResult, error) {
	runID := fmt.Sprintf("run-%s-%d", sub.ID, time.Now().UnixNano())
	result := &RunResult{
		RunID:        runID,
		SubmissionID: sub.ID,
		ContestantID: sub.ContestantID,
		Status:       StatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	e.saveResult(ctx, result)

	// Ensure the internal Docker network exists.
	netCtx, netCancel := context.WithTimeout(ctx, 15*time.Second)
	defer netCancel()
	if _, err := e.docker.EnsureInternalNetwork(netCtx, "iicpc_internal"); err != nil {
		return e.fail(ctx, result, fmt.Errorf("ensure network: %w", err))
	}

	memBytes, err := sandbox.ParseBytes(e.cfg.SandboxMemory)
	if err != nil {
		return e.fail(ctx, result, fmt.Errorf("parse memory: %w", err))
	}

	runCtx, runCancel := context.WithTimeout(ctx, e.cfg.Duration+3*time.Minute)
	defer runCancel()

	containerName := fmt.Sprintf("iicpc-contestant-%s", sub.ID)
	opt := sandbox.RunOptions{
		Image:           sub.ImageTag,
		ContainerName:   containerName,
		NetworkName:     "iicpc_internal",
		HostPort:        e.cfg.SandboxPort,
		ContainerPort:   50051,
		MemoryBytes:     memBytes,
		NanoCPUs:        int64(e.cfg.SandboxCPUs) * 1_000_000_000,
		CpusetCpus:      e.cfg.SandboxCPUSet,
		PidsLimit:       e.cfg.SandboxPIDs,
		ReadonlyRootfs:  true,
		NoNewPrivileges: true,
		DropAllCaps:     true,
	}

	containerID, hostAddr, err := e.docker.RunSandbox(runCtx, opt)
	if err != nil {
		return e.fail(ctx, result, fmt.Errorf("start sandbox: %w", err))
	}
	log.Printf("judge: sandbox started container=%s addr=%s", containerID, hostAddr)

	// Always stop and remove the container when we're done, regardless of outcome.
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		e.docker.StopAndRemove(cleanCtx, containerID)
		log.Printf("judge: sandbox cleaned up container=%s", containerID)
	}()

	// Wait for the gRPC port to be ready (retry up to 10s).
	if err := waitForGRPC(runCtx, hostAddr); err != nil {
		return e.fail(ctx, result, fmt.Errorf("sandbox not ready: %w", err))
	}

	// Validate the engine implements TradingGateway before launching the full fleet.
	if err := validateEngine(runCtx, hostAddr); err != nil {
		return e.fail(ctx, result, fmt.Errorf("engine validation failed: %w", err))
	}
	log.Printf("judge: engine validated for submission=%s", sub.ID)

	// Set up optional Kafka producer.
	var producer *kafkapub.Producer
	if len(e.cfg.KafkaBrokers) > 0 {
		p, err := kafkapub.NewProducer(kafkapub.ProducerConfig{
			Brokers: e.cfg.KafkaBrokers,
			Topic:   e.cfg.KafkaTopic,
		})
		if err != nil {
			log.Printf("judge: kafka producer error (continuing without Kafka): %v", err)
		} else {
			producer = p
			defer producer.Close()
		}
	}

	rb := telemetry.NewRingBuffer(500_000)

	fleetCfg := botfleet.Config{
		TargetAddr:   hostAddr,
		BotCount:     e.cfg.BotCount,
		OrdersPerSec: e.cfg.OrdersPerSec,
		Duration:     e.cfg.Duration,
		TestRunID:    runID,
		ContestantID: sub.ContestantID,
		Symbol:       e.cfg.Symbol,
		KafkaBrokers: e.cfg.KafkaBrokers,
		KafkaTopic:   e.cfg.KafkaTopic,
	}

	fleetErr := botfleet.Run(runCtx, fleetCfg, rb)
	stats := drainRingBuffer(rb)

	result.FinishedAt = time.Now().UTC()
	if fleetErr != nil && runCtx.Err() == nil {
		return e.fail(ctx, result, fmt.Errorf("bot fleet: %w", fleetErr))
	}

	result.Status = StatusCompleted
	result.TotalOrders = stats.Count
	result.CorrectRatio = stats.CorrectRatio
	result.P50Us = stats.P50Us
	result.P90Us = stats.P90Us
	result.P99Us = stats.P99Us
	result.Score = stats.Score
	e.saveResult(ctx, result)

	_ = e.rdb.Publish(ctx, "leaderboard_updates", sub.ContestantID).Err()
	log.Printf("judge: run complete run_id=%s contestant=%s score=%.2f", runID, sub.ContestantID, result.Score)
	return result, nil
}

// waitForGRPC dials addr and retries until the server accepts connections or
// the context expires (hard cap: 10 seconds).
func waitForGRPC(ctx context.Context, addr string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		conn, err := grpc.DialContext(dialCtx, addr, //nolint:staticcheck
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		cancel()
		if err == nil {
			conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gRPC server at %s not ready after 10s: %w", addr, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// validateEngine sends a single probe PlaceOrder to confirm the engine
// implements TradingGateway correctly before launching the full bot fleet.
func validateEngine(ctx context.Context, addr string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, addr, //nolint:staticcheck
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer conn.Close()

	client := tradingpb.NewTradingGatewayClient(conn)
	callCtx, callCancel := context.WithTimeout(ctx, 3*time.Second)
	defer callCancel()

	resp, err := client.PlaceOrder(callCtx, &tradingpb.OrderRequest{
		OrderId:     "probe-001",
		ClientId:    "judge-probe",
		Symbol:      "AAPL",
		Side:        tradingpb.Side_BUY,
		OrderType:   tradingpb.OrderType_MARKET,
		Quantity:    1,
		TimestampNs: uint64(time.Now().UnixNano()),
	})
	if err != nil {
		return fmt.Errorf("PlaceOrder probe failed: %w", err)
	}
	if resp.GetStatus() == "" {
		return fmt.Errorf("engine returned empty status on probe order")
	}
	return nil
}

func (e *Engine) fail(ctx context.Context, r *RunResult, err error) (*RunResult, error) {
	r.Status = StatusFailed
	r.Error = err.Error()
	r.FinishedAt = time.Now().UTC()
	e.saveResult(ctx, r)
	log.Printf("judge: run failed run_id=%s: %v", r.RunID, err)
	return r, err
}

func (e *Engine) saveResult(ctx context.Context, r *RunResult) {
	b, _ := json.Marshal(r)
	key := "run:" + r.RunID
	_ = e.rdb.Set(ctx, key, string(b), 24*time.Hour).Err()
	_ = e.rdb.LPush(ctx, "runs:submission:"+r.SubmissionID, r.RunID).Err()
	_ = e.rdb.LPush(ctx, "runs:contestant:"+r.ContestantID, r.RunID).Err()
	_ = e.rdb.LTrim(ctx, "runs:contestant:"+r.ContestantID, 0, 49).Err()
}

// ── Local stats (ring buffer drain) ──────────────────────────────────────────

type localStats struct {
	Count        uint64
	CorrectRatio float64
	P50Us        uint64
	P90Us        uint64
	P99Us        uint64
	Score        float64
}

func drainRingBuffer(rb *telemetry.RingBuffer) localStats {
	batch := make([]telemetry.Metric, 8192)
	var latencies []uint64
	var correct, total uint64

	for {
		n := rb.PopInto(batch)
		if n == 0 {
			break
		}
		for i := 0; i < n; i++ {
			m := batch[i]
			total++
			if m.IsCorrect {
				correct++
			}
			if m.RecvTimeNs >= m.SentTimeNs {
				us := (m.RecvTimeNs - m.SentTimeNs) / 1000
				if us == 0 {
					us = 1
				}
				latencies = append(latencies, us)
			}
		}
	}

	if total == 0 {
		return localStats{}
	}

	ratio := float64(correct) / float64(total)
	p50, p90, p99 := percentiles(latencies)
	return localStats{
		Count:        total,
		CorrectRatio: ratio,
		P50Us:        p50,
		P90Us:        p90,
		P99Us:        p99,
		Score:        ratio*1_000_000 - float64(p99),
	}
}

func percentiles(data []uint64) (p50, p90, p99 uint64) {
	n := len(data)
	if n == 0 {
		return 0, 0, 0
	}
	sorted := make([]uint64, n)
	copy(sorted, data)
	// O(n log n) sort — replaces O(n²) insertion sort for large run datasets.
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := func(pct float64) uint64 {
		return sorted[int(float64(n-1)*pct/100.0)]
	}
	return idx(50), idx(90), idx(99)
}
