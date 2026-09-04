#!/bin/bash
# build.sh — Build deepwork-terminal (frontend + Go binary)
#
# Usage:
#   ./build.sh                  # full build: pull latest CE App Shell + deps + compile
#   ./build.sh --skip-frontend  # Go binary only (uses pre-built dist)
#
# The pre-built frontend is committed in internal/spa/dist/.
# You only need Node.js if you modify the frontend source.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Recorded before anything is built, so the freshness check below compares against the
# start of THIS run rather than against a wall-clock guess.
BUILD_STARTED="$(date +%s)"

SKIP_FRONTEND=0
for arg in "$@"; do
  case "$arg" in
    --skip-frontend) SKIP_FRONTEND=1 ;;
  esac
done

CE_SHELL="$SCRIPT_DIR/../deepwork"

if [ "$SKIP_FRONTEND" -eq 0 ]; then
  echo "=== Building frontend ==="

  # Ensure CE App Shell (brightman-ai/deepwork) is present and up to date.
  # The @ce Vite alias resolves to ../deepwork/frontend/src — required at build time.
  if [ -d "$CE_SHELL/.git" ]; then
    echo "=== Updating CE App Shell ==="
    git -C "$CE_SHELL" pull --ff-only
  else
    echo "=== CE App Shell not found — cloning brightman-ai/deepwork ==="
    git clone --depth 1 https://github.com/brightman-ai/deepwork.git "$CE_SHELL"
  fi

  cd frontend

  # Pick a package runner.
  #
  # npm is the documented one, but it is not always present — a machine can have node
  # supplied by bun (`node` is then a symlink to it) and no npm anywhere on PATH. That is
  # not hypothetical: it is this project's own dev box. Before this, such a machine got
  # "npm: command not found" from `set -e`, which is an honest failure but a useless one,
  # since a perfectly good toolchain was sitting right there.
  # One selection, both operations bound to it. Deciding "which runner" separately at the
  # install step and again at the build step is how a tree gets installed by one and built by
  # the other.
  if command -v npm >/dev/null 2>&1; then
    RUNNER=npm
    run_install() {
      # npm is idempotent: it skips packages already at the correct version, so this is fast
      # on repeat runs, and it picks up anything newly added to package.json.
      PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 PUPPETEER_SKIP_DOWNLOAD=1 npm install
    }
    run_build() { VITE_PORTALS=cli,settings npm run build; }
  elif command -v bun >/dev/null 2>&1; then
    RUNNER=bun
    run_install() {
      # Only reached when node_modules is absent — see below.
      PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 PUPPETEER_SKIP_DOWNLOAD=1 bun install
    }
    # Invoke vite directly rather than `bun run build`: one less interpreter between the env
    # var and the tool that reads it.
    run_build() { VITE_PORTALS=cli,settings ./node_modules/.bin/vite build; }
  else
    echo "error: need npm or bun on PATH to build the frontend." >&2
    echo "       (install Node.js, or run ./build.sh --skip-frontend to use the" >&2
    echo "        pre-built internal/spa/dist)" >&2
    exit 1
  fi
  echo "=== Frontend runner: $RUNNER ==="

  # Install dependencies. Skip browser downloads from Playwright/Puppeteer hooks.
  #
  # Under bun this is deliberately conditional: bun resolves and writes its own lockfile,
  # so running it over a tree npm produced would rewrite state the documented path owns.
  # An existing node_modules is left exactly as it is — bun is used to RUN the build, not
  # to take over dependency management.
  if [ "$RUNNER" = npm ] || [ ! -d node_modules ]; then
    run_install
  else
    echo "=== node_modules present — leaving it to npm's lockfile (bun only runs the build) ==="
  fi

  # CE shell has no own node_modules — it resolves packages through ours.
  if [ ! -e "$CE_SHELL/frontend/node_modules" ]; then
    ln -s "$(pwd)/node_modules" "$CE_SHELL/frontend/node_modules"
  fi

  # VITE_PORTALS is load-bearing, not cosmetic: a bare build defaults VITE_EDITION to pro
  # and puts the full pro nav on the standalone :18074 UI.
  run_build
  cd ..

  # Prove the frontend actually rebuilt — BEFORE copying.
  #
  # `set -e` catches a command that fails; it cannot catch one that succeeds without
  # producing anything, and the embedded dist is exactly where that goes unnoticed: the Go
  # binary compiles fine and serves yesterday's UI, so the log says "built" and the running
  # app disagrees.
  #
  # The check must look at frontend/dist, which vite writes, and it must run before the
  # copy. Checking internal/spa/dist afterwards proves nothing: `cp` stamps its own output
  # with the current time, so a stale tree copies into a directory that then looks freshly
  # built. (Written the wrong way round first; caught by asking what would still be green
  # if vite were replaced by `true`.)
  if [ ! -f frontend/dist/index.html ]; then
    echo "error: frontend build produced no frontend/dist/index.html" >&2
    exit 1
  fi
  if [ -z "$(find frontend/dist/index.html -newermt "@$BUILD_STARTED" 2>/dev/null)" ]; then
    echo "error: frontend/dist/index.html predates this build — the frontend did not" >&2
    echo "       actually rebuild, and copying it would embed a stale UI." >&2
    exit 1
  fi

  # Copy built frontend to Go embed location
  rm -rf internal/spa/dist
  cp -r frontend/dist internal/spa/dist
  echo "=== Frontend built and copied to internal/spa/dist ==="
else
  echo "=== Skipping frontend build (using pre-built internal/spa/dist) ==="
fi

echo "=== Building Go binary ==="
# Stamp the version from git so a source build's UI badge / --version shows where it sits
# relative to the last release (e.g. "v0.5.0" on a tag, "v0.5.0-3-g1a2b3c4" three commits
# past it). Falls back to "dev" outside a git checkout (main.go then derives dev-<hash>).
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
# Allow caller to override GOPROXY; default to goproxy.cn which works in China
# and falls back to direct. Machines with direct access to proxy.golang.org can
# override: GOPROXY=https://proxy.golang.org,direct ./build.sh
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" go mod download
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}" go build -ldflags "-X main.version=${VERSION}" -o dw-terminal ./cmd/dw-terminal/

echo "=== Done: ./dw-terminal ==="
