// Shared logic for committing a batch of scanner rows to an industry project.
// Extracted from the (deleted) AddBlueprintsToProjectModal so the scanner
// panel can drive the same flow inline via a sticky footer instead of a modal.

import {
  analyzeIndustry,
  createAuthIndustryProject,
  getAuthIndustryCoverage,
  getAuthIndustryOwnedBlueprints,
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
  applyJobSplitToIndustryPlanPatch,
  buildIndustryPlanPatch,
  mergeIndustryPlanPatches,
  type JobSplitLimits,
} from "@/lib/industryPlanPatch";
import {
  DECRYPTORS,
  effectiveInventionParams,
  type DecryptorKey,
} from "@/lib/industryDecryptors";
import { formatISK } from "@/lib/format";

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
  /** Recursion depth for the material tree. MUST be set — the backend clamps
   *  a missing/zero value to 1, which collapses the whole sub-tree and makes
   *  build_all emit only the root step (no component fan-out at all). The
   *  Analyze tab hardcodes 10; the scanner should match. */
  maxDepth: number;
  /** Treat reaction-produced materials as buy-only. Mirrors the Analyze
   *  tab's sharedPrefs.skipReactions — without it the scanner silently
   *  disagrees with Analyze on every T2 chain containing reactions. */
  skipReactions: boolean;
  /** Structure rig / bonus context — affects ME/TE and job cost per node. */
  structureRigTypeIDs: number[];
  structureTypeID: number;
  structureJobCostReduction: number;
  /** Revenue / cost model selection, same values the Analyze tab sends. */
  revenueModel: IndustryParams["revenue_model"];
  costModel: IndustryParams["cost_model"];
  /** Optional per-product owned-BP index, threaded through to the analyzer
   *  so sub-tree recursion uses each product's own ME/TE (instead of
   *  cascading the top-level) AND so copy-step detection can fire when
   *  invention needs BPCs the user hasn't yet made. Same shape the
   *  Analysis tab already sends; scanner commit path fetches once per
   *  batch via getAuthIndustryOwnedBlueprints. */
  ownedBlueprints?: Array<{
    product_type_id: number;
    me: number;
    te: number;
    is_bpo?: boolean;
    available_runs?: number;
  }>;
  /** Split committed jobs so no single install exceeds these limits. Omit
   *  (or set both to 0) to emit one job per task regardless of length. */
  jobSplit?: JobSplitLimits;
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
 * How much ISK of build cost the batch may tie up, and how long you are
 * willing to still be selling it. These two ceilings are what separate a
 * bulk drone batch from a large-ticket module batch without hardcoding
 * anything about drones or modules.
 */
export interface RunsSuggestionPrefs {
  /** Batch sizes you actually like to run, tried largest first. Multiples
   *  of ten divide evenly across ten manufacturing / invention slots. */
  tiers: number[];
  /** Share of the product's daily aggressive-buy volume you expect to win. */
  marketSharePct: number;
  /** Longest you are willing to still be shifting one batch. */
  maxFillDays: number;
  /** Most ISK of build cost to have tied up in one batch. */
  maxBatchCapitalISK: number;
}

export const DEFAULT_RUNS_PREFS: RunsSuggestionPrefs = {
  tiers: [400, 200, 100],
  marketSharePct: 10,
  maxFillDays: 90,
  maxBatchCapitalISK: 3_000_000_000,
};

/**
 * Owned stock within this multiple of the target gets consumed whole rather
 * than leaving a stub behind. 1.25 = "if finishing the copies costs me less
 * than a quarter batch extra, finish them".
 */
const OWNED_STOCK_ABSORB = 1.25;

export interface RunsSuggestion {
  runs: number;
  /** Line-per-constraint derivation, rendered into the Planned cell tooltip
   *  so a surprising number is auditable instead of just wrong-looking. */
  reason: string;
}

/**
 * Suggests a per-row batch size.
 *
 * Three constraints, applied in order:
 *
 *   1. Demand — units the market will take off you within `maxFillDays` at
 *      your share of the product's daily aggressive-buy volume. The veto:
 *      no building 100 smartbombs when three sell a day.
 *   2. Capital — ISK tied up in one batch. This is what pulls large-ticket
 *      items down to a small batch; they hit the capital wall long before
 *      the demand wall.
 *   3. Preference — the largest of `tiers` that clears both. Below the
 *      smallest tier we fall back to slot-friendly tens.
 *
 * Then the answer is snapped to whole blueprints. Inventing a ten-run BPC
 * to use one run of it is how a hangar fills up with part-used copies, so
 * blueprint granularity outranks the tier: 403 runs off 31 whole copies
 * beats 400 runs off 30 copies and a stub.
 */
export function suggestRunsForRow(
  row: ProfitableScanRow,
  prefs: RunsSuggestionPrefs = DEFAULT_RUNS_PREFS,
): RunsSuggestion {
  const outputQty = row.output_qty_per_run && row.output_qty_per_run > 0 ? row.output_qty_per_run : 1;
  const isInvention = row.scan_mode === "t2_invention" || row.scan_mode === "t3_invention";
  const isOwnedBPC = (row.owned ?? true) && !row.is_bpo && !isInvention;

  // Runs on one invented BPC, decryptor included. Owned stock only reports
  // a total across copies, so there is no per-copy step to respect there.
  const decryptorKey: DecryptorKey =
    row.best_decryptor_key && row.best_decryptor_key in DECRYPTORS
      ? (row.best_decryptor_key as DecryptorKey)
      : "none";
  const bpcStep = isInvention
    ? Math.max(1, effectiveInventionParams(decryptorKey, row.output_bpc_runs).outputRuns)
    : 1;

  const daily = row.product_daily_volume ?? 0;
  const share = Math.max(0.001, prefs.marketSharePct / 100);
  const costPerRun = row.runs > 0 && row.optimal_build_cost > 0 ? row.optimal_build_cost / row.runs : 0;
  const tiers = prefs.tiers.filter((t) => t > 0).sort((a, b) => b - a);
  const smallestTier = tiers.length > 0 ? tiers[tiers.length - 1] : 1;
  const lines: string[] = [];

  let target: number;
  if (daily <= 0) {
    // No 30d history. Guessing a batch size off nothing is worse than
    // planning one blueprint and letting the row's risk flag argue.
    target = bpcStep;
    lines.push("No 30d volume signal — planning one blueprint's worth");
  } else {
    const demandRuns = (daily * share * prefs.maxFillDays) / outputQty;
    lines.push(
      `Demand: ${Math.round(daily).toLocaleString()}/day × ${prefs.marketSharePct}% × ${prefs.maxFillDays}d = ` +
        `${Math.floor(demandRuns).toLocaleString()} runs`,
    );
    const capitalRuns = costPerRun > 0 ? prefs.maxBatchCapitalISK / costPerRun : Number.POSITIVE_INFINITY;
    if (Number.isFinite(capitalRuns)) {
      lines.push(
        `Capital: ${formatISK(prefs.maxBatchCapitalISK)} ÷ ${formatISK(costPerRun)}/run = ` +
          `${Math.floor(capitalRuns).toLocaleString()} runs`,
      );
    }
    const ceiling = Math.min(demandRuns, capitalRuns);
    const tier = tiers.find((t) => t <= ceiling);
    if (tier !== undefined) {
      target = tier;
      lines.push(`Batch: ${tier} — largest preferred size that fits`);
    } else {
      target = ceiling >= 10 ? Math.floor(ceiling / 10) * 10 : Math.max(1, Math.round(ceiling));
      lines.push(`Batch: ${target.toLocaleString()} — below the smallest preferred size (${smallestTier})`);
    }
  }

  if (bpcStep > 1) {
    const copies = Math.max(1, Math.round(target / bpcStep));
    target = copies * bpcStep;
    lines.push(`Blueprints: ${copies} × ${bpcStep}-run BPC = ${target.toLocaleString()} runs`);
  }

  if (isOwnedBPC && row.available_runs > 0) {
    if (row.available_runs <= target) {
      target = row.available_runs;
      lines.push(`Stock: capped at your ${row.available_runs.toLocaleString()} available runs`);
    } else if (row.available_runs <= target * OWNED_STOCK_ABSORB) {
      target = row.available_runs;
      lines.push(`Stock: rounded up to ${row.available_runs.toLocaleString()} to use the copies up`);
    }
  }

  const runs = Math.max(1, Math.round(target));
  if (daily > 0) {
    const units = runs * outputQty;
    const fillDays = units / (daily * share);
    lines.push(`≈ ${Math.round(fillDays).toLocaleString()}d to sell ${units.toLocaleString()} units at your share`);
  }

  return { runs, reason: lines.join("\n") };
}

/** Build the analyzer request body from a scanner row + shared context. */
export function buildParamsForRow(
  row: ProfitableScanRow,
  ctx: ScannerAnalysisContext,
  runsPerJob: number,
): IndustryParams {
  const isT2 = row.scan_mode === "t2_invention";
  // Scanner picks the highest-ISK/h decryptor PER ROW (row.best_decryptor_key)
  // — that's what the row's profit numbers assume. Honoring it here keeps the
  // committed plan aligned with what the row displayed (e.g. "[Accelerant]"
  // tag). Fall back to the shared-prefs decryptor for legacy rows without
  // best_decryptor_key set.
  const rowKey = row.best_decryptor_key as DecryptorKey | undefined;
  const decryptorKey: DecryptorKey =
    isT2 && rowKey && rowKey in DECRYPTORS ? rowKey : ctx.decryptorKey;
  const inv = effectiveInventionParams(decryptorKey, row.output_bpc_runs);
  // Cost: use the picked decryptor's default when it differs from the shared-
  // prefs pick (shared cost only tracks the shared pick). Users can override
  // per-row cost in a follow-up if it matters.
  const decryptorCost =
    decryptorKey === ctx.decryptorKey
      ? ctx.decryptorCost
      : DECRYPTORS[decryptorKey]?.defaultCost ?? 0;
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
    max_depth: ctx.maxDepth,
    skip_reactions: ctx.skipReactions,
    structure_rig_type_ids: ctx.structureRigTypeIDs,
    structure_type_id: ctx.structureTypeID,
    structure_job_cost_reduction: ctx.structureJobCostReduction,
    revenue_model: ctx.revenueModel,
    cost_model: ctx.costModel,
    ...(isT2
      ? {
          invention_chance: (row.invention_probability ?? 0) * 100,
          invention_output_runs: inv.outputRuns,
          decryptor_cost: decryptorCost,
          // Only send typeID when a real decryptor is picked; "none" (typeID 0)
          // would otherwise trigger the analyzer's decryptor-emit path with a
          // bogus typeID.
          ...(inv.decryptorTypeID > 0 ? { decryptor_type_id: inv.decryptorTypeID } : {}),
        }
      : {}),
    ...(ctx.ownedBlueprints && ctx.ownedBlueprints.length > 0
      ? { owned_blueprints: ctx.ownedBlueprints }
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
    context: rawContext,
    onProgress,
    onRowStatus,
    signal,
  } = args;

  if (rows.length === 0) {
    return { analyses: [], dedupedCount: 0, coverage: null, materials: [], shortfall: [], coverageWarnings: [] };
  }

  // Guarantee the analyzer sees owned_blueprints for invention pipelines —
  // without it, the copy-step detector (IsBPO && AvailableRuns==0) can't
  // fire and users see invention plans that skip the "print copies first"
  // step even when they only own a BPO. Prefer the caller-supplied list
  // (IndustryTab keeps a fetched-once cache), fetch fresh on the fly when
  // absent. Cheap enough — one ESI blueprints call per authenticated
  // character — and only actually hit when the caller didn't pre-load.
  const context = rawContext.ownedBlueprints && rawContext.ownedBlueprints.length > 0
    ? rawContext
    : await (async (): Promise<ScannerAnalysisContext> => {
        try {
          const resp = await getAuthIndustryOwnedBlueprints();
          return {
            ...rawContext,
            ownedBlueprints: resp.blueprints.map((b) => ({
              product_type_id: b.product_type_id,
              me: b.me,
              te: b.te,
              is_bpo: b.is_bpo,
              available_runs: b.available_runs,
            })),
          };
        } catch {
          // Non-fatal: analyzer falls back to legacy cascade for sub-BP ME/TE
          // and no copy step gets emitted. Same behavior as pre-owned-BP-aware
          // callers, so preview still works; the user just doesn't see copy
          // jobs even where they'd apply. Surface via console for diagnosis.
          // eslint-disable-next-line no-console
          console.warn("previewBatch: getAuthIndustryOwnedBlueprints failed, analyzer will lack owned-BP context");
          return rawContext;
        }
      })();

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
    // Step-specific materials (invention datacores, etc.) aren't in
    // flat_materials — fold them in so the coverage scan asks the ESI
    // asset endpoint for their inventory too. Without this, the invention
    // task shows "have 0" for datacores the user actually owns.
    for (const step of analysis.activity_plan ?? []) {
      for (const m of step.materials ?? []) {
        if (!m.type_id || m.type_id <= 0) continue;
        const qty = Math.max(0, Math.ceil(m.quantity ?? 0));
        if (qty <= 0) continue;
        const existing = materialsForCoverage.get(m.type_id);
        materialsForCoverage.set(m.type_id, {
          type_id: m.type_id,
          type_name: m.type_name || existing?.type_name || "",
          required_qty: (existing?.required_qty ?? 0) + qty,
        });
      }
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
  const mergedWithCoverage = applyCoverageToIndustryPlanPatch(mergeIndustryPlanPatches(patches), coverage);
  // Split last: merging first means two rows sharing a component produce one
  // combined job, which is then cut into install-sized batches once.
  const merged = context.jobSplit
    ? applyJobSplitToIndustryPlanPatch(mergedWithCoverage, context.jobSplit)
    : mergedWithCoverage;

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
