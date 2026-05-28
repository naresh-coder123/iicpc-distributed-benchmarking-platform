package telemetry

import (
	"sync/atomic"
)

// Metric is an internal, GC-friendly representation.
// (We keep it simple for Phase 2 MVP; later phases can optimize further.)
type Metric struct {
	TestRunID    string
	ContestantID string
	ClientID     string
	OrderID      string
	SentTimeNs   uint64
	RecvTimeNs   uint64
	EngineTimeNs uint64
	IsCorrect    bool
	ErrorCode    string
}

// RingBuffer is a bounded, multi-producer / single-consumer ring buffer.
//
// Push is lock-free (CAS-based). PopInto is intended for a single consumer goroutine.
type RingBuffer struct {
	size uint64
	buf  []Metric

	write uint64 // next write position (monotonic)
	read  uint64 // next read position (monotonic)
}

func NewRingBuffer(size uint64) *RingBuffer {
	if size == 0 {
		size = 1
	}
	return &RingBuffer{
		size: size,
		buf:  make([]Metric, size),
	}
}

// Push returns false if the buffer is full.
func (r *RingBuffer) Push(m Metric) bool {
	for {
		w := atomic.LoadUint64(&r.write)
		rd := atomic.LoadUint64(&r.read)
		if w-rd >= r.size {
			return false
		}
		if atomic.CompareAndSwapUint64(&r.write, w, w+1) {
			r.buf[w%r.size] = m
			return true
		}
	}
}

// PopInto pops up to len(dst) metrics into dst.
// It returns the number of elements written.
func (r *RingBuffer) PopInto(dst []Metric) int {
	rd := atomic.LoadUint64(&r.read)
	w := atomic.LoadUint64(&r.write)
	if w <= rd {
		return 0
	}

	avail := w - rd
	n := uint64(len(dst))
	if avail < n {
		n = avail
	}

	for i := uint64(0); i < n; i++ {
		dst[i] = r.buf[(rd+i)%r.size]
	}
	atomic.StoreUint64(&r.read, rd+n)
	return int(n)
}
