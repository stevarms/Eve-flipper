#!/usr/bin/env node

import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";

const API_VERSION = "2022-11-28";
const DEFAULT_LABEL = "downloads";

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (!arg.startsWith("--")) {
      throw new Error(`Unexpected argument: ${arg}`);
    }
    const key = arg.slice(2);
    const value = argv[i + 1];
    if (!value || value.startsWith("--")) {
      throw new Error(`Missing value for --${key}`);
    }
    args[key] = value;
    i += 1;
  }
  return args;
}

function escapeXML(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function textWidth(value) {
  return Math.ceil(String(value).length * 6.2 + 12);
}

function formatCount(value) {
  return String(Math.max(0, Number(value) || 0));
}

function renderBadge({ label, value, color = "#4c1" }) {
  const labelText = String(label || DEFAULT_LABEL);
  const valueText = String(value);
  const labelWidth = Math.max(66, textWidth(labelText));
  const valueWidth = Math.max(38, textWidth(valueText));
  const width = labelWidth + valueWidth;
  const title = `${labelText}: ${valueText}`;

  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="20" role="img" aria-label="${escapeXML(title)}">
  <title>${escapeXML(title)}</title>
  <linearGradient id="s" x2="0" y2="100%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r">
    <rect width="${width}" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#r)">
    <rect width="${labelWidth}" height="20" fill="#555"/>
    <rect x="${labelWidth}" width="${valueWidth}" height="20" fill="${escapeXML(color)}"/>
    <rect width="${width}" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110">
    <text aria-hidden="true" x="${labelWidth * 5}" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="${(labelWidth - 12) * 10}">${escapeXML(labelText)}</text>
    <text x="${labelWidth * 5}" y="140" transform="scale(.1)" fill="#fff" textLength="${(labelWidth - 12) * 10}">${escapeXML(labelText)}</text>
    <text aria-hidden="true" x="${(labelWidth + valueWidth / 2) * 10}" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="${(valueWidth - 12) * 10}">${escapeXML(valueText)}</text>
    <text x="${(labelWidth + valueWidth / 2) * 10}" y="140" transform="scale(.1)" fill="#fff" textLength="${(valueWidth - 12) * 10}">${escapeXML(valueText)}</text>
  </g>
</svg>
`;
}

async function fetchJSON(url, token) {
  const headers = {
    Accept: "application/vnd.github+json",
    "X-GitHub-Api-Version": API_VERSION,
    "User-Agent": "eve-flipper-download-badge",
  };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  const response = await fetch(url, { headers });
  if (!response.ok) {
    const body = await response.text();
    throw new Error(`GitHub API ${response.status} ${response.statusText}: ${body.slice(0, 400)}`);
  }
  return response.json();
}

async function fetchReleases(repo, token) {
  const releases = [];
  for (let page = 1; page <= 100; page += 1) {
    const url = `https://api.github.com/repos/${repo}/releases?per_page=100&page=${page}`;
    const batch = await fetchJSON(url, token);
    releases.push(...batch);
    if (batch.length < 100) break;
  }
  return releases;
}

async function readPrevious(file) {
  if (!file) return null;
  try {
    return JSON.parse((await readFile(file, "utf8")).replace(/^\uFEFF/, ""));
  } catch (err) {
    if (err.code === "ENOENT") return null;
    throw err;
  }
}

function previousAssetMap(previous) {
  const map = new Map();
  if (!previous || !Array.isArray(previous.assets)) {
    return map;
  }
  for (const asset of previous.assets) {
    if (asset && asset.id != null) {
      map.set(String(asset.id), asset);
    }
  }
  return map;
}

function buildSnapshot({ repo, releases, previous }) {
  const previousAssets = previousAssetMap(previous);
  const seen = new Set();
  const assets = [];
  const generatedAt = new Date().toISOString();

  for (const release of releases) {
    if (release.draft) continue;
    for (const asset of release.assets || []) {
      const id = String(asset.id);
      seen.add(id);
      const old = previousAssets.get(id);
      const downloadCount = Math.max(Number(asset.download_count) || 0, Number(old?.download_count) || 0);
      const githubDownloadCount = Number(asset.download_count) || 0;
      const oldUnchanged =
        old &&
        old.state === "active" &&
        old.name === asset.name &&
        old.tag_name === release.tag_name &&
        old.release_id === String(release.id) &&
        Number(old.download_count) === downloadCount &&
        Number(old.github_download_count) === githubDownloadCount &&
        old.browser_download_url === asset.browser_download_url &&
        old.created_at === asset.created_at &&
        old.updated_at === asset.updated_at;
      assets.push({
        id,
        name: asset.name,
        tag_name: release.tag_name,
        release_id: String(release.id),
        state: "active",
        download_count: downloadCount,
        github_download_count: githubDownloadCount,
        browser_download_url: asset.browser_download_url,
        created_at: asset.created_at,
        updated_at: asset.updated_at,
        first_seen_at: old?.first_seen_at || generatedAt,
        last_seen_at: oldUnchanged ? old.last_seen_at : generatedAt,
      });
    }
  }

  for (const [id, old] of previousAssets) {
    if (seen.has(id)) continue;
    assets.push({
      ...old,
      id,
      state: "archived",
      last_seen_at: old.last_seen_at || old.generated_at || generatedAt,
    });
  }

  assets.sort((a, b) => {
    const tag = String(b.tag_name || "").localeCompare(String(a.tag_name || ""));
    if (tag !== 0) return tag;
    return String(a.name || "").localeCompare(String(b.name || ""));
  });

  const totalDownloads = assets.reduce((sum, asset) => sum + (Number(asset.download_count) || 0), 0);
  const currentGithubTotal = assets
    .filter((asset) => asset.state === "active")
    .reduce((sum, asset) => sum + (Number(asset.github_download_count) || 0), 0);
  const latestRelease = latestReleaseTag(releases);
  const activeAssetCount = assets.filter((asset) => asset.state === "active").length;
  const archivedAssetCount = assets.filter((asset) => asset.state === "archived").length;
  const generatedAtForSnapshot = isMateriallySameSnapshot(previous, {
    repo,
    latestRelease,
    totalDownloads,
    currentGithubTotal,
    activeAssetCount,
    archivedAssetCount,
    assets,
  })
    ? previous.generated_at
    : generatedAt;

  return {
    schema_version: 1,
    repo,
    generated_at: generatedAtForSnapshot,
    latest_release: latestRelease,
    total_downloads: totalDownloads,
    current_github_total: currentGithubTotal,
    active_asset_count: activeAssetCount,
    archived_asset_count: archivedAssetCount,
    assets,
  };
}

function isMateriallySameSnapshot(previous, next) {
  if (!previous) return false;
  if (
    previous.repo !== next.repo ||
    previous.latest_release !== next.latestRelease ||
    Number(previous.total_downloads) !== next.totalDownloads ||
    Number(previous.current_github_total) !== next.currentGithubTotal ||
    Number(previous.active_asset_count) !== next.activeAssetCount ||
    Number(previous.archived_asset_count) !== next.archivedAssetCount
  ) {
    return false;
  }
  const oldAssets = Array.isArray(previous.assets) ? previous.assets : [];
  if (oldAssets.length !== next.assets.length) return false;
  return oldAssets.every((oldAsset, index) => {
    const asset = next.assets[index];
    return (
      String(oldAsset.id) === String(asset.id) &&
      oldAsset.name === asset.name &&
      oldAsset.tag_name === asset.tag_name &&
      oldAsset.release_id === asset.release_id &&
      oldAsset.state === asset.state &&
      Number(oldAsset.download_count) === Number(asset.download_count) &&
      Number(oldAsset.github_download_count) === Number(asset.github_download_count) &&
      oldAsset.browser_download_url === asset.browser_download_url &&
      oldAsset.created_at === asset.created_at &&
      oldAsset.updated_at === asset.updated_at &&
      oldAsset.first_seen_at === asset.first_seen_at &&
      oldAsset.last_seen_at === asset.last_seen_at
    );
  });
}

function latestReleaseTag(releases) {
  const published = releases
    .filter((release) => !release.draft && !release.prerelease && release.tag_name)
    .sort((a, b) => String(b.published_at || "").localeCompare(String(a.published_at || "")));
  return published[0]?.tag_name || "none";
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const repo = args.repo || process.env.GITHUB_REPOSITORY;
  const output = args.output;
  const releaseOutput = args["release-output"];
  const jsonOutput = args.json;
  const label = args.label || DEFAULT_LABEL;
  const token = process.env.GITHUB_TOKEN || process.env.GH_TOKEN || "";

  if (!repo || !/^[^/]+\/[^/]+$/.test(repo)) {
    throw new Error("Pass --repo owner/name or set GITHUB_REPOSITORY.");
  }
  if (!output) {
    throw new Error("Pass --output path/to/downloads.svg.");
  }
  if (!jsonOutput) {
    throw new Error("Pass --json path/to/downloads.json.");
  }

  const [releases, previous] = await Promise.all([
    fetchReleases(repo, token),
    readPrevious(args.previous),
  ]);
  const snapshot = buildSnapshot({ repo, releases, previous });
  const downloadBadge = renderBadge({ label, value: formatCount(snapshot.total_downloads) });

  await mkdir(path.dirname(output), { recursive: true });
  await mkdir(path.dirname(jsonOutput), { recursive: true });
  await writeFile(output, downloadBadge, "utf8");
  if (releaseOutput) {
    await mkdir(path.dirname(releaseOutput), { recursive: true });
    await writeFile(
      releaseOutput,
      renderBadge({ label: "release", value: snapshot.latest_release, color: "#007ec6" }),
      "utf8",
    );
  }
  await writeFile(jsonOutput, `${JSON.stringify(snapshot, null, 2)}\n`, "utf8");

  console.log(`Generated ${output} with ${snapshot.total_downloads} downloads from ${snapshot.active_asset_count} active assets.`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
