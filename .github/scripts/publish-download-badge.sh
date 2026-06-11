#!/usr/bin/env bash
set -euo pipefail

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
TARGET_BRANCH="${BADGE_TARGET_BRANCH:-master}"
OUT_DIR="assets/badges"
PREVIOUS_JSON="${OUT_DIR}/downloads.json"

mkdir -p "${OUT_DIR}"

if [ -f "${PREVIOUS_JSON}" ]; then
  PREVIOUS_ARGS=(--previous "${PREVIOUS_JSON}")
else
  PREVIOUS_ARGS=()
fi

NODE_ARGS=(
  --repo "${REPO}"
  --output "${OUT_DIR}/downloads.svg"
  --release-output "${OUT_DIR}/release.svg"
  --clones-output "${OUT_DIR}/clones.svg"
  --json "${OUT_DIR}/downloads.json"
)
node .github/scripts/generate-download-badge.mjs "${NODE_ARGS[@]}" "${PREVIOUS_ARGS[@]}"

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"

git add "${OUT_DIR}/release.svg" "${OUT_DIR}/downloads.svg" "${OUT_DIR}/clones.svg" "${OUT_DIR}/downloads.json"
if git diff --cached --quiet; then
  echo "Release badges are already up to date."
else
  git commit -m "Update release badges"
  git push origin "HEAD:${TARGET_BRANCH}"
fi
