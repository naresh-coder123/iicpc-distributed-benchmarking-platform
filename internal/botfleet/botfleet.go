// Package botfleet implements the distributed load generator.
//
// Each bot maintains a single persistent gRPC bidirectional stream
// (StreamOrders) rather than issuing individual unary PlaceOrder calls.
// This satisfies the "Persistent Socket Connection Pools" requirement and
// avoids per-request TCP/TLS handshake overhead at high throughput.
//
// Order mix: 60% MARKET, 30% LIMIT, 10% simulated CANCEL (sent as a LIMIT
// order with quantity=0 which the engine must reject — used to test cancel
// handling without a separate RPC).
package botfleet

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	telemetrypb "github.com/iicpc/platform/gen/go/iicpc/telemetry"
	tradingpb "github.com/iicpc/platform/gen/go/iicpc/trading"
	kafkapub "github.com/iicpc/platform/internal/kafka"
	"github.com/iicpc/platform/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	TargetAddr   string
	BotCount     int
	OrdersPerSec int
	Duration     time.Duration

	TestRunID    string
	ContestantID string
	Symbol       string

	// Optional Kafka publish.
	KafkaBrokers []string
	KafkaTopic   string
}

func Run(ctx context.Context, cfg Config, rb *telemetry.RingBuffer) error {
	if cfg.BotCount <= 0 {
		cfg.BotCount = 1
	}
	if cfg.OrdersPerSec <= 0 {
		cfg.OrdersPerSec = 100
	}
	if cfg.Duration <= 0 {
		cfg.Duration = 10 * time.Second
	}
	if cfg.Symbol == "" {
		cfg.Symbol = "AAPL"
	}

	// One shared gRPC connection; each bot opens its own stream over it.
	// WithBlock ensures we fail fast if the target is unreachable.
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext( //nolint:staticcheck
		dialCtx,
		cfg.TargetAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", cfg.TargetAddr, err)
	}
	defer conn.Close()

	client := tradingpb.NewTradingGatewayClient(conn)

	runCtx, runCancel := context.WithTimeout(ctx, cfg.Duration)
	defer runCancel()

	var producer *kafkapub.Producer
	if len(cfg.KafkaBrokers) > 0 {
		p, err := kafkapub.NewProducer(kafkapub.ProducerConfig{
			Brokers: cfg.KafkaBrokers,
			Topic:   cfg.KafkaTopic,
		})
		if err != nil {
			return err
		}
		producer = p
		defer producer.Close()
	}

	var wg sync.WaitGroup
	wg.Add(cfg.BotCount)

	for i := 0; i < cfg.BotCount; i++ {
		botID := fmt.Sprintf("bot-%d", i+1)
		rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(i)))
		go func() {
			defer wg.Done()
			runBot(runCtx, client, botID, rng, cfg, rb, producer)
		}()
	}

	wg.Wait()
	return nil
}

// runBot opens a single StreamOrders bidirectional stream and drives the full
// order loop over it. This keeps one persistent TCP connection per bot.
func runBot(
	ctx context.Context,
	client tradingpb.TradingGatewayClient,
	botID string,
	rng *rand.Rand,
	cfg Config,
	rb *telemetry.RingBuffer,
	producer *kafkapub.Producer,
) {
	// Open the persistent bidirectional stream.
	stream, err := client.StreamOrders(ctx)
	if err != nil {
		// Fall back to unary if streaming is unavailable.
		runBotUnary(ctx, client, botID, rng, cfg, rb, producer)
		return
	}

	// Receive goroutine: reads responses from the stream and records metrics.
	type pendingEntry struct {
		orderID  string
		sentNs   uint64
		isCancel bool
	}
	pending := make(map[string]pendingEntry, 256)
	var mu sync.Mutex

	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		for {
			resp, err := stream.Recv()
			if err != nil {
				if err != io.EOF && ctx.Err() == nil {
					// stream error — bot will exit on next send failure
				}
				return
			}
			recvNs := uint64(time.Now().UTC().UnixNano())

			mu.Lock()
			entry, ok := pending[resp.GetOrderId()]
			if ok {
				delete(pending, resp.GetOrderId())
			}
			mu.Unlock()

			if !ok {
				continue
			}

			// Correctness: CANCEL orders (qty=0) must be REJECTED.
			// MARKET/LIMIT orders must be FILLED or PARTIALLY_FILLED.
			var isCorrect bool
			status := resp.GetStatus()
			if entry.isCancel {
				isCorrect = status == "REJECTED"
			} else {
				isCorrect = status == "FILLED" || status == "PARTIALLY_FILLED"
			}

			m := telemetry.Metric{
				TestRunID:    cfg.TestRunID,
				ContestantID: cfg.ContestantID,
				ClientID:     botID,
				OrderID:      resp.GetOrderId(),
				SentTimeNs:   entry.sentNs,
				RecvTimeNs:   recvNs,
				EngineTimeNs: resp.GetTimestampNs(),
				IsCorrect:    isCorrect,
			}
			if !isCorrect {
				m.ErrorCode = "WRONG_STATUS:" + status
			}

			if rb != nil {
				_ = rb.Push(m)
			}
			if producer != nil {
				publishMetric(ctx, producer, cfg, m)
			}
		}
	}()

	interval := time.Second / time.Duration(cfg.OrdersPerSec)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq uint64
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			<-recvDone
			return
		case <-ticker.C:
			seq++
			req, isCancel := buildOrder(botID, seq, cfg.Symbol, rng)
			sentNs := uint64(time.Now().UTC().UnixNano())

			mu.Lock()
			pending[req.OrderId] = pendingEntry{
				orderID:  req.OrderId,
				sentNs:   sentNs,
				isCancel: isCancel,
			}
			mu.Unlock()

			if err := stream.Send(req); err != nil {
				mu.Lock()
				delete(pending, req.OrderId)
				mu.Unlock()
				// Stream broken — exit; the bot fleet will still have other bots running.
				_ = stream.CloseSend()
				<-recvDone
				return
			}
		}
	}
}

// runBotUnary is the fallback path used when StreamOrders is unavailable.
// It uses individual PlaceOrder calls (original Phase 2 behaviour).
func runBotUnary(
	ctx context.Context,
	client tradingpb.TradingGatewayClient,
	botID string,
	rng *rand.Rand,
	cfg Config,
	rb *telemetry.RingBuffer,
	producer *kafkapub.Producer,
) {
	interval := time.Second / time.Duration(cfg.OrdersPerSec)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq++
			req, isCancel := buildOrder(botID, seq, cfg.Symbol, rng)
			sentNs := uint64(time.Now().UTC().UnixNano())
			resp, err := client.PlaceOrder(ctx, req)
			recvNs := uint64(time.Now().UTC().UnixNano())

			var isCorrect bool
			var errCode string
			if err != nil {
				errCode = err.Error()
			} else if resp != nil {
				status := resp.GetStatus()
				if isCancel {
					isCorrect = status == "REJECTED"
				} else {
					isCorrect = status == "FILLED" || status == "PARTIALLY_FILLED"
				}
				if !isCorrect {
					errCode = "WRONG_STATUS:" + status
				}
			}

			m := telemetry.Metric{
				TestRunID:    cfg.TestRunID,
				ContestantID: cfg.ContestantID,
				ClientID:     botID,
				OrderID:      req.OrderId,
				SentTimeNs:   sentNs,
				RecvTimeNs:   recvNs,
				IsCorrect:    isCorrect,
				ErrorCode:    errCode,
			}
			if resp != nil {
				m.EngineTimeNs = resp.GetTimestampNs()
			}

			if rb != nil {
				_ = rb.Push(m)
			}
			if producer != nil {
				publishMetric(ctx, producer, cfg, m)
			}
		}
	}
}

// buildOrder generates a structured order request.
// Order mix: 60% MARKET, 30% LIMIT, 10% CANCEL (qty=0, LIMIT).
// Returns the request and whether it is a cancel probe.
func buildOrder(botID string, seq uint64, symbol string, rng *rand.Rand) (*tradingpb.OrderRequest, bool) {
	orderID := fmt.Sprintf("%s-%d", botID, seq)
	side := tradingpb.Side_BUY
	if rng.Intn(2) == 0 {
		side = tradingpb.Side_SELL
	}

	roll := rng.Intn(10) // 0-9
	var orderType tradingpb.OrderType
	var price float64
	var qty uint64
	isCancel := false

	switch {
	case roll < 6: // 60% MARKET
		orderType = tradingpb.OrderType_MARKET
		qty = uint64(1 + rng.Intn(10))
	case roll < 9: // 30% LIMIT
		orderType = tradingpb.OrderType_LIMIT
		// Synthetic limit price ±2% around mid 100.0
		price = 100.0 + (rng.Float64()*4.0 - 2.0)
		qty = uint64(1 + rng.Intn(10))
	default: // 10% CANCEL probe (qty=0 → engine must REJECT)
		orderType = tradingpb.OrderType_LIMIT
		price = 100.0
		qty = 0
		isCancel = true
	}

	return &tradingpb.OrderRequest{
		OrderId:     orderID,
		ClientId:    botID,
		Symbol:      symbol,
		Side:        side,
		OrderType:   orderType,
		Price:       price,
		Quantity:    qty,
		TimestampNs: uint64(time.Now().UTC().UnixNano()),
	}, isCancel
}

func publishMetric(ctx context.Context, producer *kafkapub.Producer, cfg Config, m telemetry.Metric) {
	rec := &telemetrypb.MetricRecord{
		TestRunId:    m.TestRunID,
		ContestantId: m.ContestantID,
		ClientId:     m.ClientID,
		OrderId:      m.OrderID,
		SentTimeNs:   m.SentTimeNs,
		RecvTimeNs:   m.RecvTimeNs,
		EngineTimeNs: m.EngineTimeNs,
		IsCorrect:    m.IsCorrect,
		ErrorCode:    m.ErrorCode,
	}
	b, _ := proto.Marshal(rec)
	_ = producer.Publish(ctx, []byte(cfg.ContestantID), b)
}
