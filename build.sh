#!/bin/bash
# build.sh — Build deepwork-terminal (frontend + Go binary)
#
# Usage:
#   ./build.sh                  # full build (frontend + Go)
#   ./build.sh --skip-frontend  # Go binary only (uses pre-built dist)
#
# The pre-built frontend is committed in internal/spa/dist/.
# You only need Node.js if you modify the frontend source.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

SKIP_FRONTEND=0
for arg in "$@"; do
  case "$arg" in
    --skip-frontend) SKIP_FRONTEND=1 ;;
  esac
done

if [ "$SKIP_FRONTEND" -eq 0 ]; then
  echo "=== Building frontend ==="
  cd frontend

  if [ ! -d node_modules ]; then
    # Skip browser downloads from Playwright/Puppeteer postinstall hooks
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    PUPPETEER_SKIP_DOWNLOAD=1 \
    npm install
  fi

  VITE_PORTALS=cli,settings npm run build
  cd ..

  # Copy built frontend to Go embed location
  rm -rf internal/spa/dist
  cp -r frontend/dist internal/spa/dist
  echo "=== Frontend built and copied to internal/spa/dist ==="
else
  echo "=== Skipping frontend build (using pre-built internal/spa/dist) ==="
fi

echo "=== Building Go binary ==="
go build -o dw-terminal ./cmd/dw-terminal/

echo "=== Done: ./dw-terminal ==="
