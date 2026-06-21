#!/bin/sh
# install.sh — one-line installer for deepwork-terminal (dw-terminal).
#
#   curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh
#
# Default: downloads a prebuilt binary for your OS/arch (no Go or Node needed).
# Linux (amd64/arm64) and macOS (universal) are supported; WSL uses the Linux build.
#
# Environment / flags:
#   DW_VERSION=v0.3.0        pin a version (default: latest release)
#   DW_INSTALL_DIR=/path     install dir   (default: ~/.local/bin)
#   --dir=/path              same as DW_INSTALL_DIR
#   --version=v0.3.0         same as DW_VERSION
#   --from-source            build with `go install` instead of downloading a binary
#   --install-go             with --from-source: install the latest stable Go if missing
#   -h, --help               show this help
set -eu

REPO="brightman-ai/deepwork-terminal"
BINARY="dw-terminal"
INSTALL_DIR="${DW_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${DW_VERSION:-latest}"
FROM_SOURCE=0
INSTALL_GO=0

# ---- tiny helpers -----------------------------------------------------------
info() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# Print the leading comment block (everything after the shebang up to the first
# non-comment line) as help text.
usage() { awk 'NR>1 && /^#/ {sub(/^# ?/,""); print; next} NR>1 {exit}' "$0"; }

download() { # download <url> <out-file>
  if   have curl; then curl -fsSL "$1" -o "$2"
  elif have wget; then wget -qO "$2" "$1"
  else err "need curl or wget to download"; fi
}
fetch() { # fetch <url> -> stdout
  if   have curl; then curl -fsSL "$1"
  elif have wget; then wget -qO- "$1"
  else err "need curl or wget to download"; fi
}

# ---- args -------------------------------------------------------------------
for arg in "$@"; do
  case "$arg" in
    --from-source) FROM_SOURCE=1 ;;
    --install-go)  INSTALL_GO=1 ;;
    --dir=*)       INSTALL_DIR="${arg#*=}" ;;
    --version=*)   VERSION="${arg#*=}" ;;
    -h|--help)     usage; exit 0 ;;
    *)             err "unknown argument: $arg (try --help)" ;;
  esac
done

# ---- detect platform --------------------------------------------------------
os="$(uname -s)"; arch="$(uname -m)"
case "$os" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *)      err "unsupported OS: $os — on Windows use WSL, or build with --from-source" ;;
esac
case "$arch" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *)             err "unsupported architecture: $arch" ;;
esac

# ---- resolve version --------------------------------------------------------
if [ "$VERSION" = latest ]; then
  TAG="$(fetch "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' | head -1 \
        | sed -E 's/.*"tag_name":[[:space:]]*"([^"]+)".*/\1/')"
  [ -n "$TAG" ] || err "could not resolve the latest release (rate-limited? set DW_VERSION)"
else
  case "$VERSION" in v*) TAG="$VERSION" ;; *) TAG="v$VERSION" ;; esac
fi
VER="${TAG#v}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# ---- build from source (opt-in) ---------------------------------------------
bootstrap_go() {
  GOVER="$(fetch 'https://go.dev/VERSION?m=text' | head -1)"
  [ -n "$GOVER" ] || err "could not resolve the latest Go version"
  gotar="${GOVER}.${OS}-${ARCH}.tar.gz"
  info "Installing $GOVER to \$HOME/.local/go (latest stable Go) ..."
  download "https://go.dev/dl/$gotar" "$tmp/$gotar"
  rm -rf "$HOME/.local/go"; mkdir -p "$HOME/.local"
  tar -xzf "$tmp/$gotar" -C "$HOME/.local"      # extracts ./go
  export PATH="$HOME/.local/go/bin:$PATH"
  warn "Add Go to your PATH permanently: export PATH=\"\$HOME/.local/go/bin:\$PATH\""
}

if [ "$FROM_SOURCE" -eq 1 ]; then
  if ! have go; then
    if [ "$INSTALL_GO" -eq 1 ]; then
      bootstrap_go
    else
      err "Go is not installed. Re-run with --install-go to install the latest stable Go, or drop --from-source to download a prebuilt binary (no Go needed)."
    fi
  fi
  ref="$TAG"; [ "$VERSION" = latest ] && ref="latest"
  info "Building $BINARY from source (go install @$ref) ..."
  mkdir -p "$INSTALL_DIR"
  GOBIN="$INSTALL_DIR" go install "github.com/$REPO/cmd/$BINARY@$ref"
else
  # ---- download prebuilt binary ---------------------------------------------
  # macOS ships a single universal (amd64+arm64) archive.
  asset_arch="$ARCH"; [ "$OS" = darwin ] && asset_arch=all
  ASSET="${BINARY}_${VER}_${OS}_${asset_arch}.tar.gz"
  URL="https://github.com/$REPO/releases/download/$TAG/$ASSET"

  info "Downloading $ASSET ($TAG) ..."
  download "$URL" "$tmp/$ASSET" || err "download failed: $URL"
  tar -xzf "$tmp/$ASSET" -C "$tmp" || err "extract failed (corrupt archive?)"
  [ -f "$tmp/$BINARY" ] || err "archive did not contain $BINARY"

  mkdir -p "$INSTALL_DIR"
  cp "$tmp/$BINARY" "$INSTALL_DIR/$BINARY"
  chmod 0755 "$INSTALL_DIR/$BINARY"

  # macOS: clear the quarantine flag so Gatekeeper won't block the binary.
  # Harmless when the binary is already signed + notarized.
  if [ "$OS" = darwin ] && have xattr; then
    xattr -d com.apple.quarantine "$INSTALL_DIR/$BINARY" 2>/dev/null || true
  fi
fi

# ---- verify + PATH hint -----------------------------------------------------
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) warn "$INSTALL_DIR is not on your PATH. Add this to your shell rc:"
     warn "    export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

if "$INSTALL_DIR/$BINARY" --version >/dev/null 2>&1; then
  info "Installed: $("$INSTALL_DIR/$BINARY" --version)"
else
  warn "Installed to $INSTALL_DIR/$BINARY but could not run it — check your platform."
fi
info "Run '$BINARY' to start. Done."
