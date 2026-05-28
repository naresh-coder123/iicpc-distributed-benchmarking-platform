#!/usr/bin/env pwsh
# =============================================================================
# start_local.ps1  —  IICPC Platform single-command local orchestrator
# =============================================================================
#
# USAGE
#   .\start_local.ps1                   # build + start everything
#   .\start_local.ps1 -SkipBuild        # skip go build (use existing bin\)
#   .\start_local.ps1 -SkipUI           # skip Next.js UI
#   .\start_local.ps1 -Demo             # start + auto-register + run bot fleet
#   .\start_local.ps1 -Stop             # graceful shutdown
#   .\start_local.ps1 -Status           # show what's running
#
# WHAT IT DOES
#   1. Builds all Go binaries into bin\
#   2. Starts Redpanda + Redis + TimescaleDB via Docker Compose
#   3. Waits for each to be healthy (with real health checks)
#   4. Starts all Go services as background jobs (not new windows)
#   5. Starts the Next.js UI
#   6. Writes a PID file so -Stop can cleanly shut everything down
#   7. Optionally runs a demo bot fleet (-Demo flag)
# =============================================================================

param(
  [switch]$Stop,
  [switch]$Status,
  [switch]$SkipBuild,
  [switch]$SkipUI,
  [switch]$Demo,
  [int]$DemoBots     = 20,
  [int]$DemoOps      = 100,
  [int]$DemoDuration = 30
)

$ErrorActionPreference = "Stop"
$root    = $PSScriptRoot
$pidFile = "$root\.iicpc_pids"
$pgDSN   = "postgres://postgres:postgres@localhost:5432/iicpc?sslmode=disable"

# ── Colour helpers ────────────────────────────────────────────────────────────
function Write-Ok($msg)   { Write-Host "  [OK] $msg"    -ForegroundColor Green  }
function Write-Info($msg) { Write-Host "  [..] $msg"    -ForegroundColor Cyan   }
function Write-Warn($msg) { Write-Host "  [!!] $msg"    -ForegroundColor Yellow }
function Write-Err($msg)  { Write-Host "  [XX] $msg"    -ForegroundColor Red    }
function Write-Banner($msg) {
  Write-Host ""
  Write-Host ("=" * 60) -ForegroundColor DarkCyan
  Write-Host "  $msg"   -ForegroundColor White
  Write-Host ("=" * 60) -ForegroundColor DarkCyan
}

# ── Status mode ───────────────────────────────────────────────────────────────
if ($Status) {
  Write-Banner "IICPC Local Stack Status"
  $services = @(
    @{ Name="mock-matcher";       Port=50051; Proto="tcp" },
    @{ Name="telemetry-ingester"; Port=$null; Proto=$null },
    @{ Name="leaderboard-api";    Port=8080;  Proto="http" },
    @{ Name="judge-api";          Port=8081;  Proto="http" },
    @{ Name="leaderboard-ui";     Port=3000;  Proto="http" }
  )
  foreach ($svc in $services) {
    if ($svc.Port) {
      try {
        if ($svc.Proto -eq "http") {
          $r = Invoke-WebRequest "http://localhost:$($svc.Port)/healthz" -TimeoutSec 2 -ErrorAction Stop
          Write-Ok "$($svc.Name) — http://localhost:$($svc.Port) (HTTP $($r.StatusCode))"
        } else {
          $tcp = New-Object System.Net.Sockets.TcpClient
          $tcp.Connect("localhost", $svc.Port)
          $tcp.Close()
          Write-Ok "$($svc.Name) — localhost:$($svc.Port) (TCP open)"
        }
      } catch { Write-Warn "$($svc.Name) — NOT reachable on port $($svc.Port)" }
    } else {
      Write-Info "$($svc.Name) — background job (no HTTP port)"
    }
  }
  Write-Host ""
  docker compose -f "$root\deploy\phase4\docker-compose.yml" ps
  exit 0
}

# ── Stop mode ─────────────────────────────────────────────────────────────────
if ($Stop) {
  Write-Banner "Stopping IICPC Local Stack"

  # Kill tracked background jobs.
  if (Test-Path $pidFile) {
    $pids = Get-Content $pidFile
    foreach ($p in $pids) {
      try {
        Stop-Process -Id ([int]$p) -Force -ErrorAction SilentlyContinue
        Write-Ok "Stopped PID $p"
      } catch { Write-Warn "PID $p already gone" }
    }
    Remove-Item $pidFile -Force
  }

  # Stop Docker Compose infra.
  Write-Info "Stopping Docker Compose infrastructure..."
  docker compose -f "$root\deploy\phase4\docker-compose.yml" down
  Write-Ok "Infrastructure stopped."
  Write-Host ""
  exit 0
}

# ── Build ─────────────────────────────────────────────────────────────────────
Write-Banner "IICPC Platform — Local Startup"

if (-not $SkipBuild) {
  Write-Info "Building Go binaries..."
  Push-Location $root
  New-Item -ItemType Directory -Force -Path bin | Out-Null
  $bins = @(
    @{ Out="bin\mock_matcher_go.exe";    Pkg=".\cmd\mock_matcher_go"    },
    @{ Out="bin\telemetry_ingester.exe"; Pkg=".\cmd\telemetry_ingester" },
    @{ Out="bin\leaderboard_api.exe";    Pkg=".\cmd\leaderboard_api"    },
    @{ Out="bin\judge_api.exe";          Pkg=".\cmd\judge_api"          },
    @{ Out="bin\bot_fleet.exe";          Pkg=".\cmd\bot_fleet"          }
  )
  foreach ($b in $bins) {
    Write-Info "  go build $($b.Pkg)"
    go build -o $b.Out $b.Pkg
    if ($LASTEXITCODE -ne 0) { throw "Build failed: $($b.Pkg)" }
  }
  Pop-Location
  Write-Ok "All binaries built."
} else {
  Write-Warn "Skipping build (using existing bin\)"
}

# ── Infrastructure ────────────────────────────────────────────────────────────
Write-Info "Starting infrastructure (Redpanda, Redis, TimescaleDB)..."
docker compose -f "$root\deploy\phase4\docker-compose.yml" up -d
if ($LASTEXITCODE -ne 0) { throw "docker compose up failed" }

# Health check helper.
function Wait-Healthy($name, $check, $maxRetries = 40, $sleepSec = 2) {
  Write-Info "Waiting for $name..."
  for ($i = 0; $i -lt $maxRetries; $i++) {
    try {
      $result = & $check
      if ($result) { Write-Ok "$name is ready."; return }
    } catch {}
    Start-Sleep -Seconds $sleepSec
  }
  throw "$name did not become healthy after $($maxRetries * $sleepSec)s"
}

# Redis.
$redisId = (docker compose -f "$root\deploy\phase4\docker-compose.yml" ps -q redis).Trim()
Wait-Healthy "Redis" {
  (docker exec $redisId redis-cli ping 2>$null).Trim() -eq "PONG"
}

# TimescaleDB.
$pgId = (docker compose -f "$root\deploy\phase4\docker-compose.yml" ps -q timescaledb).Trim()
Wait-Healthy "TimescaleDB" {
  (docker exec $pgId pg_isready -U postgres 2>$null) -like "*accepting*"
}

# Redpanda — wait for Kafka port to be open.
Wait-Healthy "Redpanda" {
  try {
    $tcp = New-Object System.Net.Sockets.TcpClient
    $tcp.Connect("localhost", 9092)
    $tcp.Close(); $true
  } catch { $false }
} -maxRetries 30 -sleepSec 2

# ── Start Go services as background jobs ──────────────────────────────────────
Write-Info "Starting Go services..."
$jobs = @()
$pids = @()

function Start-GoService($name, $exe, $args) {
  $job = Start-Job -ScriptBlock {
    param($e, $a)
    & $e @a 2>&1
  } -ArgumentList "$root\$exe", $args
  $jobs += $job
  # Give it a moment to start, then grab the child process PID.
  Start-Sleep -Milliseconds 800
  $childPid = (Get-Process | Where-Object { $_.Path -like "*$($exe.Split('\')[-1].Replace('.exe',''))*" } | Select-Object -First 1).Id
  if ($childPid) { $pids += $childPid }
  return $job
}

$j1 = Start-Job -Name "mock-matcher" -ScriptBlock {
  param($root)
  & "$root\bin\mock_matcher_go.exe" --addr 0.0.0.0:50051 2>&1
} -ArgumentList $root

$j2 = Start-Job -Name "telemetry-ingester" -ScriptBlock {
  param($root, $pg)
  & "$root\bin\telemetry_ingester.exe" --kafka localhost:9092 --redis localhost:6379 --pg $pg 2>&1
} -ArgumentList $root, $pgDSN

$j3 = Start-Job -Name "leaderboard-api" -ScriptBlock {
  param($root, $pg)
  & "$root\bin\leaderboard_api.exe" --addr :8080 --redis localhost:6379 --pg $pg 2>&1
} -ArgumentList $root, $pgDSN

$j4 = Start-Job -Name "judge-api" -ScriptBlock {
  param($root)
  & "$root\bin\judge_api.exe" --addr :8081 --redis localhost:6379 --kafka localhost:9092 2>&1
} -ArgumentList $root

$allJobs = @($j1, $j2, $j3, $j4)

# Save job IDs for -Stop.
($allJobs | ForEach-Object { $_.Id }) | Set-Content $pidFile

# Wait for HTTP services to be reachable.
function Wait-Http($name, $url, $maxRetries = 30, $sleepSec = 2) {
  Write-Info "Waiting for $name at $url..."
  for ($i = 0; $i -lt $maxRetries; $i++) {
    try {
      $r = Invoke-WebRequest $url -TimeoutSec 2 -ErrorAction Stop
      if ($r.StatusCode -eq 200) { Write-Ok "$name ready."; return }
    } catch {}
    Start-Sleep -Seconds $sleepSec
  }
  throw "$name did not respond at $url after $($maxRetries * $sleepSec)s"
}

Wait-Http "leaderboard-api" "http://localhost:8080/healthz"
Wait-Http "judge-api"       "http://localhost:8081/healthz"

# ── UI ────────────────────────────────────────────────────────────────────────
if (-not $SkipUI) {
  $uiDir = "$root\frontend\leaderboard-ui"
  if (-not (Test-Path "$uiDir\node_modules")) {
    Write-Info "Installing UI dependencies (first run)..."
    Push-Location $uiDir; npm install --silent; Pop-Location
  }
  $j5 = Start-Job -Name "leaderboard-ui" -ScriptBlock {
    param($uiDir)
    $env:NEXT_PUBLIC_API_BASE   = "http://localhost:8080"
    $env:NEXT_PUBLIC_JUDGE_BASE = "http://localhost:8081"
    Set-Location $uiDir
    npm run dev 2>&1
  } -ArgumentList $uiDir
  $allJobs += $j5
  ($allJobs | ForEach-Object { $_.Id }) | Set-Content $pidFile
  Wait-Http "leaderboard-ui" "http://localhost:3000" -maxRetries 40
}

# ── Demo mode ─────────────────────────────────────────────────────────────────
if ($Demo) {
  Write-Banner "Running Demo"
  Write-Info "Registering demo contestant..."
  try {
    Invoke-RestMethod "http://localhost:8081/contestants" -Method POST `
      -ContentType "application/json" `
      -Body '{"id":"demo-team","display_name":"Demo Team"}' | Out-Null
    Write-Ok "Contestant registered: demo-team"
  } catch { Write-Warn "Contestant may already exist." }

  Write-Info "Running bot fleet (bots=$DemoBots, ops=$DemoOps, duration=${DemoDuration}s)..."
  $j6 = Start-Job -Name "bot-fleet-demo" -ScriptBlock {
    param($root, $bots, $ops, $dur)
    & "$root\bin\bot_fleet.exe" `
      --target localhost:50051 `
      --kafka localhost:9092 `
      --bots $bots --ops $ops --duration $dur `
      --contestant-id demo-team --run-id demo-run-1 2>&1
  } -ArgumentList $root, $DemoBots, $DemoOps, $DemoDuration

  Write-Info "Bot fleet running in background (job: $($j6.Id))..."
  Write-Info "Watch the Dashboard at http://localhost:3000"
}

# ── Summary ───────────────────────────────────────────────────────────────────
Write-Banner "Stack is UP"
Write-Host ""
Write-Host "  Dashboard UI      : http://localhost:3000" -ForegroundColor White
Write-Host "  Leaderboard API   : http://localhost:8080/leaderboard" -ForegroundColor White
Write-Host "  Judge API         : http://localhost:8081" -ForegroundColor White
Write-Host "  Leaderboard SSE   : http://localhost:8080/stream" -ForegroundColor White
Write-Host ""
Write-Host "  Background jobs   :" -ForegroundColor DarkGray
$allJobs | ForEach-Object { Write-Host "    [$($_.Id)] $($_.Name)" -ForegroundColor DarkGray }
Write-Host ""
Write-Host "  Useful commands:" -ForegroundColor DarkGray
Write-Host "    .\start_local.ps1 -Status          # check health" -ForegroundColor DarkGray
Write-Host "    .\start_local.ps1 -Stop             # shut everything down" -ForegroundColor DarkGray
Write-Host "    Receive-Job -Id <N> -Keep           # view service logs" -ForegroundColor DarkGray
Write-Host ""
Write-Host "  Run a bot fleet:" -ForegroundColor DarkGray
Write-Host "    .\bin\bot_fleet.exe --target localhost:50051 --kafka localhost:9092 --bots 50 --ops 200 --duration 60 --contestant-id team-alpha" -ForegroundColor DarkGray
Write-Host ""
