package telemetry

import "testing"

// Ensures constructor guard against invalid capacity keeps buffer usable.
func TestRingBuffer_NewRingBuffer_ZeroSizeCoercesToOne(t *testing.T) {
	rb := NewRingBuffer(0)
	if !rb.Push(Metric{OrderID: "first"}) {
		t.Fatal("expected first push to succeed with coerced size=1")
	}
	if rb.Push(Metric{OrderID: "second"}) {
		t.Fatal("expected second push to fail because capacity should be 1")
	}
}

// Verifies wrap-around behavior preserves FIFO ordering across modulo index rollover.
func TestRingBuffer_WrapAroundMaintainsOrder(t *testing.T) {
	rb := NewRingBuffer(3)
	if !rb.Push(Metric{OrderID: "a"}) || !rb.Push(Metric{OrderID: "b"}) || !rb.Push(Metric{OrderID: "c"}) {
		t.Fatal("failed to fill ring buffer")
	}

	dst := make([]Metric, 2)
	n := rb.PopInto(dst)
	if n != 2 || dst[0].OrderID != "a" || dst[1].OrderID != "b" {
		t.Fatalf("unexpected first pop result: n=%d dst=%+v", n, dst)
	}

	if !rb.Push(Metric{OrderID: "d"}) || !rb.Push(Metric{OrderID: "e"}) {
		t.Fatal("expected pushes after pop to succeed during wrap-around")
	}

	all := make([]Metric, 4)
	n = rb.PopInto(all)
	if n != 3 {
		t.Fatalf("expected 3 remaining metrics, got %d", n)
	}
	want := []string{"c", "d", "e"}
	for i := 0; i < n; i++ {
		if all[i].OrderID != want[i] {
			t.Fatalf("order mismatch at %d: want=%s got=%s", i, want[i], all[i].OrderID)
		}
	}
}
