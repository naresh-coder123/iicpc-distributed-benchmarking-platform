package ingest

import (
	"testing"
	"time"
)

func TestAggregator_SingleContestant(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	agg.Add("alice", 100, true)
	agg.Add("alice", 200, true)
	agg.Add("alice", 300, false)

	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	w := windows[0]
	if w.ContestantID != "alice" {
		t.Errorf("unexpected contestant: %s", w.ContestantID)
	}
	if w.Count != 3 {
		t.Errorf("expected count=3, got %d", w.Count)
	}
	if w.CorrectCount != 2 {
		t.Errorf("expected correct=2, got %d", w.CorrectCount)
	}
	ratio := 2.0 / 3.0
	if w.CorrectRatio < ratio-0.001 || w.CorrectRatio > ratio+0.001 {
		t.Errorf("unexpected correct_ratio: %f", w.CorrectRatio)
	}
}

func TestAggregator_MultipleContestants(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	agg.Add("alice", 100, true)
	agg.Add("bob", 500, false)
	agg.Add("alice", 200, true)

	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(windows))
	}
}

func TestAggregator_EmptyFlush(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 0 {
		t.Fatalf("expected 0 windows on empty flush, got %d", len(windows))
	}
}

func TestAggregator_ScoreFormula(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	// 100% correct, all latencies = 1000µs
	for i := 0; i < 100; i++ {
		agg.Add("alice", 1000, true)
	}
	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window")
	}
	w := windows[0]
	// With the checklist formula:
	// L_composite = 0.2*1000 + 0.3*1000 + 0.5*1000 = 1000
	// S_L = 100 * e^(-0.001 * max(0, 1000-1000)) = 100 * e^0 = 100
	// S_T = 100 * min(1, tps/10000) — tps ≈ 100/1s = 100, so S_T ≈ 1.0
	// S_C = 100 * 1.0^4 = 100
	// S_Total = 0.4*100 + 0.3*1.0 + 0.3*100 ≈ 70.3
	if w.Score < 60 || w.Score > 110 {
		t.Errorf("score out of expected range [60,110]: got %.2f", w.Score)
	}
	if w.ScoreLatency < 99 || w.ScoreLatency > 101 {
		t.Errorf("S_L expected ~100, got %.2f", w.ScoreLatency)
	}
	if w.ScoreCorrectness < 99 || w.ScoreCorrectness > 101 {
		t.Errorf("S_C expected ~100, got %.2f", w.ScoreCorrectness)
	}
}

func TestAggregator_ResetAfterFlush(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	agg.Add("alice", 100, true)
	agg.FlushAndReset(1 * time.Second)

	// After reset, a second flush should be empty.
	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 0 {
		t.Fatalf("expected 0 windows after reset, got %d", len(windows))
	}
}
