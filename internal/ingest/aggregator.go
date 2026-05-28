package ingest

import (
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

// Scoring constants — tunable via flags in the ingester.
const (
	// S_L: latency exponential decay
	DefaultLambda  = 0.001  // decay rate λ
	DefaultLTarget = 1000.0 // target latency µs (1ms)

	// S_T: throughput efficiency
	DefaultTTarget = 10000.0 // target TPS

	// S_Total weights
	WeightLatency     = 0.40
	WeightThroughput  = 0.30
	WeightCorrectness = 0.30
)

// ScoringParams holds the tunable scoring parameters.
type ScoringParams struct {
	Lambda  float64 // latency decay rate
	LTarget float64 // target latency µs
	TTarget float64 // target TPS
}

func DefaultScoringParams() ScoringParams {
	return ScoringParams{
		Lambda:  DefaultLambda,
		LTarget: DefaultLTarget,
		TTarget: DefaultTTarget,
	}
}

// ComputeScore implements the checklist scoring formula:
//
//	L_composite = 0.2·p50 + 0.3·p90 + 0.5·p99
//	S_L = 100 · e^(-λ · max(0, L_composite - L_target))
//	S_T = 100 · min(1.0, sustainedTPS / T_target)
//	S_C = 100 · (correctRatio)^4
//	S_Total = 0.40·S_L + 0.30·S_T + 0.30·S_C
func ComputeScore(p50, p90, p99 float64, correctRatio, sustainedTPS float64, p ScoringParams) float64 {
	lComposite := 0.2*p50 + 0.3*p90 + 0.5*p99
	sL := 100.0 * math.Exp(-p.Lambda*math.Max(0, lComposite-p.LTarget))

	sT := 100.0 * math.Min(1.0, sustainedTPS/p.TTarget)

	// S_C = 100 · (correct_ratio)^4 — 4th power drastically penalises even 1% error
	sC := 100.0 * math.Pow(correctRatio, 4)

	return WeightLatency*sL + WeightThroughput*sT + WeightCorrectness*sC
}

type ContestantWindow struct {
	ContestantID string `json:"contestant_id"`
	WindowStart  int64  `json:"window_start_unix"`
	WindowEnd    int64  `json:"window_end_unix"`

	Count        uint64  `json:"count"`
	CorrectCount uint64  `json:"correct_count"`
	CorrectRatio float64 `json:"correct_ratio"`

	P50Us uint64 `json:"p50_us"`
	P90Us uint64 `json:"p90_us"`
	P99Us uint64 `json:"p99_us"`

	MinUs uint64 `json:"min_us"`
	MaxUs uint64 `json:"max_us"`
	AvgUs uint64 `json:"avg_us"`

	// Sustained TPS over the window.
	SustainedTPS float64 `json:"sustained_tps"`

	// Composite score (S_Total) used for Redis sorted set.
	Score float64 `json:"score"`

	// Sub-scores for UI display.
	ScoreLatency     float64 `json:"score_latency"`
	ScoreThroughput  float64 `json:"score_throughput"`
	ScoreCorrectness float64 `json:"score_correctness"`
}

func (w ContestantWindow) JSON() string {
	b, _ := json.Marshal(w)
	return string(b)
}

type Aggregator struct {
	mu     sync.Mutex
	start  time.Time
	end    time.Time
	params ScoringParams

	// per contestant
	hists map[string]*hdrhistogram.Histogram
	sumUs map[string]uint64
	cnt   map[string]uint64
	ok    map[string]uint64
	minUs map[string]uint64
	maxUs map[string]uint64
}

func NewAggregator(window time.Duration) *Aggregator {
	return NewAggregatorWithParams(window, DefaultScoringParams())
}

func NewAggregatorWithParams(window time.Duration, p ScoringParams) *Aggregator {
	now := time.Now().UTC()
	return &Aggregator{
		start:  now,
		end:    now.Add(window),
		params: p,
		hists:  make(map[string]*hdrhistogram.Histogram),
		sumUs:  make(map[string]uint64),
		cnt:    make(map[string]uint64),
		ok:     make(map[string]uint64),
		minUs:  make(map[string]uint64),
		maxUs:  make(map[string]uint64),
	}
}

func (a *Aggregator) ensure(cid string) *hdrhistogram.Histogram {
	h := a.hists[cid]
	if h == nil {
		h = hdrhistogram.New(1, 60_000_000, 3)
		a.hists[cid] = h
		a.minUs[cid] = 0
		a.maxUs[cid] = 0
	}
	return h
}

func (a *Aggregator) Add(cid string, latencyUs uint64, correct bool) {
	if cid == "" {
		cid = "unknown"
	}
	if latencyUs == 0 {
		latencyUs = 1
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	h := a.ensure(cid)
	_ = h.RecordValue(int64(latencyUs))

	a.cnt[cid]++
	a.sumUs[cid] += latencyUs
	if correct {
		a.ok[cid]++
	}

	if a.minUs[cid] == 0 || latencyUs < a.minUs[cid] {
		a.minUs[cid] = latencyUs
	}
	if latencyUs > a.maxUs[cid] {
		a.maxUs[cid] = latencyUs
	}
}

func (a *Aggregator) FlushAndReset(nextWindow time.Duration) []ContestantWindow {
	a.mu.Lock()
	defer a.mu.Unlock()

	windowSecs := a.end.Sub(a.start).Seconds()
	if windowSecs <= 0 {
		windowSecs = 1
	}

	out := make([]ContestantWindow, 0, len(a.cnt))
	for cid, c := range a.cnt {
		if c == 0 {
			continue
		}
		h := a.hists[cid]
		ok := a.ok[cid]
		sum := a.sumUs[cid]

		ratio := float64(ok) / float64(c)
		avg := uint64(math.Round(float64(sum) / float64(c)))

		p50 := float64(h.ValueAtQuantile(50))
		p90 := float64(h.ValueAtQuantile(90))
		p99 := float64(h.ValueAtQuantile(99))

		sustainedTPS := float64(c) / windowSecs

		// Compute sub-scores for transparency.
		lComposite := 0.2*p50 + 0.3*p90 + 0.5*p99
		sL := 100.0 * math.Exp(-a.params.Lambda*math.Max(0, lComposite-a.params.LTarget))
		sT := 100.0 * math.Min(1.0, sustainedTPS/a.params.TTarget)
		sC := 100.0 * math.Pow(ratio, 4)
		total := WeightLatency*sL + WeightThroughput*sT + WeightCorrectness*sC

		out = append(out, ContestantWindow{
			ContestantID:     cid,
			WindowStart:      a.start.Unix(),
			WindowEnd:        a.end.Unix(),
			Count:            c,
			CorrectCount:     ok,
			CorrectRatio:     ratio,
			P50Us:            uint64(p50),
			P90Us:            uint64(p90),
			P99Us:            uint64(p99),
			MinUs:            a.minUs[cid],
			MaxUs:            a.maxUs[cid],
			AvgUs:            avg,
			SustainedTPS:     sustainedTPS,
			Score:            total,
			ScoreLatency:     sL,
			ScoreThroughput:  sT,
			ScoreCorrectness: sC,
		})
	}

	// Reset for next window.
	now := time.Now().UTC()
	a.start = now
	a.end = now.Add(nextWindow)
	a.hists = make(map[string]*hdrhistogram.Histogram)
	a.sumUs = make(map[string]uint64)
	a.cnt = make(map[string]uint64)
	a.ok = make(map[string]uint64)
	a.minUs = make(map[string]uint64)
	a.maxUs = make(map[string]uint64)

	return out
}
