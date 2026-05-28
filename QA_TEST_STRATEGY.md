# QA Sprint Strategy (Principal QA + Systems Architecture)

## Phase 0: Autonomous Context Extraction
The platform’s core business need is to run fair, repeatable, high-throughput benchmarking of contestant trading engines and convert raw performance/correctness telemetry into a trusted leaderboard.  
Its primary functionality is orchestrating judge runs (sandbox lifecycle + bot fleet load), ingesting streaming metrics, scoring contestants, and exposing live + historical leaderboard APIs/UI backed by Redis and TimescaleDB.

## Phase 1: Codebase & Vulnerability Triage
Top 3 critical risks that can break core functionality:

1. **Scoring integrity drift / input-domain risk (Aggregator)**
   - Risk: `ComputeScore` accepts unconstrained numeric inputs (`correctRatio`, `sustainedTPS`, params), so malformed/poisoned data can inflate/deflate scores unexpectedly (e.g., ratio > 1).  
   - Impact: leaderboard trust is compromised.

2. **Telemetry loss under burst pressure (RingBuffer)**
   - Risk: bounded ring buffer intentionally drops metrics when full; under judge bursts, silent loss can bias latency/correctness stats.  
   - Impact: run stats diverge from reality; unfair ranking.

3. **Cross-service persistence fragility (Timescale insert/query path)**
   - Risk: ingestion/persistence pipeline depends on Kafka->ingester->Timescale and query semantics (time buckets/percentiles). Any schema mismatch, negative latency handling, or DB outage can break `/history` and `/runs/{id}/stats`.  
   - Impact: historical analytics and run validation fail despite ongoing runs.

## Phase 2: TDD & Algorithmic Edge-Case Testing
TDD scenarios (write test first, implement/adjust code second if failing):

### Aggregator / Scoring
- Empty flush returns no windows.
- `Add` with empty contestant id maps to `unknown`.
- `Add` with zero latency normalizes to 1µs to preserve HDR histogram bounds.
- Multi-contestant isolation (stats never bleed across IDs).
- Window duration edge: non-positive window seconds should be guarded (fallback to 1s).
- Score formula monotonicity checks:
  - Higher latency at same correctness/TPS should never increase score.
  - Lower correctness should penalize score superlinearly due to power-4 term.
- Malformed numeric inputs (future hardening): `NaN`, negative throughput, ratio out of range.

### RingBuffer
- Size=0 constructor coercion to size=1.
- Full buffer rejects push (`false`) and does not corrupt existing entries.
- Wrap-around correctness after pop+push cycles.
- PopInto with small destination slices drains in chunks without reordering.
- Concurrent multi-producer push accuracy at capacities large enough to avoid expected drops.

### Judge stats reducer
- `percentiles` correctness for unsorted, tiny, and empty slices.
- `drainRingBuffer` correctness ratio on mixed true/false stream.
- Non-negative latency handling when `recv < sent` (no underflow) in downstream persistence path.

## Phase 3: Integration & Pipeline Resilience
Integration tests designed to break boundaries:

1. **Judge -> Sandbox startup failure path**
   - Inject container startup failure; assert run marked `FAILED`, error persisted, cleanup attempted.

2. **Judge -> gRPC readiness timeout**
   - Start container without responsive gRPC endpoint; assert readiness retries then fail fast with deterministic error.

3. **Bot fleet partial run + context cancellation**
   - Cancel run context mid-flight; verify graceful stop, no zombie containers, run status terminal.

4. **Ingester with Redis unavailable**
   - Kafka metrics consumed but Redis down: verify retry/backoff and no process crash; Timescale path still attempted/logged.

5. **Ingester with Timescale unavailable**
   - DB outage during batch insert: verify errors surfaced/metrics for dropped batches emitted.

6. **Leaderboard API stale or missing data**
   - Redis missing ZSET members and Timescale query errors: verify HTTP status semantics and payload stability.

7. **Schema compatibility guard**
   - Run migration + smoke query test for percentile/time_bucket query plan to catch SQL drift before deploy.

## Phase 4: Sprint-Ready Code Generation (Implemented)
Added focused Go tests for highest-impact core logic scenarios:

- Aggregator edge-case normalization and contestant ID fallback.
- Aggregator multi-window reset behavior.
- Ring buffer constructor guard + wrap-around ordering.

These tests are aligned to business stories: **fair scoring**, **accurate telemetry**, and **deterministic run statistics**.
