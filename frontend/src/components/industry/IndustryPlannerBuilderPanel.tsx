import { useState, type Dispatch, type ReactNode, type SetStateAction } from "react";
import { useI18n } from "@/lib/i18n";
import type {
  IndustryBlueprintPoolInput,
  IndustryJobPlanInput,
  IndustryJobStatus,
  IndustryMaterialPlanInput,
  IndustryTaskPlanInput,
} from "@/lib/types";
import type { IndustryJobsWorkspaceTab } from "./IndustryJobsWorkspaceNav";

// AddRowButton is the compact + control that lives in each section header.
// Replaces the previous four "+Task / +Job / +Material / +Blueprint" buttons
// that used to clutter the planner action bar above.
function AddRowButton({ onClick, title }: { onClick: () => void; title: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className="px-1.5 py-0.5 text-[10px] rounded-sm border border-eve-accent/40 text-eve-accent hover:bg-eve-accent/10 leading-none"
    >
      +
    </button>
  );
}

// RowIconButton is a fixed-size square icon button used in each section's
// last column (delete, and the task-row expand chevron). Keeps the delete
// controls visually identical across all four sections regardless of which
// other buttons share the cell.
function RowIconButton({
  onClick,
  title,
  tone,
  children,
}: {
  onClick: () => void;
  title: string;
  tone: "danger" | "neutral";
  children: ReactNode;
}) {
  const toneClass =
    tone === "danger"
      ? "border-red-500/40 text-red-300 hover:bg-red-500/10"
      : "border-eve-border/50 text-eve-dim hover:text-eve-accent hover:border-eve-accent/40";
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={`inline-flex items-center justify-center w-6 h-6 rounded-sm border text-[11px] leading-none transition-colors ${toneClass}`}
    >
      {children}
    </button>
  );
}

type PlanBuilderSection = "tasks" | "jobs" | "materials" | "blueprints";

interface PlanTaskBlueprintOption {
  value: string;
  label: string;
  blueprintTypeID: number;
  blueprintLocationID: number;
  me: number;
  te: number;
}

interface IndustryPlannerBuilderStats {
  exact: number;
  fallback: number;
  missing: number;
}

interface IndustryPlannerBuilderContext {
  jobsWorkspaceTab: IndustryJobsWorkspaceTab;
  useVisualPlanBuilder: boolean;
  planBuilderCompactMode: boolean;
  setPlanBuilderCompactMode: Dispatch<SetStateAction<boolean>>;
  planBuilderPageSize: number;
  setPlanBuilderPageSize: Dispatch<SetStateAction<number>>;
  togglePlanBuilderSection: (section: PlanBuilderSection) => void;
  planBuilderCollapsed: Record<PlanBuilderSection, boolean>;
  planDraftTasks: IndustryTaskPlanInput[];
  visualTaskBlueprintBindingStats: IndustryPlannerBuilderStats;
  visiblePlanDraftTasks: IndustryTaskPlanInput[];
  taskPageStart: number;
  taskConstraintNumber: (value: unknown, key: string) => number;
  planTaskBlueprintOptionByPair: Map<string, string>;
  planTaskBlueprintOptionByType: Map<number, string>;
  planTaskBlueprintOptions: PlanTaskBlueprintOption[];
  updateVisualTaskRow: (index: number, next: Partial<IndustryTaskPlanInput>) => void;
  removeVisualTaskRow: (index: number) => void;
  updateVisualTaskConstraints: (index: number, patch: Record<string, number>) => void;
  updateVisualTaskConstraint: (index: number, key: string, value: number) => void;
  planBuilderPage: Record<PlanBuilderSection, number>;
  changePlanBuilderPage: (section: PlanBuilderSection, nextPage: number) => void;
  planBuilderTotalPages: Record<PlanBuilderSection, number>;
  planDraftJobs: IndustryJobPlanInput[];
  visiblePlanDraftJobs: IndustryJobPlanInput[];
  jobPageStart: number;
  updateVisualJobRow: (index: number, next: Partial<IndustryJobPlanInput>) => void;
  removeVisualJobRow: (index: number) => void;
  planDraftMaterials: IndustryMaterialPlanInput[];
  visiblePlanDraftMaterials: IndustryMaterialPlanInput[];
  materialPageStart: number;
  updateVisualMaterialRow: (index: number, next: Partial<IndustryMaterialPlanInput>) => void;
  removeVisualMaterialRow: (index: number) => void;
  planDraftBlueprints: IndustryBlueprintPoolInput[];
  visiblePlanDraftBlueprints: IndustryBlueprintPoolInput[];
  blueprintPageStart: number;
  updateVisualBlueprintRow: (index: number, next: Partial<IndustryBlueprintPoolInput>) => void;
  removeVisualBlueprintRow: (index: number) => void;
  addVisualTaskRow: () => void;
  addVisualJobRow: () => void;
  addVisualMaterialRow: () => void;
  addVisualBlueprintRow: () => void;
}

interface IndustryPlannerBuilderPanelProps {
  ctx: IndustryPlannerBuilderContext;
}

export function IndustryPlannerBuilderPanel({ ctx }: IndustryPlannerBuilderPanelProps) {
  const {
    jobsWorkspaceTab,
    useVisualPlanBuilder,
    planBuilderCompactMode,
    setPlanBuilderCompactMode,
    planBuilderPageSize,
    setPlanBuilderPageSize,
    togglePlanBuilderSection,
    planBuilderCollapsed,
    planDraftTasks,
    visualTaskBlueprintBindingStats,
    visiblePlanDraftTasks,
    taskPageStart,
    taskConstraintNumber,
    planTaskBlueprintOptionByPair,
    planTaskBlueprintOptionByType,
    planTaskBlueprintOptions,
    updateVisualTaskRow,
    removeVisualTaskRow,
    updateVisualTaskConstraints,
    updateVisualTaskConstraint,
    planBuilderPage,
    changePlanBuilderPage,
    planBuilderTotalPages,
    planDraftJobs,
    visiblePlanDraftJobs,
    jobPageStart,
    updateVisualJobRow,
    removeVisualJobRow,
    planDraftMaterials,
    visiblePlanDraftMaterials,
    materialPageStart,
    updateVisualMaterialRow,
    removeVisualMaterialRow,
    planDraftBlueprints,
    visiblePlanDraftBlueprints,
    blueprintPageStart,
    updateVisualBlueprintRow,
    removeVisualBlueprintRow,
    addVisualTaskRow,
    addVisualJobRow,
    addVisualMaterialRow,
    addVisualBlueprintRow,
  } = ctx;

  const { t } = useI18n();
  // Local per-row expansion state for the Tasks section. Row 2 (bp binding,
  // station, sec/run, isk/run, ME/TE) collapses by default so the row is
  // one line at a glance; click the chevron to expand for edits.
  const [expandedTaskRows, setExpandedTaskRows] = useState<Set<number>>(new Set());
  const toggleTaskExpanded = (rowIndex: number) => {
    setExpandedTaskRows((prev) => {
      const next = new Set(prev);
      if (next.has(rowIndex)) next.delete(rowIndex);
      else next.add(rowIndex);
      return next;
    });
  };

  return (
    <>
{jobsWorkspaceTab === "planning" && useVisualPlanBuilder && (
  <div className="mt-2">
    <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1">
      Visual Plan Builder
    </div>
    <div className="mb-2 flex flex-wrap items-center gap-2">
      <label className="flex items-center gap-1 text-[11px] text-eve-dim select-none">
        <input
          type="checkbox"
          checked={planBuilderCompactMode}
          onChange={(e) => setPlanBuilderCompactMode(e.target.checked)}
          className="accent-eve-accent"
        />
        Compact mode
      </label>
      {planBuilderCompactMode && (
        <>
          <span className="text-[11px] text-eve-dim">Rows/page</span>
          <select
            value={planBuilderPageSize}
            onChange={(e) => setPlanBuilderPageSize(Math.max(1, Number(e.target.value) || 6))}
            className="px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
          >
            <option value={4}>4</option>
            <option value={6}>6</option>
            <option value={10}>10</option>
            <option value={20}>20</option>
          </select>
        </>
      )}
    </div>
    <div className="grid grid-cols-1 xl:grid-cols-2 gap-2">
      <div className="border border-eve-border rounded-sm p-2 bg-eve-dark/20">
        <div className="flex items-center justify-between gap-2 mb-1">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => togglePlanBuilderSection("tasks")}
              className="text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-accent"
            >
              {planBuilderCollapsed.tasks ? "▶" : "▼"} {t("industryPlannerSectionTasks")} ({planDraftTasks.length})
            </button>
            <AddRowButton onClick={addVisualTaskRow} title={t("industryLedgerAddTask")} />
          </div>
          {planBuilderCompactMode && !planBuilderCollapsed.tasks && (
            <div className="inline-flex items-center gap-1 text-[10px] text-eve-dim">
              <button
                type="button"
                onClick={() => changePlanBuilderPage("tasks", planBuilderPage.tasks - 1)}
                disabled={planBuilderPage.tasks <= 1}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {"<"}
              </button>
              <span>{planBuilderPage.tasks}/{planBuilderTotalPages.tasks}</span>
              <button
                type="button"
                onClick={() => changePlanBuilderPage("tasks", planBuilderPage.tasks + 1)}
                disabled={planBuilderPage.tasks >= planBuilderTotalPages.tasks}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {">"}
              </button>
            </div>
          )}
        </div>
        {planDraftTasks.length > 0 && (
          <div className="mt-1 text-[10px] text-eve-dim">
            bp binding: exact {visualTaskBlueprintBindingStats.exact} | fallback {visualTaskBlueprintBindingStats.fallback} | missing {visualTaskBlueprintBindingStats.missing}
          </div>
        )}
        {!planBuilderCollapsed.tasks && (
          // Grid-aligned column header for the task rows. Row 1 columns:
          //   name (5) · activity (3) · runs (1) · parent (1) · prio (1) · × / ▾ (1)
          // Product column removed — task name already conveys the product.
          // The ▾ column doubles as row expand toggle → row 2 (bp binding etc.)
          <div className="grid grid-cols-12 gap-1 mb-1 text-[10px] uppercase tracking-wider text-eve-dim px-1">
            <div className="col-span-5">{t("industryPlannerColTaskName")}</div>
            <div className="col-span-3">{t("industryPlannerColTaskActivity")}</div>
            <div className="col-span-1">{t("industryPlannerColTaskRuns")}</div>
            <div className="col-span-1">{t("industryPlannerColTaskParent")}</div>
            <div className="col-span-1">{t("industryPlannerColTaskPrio")}</div>
            <div className="col-span-1">{t("industryPlannerColActions")}</div>
          </div>
        )}
        {!planBuilderCollapsed.tasks && (
        <div className="space-y-1.5 max-h-[220px] overflow-auto pr-1">
          {visiblePlanDraftTasks.map((task, idx) => {
            const rowIndex = taskPageStart + idx;
            const bpTypeID = taskConstraintNumber(task.constraints, "blueprint_type_id");
            const bpLocationID = taskConstraintNumber(task.constraints, "blueprint_location_id");
            const stationID = taskConstraintNumber(task.constraints, "station_id");
            const durationPerRun = taskConstraintNumber(task.constraints, "duration_seconds_per_run");
            const costPerRun = taskConstraintNumber(task.constraints, "cost_isk_per_run");
            const meConstraint = taskConstraintNumber(task.constraints, "me");
            const teConstraint = taskConstraintNumber(task.constraints, "te");
            const hasExactBinding = bpTypeID > 0 && planTaskBlueprintOptionByPair.has(`${bpTypeID}:${bpLocationID}`);
            const hasTypeBinding = bpTypeID > 0 && planTaskBlueprintOptionByType.has(bpTypeID);
            const bindingState = bpTypeID <= 0
              ? "none"
              : hasExactBinding
                ? "exact"
                : hasTypeBinding
                  ? "fallback"
                  : "missing";
            const selectedBlueprintBindingValue = bpTypeID > 0
              ? (
                  planTaskBlueprintOptionByPair.get(`${bpTypeID}:${bpLocationID}`)
                  ?? planTaskBlueprintOptionByType.get(bpTypeID)
                  ?? "custom"
                )
              : "none";
            const bindingSelectClass = bindingState === "missing"
              ? "border-red-500/60 text-red-200"
              : bindingState === "fallback"
                ? "border-amber-500/50 text-amber-200"
                : "border-eve-border text-eve-text";
            return (
            <div key={`task-${rowIndex}`} className="space-y-1">
              <div className="grid grid-cols-12 gap-1">
                  <input
                    type="text"
                    value={task.name ?? ""}
                    onChange={(e) => updateVisualTaskRow(rowIndex, { name: e.target.value })}
                    placeholder="name"
                    className="col-span-5 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
                  />
                  <select
                    value={task.activity ?? "manufacturing"}
                    onChange={(e) => updateVisualTaskRow(rowIndex, { activity: e.target.value })}
                    className="col-span-3 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
                >
                  <option value="manufacturing">manufacturing</option>
                  <option value="reaction">reaction</option>
                  <option value="copy">copy</option>
                  <option value="invention">invention</option>
                </select>
                  <input
                    type="number"
                    value={task.target_runs ?? 0}
                    onChange={(e) => updateVisualTaskRow(rowIndex, { target_runs: Number(e.target.value) || 0 })}
                    placeholder="runs"
                    className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                  />
                  <input
                    type="number"
                    value={task.parent_task_id ?? 0}
                    onChange={(e) => updateVisualTaskRow(rowIndex, { parent_task_id: Number(e.target.value) || 0 })}
                    placeholder="parent"
                    className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                    title="Parent task: use existing task ID, or negative row ref (-1 = row 1, -2 = row 2)"
                  />
                  <input
                    type="number"
                    value={task.priority ?? 0}
                    onChange={(e) => updateVisualTaskRow(rowIndex, { priority: Number(e.target.value) || 0 })}
                    placeholder="prio"
                    className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                  />
                  <div className="col-span-1 flex items-center justify-end gap-1">
                    <RowIconButton
                      onClick={() => toggleTaskExpanded(rowIndex)}
                      title={expandedTaskRows.has(rowIndex) ? t("industryPlannerCollapseTask") : t("industryPlannerExpandTask")}
                      tone="neutral"
                    >
                      {expandedTaskRows.has(rowIndex) ? "▾" : "▸"}
                    </RowIconButton>
                    <RowIconButton
                      onClick={() => removeVisualTaskRow(rowIndex)}
                      title={t("industryPlannerDeleteRow")}
                      tone="danger"
                    >
                      ×
                    </RowIconButton>
                  </div>
              </div>
              {expandedTaskRows.has(rowIndex) && (
              <div className="grid grid-cols-12 gap-1 mb-0.5 text-[10px] uppercase tracking-wider text-eve-dim px-1">
                <div className="col-span-4">{t("industryPlannerColTaskBpBinding")}</div>
                <div className="col-span-2 ">{t("industryPlannerColTaskStation")}</div>
                <div className="col-span-2 ">{t("industryPlannerColTaskSecPerRun")}</div>
                <div className="col-span-2 ">{t("industryPlannerColTaskIskPerRun")}</div>
                <div className="col-span-1 ">{t("industryPlannerColTaskME")}</div>
                <div className="col-span-1 ">{t("industryPlannerColTaskTE")}</div>
              </div>
              )}
              {expandedTaskRows.has(rowIndex) && (
              <div className="grid grid-cols-12 gap-1">
                <select
                  value={selectedBlueprintBindingValue}
                  onChange={(e) => {
                    const value = e.target.value;
                    if (value === "none") {
                      updateVisualTaskConstraints(rowIndex, {
                        blueprint_type_id: 0,
                        blueprint_location_id: 0,
                      });
                      return;
                    }
                    if (value === "custom") {
                      return;
                    }
                                  const selected = planTaskBlueprintOptions.find((option) => option.value === value);
                    if (!selected) {
                      return;
                    }
                    updateVisualTaskConstraints(rowIndex, {
                      blueprint_type_id: selected.blueprintTypeID,
                      blueprint_location_id: selected.blueprintLocationID,
                      station_id: selected.blueprintLocationID > 0 ? selected.blueprintLocationID : stationID,
                      me: selected.me,
                      te: selected.te,
                    });
                  }}
                  className={`col-span-4 px-1.5 py-1 bg-eve-input border rounded-sm text-[11px] ${bindingSelectClass}`}
                  title="Bind task to a blueprint pool row for run-cap limits"
                >
                  <option value="none">bp binding: none</option>
                  {planTaskBlueprintOptions.map((option) => (
                    <option key={option.value} value={option.value}>{option.label}</option>
                  ))}
                  {selectedBlueprintBindingValue === "custom" && (
                    <option value="custom">
                      bp binding: custom [{bpTypeID}] @{bpLocationID || "any"}
                    </option>
                  )}
                </select>
                <input
                  type="number"
                  value={stationID}
                  onChange={(e) => updateVisualTaskConstraint(rowIndex, "station_id", Number(e.target.value) || 0)}
                  placeholder="station"
                  className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                  title="Task station/location ID"
                />
                <input
                  type="number"
                  value={durationPerRun}
                  onChange={(e) => updateVisualTaskConstraint(rowIndex, "duration_seconds_per_run", Number(e.target.value) || 0)}
                  placeholder="sec/run"
                  className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                  title="Planner hint: duration seconds per run"
                />
                <input
                  type="number"
                  value={costPerRun}
                  onChange={(e) => updateVisualTaskConstraint(rowIndex, "cost_isk_per_run", Number(e.target.value) || 0)}
                  placeholder="isk/run"
                  className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                  title="Planner hint: job cost ISK per run"
                />
                <input
                  type="number"
                  value={meConstraint}
                  onChange={(e) => updateVisualTaskConstraint(rowIndex, "me", Number(e.target.value) || 0)}
                  placeholder="ME"
                  className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                />
                <input
                  type="number"
                  value={teConstraint}
                  onChange={(e) => updateVisualTaskConstraint(rowIndex, "te", Number(e.target.value) || 0)}
                  placeholder="TE"
                  className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                />
              </div>
              )}
              {bindingState === "missing" && (
                <div className="text-[10px] text-red-300">
                  Blueprint binding missing in pool: bp {bpTypeID} @ {bpLocationID || "any"}.
                </div>
              )}
              {bindingState === "fallback" && (
                <div className="text-[10px] text-amber-300">
                  Blueprint binding matched by type only (location fallback): bp {bpTypeID}.
                </div>
              )}
            </div>
            );
          })}
          {planDraftTasks.length === 0 && (
            <div className="text-[11px] text-eve-dim">No task rows yet.</div>
          )}
        </div>
        )}
      </div>

      <div className="border border-eve-border rounded-sm p-2 bg-eve-dark/20">
        <div className="flex items-center justify-between gap-2 mb-1">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => togglePlanBuilderSection("jobs")}
              className="text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-accent"
            >
              {planBuilderCollapsed.jobs ? "▶" : "▼"} {t("industryPlannerSectionJobs")} ({planDraftJobs.length})
            </button>
            <AddRowButton onClick={addVisualJobRow} title={t("industryLedgerAddJob")} />
          </div>
          {planBuilderCompactMode && !planBuilderCollapsed.jobs && (
            <div className="inline-flex items-center gap-1 text-[10px] text-eve-dim">
              <button
                type="button"
                onClick={() => changePlanBuilderPage("jobs", planBuilderPage.jobs - 1)}
                disabled={planBuilderPage.jobs <= 1}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {"<"}
              </button>
              <span>{planBuilderPage.jobs}/{planBuilderTotalPages.jobs}</span>
              <button
                type="button"
                onClick={() => changePlanBuilderPage("jobs", planBuilderPage.jobs + 1)}
                disabled={planBuilderPage.jobs >= planBuilderTotalPages.jobs}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {">"}
              </button>
            </div>
          )}
        </div>
        {!planBuilderCollapsed.jobs && (
          // 7 cells matching the 7-cell row layout below (no more facility
          // header without a matching input, no more notes column).
          <div className="grid grid-cols-12 gap-1 mb-1 text-[10px] uppercase tracking-wider text-eve-dim px-1">
            <div className="col-span-2">{t("industryPlannerColJobActivity")}</div>
            <div className="col-span-1">{t("industryPlannerColJobTaskRef")}</div>
            <div className="col-span-1">{t("industryPlannerColJobRuns")}</div>
            <div className="col-span-2">{t("industryPlannerColJobDuration")}</div>
            <div className="col-span-2">{t("industryPlannerColJobCost")}</div>
            <div className="col-span-3">{t("industryPlannerColJobStatus")}</div>
            <div className="col-span-1">{t("industryPlannerColActions")}</div>
          </div>
        )}
        {!planBuilderCollapsed.jobs && (
        <div className="space-y-1.5 max-h-[220px] overflow-auto pr-1">
          {visiblePlanDraftJobs.map((job, idx) => {
            const rowIndex = jobPageStart + idx;
            return (
            <div key={`job-${rowIndex}`} className="grid grid-cols-12 gap-1">
                <select
                  value={job.activity ?? "manufacturing"}
                  onChange={(e) => updateVisualJobRow(rowIndex, { activity: e.target.value })}
                  className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
                >
                  <option value="manufacturing">manufacturing</option>
                  <option value="reaction">reaction</option>
                  <option value="copy">copy</option>
                  <option value="invention">invention</option>
                </select>
                <input
                  type="number"
                  value={job.task_id ?? 0}
                  onChange={(e) => updateVisualJobRow(rowIndex, { task_id: Number(e.target.value) || 0 })}
                  placeholder="task"
                  className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
                  title="Task link: use existing task ID, or negative row ref (-1 = row 1, -2 = row 2)"
                />
                <input
                  type="number"
                  value={job.runs ?? 0}
                  onChange={(e) => updateVisualJobRow(rowIndex, { runs: Number(e.target.value) || 0 })}
                  placeholder="runs"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={job.duration_seconds ?? 0}
                onChange={(e) => updateVisualJobRow(rowIndex, { duration_seconds: Number(e.target.value) || 0 })}
                placeholder="sec"
                className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={Math.round(job.cost_isk ?? 0)}
                onChange={(e) => updateVisualJobRow(rowIndex, { cost_isk: Number(e.target.value) || 0 })}
                placeholder="cost"
                title={String(job.cost_isk ?? 0)}
                className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <select
                value={job.status ?? "planned"}
                onChange={(e) => updateVisualJobRow(rowIndex, { status: e.target.value as IndustryJobStatus })}
                className="col-span-3 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
              >
                <option value="planned">planned</option>
                <option value="queued">queued</option>
                <option value="active">active</option>
                <option value="paused">paused</option>
                <option value="completed">completed</option>
                <option value="failed">failed</option>
                <option value="cancelled">cancelled</option>
              </select>
              <div className="col-span-1 flex items-center justify-end">
                <RowIconButton
                  onClick={() => removeVisualJobRow(rowIndex)}
                  title={t("industryPlannerDeleteRow")}
                  tone="danger"
                >
                  ×
                </RowIconButton>
              </div>
            </div>
            );
          })}
          {planDraftJobs.length === 0 && (
            <div className="text-[11px] text-eve-dim">No job rows yet.</div>
          )}
        </div>
        )}
      </div>

      <div className="border border-eve-border rounded-sm p-2 bg-eve-dark/20">
        <div className="flex items-center justify-between gap-2 mb-1">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => togglePlanBuilderSection("materials")}
              className="text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-accent"
            >
              {planBuilderCollapsed.materials ? "▶" : "▼"} {t("industryPlannerSectionMaterials")} ({planDraftMaterials.length})
            </button>
            <AddRowButton onClick={addVisualMaterialRow} title={t("industryLedgerAddMaterial")} />
          </div>
          {planBuilderCompactMode && !planBuilderCollapsed.materials && (
            <div className="inline-flex items-center gap-1 text-[10px] text-eve-dim">
              <button
                type="button"
                onClick={() => changePlanBuilderPage("materials", planBuilderPage.materials - 1)}
                disabled={planBuilderPage.materials <= 1}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {"<"}
              </button>
              <span>{planBuilderPage.materials}/{planBuilderTotalPages.materials}</span>
              <button
                type="button"
                onClick={() => changePlanBuilderPage("materials", planBuilderPage.materials + 1)}
                disabled={planBuilderPage.materials >= planBuilderTotalPages.materials}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {">"}
              </button>
            </div>
          )}
        </div>
        {!planBuilderCollapsed.materials && (
          // 8 cells matching the 8-cell row layout below (no more Type-ID
          // column; Have added as a real input so users can see coverage).
          <div className="grid grid-cols-12 gap-1 mb-1 text-[10px] uppercase tracking-wider text-eve-dim px-1">
            <div className="col-span-4">{t("industryPlannerColMatName")}</div>
            <div className="col-span-1">{t("industryPlannerColMatRequired")}</div>
            <div className="col-span-1">{t("industryPlannerColMatAvailable")}</div>
            <div className="col-span-1">{t("industryPlannerColMatBuy")}</div>
            <div className="col-span-1">{t("industryPlannerColMatBuild")}</div>
            <div className="col-span-2">{t("industryPlannerColMatUnitCost")}</div>
            <div className="col-span-1">{t("industryPlannerColMatSource")}</div>
            <div className="col-span-1">{t("industryPlannerColActions")}</div>
          </div>
        )}
        {!planBuilderCollapsed.materials && (
        <div className="space-y-1.5 max-h-[220px] overflow-auto pr-1">
          {visiblePlanDraftMaterials.map((material, idx) => {
            const rowIndex = materialPageStart + idx;
            return (
            <div key={`material-${rowIndex}`} className="grid grid-cols-12 gap-1">
              <input
                type="text"
                value={material.type_name ?? ""}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { type_name: e.target.value })}
                placeholder="name"
                title={`type_id ${material.type_id ?? 0}`}
                className="col-span-4 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
              />
              <input
                type="number"
                value={material.required_qty ?? 0}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { required_qty: Number(e.target.value) || 0 })}
                placeholder="req"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={material.available_qty ?? 0}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { available_qty: Number(e.target.value) || 0 })}
                placeholder="have"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={material.buy_qty ?? 0}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { buy_qty: Number(e.target.value) || 0 })}
                placeholder="buy"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={material.build_qty ?? 0}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { build_qty: Number(e.target.value) || 0 })}
                placeholder="build"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={Math.round((material.unit_cost_isk ?? 0) * 100) / 100}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { unit_cost_isk: Number(e.target.value) || 0 })}
                placeholder="unit cost"
                title={String(material.unit_cost_isk ?? 0)}
                step={0.01}
                className="col-span-2 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <select
                value={material.source ?? "market"}
                onChange={(e) => updateVisualMaterialRow(rowIndex, { source: e.target.value as IndustryMaterialPlanInput["source"] })}
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
              >
                <option value="market">market</option>
                <option value="stock">stock</option>
                <option value="build">build</option>
                <option value="reprocess">reprocess</option>
                <option value="contract">contract</option>
              </select>
              <div className="col-span-1 flex items-center justify-end">
                <RowIconButton
                  onClick={() => removeVisualMaterialRow(rowIndex)}
                  title={t("industryPlannerDeleteRow")}
                  tone="danger"
                >
                  ×
                </RowIconButton>
              </div>
            </div>
            );
          })}
          {planDraftMaterials.length === 0 && (
            <div className="text-[11px] text-eve-dim">No material rows yet.</div>
          )}
        </div>
        )}
      </div>

      <div className="border border-eve-border rounded-sm p-2 bg-eve-dark/20">
        <div className="flex items-center justify-between gap-2 mb-1">
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => togglePlanBuilderSection("blueprints")}
              className="text-[10px] uppercase tracking-wider text-eve-dim hover:text-eve-accent"
            >
              {planBuilderCollapsed.blueprints ? "▶" : "▼"} {t("industryPlannerSectionBlueprints")} ({planDraftBlueprints.length})
            </button>
            <AddRowButton onClick={addVisualBlueprintRow} title={t("industryLedgerAddBlueprint")} />
          </div>
          {planBuilderCompactMode && !planBuilderCollapsed.blueprints && (
            <div className="inline-flex items-center gap-1 text-[10px] text-eve-dim">
              <button
                type="button"
                onClick={() => changePlanBuilderPage("blueprints", planBuilderPage.blueprints - 1)}
                disabled={planBuilderPage.blueprints <= 1}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {"<"}
              </button>
              <span>{planBuilderPage.blueprints}/{planBuilderTotalPages.blueprints}</span>
              <button
                type="button"
                onClick={() => changePlanBuilderPage("blueprints", planBuilderPage.blueprints + 1)}
                disabled={planBuilderPage.blueprints >= planBuilderTotalPages.blueprints}
                className="px-1 border border-eve-border rounded-sm disabled:opacity-40"
              >
                {">"}
              </button>
            </div>
          )}
        </div>
        {!planBuilderCollapsed.blueprints && (
          // 8 cells matching the 8-cell row layout below (no more Type-ID
          // column; name expands to col-span-5).
          <div className="grid grid-cols-12 gap-1 mb-1 text-[10px] uppercase tracking-wider text-eve-dim px-1">
            <div className="col-span-5">{t("industryPlannerColBpName")}</div>
            <div className="col-span-1">{t("industryPlannerColBpLocation")}</div>
            <div className="col-span-1">{t("industryPlannerColBpQty")}</div>
            <div className="col-span-1">{t("industryPlannerColBpME")}</div>
            <div className="col-span-1">{t("industryPlannerColBpTE")}</div>
            <div className="col-span-1">{t("industryPlannerColBpBPO")}</div>
            <div className="col-span-1">{t("industryPlannerColBpRunsLeft")}</div>
            <div className="col-span-1">{t("industryPlannerColActions")}</div>
          </div>
        )}
        {!planBuilderCollapsed.blueprints && (
        <div className="space-y-1.5 max-h-[220px] overflow-auto pr-1">
          {visiblePlanDraftBlueprints.map((bp, idx) => {
            const rowIndex = blueprintPageStart + idx;
            return (
            <div key={`bp-${rowIndex}`} className="grid grid-cols-12 gap-1">
              <input
                type="text"
                value={bp.blueprint_name ?? ""}
                onChange={(e) => updateVisualBlueprintRow(rowIndex, { blueprint_name: e.target.value })}
                placeholder="name"
                title={`type_id ${bp.blueprint_type_id ?? 0}`}
                className="col-span-5 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text"
              />
              <input
                type="number"
                value={bp.location_id ?? 0}
                onChange={(e) => updateVisualBlueprintRow(rowIndex, { location_id: Number(e.target.value) || 0 })}
                placeholder="location"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={bp.quantity ?? 0}
                onChange={(e) => updateVisualBlueprintRow(rowIndex, { quantity: Number(e.target.value) || 0 })}
                placeholder="qty"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={bp.me ?? 0}
                onChange={(e) => updateVisualBlueprintRow(rowIndex, { me: Number(e.target.value) || 0 })}
                placeholder="ME"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <input
                type="number"
                value={bp.te ?? 0}
                onChange={(e) => updateVisualBlueprintRow(rowIndex, { te: Number(e.target.value) || 0 })}
                placeholder="TE"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
              />
              <label className="col-span-1 flex items-center justify-center border border-eve-border rounded-sm bg-eve-input">
                  <input
                    type="checkbox"
                    checked={Boolean(bp.is_bpo)}
                    onChange={(e) => updateVisualBlueprintRow(rowIndex, { is_bpo: e.target.checked })}
                    className="accent-eve-accent"
                  />
                </label>
              <input
                type="number"
                value={bp.available_runs ?? 0}
                onChange={(e) => updateVisualBlueprintRow(rowIndex, { available_runs: Number(e.target.value) || 0 })}
                disabled={Boolean(bp.is_bpo)}
                placeholder="runs"
                className="col-span-1 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono disabled:opacity-50"
              />
              <div className="col-span-1 flex items-center justify-end">
                <RowIconButton
                  onClick={() => removeVisualBlueprintRow(rowIndex)}
                  title={t("industryPlannerDeleteRow")}
                  tone="danger"
                >
                  ×
                </RowIconButton>
              </div>
            </div>
            );
          })}
          {planDraftBlueprints.length === 0 && (
            <div className="text-[11px] text-eve-dim">No blueprint rows yet.</div>
          )}
        </div>
        )}
      </div>
    </div>
  </div>
)}
    </>
  );
}
