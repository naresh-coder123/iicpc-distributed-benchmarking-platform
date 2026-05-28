#!/usr/bin/env pwsh
# =============================================================================
# 00_full_deploy.ps1  —  IICPC Platform complete Kubernetes deployment
# =============================================================================
#
# Runs every step from scratch: installs kind, creates cluster, builds images,
# loads them, deploys all manifests, waits for health, and prints access URLs.
#
# USAGE
#   .\deploy\phase5\scripts\00_full_deploy.ps1
#   .\deploy\phase5\scripts\00_full_deploy.ps1 -SkipBuild    # reuse existing images
#   .\deploy\phase5\scripts\00_full_deploy.ps1 -Destroy      # delete the cluster
# =============================================================================

param(
  [switch]$SkipBuild,
  [switch]$Destroy
)

$ErrorActionPreference = "Stop"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..\..") 
$scripts  = Join-Path $repoRoot "deploy\phase5\scripts"

function Write-Step($n, $msg) {
  Write-Host ""
  Write-Host "[$n/7] $msg" -ForegroundColor Cyan
  Write-Host ("-" * 50) -ForegroundColor DarkGray
}
function Write-Ok($msg)   { Write-Host "  OK  $msg" -ForegroundColor Green  }
function Write-Info($msg) { Write-Host "  ..  $msg" -ForegroundColor White  }

# ── Destroy mode ──────────────────────────────────────────────────────────────
if ($Destroy) {
  Write-Host "Destroying IICPC kind cluster..." -ForegroundColor Yellow
  $gopath = (go env GOPATH).Trim()
  $kindExe = Join-Path $gopath "bin\kind.exe"
  if (Test-Path $kindExe) {
    & $kindExe delete cluster --name iicpc
    Write-Ok "Cluster deleted."
  } else {
    Write-Host "kind not found — cluster may not exist." -ForegroundColor Yellow
  }
  exit 0
}

# ── Step 1: Prerequisites ─────────────────────────────────────────────────────
Write-Step 1 "Checking prerequisites"

foreach ($cmd in @("go", "docker", "kubectl")) {
  if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
    throw "$cmd not found in PATH. Install it and retry."
  }
  Write-Ok "$cmd found"
}

$dockerInfo = docker info 2>&1
if ($LASTEXITCODE -ne 0) { throw "Docker daemon not running. Start Docker Desktop." }
Write-Ok "Docker daemon running"

# ── Step 2: Install kind ──────────────────────────────────────────────────────
Write-Step 2 "Installing / verifying kind"
& "$scripts\01_install_kind.ps1"

# ── Step 3: Create cluster ────────────────────────────────────────────────────
Write-Step 3 "Creating kind cluster"
$gopath  = (go env GOPATH).Trim()
$kindExe = Join-Path $gopath "bin\kind.exe"

$existing = & $kindExe get clusters 2>$null
if ($existing -contains "iicpc") {
  Write-Info "Cluster 'iicpc' already exists — skipping creation."
} else {
  & "$scripts\02_create_cluster.ps1"
}
Write-Ok "Cluster ready"

# ── Step 4: Build images ──────────────────────────────────────────────────────
Write-Step 4 "Building Docker images"
if ($SkipBuild) {
  Write-Info "Skipping build (--SkipBuild)"
} else {
  & "$scripts\03_build_images.ps1"
  Write-Ok "Images built"
}

# ── Step 5: Load images into kind ────────────────────────────────────────────
Write-Step 5 "Loading images into kind cluster"
& "$scripts\04_load_images.ps1"
Write-Ok "Images loaded"

# ── Step 6: Deploy manifests ──────────────────────────────────────────────────
Write-Step 6 "Deploying Kubernetes manifests"
& "$scripts\05_apply_k8s.ps1"
Write-Ok "All deployments healthy"

# ── Step 7: Smoke test ────────────────────────────────────────────────────────
Write-Step 7 "Running smoke test"
& "$scripts\07_smoke_test.ps1" -K8s
Write-Ok "Smoke test passed"

# ── Done ──────────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host ("=" * 60) -ForegroundColor Green
Write-Host "  IICPC Platform deployed successfully!" -ForegroundColor Green
Write-Host ("=" * 60) -ForegroundColor Green
Write-Host ""
Write-Host "  Dashboard UI  : http://localhost:30090" -ForegroundColor White
Write-Host "  Leaderboard   : http://localhost:30080/leaderboard" -ForegroundColor White
Write-Host "  Judge API     : http://localhost:30081" -ForegroundColor White
Write-Host ""
Write-Host "  Next steps:" -ForegroundColor DarkGray
Write-Host "    Register a contestant and run the bot fleet:" -ForegroundColor DarkGray
Write-Host "    .\deploy\phase5\scripts\06_run_botfleet.ps1 -ContestantId team-alpha -Bots 50 -Duration 60" -ForegroundColor DarkGray
Write-Host ""
Write-Host "  Tear down:" -ForegroundColor DarkGray
Write-Host "    .\deploy\phase5\scripts\00_full_deploy.ps1 -Destroy" -ForegroundColor DarkGray
Write-Host "    .\deploy\phase5\scripts\08_teardown.ps1" -ForegroundColor DarkGray
Write-Host ""
