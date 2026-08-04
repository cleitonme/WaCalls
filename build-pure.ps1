# Build and push the pure-Go Docker image from Windows PowerShell.
# Usage: .\build-pure.ps1 [-Push] [-Platform "linux/amd64"]

param(
  [switch]$Push,
  [string]$Platform = "linux/amd64"
)

$ErrorActionPreference = "Stop"

$ImageName = "whazing/wacalls:latest"
$BuilderName = "whazing-builder"
$PushArg = if ($Push) { "--push" } else { "" }

Write-Host "Building PURE-GO variant (CGO_ENABLED=0, no native encoder)..." -ForegroundColor Cyan

$builderExists = docker buildx inspect $BuilderName 2>$null
if ($LASTEXITCODE -ne 0) {
  Write-Host "Creating builder '$BuilderName'..."
  docker buildx create --name $BuilderName --driver docker-container --use
  docker buildx inspect --bootstrap
} else {
  docker buildx use $BuilderName
}

$args = @("buildx", "build", "--platform", $Platform, "--build-arg", "VARIANT=pure", "-t", $ImageName)
if ($Push) { $args += "--push" }
$args += "."

docker @args
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

Write-Host "Done: $ImageName (pure-go)" -ForegroundColor Green
