#!/usr/bin/env bash
# Builds the two eval binaries into bin/:
#   dtctl-recipes  — from the current worktree HEAD (the recipes feature)
#   dtctl-main     — from EVAL_BASELINE_REF (default: merge-base with upstream/main)
set -euo pipefail
cd "$(dirname "$0")"
EVALDIR=$PWD
REPO=$(git rev-parse --show-toplevel)
mkdir -p bin

echo "building dtctl-recipes from HEAD ($(git -C "$REPO" rev-parse --short HEAD))"
(cd "$REPO" && go build -o "$EVALDIR/bin/dtctl-recipes" .)

REF=${EVAL_BASELINE_REF:-}
if [ -z "$REF" ]; then
    REF=$(git -C "$REPO" merge-base HEAD upstream/main 2>/dev/null \
        || git -C "$REPO" merge-base HEAD origin/main)
fi
echo "building dtctl-main from $REF ($(git -C "$REPO" rev-parse --short "$REF"))"
WT=$(mktemp -d)
git -C "$REPO" worktree add --detach "$WT" "$REF" >/dev/null
trap 'git -C "$REPO" worktree remove --force "$WT" >/dev/null 2>&1 || true' EXIT
(cd "$WT" && go build -o "$EVALDIR/bin/dtctl-main" .)

{
    echo "built:          $(date -Is)"
    echo "dtctl-recipes:  $(git -C "$REPO" rev-parse HEAD)"
    echo "dtctl-main:     $(git -C "$REPO" rev-parse "$REF")"
} > bin/BUILDINFO
cat bin/BUILDINFO
