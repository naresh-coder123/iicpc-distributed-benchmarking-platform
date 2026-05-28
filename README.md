# IICPC Platform

A competitive programming contest platform for trading engine benchmarking.
Contestants submit Docker images that implement a gRPC matching engine; the
platform stress-tests them with a bot fleet, measures latency and correctness,
and displays a live leaderboard.

---

## Architecture

```
Bot Fleet (gRPC clients)
    │  PlaceOrder / StreamOrders
    ▼
Contestant Engine (sandboxed Docker container)
    │  MetricRecord (protobuf) via Kafka
    ▼
Telemetry Ingester ──► TimescaleDB  (raw time-series, queryable via /history)
    │
    └──► Redis  (live leaderboard sorted set + pub/sub)
              │
         Leaderboard API  (HTTP + SSE)
              │
         Leaderboard UI   (Next.js, 5 tabs)

Judge API  (submission intake → sandbox lifecycle → bot fleet → results)
```

---

## Services

| Service | Port | Description |
|---|---|---|
| `mock_matcher_go` | 50051 | Reference gRPC matching engine (1–15ms latency) |
| `telemetry_ingester` | — | Kafka consumer → Redis leaderboard + TimescaleDB |
| `leaderboard_api` | 8080 | REST + SSE leaderboard; `/history` from TimescaleDB |
| `judge_api` | 8081 | Submission intake, sandbox orchestration, run results |
| `leaderboard-ui` | 3000 | Next.js UI (Leaderboard / Submit / My Runs / History / Contestants) |

---

## API Reference

### Leaderboard API (`localhost:8080`)

| Method | Path | Description |
|---|---|---|
| GET | `/leaderboard?limit=N` | Top N contestants from Redis |
| GET | `/stream` | SSE stream of leaderboard updates |
| GET | `/contestants/{id}/history?hours=N` | Per-minute stats from TimescaleDB |
| GET | `/runs/{id}/stats` | Aggregate stats for a test run from TimescaleDB |
| GET | `/healthz` | Health check |

### Judge API (`localhost:8081`)

| Method | Path | Description |
|---|---|---|
| POST | `/contestants` | Register a contestant `{"id":"…","display_name":"…"}` |
| GET | `/contestants` | List all contestants |
| GET | `/contestants/{id}` | Get a contestant |
| GET | `/contestants/{id}/submissions` | Submission history |
| GET | `/contestants/{id}/runs` | Run history (last 20) |
| POST | `/submissions` | Submit image `{"contestant_id":"…","image_tag":"…"}` |
| GET | `/submissions/{id}` | Get a submission |
| GET | `/runs/{id}` | Get a run result |
| DELETE | `/leaderboard` | Reset the leaderboard (admin) |
| GET | `/admin/queue` | Queue depth `{"pending":N,"running":N}` |
| DELETE | `/admin/runs` | Clear all run history from Redis (admin) |
| GET | `/admin/health` | Deep health check (Redis + Docker) |
| GET | `/healthz` | Health check |

---

## Phases

### Phase 1 — Contracts & Mock
- Protobuf contracts (`TradingGateway`, `MetricRecord`)
- Python mock matcher (async gRPC, 1–15ms delay)
- Go mock matcher (`cmd/mock_matcher_go`)

### Phase 2 — Bot Fleet
- Go bot fleet (`cmd/bot_fleet`) with lock-free ring buffer telemetry
- Configurable bots, ops/sec, duration, symbol

### Phase 3 — Sandbox Manager
- Docker Engine API client (`internal/sandbox`)
- Isolated bridge network (`Internal=true`, no internet egress)
- Resource limits: memory, CPU, cpuset, PID limit
- Security: readonly rootfs, no-new-privileges, drop all capabilities

### Phase 4 — Streaming Telemetry + Leaderboard
- Redpanda (Kafka API) for metric streaming
- TimescaleDB hypertable for raw time-series storage
- Redis sorted set for live leaderboard + pub/sub
- Telemetry ingester: Kafka → HDR histogram aggregation → Redis + TimescaleDB
- Leaderboard API: REST + SSE
- Next.js UI

### Phase 5 — Kubernetes (kind)
- All services containerised with distroless images
- 3 namespaces: `iicpc-telemetry`, `iicpc-runner`, `iicpc-frontend`
- NetworkPolicies (default-deny-all + explicit allow rules)
- Kubernetes Secrets (no hardcoded credentials)
- PodDisruptionBudgets + HorizontalPodAutoscalers
- Persistent volumes for Redpanda and TimescaleDB
- 5-step PowerShell deployment scripts

### Phase 6 — Judge Engine & Submission Portal
- Judge API: submission intake, sandbox lifecycle, bot fleet orchestration
- gRPC readiness probe + engine validation before full fleet launch
- Container cleanup on all paths (defer stop+remove)
- Buffered submission queue (max 20, single worker)
- Admin endpoints: reset leaderboard, clear runs, deep health check
- TimescaleDB query methods: per-minute history, per-run stats
- History tab in UI backed by TimescaleDB
- My Runs tab with auto-refresh while runs are in progress
- React Error Boundary wrapping all tabs

---

## Quick Start — Phase 4 (local, no Kubernetes)

### Option A: Docker Compose (full stack)

```powershell
# Build and start everything
docker compose -f deploy/phase4/docker-compose.full.yml up --build

# Open the UI
start http://localhost:3000
```

### Option B: Manual (faster iteration)

**Terminal 1 — Infrastructure:**
```powershell
docker compose -f deploy/phase4/docker-compose.yml up
```

**Terminal 2 — Mock matcher:**
```powershell
go run ./cmd/mock_matcher_go
```

**Terminal 3 — Telemetry ingester:**
```powershell
go run ./cmd/telemetry_ingester --kafka localhost:9092 --redis localhost:6379 --pg "postgres://postgres:postgres@localhost:5432/iicpc?sslmode=disable"
```

**Terminal 4 — Leaderboard API:**
```powershell
go run ./cmd/leaderboard_api --redis localhost:6379 --pg "postgres://postgres:postgres@localhost:5432/iicpc?sslmode=disable"
```

**Terminal 5 — Judge API:**
```powershell
go run ./cmd/judge_api --redis localhost:6379 --kafka localhost:9092
```

**Terminal 6 — UI:**
```powershell
cd frontend\leaderboard-ui
npm install
$env:NEXT_PUBLIC_API_BASE="http://localhost:8080"
$env:NEXT_PUBLIC_JUDGE_BASE="http://localhost:8081"
npm run dev
```

**Terminal 7 — Bot fleet (generates load):**
```powershell
go run ./cmd/bot_fleet --target localhost:50051 --kafka localhost:9092 --bots 50 --ops 200 --duration 60 --contestant-id team-alpha
```

---

## Quick Start — Phase 5 (Kubernetes / kind)

```powershell
# 1. Create cluster
.\deploy\phase5\scripts\02_create_cluster.ps1

# 2. Build images
.\deploy\phase5\scripts\03_build_images.ps1

# 3. Load into kind
.\deploy\phase5\scripts\04_load_images.ps1

# 4. Deploy
.\deploy\phase5\scripts\05_apply_k8s.ps1

# 5. Run bot fleet
.\deploy\phase5\scripts\06_run_botfleet.ps1 -ContestantId "team-alpha" -Bots 50 -Duration 60
```

URLs after deploy:
- UI: http://localhost:30090
- Leaderboard API: http://localhost:30080
- Judge API: http://localhost:30081

---

## Submitting a Contestant Engine

Your engine must implement the `TradingGateway` gRPC service (see `proto/iicpc/trading/trading.proto`) and listen on port `50051`.

```powershell
# Register your team
curl.exe -X POST http://localhost:8081/contestants `
  -H "Content-Type: application/json" `
  -d '{\"id\":\"team-alpha\",\"display_name\":\"Team Alpha\"}'

# Submit your engine image
curl.exe -X POST http://localhost:8081/submissions `
  -H "Content-Type: application/json" `
  -d '{\"contestant_id\":\"team-alpha\",\"image_tag\":\"your-registry/engine:v1\"}'

# Poll results
curl.exe http://localhost:8081/contestants/team-alpha/runs
```

Or use the web UI at http://localhost:3000 (or :30090 in k8s).

---

## Scoring

```
score = correct_ratio × 1,000,000 − p99_latency_µs
```

- `correct_ratio`: fraction of orders that received a valid non-empty response
- `p99_latency_µs`: 99th percentile end-to-end latency in microseconds

Higher is better. Correctness dominates; p99 latency is the tiebreaker.

---

## Development

### Build all Go binaries
```powershell
make build
```

### Run tests
```powershell
go test ./...
```

### Generate protobuf stubs

**Python:**
```powershell
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
python tools\gen_protos.py
```

**Go:**
```powershell
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
python tools\gen_protos_go.py
```

### Environment variables

See `.env.example` for the full list of environment variables used across all services.

---

## Prerequisites

- Go 1.22+
- Docker Desktop (Linux engine)
- Node.js 22+ (for UI development)
- Python 3.10+ (for proto generation only)
- kubectl + kind (for Phase 5)
