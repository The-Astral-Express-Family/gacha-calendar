# Build the single deployable binary: frontend (Vite) → embed → Go.
$ErrorActionPreference = 'Stop'
Set-Location "$PSScriptRoot"

pnpm --dir frontend build
if (Test-Path backend/web/assets/dist) { Remove-Item -Recurse -Force backend/web/assets/dist }
Copy-Item -Recurse frontend/dist backend/web/assets/dist

$version = (git describe --tags --always 2>$null)
if (-not $version) { $version = 'dev' }
Set-Location backend
go build -ldflags "-X main.version=$version" -o gacha-calendar.exe ./cmd/gacha-calendar
Write-Host 'built backend/gacha-calendar.exe'
