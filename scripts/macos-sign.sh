#!/bin/bash
# macos-sign.sh — sign + notarize the macOS binary of a published release.
#
# WHY this exists: CI runs on Linux and cannot codesign Apple binaries. So the
# release is published unsigned, and you run THIS on a Mac that holds your
# "Developer ID Application" certificate to sign + notarize + re-upload it.
#
# Handles every Apple-specific requirement:
#   1. Cert must be "Developer ID Application" (NOT Apple Distribution / App Store).
#   2. codesign with --options runtime (Hardened Runtime) + --timestamp, else
#      notarytool rejects it.
#   3. A bare CLI binary CANNOT be stapled — notarization is recorded against the
#      binary hash and checked online on first run. We submit a .zip to notarize.
#   4. We ship a macOS *universal* binary (one artifact), so we sign once.
#   5. Must run on macOS (codesign/xcrun are Apple-only).
#
# Usage:
#   scripts/macos-sign.sh <tag> [--upload] [--update-tap]
#   scripts/macos-sign.sh v0.3.0 --upload --update-tap
#
# Required env (notarization credentials — pick ONE):
#   NOTARY_PROFILE=dw-notary        # a stored `xcrun notarytool store-credentials` profile
#   -- or --
#   APPLE_ID=you@example.com APPLE_PASSWORD=app-specific-pw APPLE_TEAM_ID=ABCDE12345
#
# Optional env:
#   DEVELOPER_ID="Developer ID Application: Name (TEAMID)"  # auto-detected if unset
set -euo pipefail

REPO="brightman-ai/deepwork-terminal"
BINARY="dw-terminal"
TAP_REPO="brightman-ai/homebrew-tap"

die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
info() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }

[ "$(uname -s)" = "Darwin" ] || die "must run on macOS"
command -v gh >/dev/null || die "needs the GitHub CLI (gh) — brew install gh"
command -v xcrun >/dev/null || die "needs Xcode command line tools — xcode-select --install"

TAG="${1:-}"; [ -n "$TAG" ] || die "usage: $0 <tag> [--upload] [--update-tap]"
case "$TAG" in v*) ;; *) TAG="v$TAG" ;; esac
shift || true
DO_UPLOAD=0; DO_TAP=0
for a in "$@"; do
  case "$a" in
    --upload) DO_UPLOAD=1 ;;
    --update-tap) DO_TAP=1 ;;
    *) die "unknown arg: $a" ;;
  esac
done
VER="${TAG#v}"
ASSET="${BINARY}_${VER}_darwin_all.tar.gz"

# --- locate signing identity -------------------------------------------------
if [ -z "${DEVELOPER_ID:-}" ]; then
  DEVELOPER_ID="$(security find-identity -v -p codesigning \
    | grep -o 'Developer ID Application: [^"]*' | head -1)" \
    || die "no 'Developer ID Application' identity found in keychain"
fi
[ -n "$DEVELOPER_ID" ] || die "could not determine Developer ID; set DEVELOPER_ID env"
info "Signing identity: $DEVELOPER_ID"

work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
cd "$work"

# --- fetch the published (unsigned) universal tarball ------------------------
info "Downloading $ASSET from release $TAG ..."
gh release download "$TAG" --repo "$REPO" --pattern "$ASSET" --dir "$work" \
  || die "could not download $ASSET — is the release published?"
tar -xzf "$ASSET"
[ -f "$BINARY" ] || die "archive did not contain $BINARY"

# --- sign (requirement 1, 2) -------------------------------------------------
info "Signing (Hardened Runtime + secure timestamp) ..."
codesign --force --timestamp --options runtime --sign "$DEVELOPER_ID" "$BINARY"
codesign --verify --strict --verbose=2 "$BINARY"

# --- notarize (requirement 3: submit a zip, bare binary can't be stapled) ----
info "Submitting to Apple notary service (this can take a few minutes) ..."
/usr/bin/ditto -c -k --keepParent "$BINARY" "$BINARY.zip"
if [ -n "${NOTARY_PROFILE:-}" ]; then
  xcrun notarytool submit "$BINARY.zip" --keychain-profile "$NOTARY_PROFILE" --wait
else
  [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] \
    || die "set NOTARY_PROFILE, or APPLE_ID + APPLE_PASSWORD + APPLE_TEAM_ID"
  xcrun notarytool submit "$BINARY.zip" \
    --apple-id "$APPLE_ID" --password "$APPLE_PASSWORD" --team-id "$APPLE_TEAM_ID" --wait
fi
info "Notarization accepted. (Bare CLI binary: no staple; Gatekeeper verifies online.)"

# --- repackage with the signed binary (match goreleaser archive contents) ----
gh release download "$TAG" --repo "$REPO" --pattern "LICENSE" --dir "$work" 2>/dev/null || true
[ -f LICENSE ] || tar -xzf "$ASSET" LICENSE 2>/dev/null || true
[ -f README.md ] || tar -xzf "$ASSET" README.md 2>/dev/null || true
tar -czf "$ASSET.signed" "$BINARY" $( [ -f LICENSE ] && echo LICENSE ) $( [ -f README.md ] && echo README.md )
mv "$ASSET.signed" "$ASSET"
NEWSHA="$(shasum -a 256 "$ASSET" | awk '{print $1}')"
info "Signed tarball: $work/$ASSET"
info "New sha256: $NEWSHA"

# --- upload back to the release (clobber) ------------------------------------
if [ "$DO_UPLOAD" -eq 1 ]; then
  info "Uploading signed $ASSET to release $TAG (clobber) ..."
  gh release upload "$TAG" "$ASSET" --repo "$REPO" --clobber
  # keep checksums.txt consistent
  if gh release download "$TAG" --repo "$REPO" --pattern checksums.txt --dir "$work" 2>/dev/null; then
    sed -i '' -E "s/^[0-9a-f]+([[:space:]]+${ASSET//./\\.})$/${NEWSHA}\1/" checksums.txt || true
    gh release upload "$TAG" checksums.txt --repo "$REPO" --clobber
    info "Updated checksums.txt"
  fi
else
  info "Dry run — re-run with --upload to replace the release asset."
fi

# --- bump the Homebrew formula sha (the tarball changed) ---------------------
if [ "$DO_TAP" -eq 1 ]; then
  info "Updating $TAP_REPO formula sha256 ..."
  tapdir="$work/tap"
  gh repo clone "$TAP_REPO" "$tapdir" -- --depth 1 || die "could not clone $TAP_REPO"
  formula="$tapdir/Formula/$BINARY.rb"
  [ -f "$formula" ] || formula="$tapdir/$BINARY.rb"
  [ -f "$formula" ] || die "formula not found in tap repo"
  # Replace the sha256 that follows the darwin_all url line.
  awk -v sha="$NEWSHA" '
    /_darwin_all\.tar\.gz/ { print; getline; sub(/sha256 "[0-9a-f]+"/, "sha256 \"" sha "\""); print; next }
    { print }' "$formula" > "$formula.tmp" && mv "$formula.tmp" "$formula"
  git -C "$tapdir" add -A
  git -C "$tapdir" commit -m "dw-terminal: notarized macOS build for $TAG"
  git -C "$tapdir" push
  info "Tap updated."
fi

info "Done."
