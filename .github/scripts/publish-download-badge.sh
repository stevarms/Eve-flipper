#!/usr/bin/env bash
set -euo pipefail

BADGE_BRANCH="${BADGE_BRANCH:-badges}"
REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
ROOT_DIR="$(pwd)"
OUT_DIR="${RUNNER_TEMP:-${ROOT_DIR}}/eve-flipper-badge-output"
WORKTREE_DIR="${RUNNER_TEMP:-${ROOT_DIR}}/eve-flipper-badge-branch"
PREVIOUS_JSON="${OUT_DIR}/previous-downloads.json"

rm -rf "${OUT_DIR}" "${WORKTREE_DIR}"
mkdir -p "${OUT_DIR}"

git fetch origin "${BADGE_BRANCH}" --depth=1 || true
if git show "origin/${BADGE_BRANCH}:downloads.json" > "${PREVIOUS_JSON}" 2>/dev/null; then
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

git worktree add --detach "${WORKTREE_DIR}"
pushd "${WORKTREE_DIR}" >/dev/null

if git rev-parse --verify --quiet "origin/${BADGE_BRANCH}" >/dev/null; then
  git checkout -B "${BADGE_BRANCH}" "origin/${BADGE_BRANCH}"
else
  git checkout --orphan "${BADGE_BRANCH}"
  git rm -rf . >/dev/null 2>&1 || true
fi

cp "${OUT_DIR}/downloads.svg" downloads.svg
cp "${OUT_DIR}/release.svg" release.svg
cp "${OUT_DIR}/downloads.json" downloads.json
cat > README.md <<'BADGE_README'
# EVE Flipper Badges

This branch is maintained by GitHub Actions.

- `release.svg` is generated from the latest published GitHub Release tag.
- `downloads.svg` is generated from GitHub Releases asset `download_count` values.
- `downloads.json` preserves the last known per-asset counts so totals do not drop when assets are replaced.
BADGE_README

git add release.svg downloads.svg downloads.json README.md
if git diff --cached --quiet; then
  echo "Download badge is already up to date."
else
  git commit -m "Update download badge"
  git push origin "${BADGE_BRANCH}"
fi

popd >/dev/null
git worktree remove "${WORKTREE_DIR}" --force
