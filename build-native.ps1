# Build and push the native CGO Docker image from Windows PowerShell.
# Requires the nativemlow build tag; libopus-dev is installed in the builder stage.
# Usage: .\build-native.ps1 [-Push] [-Platform "linux/amd64"]

param(
  [switch]$Push,
  [string]$Platform = "linux/amd64"
)

$ErrorActionPreference = "Stop"

$ImageName = "whazing/wacalls:native"
$BuilderName = "whazing-builder"

Write-Host "Building NATIVE variant (CGO_ENABLED=1, libopus SMPL encoder)..." -ForegroundColor Cyan

$builderExists = docker buildx inspect $BuilderName 2>$null
if ($LASTEXITCODE -ne 0) {
  Write-Host "Creating builder '$BuilderName'..."
  docker buildx create --name $BuilderName --driver docker-container --use
  docker buildx inspect --bootstrap
} else {
  docker buildx use $BuilderName
}

$args = @("buildx", "build", "--platform", $Platform, "--build-arg", "VARIANT=native", "-t", $ImageName)
if ($Push) { $args += "--push" }
$args += "."

docker @args
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

Write-Host "Done: $ImageName (native)" -ForegroundColor Green
