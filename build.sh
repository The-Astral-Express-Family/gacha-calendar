#!/usr/bin/env bash
# Build the single deployable binary: frontend (Vite) → embed → Go.
set -euo pipefail
cd "$(dirname "$0")"

pnpm --dir frontend build
rm -rf backend/web/assets/dist
cp -r frontend/dist backend/web/assets/dist

cd backend
go build -ldflags "-X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
  -o gacha-calendar ./cmd/gacha-calendar
echo "built backend/gacha-calendar"
