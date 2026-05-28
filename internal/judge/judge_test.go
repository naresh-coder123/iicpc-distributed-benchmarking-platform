package judge

import (
	"testing"

	"github.com/iicpc/platform/internal/telemetry"
)

// ── percentiles ───────────────────────────────────────────────────────────────

func TestPercentiles_Empty(t *testing.T) {
	p50, p90, p99 := percentiles(nil)
	if p50 != 0 || p90 != 0 || p99 != 0 {
		t.Fatalf("expected all zeros for empty slice, got %d %d %d", p50, p90, p99)
	}
}

func TestPercentiles_Single(t *testing.T) {
	p50, p90, p99 := percentiles([]uint64{42})
	if p50 != 42 || p90 != 42 || p99 != 42 {
		t.Fatalf("expected 42 for all percentiles, got %d %d %d", p50, p90, p99)
	}
}

func TestPercentiles_KnownValues(t *testing.T) {
	// 100 values: 1..100
	data := make([]uint64, 100)
	for i := range data {
		data[i] = uint64(i + 1)
	}
	p50, p90, p99 := percentiles(data)
	// p50 index = 49 → value 50
	if p50 != 50 {
		t.Errorf("p50: expected 50, got %d", p50)
	}
	// p90 index = 89 → value 90
	if p90 != 90 {
		t.Errorf("p90: expected 90, got %d", p90)
	}
	// p99 index = 98 → value 99
	if p99 != 99 {
		t.Errorf("p99: expected 99, got %d", p99)
	}
}

func TestPercentiles_Unsorted(t *testing.T) {
	data := []uint64{300, 100, 200, 400, 500}
	p50, _, p99 := percentiles(data)
	// sorted: [100,200,300,400,500], n=5
	// p50 index = int(4*50/100) = 2 → 300
	if p50 != 300 {
		t.Errorf("p50: expected 300, got %d", p50)
	}
	// p99 index = int(4*99/100) = int(3.96) = 3 → 400
	if p99 != 400 {
		t.Errorf("p99: expected 400, got %d", p99)
	}
}

// ── drainRingBuffer ───────────────────────────────────────────────────────────

func TestDrainRingBuffer_Empty(t *testing.T) {
	rb := telemetry.NewRingBuffer(64)
	s := drainRingBuffer(rb)
	if s.Count != 0 {
		t.Fatalf("expected count=0 for empty buffer, got %d", s.Count)
	}
}

func TestDrainRingBuffer_AllCorrect(t *testing.T) {
	rb := telemetry.NewRingBuffer(1024)
	for i := 0; i < 100; i++ {
		rb.Push(telemetry.Metric{
			SentTimeNs: 1000,
			RecvTimeNs: 2000, // 1µs latency
			IsCorrect:  true,
		})
	}
	s := drainRingBuffer(rb)
	if s.Count != 100 {
		t.Errorf("expected count=100, got %d", s.Count)
	}
	if s.CorrectRatio != 1.0 {
		t.Errorf("expected correct_ratio=1.0, got %f", s.CorrectRatio)
	}
	if s.Score <= 0 {
		t.Errorf("expected positive score, got %f", s.Score)
	}
}

func TestDrainRingBuffer_MixedCorrectness(t *testing.T) {
	rb := telemetry.NewRingBuffer(1024)
	for i := 0; i < 50; i++ {
		rb.Push(telemetry.Metric{SentTimeNs: 0, RecvTimeNs: 1000, IsCorrect: true})
	}
	for i := 0; i < 50; i++ {
		rb.Push(telemetry.Metric{SentTimeNs: 0, RecvTimeNs: 1000, IsCorrect: false})
	}
	s := drainRingBuffer(rb)
	if s.Count != 100 {
		t.Errorf("expected count=100, got %d", s.Count)
	}
	if s.CorrectRatio < 0.49 || s.CorrectRatio > 0.51 {
		t.Errorf("expected correct_ratio≈0.5, got %f", s.CorrectRatio)
	}
}
