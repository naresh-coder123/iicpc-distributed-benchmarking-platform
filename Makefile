.PHONY: venv install protos run-mock \
        build build-mock build-ingester build-api build-botfleet build-judge \
        test \
        phase4-up phase4-down phase4-full-up \
        phase5-build phase5-load phase5-up phase5-botfleet phase5-judge-local

# ── Python (legacy) ──────────────────────────────────────────────────────────
venv:
	python -m venv .venv

install:
	python -m pip install -r requirements.txt

protos:
	python tools/gen_protos.py

run-mock:
	python -m cmd.mock_matcher.main

# ── Go binaries ───────────────────────────────────────────────────────────────
build: build-mock build-ingester build-api build-botfleet build-judge

build-mock:
	go build -o bin/mock_matcher_go ./cmd/mock_matcher_go

build-ingester:
	go build -o bin/telemetry_ingester ./cmd/telemetry_ingester

build-api:
	go build -o bin/leaderboard_api ./cmd/leaderboard_api

build-botfleet:
	go build -o bin/bot_fleet ./cmd/bot_fleet

build-judge:
	go build -o bin/judge_api ./cmd/judge_api

# ── Tests ─────────────────────────────────────────────────────────────────────
test:
	go test ./...

# ── Phase 4 (local Docker Compose) ───────────────────────────────────────────
phase4-up:
	docker compose -f deploy/phase4/docker-compose.yml up -d
	@echo "Infra running. Start Go services manually:"
	@echo "  go run ./cmd/mock_matcher_go"
	@echo "  go run ./cmd/telemetry_ingester --kafka localhost:9092 --redis localhost:6379 --pg postgres://postgres:postgres@localhost:5432/iicpc?sslmode=disable"
	@echo "  go run ./cmd/leaderboard_api --redis localhost:6379 --pg postgres://postgres:postgres@localhost:5432/iicpc?sslmode=disable"
	@echo "  go run ./cmd/judge_api --redis localhost:6379 --kafka localhost:9092"

phase4-down:
	docker compose -f deploy/phase4/docker-compose.yml down

phase4-full-up:
	docker compose -f deploy/phase4/docker-compose.full.yml up --build

# ── Phase 5 (Kubernetes / kind) ───────────────────────────────────────────────
phase5-build:
	powershell -ExecutionPolicy Bypass -File deploy/phase5/scripts/03_build_images.ps1

phase5-load:
	powershell -ExecutionPolicy Bypass -File deploy/phase5/scripts/04_load_images.ps1

phase5-up:
	powershell -ExecutionPolicy Bypass -File deploy/phase5/scripts/05_apply_k8s.ps1

phase5-botfleet:
	powershell -ExecutionPolicy Bypass -File deploy/phase5/scripts/06_run_botfleet.ps1

phase5-judge-local:
	go run ./cmd/judge_api --redis localhost:6379 --kafka localhost:9092
