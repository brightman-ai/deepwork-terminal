#!/bin/bash
# run.sh — Build (if needed) and run deepwork-terminal
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

ADDR="${DW_TERMINAL_ADDR:-:18074}"
AUTH_CODE="${DW_TERMINAL_AUTH_CODE:-}"
EXTRA_ARGS=()

while [ "$#" -gt 0 ]; do
    case "$1" in
        --addr|-addr)
            if [ "$#" -lt 2 ]; then
                echo "error: $1 requires a value" >&2
                exit 2
            fi
            ADDR="$2"
            shift 2
            ;;
        --addr=*|-addr=*)
            ADDR="${1#*=}"
            shift
            ;;
        --auth-code|-auth-code)
            if [ "$#" -lt 2 ]; then
                echo "error: $1 requires a value" >&2
                exit 2
            fi
            AUTH_CODE="$2"
            shift 2
            ;;
        --auth-code=*|-auth-code=*)
            AUTH_CODE="${1#*=}"
            shift
            ;;
        --)
            shift
            EXTRA_ARGS+=("$@")
            break
            ;;
        *)
            if [ -z "$AUTH_CODE" ] && [[ "$1" != -* ]]; then
                AUTH_CODE="$1"
            else
                EXTRA_ARGS+=("$1")
            fi
            shift
            ;;
    esac
done

# Build if the binary is missing, frontend is missing/stale, or Go sources changed.
NEEDS_GO_BUILD=0
NEEDS_FRONTEND_BUILD=0

if [ ! -f dw-terminal ]; then
    NEEDS_GO_BUILD=1
fi

if [ ! -d internal/spa/dist ]; then
    NEEDS_FRONTEND_BUILD=1
    NEEDS_GO_BUILD=1
elif [ -f dw-terminal ] && [ "$(find frontend/src -newer dw-terminal -print -quit 2>/dev/null)" ]; then
    NEEDS_FRONTEND_BUILD=1
    NEEDS_GO_BUILD=1
fi

if [ -f dw-terminal ] && [ "$(find . -path ./frontend -prune -o -path ./internal/spa/dist -prune -o -name '*.go' -newer dw-terminal -print -quit 2>/dev/null)" ]; then
    NEEDS_GO_BUILD=1
fi

if [ "$NEEDS_FRONTEND_BUILD" -eq 1 ]; then
    ./build.sh
elif [ "$NEEDS_GO_BUILD" -eq 1 ]; then
    ./build.sh --skip-frontend
fi

echo "=== Starting dw-terminal ==="
CMD=(./dw-terminal -addr "$ADDR")
if [ -n "$AUTH_CODE" ]; then
    CMD+=(-auth-code "$AUTH_CODE")
fi
CMD+=("${EXTRA_ARGS[@]}")

echo "Addr: $ADDR"
if [ -n "$AUTH_CODE" ]; then
    echo "Auth Code: $AUTH_CODE"
fi
exec "${CMD[@]}"
