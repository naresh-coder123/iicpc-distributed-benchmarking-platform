$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\\..\\..")
$k8sBase = Join-Path $repoRoot "deploy\\phase5\\k8s\\base"

# ---------------------------------------------------------------------------
# 1. Apply all manifests via kustomize (excludes bot-fleet-job.yaml which is
#    applied on-demand by 06_run_botfleet.ps1).
# ---------------------------------------------------------------------------
Write-Host "Applying manifests..."
kubectl apply -k $k8sBase

# ---------------------------------------------------------------------------
# 2. Install metrics-server (required for HPA).
#    Patched with --kubelet-insecure-tls for kind's self-signed node certs.
# ---------------------------------------------------------------------------
Write-Host "Installing metrics-server..."
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
kubectl patch deployment metrics-server -n kube-system `
  --type=json `
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'

# ---------------------------------------------------------------------------
# 3. Wait for infrastructure (stateful services first, then apps).
# ---------------------------------------------------------------------------
Write-Host "Waiting for infrastructure..."
kubectl rollout status statefulset/timescaledb -n iicpc-telemetry --timeout=300s
kubectl rollout status statefulset/redpanda    -n iicpc-telemetry --timeout=300s
kubectl rollout status deploy/redis            -n iicpc-telemetry --timeout=180s

Write-Host "Waiting for application services..."
kubectl rollout status deploy/telemetry-ingester -n iicpc-telemetry --timeout=180s
kubectl rollout status deploy/mock-matcher       -n iicpc-runner    --timeout=180s
kubectl rollout status deploy/judge-api          -n iicpc-runner    --timeout=180s
kubectl rollout status deploy/leaderboard-api    -n iicpc-frontend  --timeout=180s
kubectl rollout status deploy/leaderboard-ui     -n iicpc-frontend  --timeout=240s

Write-Host ""
Write-Host "Phase 5 stack is up."
Write-Host "  UI:        http://localhost:30090"
Write-Host "  API:       http://localhost:30080/leaderboard"
Write-Host "  Judge API: http://localhost:30081"
Write-Host ""
Write-Host "To run the bot fleet:"
Write-Host "  .\deploy\phase5\scripts\06_run_botfleet.ps1"

