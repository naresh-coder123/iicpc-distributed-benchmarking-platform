$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\\..\\..")
$kindConfig = Join-Path $repoRoot "deploy\\phase5\\kind-config.yaml"

$gopath = (go env GOPATH).Trim()
$kindExe = Join-Path $gopath "bin\\kind.exe"

if (!(Get-Command kubectl -ErrorAction SilentlyContinue)) {
  throw "kubectl not found in PATH"
}
if (!(Test-Path $kindExe)) {
  Write-Host "kind not found, installing..."
  & (Join-Path $repoRoot "deploy\\phase5\\scripts\\01_install_kind.ps1")
}

Write-Host "Creating kind cluster..."
& $kindExe create cluster --config $kindConfig

Write-Host "Waiting for nodes..."
kubectl wait --for=condition=Ready nodes --all --timeout=180s

Write-Host "Labeling worker nodes (runner / telemetry)..."
$nodes = (kubectl get nodes -o jsonpath="{.items[*].metadata.name}").Split(" ", [System.StringSplitOptions]::RemoveEmptyEntries)

# Typically: iicpc-control-plane, iicpc-worker, iicpc-worker2
$workers = @()
foreach ($n in $nodes) {
  if ($n -notlike "*control-plane*") { $workers += $n }
}

if ($workers.Count -ge 1) { kubectl label node $workers[0] iicpc/node-role=runner --overwrite }
if ($workers.Count -ge 2) { kubectl label node $workers[1] iicpc/node-role=telemetry --overwrite }

Write-Host "Cluster ready."

