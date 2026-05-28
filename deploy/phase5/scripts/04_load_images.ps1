$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\\..\\..")
$gopath = (go env GOPATH).Trim()
$kindExe = Join-Path $gopath "bin\\kind.exe"

if (!(Test-Path $kindExe)) {
  throw "kind.exe not found at $kindExe. Run deploy/phase5/scripts/01_install_kind.ps1 first."
}

Write-Host "Loading images into kind cluster..."
& $kindExe load docker-image iicpc/mock-matcher-go:latest   --name iicpc
& $kindExe load docker-image iicpc/telemetry-ingester:latest --name iicpc
& $kindExe load docker-image iicpc/leaderboard-api:latest    --name iicpc
& $kindExe load docker-image iicpc/leaderboard-ui:latest     --name iicpc
& $kindExe load docker-image iicpc/bot-fleet:latest          --name iicpc
& $kindExe load docker-image iicpc/judge-api:latest          --name iicpc

Write-Host "Done."

