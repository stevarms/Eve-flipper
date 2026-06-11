import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { Modal } from "../Modal";
import {
  createAuthIndustryProject,
  getAuthIndustryProjects,
  planAuthIndustryProject,
} from "@/lib/api";
import type {
  IndustryPlanPatch,
  IndustryProject,
  IndustryTaskPlanInput,
  IndustryJobPlanInput,
  IndustryBlueprintPoolInput,
  ProfitableScanRow,
} from "@/lib/types";

type Mode = "new" | "existing";

interface PlanApplySummaryLike {
  tasks_inserted: number;
  jobs_inserted: number;
  blueprints_upserted: number;
}

interface Props {
  open: boolean;
  onClose: () => void;
  rows: ProfitableScanRow[];
  runsPerJob: number;
  onSuccess: (projectID: number, count: number, summary: PlanApplySummaryLike | null) => void;
}

export function AddBlueprintsToProjectModal({ open, onClose, rows, runsPerJob, onSuccess }: Props) {
  const { t } = useI18n();
  const [mode, setMode] = useState<Mode>("new");
  const [name, setName] = useState("");
  const [strategy, setStrategy] = useState<"conservative" | "balanced" | "aggressive">("balanced");
  const [projects, setProjects] = useState<IndustryProject[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(false);
  const [selectedProjectID, setSelectedProjectID] = useState<number>(0);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setError(null);
    setName(`Scanner ${new Date().toISOString().slice(0, 10)}`);
    setMode("new");
  }, [open]);

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

  const patch = useMemo<IndustryPlanPatch>(() => {
    const tasks: IndustryTaskPlanInput[] = rows.map((row) => ({
      name: `${row.product_name} x${runsPerJob}`,
      activity: "manufacturing",
      product_type_id: row.product_type_id,
      target_runs: runsPerJob,
      status: "planned",
    }));
    const jobs: IndustryJobPlanInput[] = rows.map((_, idx) => ({
      task_id: -(idx + 1),
      activity: "manufacturing",
      runs: runsPerJob,
      duration_seconds: rows[idx].manufacturing_time,
      cost_isk: Math.max(0, rows[idx].optimal_build_cost),
      status: "planned",
    }));
    // Also seed the project's blueprint pool with the selected items so the
    // planner sees which BPs are available without needing a separate sync.
    const blueprints: IndustryBlueprintPoolInput[] = rows.map((row) => ({
      blueprint_type_id: row.blueprint_type_id,
      blueprint_name: row.blueprint_name,
      location_id: row.location_ids?.[0] ?? 0,
      quantity: row.owned_quantity,
      me: row.me,
      te: row.te,
      is_bpo: row.is_bpo,
      available_runs: row.is_bpo ? 0 : row.available_runs,
    }));
    return {
      replace: false,
      tasks,
      jobs,
      blueprints,
    };
  }, [rows, runsPerJob]);

  const handleSubmit = useCallback(async () => {
    if (rows.length === 0) return;
    setSubmitting(true);
    setError(null);
    try {
      let projectID = selectedProjectID;
      if (mode === "new") {
        const trimmed = name.trim();
        if (!trimmed) {
          setError("Project name required");
          setSubmitting(false);
          return;
        }
        const created = await createAuthIndustryProject({
          name: trimmed,
          strategy,
        });
        projectID = Number(created.project?.id ?? 0);
        if (projectID <= 0) {
          throw new Error("Project create returned no id");
        }
      }
      if (projectID <= 0) {
        setError("Choose an existing project");
        setSubmitting(false);
        return;
      }
      const resp = await planAuthIndustryProject(projectID, patch);
      const summary = resp?.summary
        ? {
            tasks_inserted: resp.summary.tasks_inserted ?? 0,
            jobs_inserted: resp.summary.jobs_inserted ?? 0,
            blueprints_upserted: resp.summary.blueprints_upserted ?? 0,
          }
        : null;
      onSuccess(projectID, rows.length, summary);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Add to project failed");
    } finally {
      setSubmitting(false);
    }
  }, [rows, selectedProjectID, mode, name, strategy, patch, onSuccess]);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("industryScannerAddToProjectModalTitle")}
      width="max-w-lg"
    >
      <div className="p-4 space-y-4 text-sm">
        <div className="text-xs text-eve-dim">
          {rows.length} blueprint(s) selected • {runsPerJob} runs per job
        </div>

        <div className="flex flex-col gap-2">
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="addMode"
              checked={mode === "new"}
              onChange={() => setMode("new")}
            />
            <span>{t("industryScannerAddToProjectModeNew")}</span>
          </label>
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="radio"
              name="addMode"
              checked={mode === "existing"}
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
                onChange={(e) => setName(e.target.value)}
                className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30"
              />
            </div>
            <div>
              <label className="block text-[11px] uppercase tracking-wider text-eve-dim mb-1">
                {t("industryScannerStrategy")}
              </label>
              <select
                value={strategy}
                onChange={(e) => setStrategy(e.target.value as typeof strategy)}
                className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30"
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
                onChange={(e) => setSelectedProjectID(Number(e.target.value))}
                className="w-full px-3 py-1.5 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm
                           focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30"
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

        {error && <div className="text-xs text-red-300">{error}</div>}

        <div className="flex items-center justify-end gap-2 pt-2 border-t border-eve-border/40">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="px-3 py-1.5 text-xs rounded-sm border border-eve-border text-eve-dim hover:text-eve-text
                       disabled:opacity-50 transition-colors"
          >
            Cancel
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
