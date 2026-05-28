$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\\..\\..")
Set-Location $repoRoot

Write-Host "Building Go service images..."
docker build -t iicpc/mock-matcher-go:latest   -f .\deploy\phase5\images\mock_matcher_go\Dockerfile .
docker build -t iicpc/telemetry-ingester:latest -f .\deploy\phase5\images\telemetry_ingester\Dockerfile .
docker build -t iicpc/leaderboard-api:latest    -f .\deploy\phase5\images\leaderboard_api\Dockerfile .
docker build -t iicpc/bot-fleet:latest          -f .\deploy\phase5\images\bot_fleet\Dockerfile .
docker build -t iicpc/judge-api:latest          -f .\deploy\phase5\images\judge_api\Dockerfile .

Write-Host "Building Next.js UI image..."
docker build -t iicpc/leaderboard-ui:latest -f .\frontend\leaderboard-ui\Dockerfile .\frontend\leaderboard-ui

Write-Host "Done."

