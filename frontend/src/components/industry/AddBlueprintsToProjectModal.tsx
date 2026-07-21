import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { Modal } from "../Modal";
import {
  analyzeIndustry,
  createAuthIndustryProject,
  getAuthIndustryCoverage,
  getAuthIndustryProjects,
  planAuthIndustryProject,
} from "@/lib/api";
import type {
  IndustryAnalysis,
  IndustryCoverageMaterialNeed,
  IndustryCoverageBlueprintNeed,
  IndustryParams,
  IndustryPlanPatch,
  IndustryProject,
  ProfitableScanRow,
} from "@/lib/types";
import {
  buildIndustryPlanPatch,
  mergeIndustryPlanPatches,
} from "@/lib/industryPlanPatch";
import {
  DECRYPTORS,
  effectiveInventionParams,
  type DecryptorKey,
} from "@/lib/industryDecryptors";

type Mode = "new" | "existing";

interface PlanApplySummaryLike {
  tasks_inserted: number;
  jobs_inserted: number;
  blueprints_upserted: number;
}

// ScannerAnalysisContext carries the shared analysis params from the Scanner
// panel state into the modal, so each row can be turned into an IndustryParams
// for analyzeIndustry(). The Scanner already knows all of these.
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

interface Props {
  open: boolean;
  onClose: () => void;
  rows: ProfitableScanRow[];
  runsPerJob: number;
  analysisContext: ScannerAnalysisContext;
  onSuccess: (projectID: number, count: number, summary: PlanApplySummaryLike | null) => void;
}

interface RowStatus {
  index: number;
  row: ProfitableScanRow;
  state: "pending" | "analyzing" | "done" | "error";
  errorMsg?: string;
  analysis?: IndustryAnalysis;
}

// Build the analyzer request body from a scanner row + shared context. Mirrors
// the mapping the Scanner panel's "View in Analysis" handoff performs.
function buildParamsForRow(row: ProfitableScanRow, ctx: ScannerAnalysisContext, runsPerJob: number): IndustryParams {
  const isT2 = row.scan_mode === "t2_invention";
  const inv = effectiveInventionParams(ctx.decryptorKey);
  return {
    type_id: row.product_type_id,
    runs: runsPerJob,
    activity_mode: isT2 ? "invention" : "manufacturing",
    // For T2 rows the ME/TE that drive the mfg step are the *invented T2 BPC's*,
    // adjusted for the decryptor. For T1 rows it's the owned BP's ME/TE.
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

export function AddBlueprintsToProjectModal({ open, onClose, rows, runsPerJob, analysisContext, onSuccess }: Props) {
  const { t } = useI18n();
  const [mode, setMode] = useState<Mode>("new");
  const [name, setName] = useState("");
  const [strategy, setStrategy] = useState<"conservative" | "balanced" | "aggressive">("balanced");
  const [projects, setProjects] = useState<IndustryProject[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(false);
  const [selectedProjectID, setSelectedProjectID] = useState<number>(0);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [progressMsg, setProgressMsg] = useState<string>("");
  const [rowStatuses, setRowStatuses] = useState<RowStatus[]>([]);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setName(`Scanner ${new Date().toISOString().slice(0, 10)}`);
    setMode("new");
    setProgressMsg("");
    setRowStatuses(rows.map((row, index) => ({ index, row, state: "pending" })));
  }, [open, rows]);

  useEffect(() => {
    if (!open || mode !== "existing") return;
    let cancelled = false;
    setProjectsLoading(true);
    (async () => {
      try {
        const resp = await getAuthIndustryProjects({ limit: 100 });
        if (cancelled) return;
        const eligible = resp.projects.filter(
          (p) => p.status === "draft" || p.status === "planned" || p.status === "active",
        );
        setProjects(eligible);
        if (eligible.length > 0 && selectedProjectID === 0) {
          setSelectedProjectID(eligible[0].id);
        }
      } catch (e: unknown) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : "Failed to load projects");
      } finally {
        if (!cancelled) setProjectsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, mode, selectedProjectID]);

  const decryptorLabel = useMemo(() => {
    if (rows.every((r) => r.scan_mode !== "t2_invention")) return "";
    return DECRYPTORS[analysisContext.decryptorKey]?.name ?? "None";
  }, [rows, analysisContext.decryptorKey]);

  const handleCancel = useCallback(() => {
    // Abort any in-flight analysis and dismiss the modal.
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    setSubmitting(false);
    onClose();
  }, [onClose]);

  const handleSubmit = useCallback(async () => {
    if (rows.length === 0) return;
    setSubmitting(true);
    setError(null);
    setProgressMsg("");

    const controller = new AbortController();
    abortRef.current = controller;

    // Phase 1: analyze each row sequentially. Emit progress. Track per-row
    // status so the user sees which rows failed.
    const statuses: RowStatus[] = rows.map((row, index) => ({ index, row, state: "pending" }));
    setRowStatuses(statuses);
    const analyses: { row: ProfitableScanRow; analysis: IndustryAnalysis }[] = [];

    for (let i = 0; i < rows.length; i++) {
      if (controller.signal.aborted) break;
      const row = rows[i];
      statuses[i] = { ...statuses[i], state: "analyzing" };
      setRowStatuses([...statuses]);
      setProgressMsg(
        t("industryScannerAnalyzingRow")
          .replace("{i}", String(i + 1))
          .replace("{n}", String(rows.length))
          .replace("{name}", row.product_name || `Type ${row.product_type_id}`),
      );
      try {
        const params = buildParamsForRow(row, analysisContext, runsPerJob);
        const analysis = await analyzeIndustry(params, () => {}, controller.signal);
        analyses.push({ row, analysis });
        statuses[i] = { ...statuses[i], state: "done", analysis };
      } catch (e: unknown) {
        if (controller.signal.aborted) break;
        const msg = e instanceof Error ? e.message : "analyze failed";
        statuses[i] = { ...statuses[i], state: "error", errorMsg: msg };
      }
      setRowStatuses([...statuses]);
    }

    if (controller.signal.aborted) {
      setSubmitting(false);
      abortRef.current = null;
      return;
    }

    if (analyses.length === 0) {
      setError(t("industryScannerAddToProjectAllFailed"));
      setSubmitting(false);
      abortRef.current = null;
      return;
    }

    // Phase 2: one coverage call spanning every material + sub-BP touched by
    // any analysis. Feed the same coverage into every patch build so shared
    // materials pick the same available_qty snapshot.
    setProgressMsg(t("industryScannerFetchingCoverage"));
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

    let coverage = null;
    try {
      const coverageResp = await getAuthIndustryCoverage({
        scope: "all",
        materials: Array.from(materialsForCoverage.values()),
        blueprints: Array.from(bpsForCoverage.values()),
      });
      coverage = coverageResp.coverage;
    } catch {
      // Coverage is a nice-to-have; the patch builder falls back to
      // required_qty = missing_qty when coverage is absent.
      coverage = null;
    }

    // Phase 3: per-row patch build, then merge.
    setProgressMsg(t("industryScannerMergingPlans"));
    const patches: IndustryPlanPatch[] = analyses.map(({ row, analysis }) => {
      const isT2 = row.scan_mode === "t2_invention";
      const inv = effectiveInventionParams(analysisContext.decryptorKey);
      return buildIndustryPlanPatch({
        result: analysis,
        coverage,
        productTypeID: row.product_type_id,
        productName: row.product_name,
        runs: runsPerJob,
        me: isT2 ? inv.meBase : row.me,
        te: isT2 ? inv.teBase : row.te,
        systemName: analysisContext.systemName,
        stationID: analysisContext.stationID || 0,
        ownBlueprint: true,
        replace: false,
      });
    });
    const merged = mergeIndustryPlanPatches(patches);

    // Phase 4: create project (if new) and submit merged patch.
    setProgressMsg(t("industryScannerCommittingPlan"));
    try {
      let projectID = selectedProjectID;
      if (mode === "new") {
        const trimmed = name.trim();
        if (!trimmed) {
          setError("Project name required");
          setSubmitting(false);
          abortRef.current = null;
          return;
        }
        const created = await createAuthIndustryProject({ name: trimmed, strategy });
        projectID = Number(created.project?.id ?? 0);
        if (projectID <= 0) {
          throw new Error("Project create returned no id");
        }
      }
      if (projectID <= 0) {
        setError("Choose an existing project");
        setSubmitting(false);
        abortRef.current = null;
        return;
      }
      const resp = await planAuthIndustryProject(projectID, merged);
      const summary = resp?.summary
        ? {
            tasks_inserted: resp.summary.tasks_inserted ?? 0,
            jobs_inserted: resp.summary.jobs_inserted ?? 0,
            blueprints_upserted: resp.summary.blueprints_upserted ?? 0,
          }
        : null;
      onSuccess(projectID, analyses.length, summary);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Add to project failed");
    } finally {
      setSubmitting(false);
      abortRef.current = null;
    }
  }, [rows, selectedProjectID, mode, name, strategy, runsPerJob, analysisContext, onSuccess, t]);

  return (
    <Modal
      open={open}
      onClose={submitting ? handleCancel : onClose}
      title={t("industryScannerAddToProjectModalTitle")}
      width="max-w-lg"
    >
      <div className="p-4 space-y-4 text-sm">
        <div className="text-xs text-eve-dim">
          {rows.length} blueprint(s) selected • {runsPerJob} runs per job
          {decryptorLabel && (
            <span className="ml-2 text-violet-300">• decryptor: {decryptorLabel}</span>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="addMode"
              checked={mode === "new"}
              disabled={submitting}
              onChange={() => setMode("new")}
            />
            <span>{t("industryScannerAddToProjectModeNew")}</span>
          </label>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="addMode"
              checked={mode === "existing"}
              disabled={submitting}
              onChange={() => setMode("existing")}
            />
            <span>{t("industryScannerAddToProjectModeExisting")}</span>
          </label>
        </div>

        {mode === "new" && (
          <div className="space-y-2">
            <div>
              <label className="block text-[11px] uppercase tracking-wider text-eve-dim mb-1">
                {t("industryScannerProjectName")}
              </label>
              <input
                type="text"
                value={name}
                disabled={submitting}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                           disabled:opacity-50"
              />
            </div>
            <div>
              <label className="block text-[11px] uppercase tracking-wider text-eve-dim mb-1">
                {t("industryScannerStrategy")}
              </label>
              <select
                value={strategy}
                disabled={submitting}
                onChange={(e) => setStrategy(e.target.value as typeof strategy)}
                className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                           disabled:opacity-50"
              >
                <option value="conservative">conservative</option>
                <option value="balanced">balanced</option>
                <option value="aggressive">aggressive</option>
              </select>
            </div>
          </div>
        )}

        {mode === "existing" && (
          <div>
            <label className="block text-[11px] uppercase tracking-wider text-eve-dim mb-1">
              {t("industryScannerExistingProject")}
            </label>
            {projectsLoading ? (
              <div className="text-xs text-eve-dim">Loading projects...</div>
            ) : projects.length === 0 ? (
              <div className="text-xs text-eve-dim">No eligible projects found.</div>
            ) : (
              <select
                value={selectedProjectID}
                disabled={submitting}
                onChange={(e) => setSelectedProjectID(Number(e.target.value))}
                className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                           disabled:opacity-50"
              >
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} [{p.status}]
                  </option>
                ))}
              </select>
            )}
          </div>
        )}

        {submitting && (
          <div className="space-y-2">
            <div className="text-[11px] text-eve-dim">{progressMsg}</div>
            <div className="max-h-32 overflow-y-auto text-[11px] font-mono border border-eve-border/40 rounded-sm p-2 bg-eve-input/40">
              {rowStatuses.map((rs) => (
                <div key={rs.index} className="flex items-center gap-2 py-0.5">
                  <span className="w-3 text-center">
                    {rs.state === "pending" && <span className="text-eve-dim">·</span>}
                    {rs.state === "analyzing" && <span className="text-eve-accent">…</span>}
                    {rs.state === "done" && <span className="text-emerald-400">✓</span>}
                    {rs.state === "error" && <span className="text-red-400">✗</span>}
                  </span>
                  <span className="flex-1 truncate">
                    {rs.row.product_name || `Type ${rs.row.product_type_id}`}
                  </span>
                  {rs.state === "error" && (
                    <span className="text-red-300 truncate max-w-xs" title={rs.errorMsg}>
                      {rs.errorMsg}
                    </span>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {error && <div className="text-xs text-red-300">{error}</div>}

        <div className="flex items-center justify-end gap-2 pt-2 border-t border-eve-border/40">
          <button
            type="button"
            onClick={submitting ? handleCancel : onClose}
            className="px-3 py-1.5 text-xs rounded-sm border border-eve-border text-eve-dim hover:text-eve-text
                       transition-colors"
          >
            {submitting ? t("industryScannerAddToProjectCancel") : "Cancel"}
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            disabled={submitting || rows.length === 0}
            className="px-3 py-1.5 text-xs font-semibold rounded-sm border border-eve-accent text-eve-accent
                       hover:bg-eve-accent/10 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {submitting ? "..." : t("industryScannerAddToProjectConfirm")}
          </button>
        </div>
      </div>
    </Modal>
  );
}
