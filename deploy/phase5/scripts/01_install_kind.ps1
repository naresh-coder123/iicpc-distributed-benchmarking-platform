$ErrorActionPreference = "Stop"

Write-Host "Installing kind via go install..."
go install sigs.k8s.io/kind@latest

$gopath = (go env GOPATH).Trim()
$kindPath = Join-Path $gopath "bin\\kind.exe"

if (!(Test-Path $kindPath)) {
  throw "kind.exe not found at $kindPath"
}

Write-Host "Installed: $kindPath"
Write-Host "If 'kind' is not found in new terminals, add GOPATH\\bin to PATH:"
Write-Host "  $gopath\\bin"

