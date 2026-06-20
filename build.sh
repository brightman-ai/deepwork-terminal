#!/bin/bash
# build.sh — Build deepwork-terminal (frontend + Go binary)
#
# Usage:
#   ./build.sh                  # full build (frontend + Go)
#   ./build.sh --skip-frontend  # Go binary only (uses pre-built dist)
#   ./build.sh --update         # pull latest CE App Shell + deps, then full build
#
# The pre-built frontend is committed in internal/spa/dist/.
# You only need Node.js if you modify the frontend source.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

SKIP_FRONTEND=0
DO_UPDATE=0
for arg in "$@"; do
  case "$arg" in
    --skip-frontend) SKIP_FRONTEND=1 ;;
    --update)        DO_UPDATE=1 ;;
  esac
done

CE_SHELL="$SCRIPT_DIR/../deepwork"

# --update: pull latest CE App Shell, then npm install and go mod download will
# pick up any new dependencies automatically below.
if [ "$DO_UPDATE" -eq 1 ]; then
  if [ -d "$CE_SHELL/.git" ]; then
    echo "=== Updating CE App Shell ==="
    git -C "$CE_SHELL" pull --ff-only
  else
    echo "=== CE App Shell not found — cloning brightman-ai/deepwork ==="
    git clone --depth 1 https://github.com/brightman-ai/deepwork.git "$CE_SHELL"
  fi
fi

if [ "$SKIP_FRONTEND" -eq 0 ]; then
  echo "=== Building frontend ==="

  # Ensure CE App Shell (brightman-ai/deepwork) is present as a sibling directory.
  # The @ce Vite alias resolves to ../deepwork/frontend/src — required at build time.
  if [ ! -d "$CE_SHELL/frontend/src" ]; then
    echo "=== CE App Shell not found — cloning brightman-ai/deepwork ==="
    git clone --depth 1 https://github.com/brightman-ai/deepwork.git "$CE_SHELL"
  fi

  cd frontend

  # Always run npm install so new dependencies added to package.json are picked up.
  # npm is idempotent: it skips packages already at the correct version, so this is
  # fast on repeat runs. Skip browser downloads from Playwright/Puppeteer hooks.
  PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
  PUPPETEER_SKIP_DOWNLOAD=1 \
  npm install

  # CE shell has no own node_modules — it resolves packages through ours.
  if [ ! -e "$CE_SHELL/frontend/node_modules" ]; then
    ln -s "$(pwd)/node_modules" "$CE_SHELL/frontend/node_modules"
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
# Allow caller to override GOPROXY; default to goproxy.cn which works in China
# and falls back to direct. Machines with direct access to proxy.golang.org can
# override: GOPROXY=https://proxy.golang.org,direct ./build.sh
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" go mod download
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" go build -o dw-terminal ./cmd/dw-terminal/

echo "=== Done: ./dw-terminal ==="
