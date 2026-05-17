#!/bin/bash
# run.sh — Build (if needed) and run deepwork-terminal
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Build if binary doesn't exist or frontend not built
if [ ! -f dw-terminal ] || [ ! -d internal/spa/dist ] || [ "$(find frontend/src -newer dw-terminal -print -quit 2>/dev/null)" ]; then
    ./build.sh
fi

echo "=== Starting dw-terminal ==="
exec ./dw-terminal "$@"
