$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Parameters — override on the command line:
#   .\06_run_botfleet.ps1 -ContestantId "team-alpha" -RunId "run-2" -Bots 100 -Ops 500 -Duration 120
# ---------------------------------------------------------------------------
param(
  [string]$ContestantId = "mock-matcher",
  [string]$RunId        = "phase5-run-1",
  [int]   $Bots         = 50,
  [int]   $Ops          = 200,
  [int]   $Duration     = 60,
  [string]$Symbol       = "AAPL"
)

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\\..\\..")
$jobFile  = Join-Path $repoRoot "deploy\\phase5\\k8s\\base\\bot-fleet-job.yaml"

# ---------------------------------------------------------------------------
# 1. Delete any previous bot-fleet Job (Jobs are immutable once created).
# ---------------------------------------------------------------------------
Write-Host "Cleaning up any previous bot-fleet Job..."
kubectl delete job bot-fleet -n iicpc-runner --ignore-not-found

# ---------------------------------------------------------------------------
# 2. Apply the Job manifest with patched args via kubectl patch-style inline.
#    We use a kustomize strategic-merge patch approach: write a temp patch,
#    apply it, then clean up.
# ---------------------------------------------------------------------------
$patch = @"
apiVersion: batch/v1
kind: Job
metadata:
  name: bot-fleet
  namespace: iicpc-runner
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 600
  template:
    metadata:
      labels:
        app: bot-fleet
    spec:
      restartPolicy: Never
      containers:
        - name: bot-fleet
          image: iicpc/bot-fleet:latest
          imagePullPolicy: IfNotPresent
          args:
            - --target
            - mock-matcher.iicpc-runner.svc.cluster.local:50051
            - --bots
            - "$Bots"
            - --ops
            - "$Ops"
            - --duration
            - "$Duration"
            - --run-id
            - "$RunId"
            - --contestant-id
            - "$ContestantId"
            - --symbol
            - "$Symbol"
            - --kafka
            - redpanda.iicpc-telemetry.svc.cluster.local:9092
            - --kafka-topic
            - metrics
          resources:
            requests:
              cpu: 200m
              memory: 128Mi
            limits:
              cpu: "2"
              memory: 512Mi
"@

$tmpFile = [System.IO.Path]::GetTempFileName() + ".yaml"
$patch | Set-Content -Path $tmpFile -Encoding UTF8

Write-Host "Launching bot-fleet Job (contestant=$ContestantId, run=$RunId, bots=$Bots, ops=$Ops, duration=${Duration}s)..."
kubectl apply -f $tmpFile
Remove-Item $tmpFile

# ---------------------------------------------------------------------------
# 3. Stream logs until the Job completes.
# ---------------------------------------------------------------------------
Write-Host "Waiting for bot-fleet pod to start..."
kubectl wait --for=condition=Ready pod -l app=bot-fleet -n iicpc-runner --timeout=60s

Write-Host "Streaming logs (Ctrl-C to detach; Job will keep running)..."
kubectl logs -f -l app=bot-fleet -n iicpc-runner

# ---------------------------------------------------------------------------
# 4. Report final Job status.
# ---------------------------------------------------------------------------
$status = kubectl get job bot-fleet -n iicpc-runner -o jsonpath="{.status.conditions[0].type}" 2>$null
Write-Host ""
if ($status -eq "Complete") {
  Write-Host "Bot-fleet Job completed successfully."
} else {
  Write-Host "Bot-fleet Job status: $status"
  Write-Host "Check with: kubectl describe job bot-fleet -n iicpc-runner"
}
Write-Host "Leaderboard: http://localhost:30090"
