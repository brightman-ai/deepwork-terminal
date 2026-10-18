#!/usr/bin/env bash
# Rebase this checkout onto origin/main while treating internal/spa/dist as a
# generated artifact. Hash-named Vite files otherwise turn every rebase into a
# rename/rename conflict even though build.sh can regenerate them.
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
remote_name="${1:-origin}"
branch_name="${2:-main}"
cd "$repo_dir"

git fetch "$remote_name" "$branch_name"
target="$remote_name/$branch_name"

if ! git diff --quiet -- frontend internal handlers.go session_viewport.go session_screen.go reconnect_pack.go; then
  echo "error: tracked worktree changes exist; commit or stash them before syncing" >&2
  exit 2
fi

set +e
GIT_EDITOR=true git rebase "$target"
rebase_status=$?
set -e

if [ "$rebase_status" -eq 0 ]; then
  exit 0
fi

# A generated dist conflict has no source decision to make. Remove all unmerged
# dist entries from the index, finish the source rebase, then rebuild the dist
# from the rebased frontend. This also handles rename/rename pairs where
# `git checkout --ours/--theirs` cannot name a single side consistently.
if ! git diff --name-only --diff-filter=U | grep -q '^internal/spa/dist/'; then
  echo "rebase stopped on a non-generated conflict; resolve it manually" >&2
  exit "$rebase_status"
fi

git rm -r --ignore-unmatch internal/spa/dist >/dev/null
GIT_EDITOR=true git rebase --continue

./build.sh
git add internal/spa/dist
git commit -m "build(spa): regenerate embedded frontend after rebase"
echo "rebased onto $target and regenerated internal/spa/dist"
