package telemetry

import (
	"sync"
	"testing"
)

func TestRingBuffer_EmptyPop(t *testing.T) {
	rb := NewRingBuffer(8)
	dst := make([]Metric, 4)
	n := rb.PopInto(dst)
	if n != 0 {
		t.Fatalf("expected 0 from empty buffer, got %d", n)
	}
}

func TestRingBuffer_SingleItem(t *testing.T) {
	rb := NewRingBuffer(8)
	m := Metric{OrderID: "ord-1", IsCorrect: true}
	if !rb.Push(m) {
		t.Fatal("Push failed on empty buffer")
	}
	dst := make([]Metric, 4)
	n := rb.PopInto(dst)
	if n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
	if dst[0].OrderID != "ord-1" {
		t.Fatalf("unexpected OrderID: %s", dst[0].OrderID)
	}
}

func TestRingBuffer_FullDrop(t *testing.T) {
	rb := NewRingBuffer(4)
	for i := 0; i < 4; i++ {
		if !rb.Push(Metric{OrderID: "ok"}) {
			t.Fatalf("Push %d failed unexpectedly", i)
		}
	}
	// Buffer is full — next push must be dropped.
	if rb.Push(Metric{OrderID: "dropped"}) {
		t.Fatal("expected Push to return false on full buffer")
	}
}

func TestRingBuffer_DrainAll(t *testing.T) {
	rb := NewRingBuffer(16)
	for i := 0; i < 10; i++ {
		rb.Push(Metric{SentTimeNs: uint64(i)})
	}
	dst := make([]Metric, 16)
	n := rb.PopInto(dst)
	if n != 10 {
		t.Fatalf("expected 10, got %d", n)
	}
	// Second drain should be empty.
	n2 := rb.PopInto(dst)
	if n2 != 0 {
		t.Fatalf("expected 0 on second drain, got %d", n2)
	}
}

func TestRingBuffer_ConcurrentPush(t *testing.T) {
	const size = 1024
	const goroutines = 8
	const perGoroutine = 100

	rb := NewRingBuffer(size)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rb.Push(Metric{IsCorrect: true})
			}
		}()
	}
	wg.Wait()

	dst := make([]Metric, size)
	total := 0
	for {
		n := rb.PopInto(dst)
		if n == 0 {
			break
		}
		total += n
	}
	if total != goroutines*perGoroutine {
		t.Fatalf("expected %d total, got %d", goroutines*perGoroutine, total)
	}
}
