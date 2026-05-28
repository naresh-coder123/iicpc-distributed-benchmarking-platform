package telemetry

import "math"

type Stats struct {
	Count uint64

	MinLatencyNs uint64
	MaxLatencyNs uint64
	SumLatencyNs float64
}

func (s *Stats) Reset() {
	s.Count = 0
	s.MinLatencyNs = 0
	s.MaxLatencyNs = 0
	s.SumLatencyNs = 0
}

func (s *Stats) AddLatency(latNs uint64) {
	if s.Count == 0 {
		s.MinLatencyNs = latNs
		s.MaxLatencyNs = latNs
		s.SumLatencyNs = float64(latNs)
		s.Count = 1
		return
	}
	if latNs < s.MinLatencyNs {
		s.MinLatencyNs = latNs
	}
	if latNs > s.MaxLatencyNs {
		s.MaxLatencyNs = latNs
	}
	s.SumLatencyNs += float64(latNs)
	s.Count++
}

func (s *Stats) AvgLatencyNs() uint64 {
	if s.Count == 0 {
		return 0
	}
	return uint64(math.Round(s.SumLatencyNs / float64(s.Count)))
}
