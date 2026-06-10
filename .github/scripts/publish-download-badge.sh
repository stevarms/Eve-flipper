#!/usr/bin/env bash
set -euo pipefail

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
OUT_DIR="assets/badges"
PREVIOUS_JSON="${OUT_DIR}/downloads.json"

mkdir -p "${OUT_DIR}"

if [ -f "${PREVIOUS_JSON}" ]; then
  PREVIOUS_ARGS=(--previous "${PREVIOUS_JSON}")
else
  PREVIOUS_ARGS=()
fi

node .github/scripts/generate-download-badge.mjs \
  --repo "${REPO}" \
  --output "${OUT_DIR}/downloads.svg" \
  --release-output "${OUT_DIR}/release.svg" \
  --json "${OUT_DIR}/downloads.json" \
  "${PREVIOUS_ARGS[@]}"

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"

git add "${OUT_DIR}/release.svg" "${OUT_DIR}/downloads.svg" "${OUT_DIR}/downloads.json"
if git diff --cached --quiet; then
  echo "Release badges are already up to date."
else
  git commit -m "Update release badges"
  git push
fi
