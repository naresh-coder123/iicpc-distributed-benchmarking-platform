package ingest

import (
	"testing"
	"time"
)

// Ensures malformed input (empty contestant id) is canonicalized so metrics are still scored.
func TestAggregator_Add_EmptyContestantIDFallsBackToUnknown(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	agg.Add("", 250, true)

	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].ContestantID != "unknown" {
		t.Fatalf("expected contestant id 'unknown', got %q", windows[0].ContestantID)
	}
}

// Guards histogram lower-bound behavior: zero-latency metrics are normalized to 1µs.
func TestAggregator_Add_ZeroLatencyIsNormalized(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	agg.Add("alice", 0, true)

	windows := agg.FlushAndReset(1 * time.Second)
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	w := windows[0]
	if w.MinUs != 1 || w.MaxUs != 1 {
		t.Fatalf("expected normalized min/max latency of 1µs, got min=%d max=%d", w.MinUs, w.MaxUs)
	}
}

// Validates reset semantics across windows to prevent stat bleed between scoring intervals.
func TestAggregator_Flush_ResetIsolationAcrossWindows(t *testing.T) {
	agg := NewAggregator(1 * time.Second)
	agg.Add("alice", 100, true)
	first := agg.FlushAndReset(1 * time.Second)
	if len(first) != 1 || first[0].Count != 1 {
		t.Fatalf("unexpected first window: %+v", first)
	}

	agg.Add("alice", 300, false)
	second := agg.FlushAndReset(1 * time.Second)
	if len(second) != 1 || second[0].Count != 1 {
		t.Fatalf("unexpected second window: %+v", second)
	}
	if second[0].CorrectCount != 0 {
		t.Fatalf("expected second window correctness to reset, got %d", second[0].CorrectCount)
	}
}
