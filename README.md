# IICPC Distributed Benchmarking Platform

An automated, isolated benchmarking platform for competitive trading engine evaluation. Contestants submit matching engine Docker images; the platform runs stress tests via a bot fleet, gathers telemetry via Kafka/Redpanda, stores time-series metrics in TimescaleDB, and tracks rankings on a live Redis-powered leaderboard.

---

## System Architecture

```
                       [ Bot Fleet ] (gRPC load generation)
                             │
                             ▼
               [ Sandboxed Contestant Engine ] (gRPC Port 50051)
                             │
                      MetricRecord (Proto)
                             │
                             ▼
                        [ Redpanda ] (Kafka API)
                             │
                    [ Telemetry Ingester ]
                             │
              ┌──────────────┴──────────────┐
              ▼                             ▼
       [ TimescaleDB ]                   [ Redis ]
   (Raw performance logs)          (Live Leaderboard + SSE)
              │                             │
              └──────────────┬──────────────┘
                             ▼
                    [ Leaderboard API ] (Port 8080)
                             │
                    [ Leaderboard UI ] (Port 3000 / Next.js)
```

---

## Core Services

| Service | Port | Description |
| :--- | :--- | :--- |
| `mock_matcher_go` | `50051` | Reference gRPC trading gateway. |
| `telemetry_ingester` | — | Aggregates metrics from Kafka, updates Redis & TimescaleDB. |
| `leaderboard_api` | `8080` | Serves ranking data, SSE updates, and history endpoints. |
| `judge_api` | `8081` | Intake queue, sandbox lifecycle, and runner orchestration. |
| `leaderboard-ui` | `3000` | Next.js portal (Leaderboard, Submission, Runs, History). |

---

## Quick Start (Local Development)

### Option A: Complete Docker Compose Stack (Easiest)
Spin up all infrastructure, microservices, and the frontend:
```bash
docker compose -f deploy/phase4/docker-compose.full.yml up --build
```
Access the dashboard at `http://localhost:3000`.

### Option B: Local Services + Docker Infra (Fast Iteration)
1. **Start Infrastructure Only**:
   ```bash
   docker compose -f deploy/phase4/docker-compose.yml up -d
   ```
2. **Run Backend Services**:
   ```bash
   # In separate terminal windows:
   go run ./cmd/telemetry_ingester
   go run ./cmd/leaderboard_api
   go run ./cmd/judge_api
   ```
3. **Run Next.js Frontend**:
   ```bash
   cd frontend/leaderboard-ui
   npm install
   npm run dev
   ```

---

## Production Deployment (AKS / Kubernetes)

Automated deploy scripts are provided in `deploy/phase5/scripts/`:

1. **Spin up cluster**: `.\deploy\phase5\scripts\02_create_cluster.ps1`
2. **Build and Load Images**:
   ```powershell
   .\deploy\phase5\scripts\03_build_images.ps1
   .\deploy\phase5\scripts\04_load_images.ps1
   ```
3. **Deploy manifests**: `.\deploy\phase5\scripts\05_apply_k8s.ps1`
4. **Trigger bot fleet**: `.\deploy\phase5\scripts\06_run_botfleet.ps1 -ContestantId "team-alpha"`

*Note: In Kubernetes deployments, the UI is exposed on port `30090` and the API gateway on `30080` (or public LoadBalancer IPs).*

---

## Submission & Judging

Contestant engines must implement the `TradingGateway` gRPC service on port `50051`.

Submit an engine image to the judge:
```bash
curl -X POST http://localhost:8081/submissions \
  -H "Content-Type: application/json" \
  -d '{"contestant_id":"team-alpha","image_tag":"iicpcregistery.azurecr.io/engine:v1"}'
```
Or use the **Submit** tab in the web UI.

### Scoring Formula
$$Score = CorrectnessRatio \times 1,000,000 - p99Latency(\mu s)$$
- **Correctness** dominates the ranking.
- **p99 Latency** acts as the tiebreaker.

---

## Development commands

### Build binaries
```bash
make build
```

### Run tests
```bash
go test ./...
```

### Protocol Buffers Stub Generation
Run the generators if you modify the `.proto` schemas:
```bash
# Go
python tools/gen_protos_go.py
# Python
python tools/gen_protos.py
```
