// Shared logic for committing a batch of scanner rows to an industry project.
// Extracted from the (deleted) AddBlueprintsToProjectModal so the scanner
// panel can drive the same flow inline via a sticky footer instead of a modal.

import {
  analyzeIndustry,
  createAuthIndustryProject,
  getAuthIndustryCoverage,
  planAuthIndustryProject,
} from "@/lib/api";
import type {
  IndustryAnalysis,
  IndustryCoverageMaterialNeed,
  IndustryCoverageMaterialRow,
  IndustryCoverageBlueprintNeed,
  IndustryCoverageResult,
  IndustryParams,
  IndustryPlanPatch,
  ProfitableScanRow,
} from "@/lib/types";
import {
  applyCoverageToIndustryPlanPatch,
  buildIndustryPlanPatch,
  mergeIndustryPlanPatches,
} from "@/lib/industryPlanPatch";
import {
  effectiveInventionParams,
  type DecryptorKey,
} from "@/lib/industryDecryptors";

export interface ScannerAnalysisContext {
  systemName: string;
  stationID: number;
  facilityTax: number;
  structureBonus: number;
  brokerFee: number;
  salesTaxPercent: number;
  decryptorKey: DecryptorKey;
  decryptorCost: number;
  /** Global build-vs-buy override for the analyze pass on each row. */
  buildMode: "auto" | "buy_all" | "build_all";
}

export type RowCommitState = "pending" | "analyzing" | "done" | "error";

export interface RowCommitStatus {
  state: RowCommitState;
  errorMsg?: string;
}

export interface CommitBatchArgs {
  rows: ProfitableScanRow[];
  /** Runs per row, keyed by the scanner's row-key string (parallel structure
   *  to how selection is tracked in the panel). Missing keys fall back to
   *  `defaultRunsPerJob`. */
  runsByKey: Map<string, number>;
  /** Stable per-row key function — must match what the scanner uses so we can
   *  correlate row → per-row status updates back to the caller. */
  rowKeyFor: (row: ProfitableScanRow) => string;
  defaultRunsPerJob: number;
  context: ScannerAnalysisContext;
  mode: "new" | "existing";
  /** Required when mode === "new". */
  projectName: string;
  strategy: "conservative" | "balanced" | "aggressive";
  /** Required when mode === "existing". */
  existingProjectID: number;
  /** Streamed progress messages ("Analyzing 3/8 Vespa I…", "Fetching
   *  coverage…", "Committing plan…"). Presenter decides where to render. */
  onProgress?: (msg: string) => void;
  /** Per-row status stream. Presenter uses the key to look the row up in
   *  its own state and re-render the row's status pill. */
  onRowStatus?: (rowKey: string, status: RowCommitStatus) => void;
  signal?: AbortSignal;
}

export interface CommitBatchResult {
  projectID: number;
  count: number;
  summary: {
    tasks_inserted: number;
    jobs_inserted: number;
    blueprints_upserted: number;
  } | null;
  dedupedCount: number;
  coverageWarnings: string[];
}

/**
 * Suggests a per-row runs count from the row's 30-day market volume. Captures
 * `marketSharePct` of one day of aggressive-buy demand, then converts units
 * to runs via the BP's output-per-run.
 *
 * Rounding matches the natural granularity of T2 BPCs (10 runs):
 *   raw < 10   → round to nearest whole number
 *   raw ≥ 10   → round to nearest 10
 *
 * Availability cap: for owned BPCs, capped at the row's available_runs so we
 * never suggest more than the user's actual stock can cover.
 *
 * Falls back to `fallbackRuns` when the row has no volume history.
 */
export function defaultRunsForRow(
  row: ProfitableScanRow,
  marketSharePct: number,
  fallbackRuns: number,
): number {
  const daily = row.product_daily_volume ?? 0;
  const outputQty = row.output_qty_per_run && row.output_qty_per_run > 0 ? row.output_qty_per_run : 1;
  const isInvention = row.scan_mode === "t2_invention" || row.scan_mode === "t3_invention";
  const isOwnedBPC = (row.owned ?? true) && !row.is_bpo && !isInvention;

  let suggestion: number;
  if (daily <= 0) {
    suggestion = Math.max(1, fallbackRuns);
  } else {
    const shareFraction = Math.max(0.001, marketSharePct / 100);
    const unitsTarget = daily * shareFraction;
    const rawRuns = unitsTarget / outputQty;
    suggestion = rawRuns < 10 ? Math.max(1, Math.round(rawRuns)) : Math.max(10, Math.round(rawRuns / 10) * 10);
  }

  if (isOwnedBPC && row.available_runs > 0 && suggestion > row.available_runs) {
    suggestion = row.available_runs;
  }
  return Math.max(1, suggestion);
}

/** Build the analyzer request body from a scanner row + shared context. */
export function buildParamsForRow(
  row: ProfitableScanRow,
  ctx: ScannerAnalysisContext,
  runsPerJob: number,
): IndustryParams {
  const isT2 = row.scan_mode === "t2_invention";
  const inv = effectiveInventionParams(ctx.decryptorKey, row.output_bpc_runs);
  return {
    type_id: row.product_type_id,
    runs: runsPerJob,
    activity_mode: isT2 ? "invention" : "manufacturing",
    me: isT2 ? inv.meBase : row.me,
    te: isT2 ? inv.teBase : row.te,
    system_name: ctx.systemName,
    station_id: ctx.stationID || 0,
    facility_tax: ctx.facilityTax,
    structure_bonus: ctx.structureBonus,
    broker_fee: ctx.brokerFee,
    sales_tax_percent: ctx.salesTaxPercent,
    own_blueprint: true,
    blueprint_is_bpo: row.is_bpo,
    build_mode: ctx.buildMode,
    ...(isT2
      ? {
          invention_chance: (row.invention_probability ?? 0) * 100,
          invention_output_runs: inv.outputRuns,
          decryptor_cost: ctx.decryptorCost,
        }
      : {}),
  };
}

/**
 * The read-only half of the batch flow — analyze every selected row and get
 * one coverage snapshot spanning the union of their materials + sub-BPs.
 * Shared between the pre-commit materials preview in Discover (reactive
 * side-effect-free) and commitBatchToProject (which follows up with patch
 * merge + apply).
 */
export interface PreviewBatchArgs {
  rows: ProfitableScanRow[];
  runsByKey: Map<string, number>;
  rowKeyFor: (row: ProfitableScanRow) => string;
  defaultRunsPerJob: number;
  context: ScannerAnalysisContext;
  onProgress?: (msg: string) => void;
  onRowStatus?: (rowKey: string, status: RowCommitStatus) => void;
  signal?: AbortSignal;
}

export interface BatchPreview {
  /** Per-row analysis outputs in the order they succeeded — parallel to
   *  `analysesKeys`. Fed to commit's merge step verbatim. */
  analyses: { row: ProfitableScanRow; analysis: IndustryAnalysis; runs: number; rowKey: string }[];
  /** How many rows the dedupe step collapsed. Non-zero when the user
   *  selected e.g. both a BPO and a BPC of the same source blueprint. */
  dedupedCount: number;
  /** Full coverage response — merged materials + blueprints + summary.
   *  Null when the coverage endpoint threw (network / auth / role check);
   *  the preview panel falls back to raw material requirements in that case. */
  coverage: IndustryCoverageResult | null;
  /** Merged flat material requirements across every analysis, coverage
   *  overlay applied when available. Convenient for the preview UI. */
  materials: IndustryCoverageMaterialRow[];
  /** Only the materials with a positive missing_qty — the "delta" or
   *  "shortfall" list, for the preview's Shortfall tab. */
  shortfall: IndustryCoverageMaterialRow[];
  /** Coverage warnings surfaced to the caller for toast display
   *  ("re-authenticate for corp scope", "role check failed", etc.). */
  coverageWarnings: string[];
}

export async function previewBatch(args: PreviewBatchArgs): Promise<BatchPreview> {
  const {
    rows,
    runsByKey,
    rowKeyFor,
    defaultRunsPerJob,
    context,
    onProgress,
    onRowStatus,
    signal,
  } = args;

  if (rows.length === 0) {
    return { analyses: [], dedupedCount: 0, coverage: null, materials: [], shortfall: [], coverageWarnings: [] };
  }

  // --- Dedupe by (scan_mode, product_type_id) ---
  // Same rationale as the original modal: a user who owns BOTH a BPO and a
  // BPC of the same source blueprint would otherwise submit two patches for
  // the same output product, doubling every material. Prefer BPO (unlimited
  // runs); fall back to higher available_runs. Losing rows get onRowStatus
  // marked "done" so the caller can visually collapse them.
  const uniqueRows: ProfitableScanRow[] = [];
  const uniqueKeys: string[] = [];
  let dedupedCount = 0;
  {
    const bestByKey = new Map<string, { row: ProfitableScanRow; index: number; rowKey: string }>();
    for (let i = 0; i < rows.length; i++) {
      const r = rows[i];
      const rk = rowKeyFor(r);
      const dedupeKey = `${r.scan_mode ?? "t1_mfg"}:${r.product_type_id}`;
      const existing = bestByKey.get(dedupeKey);
      if (!existing) {
        bestByKey.set(dedupeKey, { row: r, index: i, rowKey: rk });
        continue;
      }
      const rIsBpo = Boolean(r.is_bpo);
      const eIsBpo = Boolean(existing.row.is_bpo);
      const rRuns = r.available_runs ?? 0;
      const eRuns = existing.row.available_runs ?? 0;
      const rWins = rIsBpo && !eIsBpo
        ? true
        : !rIsBpo && eIsBpo
          ? false
          : rRuns > eRuns;
      if (rWins) {
        onRowStatus?.(existing.rowKey, { state: "done" });
        bestByKey.set(dedupeKey, { row: r, index: i, rowKey: rk });
      } else {
        onRowStatus?.(rk, { state: "done" });
      }
      dedupedCount++;
    }
    const ordered = Array.from(bestByKey.values()).sort((a, b) => a.index - b.index);
    for (const entry of ordered) {
      uniqueRows.push(entry.row);
      uniqueKeys.push(entry.rowKey);
    }
  }

  // --- Phase 1: analyze each unique row sequentially ---
  const analyses: BatchPreview["analyses"] = [];
  for (let u = 0; u < uniqueRows.length; u++) {
    if (signal?.aborted) break;
    const row = uniqueRows[u];
    const rk = uniqueKeys[u];
    onRowStatus?.(rk, { state: "analyzing" });
    onProgress?.(`Analyzing ${u + 1}/${uniqueRows.length} · ${row.product_name || `Type ${row.product_type_id}`}`);
    try {
      const perRow = runsByKey.get(rk) ?? defaultRunsPerJob;
      const params = buildParamsForRow(row, context, perRow);
      const analysis = await analyzeIndustry(params, () => {}, signal);
      analyses.push({ row, analysis, runs: perRow, rowKey: rk });
      onRowStatus?.(rk, { state: "done" });
    } catch (e: unknown) {
      if (signal?.aborted) break;
      const msg = e instanceof Error ? e.message : "analyze failed";
      onRowStatus?.(rk, { state: "error", errorMsg: msg });
    }
  }

  if (signal?.aborted) {
    return { analyses: [], dedupedCount, coverage: null, materials: [], shortfall: [], coverageWarnings: [] };
  }

  // --- Phase 2: one coverage call spanning every material + sub-BP ---
  onProgress?.("Fetching coverage…");
  const materialsForCoverage = new Map<number, IndustryCoverageMaterialNeed>();
  const bpsForCoverage = new Map<number, IndustryCoverageBlueprintNeed>();
  for (const { analysis } of analyses) {
    for (const step of analysis.activity_plan ?? []) {
      if (!step.blueprint_type_id || step.blueprint_type_id <= 0) continue;
      const requiredRuns = Math.max(
        1,
        Math.ceil(step.activity === "invention" && step.expected_attempts
          ? step.expected_attempts
          : step.runs || 1),
      );
      const existing = bpsForCoverage.get(step.blueprint_type_id);
      bpsForCoverage.set(step.blueprint_type_id, {
        blueprint_type_id: step.blueprint_type_id,
        blueprint_name: step.blueprint_name || existing?.blueprint_name || "",
        activity:
          existing?.activity && existing.activity !== step.activity
            ? "mixed"
            : step.activity || existing?.activity || "manufacturing",
        required_runs: (existing?.required_runs ?? 0) + requiredRuns,
      });
    }
    for (const m of analysis.flat_materials ?? []) {
      const existing = materialsForCoverage.get(m.type_id);
      materialsForCoverage.set(m.type_id, {
        type_id: m.type_id,
        type_name: m.type_name || existing?.type_name || "",
        required_qty: (existing?.required_qty ?? 0) + Math.max(0, Math.ceil(m.quantity ?? 0)),
      });
    }
  }

  let coverage: IndustryCoverageResult | null = null;
  try {
    const coverageResp = await getAuthIndustryCoverage({
      scope: "all",
      materials: Array.from(materialsForCoverage.values()),
      blueprints: Array.from(bpsForCoverage.values()),
      include_corp_assets: true,
      include_corp_blueprints: true,
    });
    coverage = coverageResp.coverage;
  } catch {
    coverage = null;
  }

  // Prefer the coverage-enriched material rows when we got them; fall back
  // to bare requirements if coverage failed (still show what's needed, just
  // no have/need split).
  const materials: IndustryCoverageMaterialRow[] = coverage?.materials?.length
    ? coverage.materials
    : Array.from(materialsForCoverage.values()).map((m) => ({
        type_id: m.type_id,
        type_name: m.type_name ?? "",
        required_qty: m.required_qty,
        available_qty: 0,
        missing_qty: m.required_qty,
        coverage_pct: 0,
        status: "missing",
      }));
  const shortfall = materials.filter((m) => (m.missing_qty ?? 0) > 0);

  return {
    analyses,
    dedupedCount,
    coverage,
    materials,
    shortfall,
    coverageWarnings: coverage?.warnings ?? [],
  };
}

/**
 * Analyze every selected row → dedupe by output product → build per-row plan
 * patches → merge them → optionally attach coverage → create or select the
 * target project → apply the merged patch.
 *
 * Emits row-level status through onRowStatus so the caller can turn the flag
 * pill or a status column into "analyzing / done / error" per row.
 *
 * Throws on network / server errors so the caller can surface them; otherwise
 * resolves with the summary. Aborted runs resolve with count === 0.
 */
export async function commitBatchToProject(args: CommitBatchArgs): Promise<CommitBatchResult> {
  const {
    rows,
    runsByKey,
    rowKeyFor,
    defaultRunsPerJob,
    context,
    mode,
    projectName,
    strategy,
    existingProjectID,
    onProgress,
    onRowStatus,
    signal,
  } = args;

  if (rows.length === 0) {
    throw new Error("No rows selected");
  }

  // Phases 1-2 (dedupe + analyze + coverage) are shared with the reactive
  // preview in the scanner panel — see previewBatch above.
  const preview = await previewBatch({
    rows,
    runsByKey,
    rowKeyFor,
    defaultRunsPerJob,
    context,
    onProgress,
    onRowStatus,
    signal,
  });

  if (signal?.aborted) {
    return { projectID: 0, count: 0, summary: null, dedupedCount: preview.dedupedCount, coverageWarnings: [] };
  }
  if (preview.analyses.length === 0) {
    throw new Error("All rows failed to analyze; nothing was committed.");
  }

  const { analyses, coverage, dedupedCount, coverageWarnings } = preview;

  // Phase 3: per-row patch build then merge, coverage overlay once.
  onProgress?.("Merging plans…");
  const patches: IndustryPlanPatch[] = analyses.map(({ row, analysis, runs }) => {
    const isT2 = row.scan_mode === "t2_invention";
    const inv = effectiveInventionParams(context.decryptorKey, row.output_bpc_runs);
    return buildIndustryPlanPatch({
      result: analysis,
      productTypeID: row.product_type_id,
      productName: row.product_name,
      runs,
      me: isT2 ? inv.meBase : row.me,
      te: isT2 ? inv.teBase : row.te,
      systemName: context.systemName,
      stationID: context.stationID || 0,
      ownBlueprint: true,
      replace: false,
    });
  });
  const merged = applyCoverageToIndustryPlanPatch(mergeIndustryPlanPatches(patches), coverage);

  // Phase 4: create or select project, apply patch.
  onProgress?.("Committing plan…");
  let projectID = existingProjectID;
  if (mode === "new") {
    const trimmed = projectName.trim();
    if (!trimmed) throw new Error("Project name required");
    const created = await createAuthIndustryProject({ name: trimmed, strategy });
    projectID = Number(created.project?.id ?? 0);
    if (projectID <= 0) throw new Error("Project create returned no id");
  }
  if (projectID <= 0) {
    throw new Error("Choose an existing project");
  }
  const resp = await planAuthIndustryProject(projectID, merged);
  const summary = resp?.summary
    ? {
        tasks_inserted: resp.summary.tasks_inserted ?? 0,
        jobs_inserted: resp.summary.jobs_inserted ?? 0,
        blueprints_upserted: resp.summary.blueprints_upserted ?? 0,
      }
    : null;

  return {
    projectID,
    count: analyses.length,
    summary,
    dedupedCount,
    coverageWarnings,
  };
}
