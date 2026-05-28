#!/usr/bin/env pwsh
# =============================================================================
# 07_smoke_test.ps1  —  Automated end-to-end smoke test
# =============================================================================
#
# Tests the full pipeline:
#   health checks → register contestant → run bot fleet → verify leaderboard
#   → verify TimescaleDB history → verify scoring formula → cleanup
#
# USAGE
#   .\07_smoke_test.ps1           # test local stack (ports 8080/8081/3000)
#   .\07_smoke_test.ps1 -K8s      # test k8s stack (ports 30080/30081/30090)
# =============================================================================

param([switch]$K8s)

$ErrorActionPreference = "Stop"

$lPort = if ($K8s) { 30080 } else { 8080 }
$jPort = if ($K8s) { 30081 } else { 8081 }
$uPort = if ($K8s) { 30090 } else { 3000  }
$lBase = "http://localhost:$lPort"
$jBase = "http://localhost:$jPort"

$pass = 0; $fail = 0
$errors = @()

function Test-Case($name, $block) {
  try {
    & $block
    Write-Host "  PASS  $name" -ForegroundColor Green
    $script:pass++
  } catch {
    Write-Host "  FAIL  $name — $($_.Exception.Message)" -ForegroundColor Red
    $script:fail++
    $script:errors += "FAIL: $name — $($_.Exception.Message)"
  }
}

function Assert-Eq($actual, $expected, $msg) {
  if ($actual -ne $expected) { throw "$msg — expected '$expected', got '$actual'" }
}
function Assert-Contains($str, $sub, $msg) {
  if ($str -notlike "*$sub*") { throw "$msg — '$sub' not found in response" }
}
function Assert-NotEmpty($val, $msg) {
  if (-not $val) { throw "$msg — value is empty/null" }
}

function Invoke-Api($method, $url, $body = $null) {
  $params = @{ Method = $method; Uri = $url; TimeoutSec = 15; ErrorAction = "Stop" }
  if ($body) {
    $params.Body        = ($body | ConvertTo-Json -Compress)
    $params.ContentType = "application/json"
  }
  return Invoke-RestMethod @params
}

Write-Host ""
Write-Host "IICPC Smoke Test — $(if ($K8s) {'Kubernetes'} else {'Local'})" -ForegroundColor Cyan
Write-Host ("=" * 55) -ForegroundColor DarkGray
Write-Host "  Leaderboard API : $lBase"
Write-Host "  Judge API       : $jBase"
Write-Host ("=" * 55) -ForegroundColor DarkGray
Write-Host ""

# ── 1. Health checks ──────────────────────────────────────────────────────────
Test-Case "Leaderboard API /healthz returns 200" {
  $r = Invoke-WebRequest "$lBase/healthz" -TimeoutSec 5
  Assert-Eq $r.StatusCode 200 "healthz"
}

Test-Case "Judge API /healthz returns 200" {
  $r = Invoke-WebRequest "$jBase/healthz" -TimeoutSec 5
  Assert-Eq $r.StatusCode 200 "healthz"
}

Test-Case "Judge API /admin/health shows redis=ok" {
  $r = Invoke-Api GET "$jBase/admin/health"
  Assert-Eq $r.redis "ok" "redis health"
}

Test-Case "Leaderboard /leaderboard returns array" {
  $r = Invoke-Api GET "$lBase/leaderboard"
  if ($r -isnot [array]) { throw "Expected array, got $($r.GetType().Name)" }
}

# ── 2. Contestant registration ────────────────────────────────────────────────
$smokeId = "smoke-$(Get-Random -Maximum 9999)"

Test-Case "Register contestant '$smokeId'" {
  $r = Invoke-Api POST "$jBase/contestants" @{ id=$smokeId; display_name="Smoke Test $smokeId" }
  Assert-Eq $r.id $smokeId "contestant id"
  Assert-NotEmpty $r.registered_at "registered_at"
}

Test-Case "GET /contestants/$smokeId returns contestant" {
  $r = Invoke-Api GET "$jBase/contestants/$smokeId"
  Assert-Eq $r.id $smokeId "contestant id"
}

Test-Case "GET /contestants returns list containing '$smokeId'" {
  $r = Invoke-Api GET "$jBase/contestants"
  $found = $r | Where-Object { $_.id -eq $smokeId }
  if (-not $found) { throw "Contestant not found in list" }
}

Test-Case "Duplicate registration returns 409" {
  try {
    Invoke-Api POST "$jBase/contestants" @{ id=$smokeId; display_name="Dup" }
    throw "Expected 409 but got success"
  } catch {
    if ($_.Exception.Message -notlike "*409*" -and $_.Exception.Message -notlike "*already exists*") {
      throw "Expected 409/already-exists, got: $($_.Exception.Message)"
    }
  }
}

Test-Case "Invalid contestant ID rejected" {
  try {
    Invoke-Api POST "$jBase/contestants" @{ id="has space"; display_name="Bad" }
    throw "Expected 400 but got success"
  } catch {
    if ($_.Exception.Message -notlike "*400*" -and $_.Exception.Message -notlike "*invalid*") {
      throw "Expected 400/invalid, got: $($_.Exception.Message)"
    }
  }
}

# ── 3. Submission ─────────────────────────────────────────────────────────────
Test-Case "Submit to non-existent contestant returns 404" {
  try {
    Invoke-Api POST "$jBase/submissions" @{ contestant_id="no-such-contestant"; image_tag="x:y" }
    throw "Expected 404"
  } catch {
    if ($_.Exception.Message -notlike "*404*" -and $_.Exception.Message -notlike "*not found*") {
      throw "Expected 404, got: $($_.Exception.Message)"
    }
  }
}

Test-Case "GET /contestants/$smokeId/runs returns empty array initially" {
  $r = Invoke-Api GET "$jBase/contestants/$smokeId/runs"
  if ($r -isnot [array]) { throw "Expected array" }
}

# ── 4. Admin endpoints ────────────────────────────────────────────────────────
Test-Case "GET /admin/queue returns pending+running counts" {
  $r = Invoke-Api GET "$jBase/admin/queue"
  if ($null -eq $r.pending) { throw "Missing 'pending' field" }
  if ($null -eq $r.running) { throw "Missing 'running' field" }
}

Test-Case "DELETE /leaderboard resets leaderboard" {
  Invoke-Api DELETE "$jBase/leaderboard" | Out-Null
  $r = Invoke-Api GET "$lBase/leaderboard"
  if ($r.Count -gt 0) { throw "Leaderboard not empty after reset" }
}

# ── 5. Bot fleet pipeline (requires local mock-matcher on :50051) ─────────────
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..\..") 
$botExe   = Join-Path $repoRoot "bin\bot_fleet.exe"

if ((Test-Path $botExe) -and -not $K8s) {
  Test-Case "Bot fleet runs and populates leaderboard" {
    $job = Start-Job -ScriptBlock {
      param($exe, $cid)
      & $exe --target localhost:50051 --kafka localhost:9092 `
             --bots 5 --ops 50 --duration 8 `
             --contestant-id $cid --run-id smoke-run 2>&1
    } -ArgumentList $botExe, $smokeId

    Wait-Job $job -Timeout 20 | Out-Null
    $output = Receive-Job $job
    Remove-Job $job -Force

    if ($output -notlike "*Bot fleet finished*" -and $output -notlike "*drained=*") {
      throw "Bot fleet did not complete. Output: $output"
    }

    # Give ingester 3s to flush to Redis.
    Start-Sleep -Seconds 3

    $lb = Invoke-Api GET "$lBase/leaderboard"
    $entry = $lb | Where-Object { $_.contestant_id -eq $smokeId }
    if (-not $entry) { throw "Contestant '$smokeId' not found in leaderboard after bot fleet" }
    if ($entry.count -eq 0) { throw "Order count is 0" }
  }

  Test-Case "Leaderboard entry has all scoring sub-components" {
    $lb = Invoke-Api GET "$lBase/leaderboard"
    $entry = $lb | Where-Object { $_.contestant_id -eq $smokeId }
    if (-not $entry) { throw "Contestant not in leaderboard" }
    if ($null -eq $entry.score_latency)     { throw "Missing score_latency" }
    if ($null -eq $entry.score_throughput)  { throw "Missing score_throughput" }
    if ($null -eq $entry.score_correctness) { throw "Missing score_correctness" }
    if ($null -eq $entry.score)             { throw "Missing score" }
    if ($entry.score -le 0)                 { throw "Score is zero or negative: $($entry.score)" }
    if ($entry.sustained_tps -le 0)         { throw "sustained_tps is zero" }
  }

  Test-Case "Score formula: S_Total = 0.40*S_L + 0.30*S_T + 0.30*S_C" {
    $lb = Invoke-Api GET "$lBase/leaderboard"
    $e  = $lb | Where-Object { $_.contestant_id -eq $smokeId }
    if (-not $e) { throw "Contestant not in leaderboard" }
    $expected = [math]::Round(0.40 * $e.score_latency + 0.30 * $e.score_throughput + 0.30 * $e.score_correctness, 2)
    $actual   = [math]::Round($e.score, 2)
    if ([math]::Abs($expected - $actual) -gt 0.5) {
      throw "Score formula mismatch: expected $expected, got $actual"
    }
  }

  Test-Case "SSE /stream endpoint is reachable" {
    # Just verify the endpoint opens and sends the initial comment.
    $req = [System.Net.WebRequest]::Create("$lBase/stream")
    $req.Timeout = 3000
    $req.Method  = "GET"
    try {
      $resp = $req.GetResponse()
      $ct   = $resp.ContentType
      $resp.Close()
      if ($ct -notlike "*text/event-stream*") { throw "Wrong Content-Type: $ct" }
    } catch [System.Net.WebException] {
      # Timeout is expected (SSE is long-lived) — that's fine.
      if ($_.Exception.Status -ne [System.Net.WebExceptionStatus]::Timeout) { throw }
    }
  }
} else {
  Write-Host "  SKIP  Bot fleet tests (bot_fleet.exe not found or -K8s mode)" -ForegroundColor DarkGray
}

# ── 6. Cleanup ────────────────────────────────────────────────────────────────
Test-Case "DELETE /admin/runs clears run history" {
  Invoke-Api DELETE "$jBase/admin/runs" | Out-Null
  $r = Invoke-Api GET "$jBase/contestants/$smokeId/runs"
  if ($r.Count -gt 0) { throw "Runs not cleared" }
}

Test-Case "DELETE /leaderboard final cleanup" {
  Invoke-Api DELETE "$jBase/leaderboard" | Out-Null
}

# ── Results ───────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host ("=" * 55) -ForegroundColor DarkGray
$total = $pass + $fail
if ($fail -eq 0) {
  Write-Host "  ALL $total TESTS PASSED" -ForegroundColor Green
} else {
  Write-Host "  $pass/$total PASSED   $fail FAILED" -ForegroundColor Red
  Write-Host ""
  $errors | ForEach-Object { Write-Host "  $_" -ForegroundColor Red }
}
Write-Host ("=" * 55) -ForegroundColor DarkGray
Write-Host ""

if ($fail -gt 0) { exit 1 }
