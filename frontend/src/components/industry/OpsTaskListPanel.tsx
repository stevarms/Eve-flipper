import { Fragment, useMemo, useState, type Dispatch, type SetStateAction } from "react";
import { useI18n } from "@/lib/i18n";
import type {
  IndustryBlueprintPoolRecord,
  IndustryJobStatus,
  IndustryLedger,
  IndustryLedgerEntry,
  IndustryMaterialPlanRecord,
  IndustryProjectSnapshot,
  IndustryTaskRecord,
  IndustryTaskStatus,
} from "@/lib/types";
import {
  deriveTaskBlockStatus,
  sortOperationsTasks,
  splitTaskDecryptorSuffix,
  taskConstraintNumber,
  type IndustryTaskDependencyBoard,
  type TaskBlockLevel,
} from "./industryHelpers";

/**
 * Combined task + jobs view for Operations. Replaces IndustryTaskBoardPanel +
 * IndustryOperationsJobsPanel. Each task row shows a block-status pill
 * (ready/soft/hard/unknown) derived from per-task material coverage +
 * parent-task completion state, and expanding a task reveals the jobs
 * bound to it (via `job.task_id`) with the same status-transition
 * controls the old jobs panel had.
 *
 * Default sort: taskDependencyBoard.depth_by_task ascending → priority
 * descending → id ascending. Users can flip to priority-only by clicking
 * the priority column header.
 */

interface OpsTaskListPanelProps {
  ledgerSnapshot: IndustryProjectSnapshot | null;
  ledgerData: IndustryLedger | null;
  taskDependencyBoard: IndustryTaskDependencyBoard;
  // Task-side actions
  selectedLedgerTaskIDs: number[];
  bulkLedgerTaskPriority: number;
  setBulkLedgerTaskPriority: Dispatch<SetStateAction<number>>;
  handleBulkSetLedgerTaskPriority: (priority: number) => Promise<void>;
  updatingLedgerTasksBulk: boolean;
  handleBulkSetLedgerTaskStatus: (status: IndustryTaskStatus) => Promise<void>;
  setSelectedLedgerTaskIDs: Dispatch<SetStateAction<number[]>>;
  allVisibleLedgerTasksSelected: boolean;
  handleSelectAllVisibleLedgerTasks: (selected: boolean) => void;
  selectedLedgerTaskIDSet: Set<number>;
  toggleLedgerTaskSelection: (taskId: number, selected: boolean) => void;
  industryTaskStatusClass: (status: string) => string;
  industryJobStatusClass: (status: string) => string;
  formatUtcShort: (value: string) => string;
  formatISK: (value: number) => string;
  handleSetLedgerTaskPriority: (taskId: number, priority: number) => Promise<void>;
  updatingLedgerTaskId: number;
  handleSetLedgerTaskStatus: (taskId: number, status: IndustryTaskStatus) => Promise<void>;
  // Job-side actions (surfaced inside the task expansion)
  handleSetLedgerJobStatus: (jobId: number, status: IndustryJobStatus) => Promise<void>;
  updatingLedgerJobId: number;
  updatingLedgerJobsBulk: boolean;
}

const BLOCK_PILL_CLASSES: Record<TaskBlockLevel, string> = {
  ready: "border-emerald-500/60 text-emerald-300 bg-emerald-900/20",
  soft: "border-amber-500/60 text-amber-300 bg-amber-900/20",
  hard: "border-red-500/60 text-red-300 bg-red-900/20",
  unknown: "border-slate-500/60 text-slate-300 bg-slate-800/40",
};

// Internal type names kept as ready/soft/hard for continuity; display
// labels use the user-facing vocabulary: READY / PARTIAL / BLOCKED.
const BLOCK_PILL_LABELS: Record<TaskBlockLevel, string> = {
  ready: "READY",
  soft: "PARTIAL",
  hard: "BLOCKED",
  unknown: "—",
};

export function OpsTaskListPanel(props: OpsTaskListPanelProps) {
  const { t } = useI18n();
  const {
    ledgerSnapshot,
    ledgerData,
    taskDependencyBoard,
    selectedLedgerTaskIDs,
    bulkLedgerTaskPriority,
    setBulkLedgerTaskPriority,
    handleBulkSetLedgerTaskPriority,
    updatingLedgerTasksBulk,
    handleBulkSetLedgerTaskStatus,
    setSelectedLedgerTaskIDs,
    allVisibleLedgerTasksSelected,
    handleSelectAllVisibleLedgerTasks,
    selectedLedgerTaskIDSet,
    toggleLedgerTaskSelection,
    industryTaskStatusClass,
    industryJobStatusClass,
    formatUtcShort,
    formatISK,
    handleSetLedgerTaskPriority,
    updatingLedgerTaskId,
    handleSetLedgerTaskStatus,
    handleSetLedgerJobStatus,
    updatingLedgerJobId,
    updatingLedgerJobsBulk,
  } = props;

  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const toggleExpanded = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  // Group jobs by task_id for O(1) lookup during row expansion.
  const jobsByTaskID = useMemo(() => {
    const map = new Map<number, IndustryLedgerEntry[]>();
    for (const entry of ledgerData?.entries ?? []) {
      const list = map.get(entry.task_id) ?? [];
      list.push(entry);
      map.set(entry.task_id, list);
    }
    return map;
  }, [ledgerData]);

  // Per-task material rows (tagged after the task_id-threading fix landed).
  // Legacy plans without per-task tagging fall back to an empty list —
  // the expansion just shows no per-task material section for those.
  const materialsByTaskID = useMemo(() => {
    const map = new Map<number, IndustryMaterialPlanRecord[]>();
    for (const m of ledgerSnapshot?.materials ?? []) {
      const list = map.get(m.task_id) ?? [];
      list.push(m);
      map.set(m.task_id, list);
    }
    return map;
  }, [ledgerSnapshot]);

  // Look up any blueprint by its type_id — used to render the "blueprint
  // required" row inside a task expansion (task.constraints tells us the
  // BP type; the snapshot tells us if it's a BPO/BPC, ME/TE, runs left).
  const blueprintByTypeID = useMemo(() => {
    const map = new Map<number, IndustryBlueprintPoolRecord>();
    for (const bp of ledgerSnapshot?.blueprints ?? []) {
      map.set(bp.blueprint_type_id, bp);
    }
    return map;
  }, [ledgerSnapshot]);

  // Compute block status per task once; reused for the row pill.
  const blockByTask = useMemo(() => {
    const map = new Map<number, ReturnType<typeof deriveTaskBlockStatus>>();
    if (!ledgerSnapshot) return map;
    for (const task of ledgerSnapshot.tasks) {
      map.set(task.id, deriveTaskBlockStatus(task.id, ledgerSnapshot, taskDependencyBoard.prereqs_by_task));
    }
    return map;
  }, [ledgerSnapshot, taskDependencyBoard.prereqs_by_task]);

  // Shared with the "Do this next" card so its "step N of M" always points
  // at the same row here.
  const sortedTasks: IndustryTaskRecord[] = useMemo(() => {
    if (!ledgerSnapshot) return [];
    return sortOperationsTasks(ledgerSnapshot.tasks, taskDependencyBoard.depth_by_task);
  }, [ledgerSnapshot, taskDependencyBoard.depth_by_task]);

  if (!ledgerSnapshot) {
    return (
      <div className="mt-2 border border-eve-border/40 rounded-sm p-3 text-xs text-eve-dim">
        Select a project to see its committed tasks.
      </div>
    );
  }

  return (
    <div className="mt-2 border border-eve-border/40 rounded-sm bg-eve-dark/20">
      {/* Bulk toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2 px-2 py-1.5 border-b border-eve-border/40">
        <div className="text-[10px] uppercase tracking-wider text-eve-dim">
          {t("industryLedgerTaskBoardTitle", { count: ledgerSnapshot.tasks.length })}
        </div>
        <div className="inline-flex flex-wrap items-center gap-1 text-[11px]">
          <span className="text-eve-dim">
            {t("industryLedgerSelected")}: {selectedLedgerTaskIDs.length}
          </span>
          <input
            type="number"
            value={bulkLedgerTaskPriority}
            onChange={(e) => setBulkLedgerTaskPriority(Math.round(Number(e.target.value) || 0))}
            className="w-16 px-1.5 py-0.5 bg-eve-input border border-eve-border rounded-sm text-[11px] text-eve-text font-mono"
            title={t("industryLedgerTaskBoardBulkPriorityTitle")}
          />
          <button
            type="button"
            onClick={() => { void handleBulkSetLedgerTaskPriority(bulkLedgerTaskPriority); }}
            disabled={updatingLedgerTasksBulk || selectedLedgerTaskIDs.length === 0}
            className="px-1.5 py-0.5 border border-fuchsia-500/40 text-fuchsia-300 rounded-sm hover:bg-fuchsia-500/10 disabled:opacity-50"
          >
            {t("industryLedgerTaskBoardPriorityShort")}
          </button>
          <button
            type="button"
            onClick={() => { void handleBulkSetLedgerTaskStatus("active"); }}
            disabled={updatingLedgerTasksBulk || selectedLedgerTaskIDs.length === 0}
            className="px-1.5 py-0.5 border border-blue-500/40 text-blue-300 rounded-sm hover:bg-blue-500/10 disabled:opacity-50"
          >
            {t("industryLedgerSetActive")}
          </button>
          <button
            type="button"
            onClick={() => { void handleBulkSetLedgerTaskStatus("paused"); }}
            disabled={updatingLedgerTasksBulk || selectedLedgerTaskIDs.length === 0}
            className="px-1.5 py-0.5 border border-indigo-500/40 text-indigo-300 rounded-sm hover:bg-indigo-500/10 disabled:opacity-50"
          >
            {t("industryLedgerTaskBoardFreeze")}
          </button>
          <button
            type="button"
            onClick={() => { void handleBulkSetLedgerTaskStatus("completed"); }}
            disabled={updatingLedgerTasksBulk || selectedLedgerTaskIDs.length === 0}
            className="px-1.5 py-0.5 border border-emerald-500/40 text-emerald-300 rounded-sm hover:bg-emerald-500/10 disabled:opacity-50"
          >
            {t("industryLedgerTaskBoardComplete")}
          </button>
          <button
            type="button"
            onClick={() => setSelectedLedgerTaskIDs([])}
            disabled={selectedLedgerTaskIDs.length === 0}
            className="px-1.5 py-0.5 border border-eve-border text-eve-dim rounded-sm hover:text-eve-accent hover:border-eve-accent/40 disabled:opacity-50"
          >
            {t("industryLedgerClearSelection")}
          </button>
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-[11px]">
          <thead className="sticky top-0 bg-eve-dark z-10">
            <tr className="text-eve-dim uppercase tracking-wider border-b border-eve-border/60 text-[10px]">
              <th className="px-1.5 py-1 text-left w-6">
                <input
                  type="checkbox"
                  checked={allVisibleLedgerTasksSelected}
                  onChange={(e) => handleSelectAllVisibleLedgerTasks(e.target.checked)}
                  className="accent-eve-accent"
                />
              </th>
              <th className="px-1.5 py-1 text-left w-4" />
              <th className="px-1.5 py-1 text-left">{t("industryLedgerTask")}</th>
              <th className="px-1.5 py-1 text-left">{t("industryLedgerActivity")}</th>
              <th className="px-1.5 py-1 text-right">{t("industryLedgerRuns")}</th>
              <th className="px-1.5 py-1 text-right">{t("industryLedgerTaskBoardPriority")}</th>
              {/* "State" = task lifecycle (planned/active/paused/etc);
                  "Status" = workflow-blocker health (ready/partial/blocked).
                  Separate columns so both readouts are available at a glance. */}
              <th className="px-1.5 py-1 text-left">State</th>
              <th className="px-1.5 py-1 text-left">Status</th>
              <th className="px-1.5 py-1 text-left">{t("industryLedgerTaskBoardWindow")}</th>
              <th className="px-1.5 py-1 text-right">{t("industryLedgerActions")}</th>
            </tr>
          </thead>
          <tbody>
            {sortedTasks.map((task) => {
              const block = blockByTask.get(task.id);
              const level: TaskBlockLevel = block?.level ?? "unknown";
              const isExpanded = expanded.has(task.id);
              const taskJobs = jobsByTaskID.get(task.id) ?? [];
              const taskMaterials = materialsByTaskID.get(task.id) ?? [];
              const bpTypeID = taskConstraintNumber(task.constraints, "blueprint_type_id");
              const bpRecord = bpTypeID > 0 ? blueprintByTypeID.get(bpTypeID) : undefined;
              const bpME = taskConstraintNumber(task.constraints, "me");
              const bpTE = taskConstraintNumber(task.constraints, "te");
              const hasExpandable = taskJobs.length > 0 || taskMaterials.length > 0 || bpTypeID > 0;
              const parentID = taskDependencyBoard.parent_by_task[task.id] ?? 0;
              const depth = taskDependencyBoard.depth_by_task[task.id] ?? 1;
              const isCP = taskDependencyBoard.critical_task_ids.has(task.id);
              return (
                <Fragment key={`task-${task.id}`}>
                  <tr className="border-b border-eve-border/30 hover:bg-eve-accent/5">
                    <td className="px-1.5 py-1 text-eve-dim">
                      <input
                        type="checkbox"
                        checked={selectedLedgerTaskIDSet.has(task.id)}
                        onChange={(e) => toggleLedgerTaskSelection(task.id, e.target.checked)}
                        className="accent-eve-accent"
                      />
                    </td>
                    <td className="px-1.5 py-1">
                      <button
                        type="button"
                        onClick={() => toggleExpanded(task.id)}
                        disabled={!hasExpandable}
                        className="text-eve-dim hover:text-eve-accent disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                        title={
                          !hasExpandable
                            ? "No requirements or jobs bound to this task"
                            : `${taskMaterials.length} material${taskMaterials.length === 1 ? "" : "s"}, ${taskJobs.length} job${taskJobs.length === 1 ? "" : "s"}`
                        }
                      >
                        {isExpanded ? "▾" : "▸"}
                      </button>
                    </td>
                    <td className="px-1.5 py-1 text-eve-text">
                      <div className="truncate">
                        {(() => {
                          // Highlight the decryptor suffix that stepLabel
                          // appended for invention tasks — same sky-300 the
                          // Scanner uses for its per-row decryptor chip so the
                          // pairing pops visually and matches "[Accelerant]"-
                          // style tags there.
                          const { base, decryptor } = splitTaskDecryptorSuffix(task.name || "", task.activity);
                          if (!decryptor) return base;
                          return (
                            <>
                              {base}
                              <span className="text-eve-dim"> · </span>
                              <span className="text-sky-300">{decryptor}</span>
                            </>
                          );
                        })()}
                      </div>
                      <div className="text-[10px] text-eve-dim">
                        #{task.id}
                        {parentID > 0 && (
                          <span className="ml-2 text-eve-dim">
                            ← <span className={taskDependencyBoard.parent_missing_by_task[task.id] ? "text-yellow-300" : "text-eve-dim"}>#{parentID}</span>
                          </span>
                        )}
                        <span className="ml-2 font-mono">d{depth}</span>
                        {isCP && (
                          <span className="ml-1 px-1 py-0.5 text-[9px] uppercase rounded-sm border border-fuchsia-500/40 text-fuchsia-300 bg-fuchsia-500/10">
                            CP
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-1.5 py-1 text-eve-dim">{task.activity}</td>
                    <td className="px-1.5 py-1 text-right text-eve-accent font-mono">{task.target_runs || 0}</td>
                    <td className="px-1.5 py-1 text-right text-eve-dim font-mono">{task.priority || 0}</td>
                    <td className="px-1.5 py-1">
                      <span className={`px-1.5 py-0.5 text-[10px] uppercase rounded-sm border ${industryTaskStatusClass(task.status)}`}>
                        {task.status}
                      </span>
                    </td>
                    <td className="px-1.5 py-1">
                      <span
                        className={`px-1.5 py-0.5 text-[10px] uppercase rounded-sm border font-bold cursor-help ${BLOCK_PILL_CLASSES[level]}`}
                        title={block?.reason ?? ""}
                      >
                        {level === "soft" && (block?.prereqPending ?? 0) > 0
                          ? "WAITING"
                          : BLOCK_PILL_LABELS[level]}
                      </span>
                    </td>
                    <td className="px-1.5 py-1 text-eve-dim whitespace-nowrap text-[10px]">
                      {formatUtcShort(task.planned_start)} – {formatUtcShort(task.planned_end)}
                    </td>
                    <td className="px-1.5 py-1 text-right">
                      <div className="inline-flex gap-1">
                        <button
                          type="button"
                          onClick={() => { void handleSetLedgerTaskPriority(task.id, (task.priority || 0) + 10); }}
                          disabled={updatingLedgerTaskId === task.id || updatingLedgerTasksBulk}
                          className="px-1 py-0.5 text-[10px] border border-fuchsia-500/40 text-fuchsia-300 rounded-sm hover:bg-fuchsia-500/10 disabled:opacity-50"
                          title={t("industryLedgerTaskBoardPriorityUpTitle")}
                        >
                          +P
                        </button>
                        <button
                          type="button"
                          onClick={() => { void handleSetLedgerTaskPriority(task.id, (task.priority || 0) - 10); }}
                          disabled={updatingLedgerTaskId === task.id || updatingLedgerTasksBulk}
                          className="px-1 py-0.5 text-[10px] border border-fuchsia-500/40 text-fuchsia-300 rounded-sm hover:bg-fuchsia-500/10 disabled:opacity-50"
                          title={t("industryLedgerTaskBoardPriorityDownTitle")}
                        >
                          -P
                        </button>
                        {task.status !== "active" && task.status !== "completed" && task.status !== "cancelled" && (
                          <button
                            type="button"
                            onClick={() => { void handleSetLedgerTaskStatus(task.id, "active"); }}
                            disabled={updatingLedgerTaskId === task.id || updatingLedgerTasksBulk}
                            className="px-1 py-0.5 text-[10px] border border-blue-500/40 text-blue-300 rounded-sm hover:bg-blue-500/10 disabled:opacity-50"
                          >
                            {t("industryLedgerSetActive")}
                          </button>
                        )}
                        {task.status === "paused" ? (
                          <button
                            type="button"
                            onClick={() => { void handleSetLedgerTaskStatus(task.id, "ready"); }}
                            disabled={updatingLedgerTaskId === task.id || updatingLedgerTasksBulk}
                            className="px-1 py-0.5 text-[10px] border border-cyan-500/40 text-cyan-300 rounded-sm hover:bg-cyan-500/10 disabled:opacity-50"
                          >
                            {t("industryLedgerTaskBoardUnfreeze")}
                          </button>
                        ) : (
                          task.status !== "completed" && task.status !== "cancelled" && (
                            <button
                              type="button"
                              onClick={() => { void handleSetLedgerTaskStatus(task.id, "paused"); }}
                              disabled={updatingLedgerTaskId === task.id || updatingLedgerTasksBulk}
                              className="px-1 py-0.5 text-[10px] border border-indigo-500/40 text-indigo-300 rounded-sm hover:bg-indigo-500/10 disabled:opacity-50"
                            >
                              {t("industryLedgerTaskBoardFreeze")}
                            </button>
                          )
                        )}
                        {task.status !== "completed" && task.status !== "cancelled" && (
                          <button
                            type="button"
                            onClick={() => { void handleSetLedgerTaskStatus(task.id, "completed"); }}
                            disabled={updatingLedgerTaskId === task.id || updatingLedgerTasksBulk}
                            className="px-1 py-0.5 text-[10px] border border-emerald-500/40 text-emerald-300 rounded-sm hover:bg-emerald-500/10 disabled:opacity-50"
                          >
                            {t("industryLedgerTaskBoardComplete")}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                  {isExpanded && hasExpandable && (
                    <tr className="border-b border-eve-border/30 bg-eve-dark/40">
                      <td colSpan={10} className="px-3 py-2 space-y-3">
                        {/* Blueprint required — comes from task.constraints */}
                        {bpTypeID > 0 && (
                          <section>
                            <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1">
                              Blueprint required
                            </div>
                            <div className="text-[11px] flex items-center flex-wrap gap-x-3 gap-y-1">
                              <span className="text-eve-text">
                                {bpRecord?.blueprint_name || `Type ${bpTypeID}`}
                              </span>
                              <span className={`px-1 py-0.5 text-[9px] uppercase rounded-sm border ${
                                bpRecord?.is_bpo
                                  ? "border-emerald-500/60 text-emerald-300"
                                  : "border-amber-500/60 text-amber-300"
                              }`}>
                                {bpRecord ? (bpRecord.is_bpo ? "BPO" : "BPC") : "unknown"}
                              </span>
                              <span className="text-eve-dim">
                                ME <span className="font-mono text-eve-text">{bpME}</span>
                                {" · "}
                                TE <span className="font-mono text-eve-text">{bpTE}</span>
                              </span>
                              {bpRecord && !bpRecord.is_bpo && (
                                <span className="text-eve-dim">
                                  runs left <span className="font-mono text-eve-text">{bpRecord.available_runs}</span>
                                </span>
                              )}
                              {bpRecord?.quantity !== undefined && (
                                <span className="text-eve-dim">
                                  qty <span className="font-mono text-eve-text">{bpRecord.quantity}</span>
                                </span>
                              )}
                              {!bpRecord && (
                                <span className="text-red-300 text-[10px] uppercase">
                                  not in pool — sync BP or add manually
                                </span>
                              )}
                            </div>
                          </section>
                        )}

                        {/* Task-scoped materials */}
                        {taskMaterials.length > 0 && (
                          <section>
                            <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1">
                              Materials · {taskMaterials.length}
                            </div>
                            {/*
                              table-fixed with an explicit width on every real
                              column plus a trailing spacer. Without the spacer
                              the name column absorbs all the slack of a very
                              wide row and throws the numbers to the far right
                              edge, yards away from the material they describe.
                              Everything is left-aligned and packed against the
                              name; font-mono keeps the digits in line.
                            */}
                            <table className="w-full text-[10px] table-fixed">
                              <thead className="text-eve-dim uppercase tracking-wider">
                                <tr>
                                  <th className="px-1.5 py-0.5 text-left w-56">Material</th>
                                  <th className="px-1.5 py-0.5 text-left w-24">Need</th>
                                  <th className="px-1.5 py-0.5 text-left w-24">Have</th>
                                  <th className="px-1.5 py-0.5 text-left w-24">Missing</th>
                                  <th className="px-1.5 py-0.5 text-left w-20">Status</th>
                                  <th className="px-1.5 py-0.5" />
                                </tr>
                              </thead>
                              <tbody>
                                {taskMaterials.map((m) => {
                                  const need = Math.max(0, m.required_qty ?? 0);
                                  const have = Math.max(0, m.available_qty ?? 0);
                                  // Same in-stock semantic as the footer:
                                  // covered = fully stocked; partial = some
                                  // in stock; missing = zero on hand.
                                  const status =
                                    have >= need ? "COVERED" : have > 0 ? "PARTIAL" : "MISSING";
                                  const statusClass =
                                    status === "COVERED"
                                      ? "text-emerald-300"
                                      : status === "PARTIAL"
                                        ? "text-amber-300"
                                        : "text-red-300";
                                  const missing = Math.max(0, need - have);
                                  return (
                                    <tr key={`tm-${task.id}-${m.type_id}`} className="border-t border-eve-border/20">
                                      {/* title so a truncated name is still
                                          readable on hover — table-fixed means
                                          truncate now actually bites. */}
                                      <td
                                        className="px-1.5 py-0.5 truncate text-eve-text"
                                        title={m.type_name || `Type ${m.type_id}`}
                                      >
                                        {m.type_name || `Type ${m.type_id}`}
                                      </td>
                                      <td className="px-1.5 py-0.5 font-mono">{need.toLocaleString()}</td>
                                      <td className="px-1.5 py-0.5 font-mono text-eve-dim">{have.toLocaleString()}</td>
                                      <td className={`px-1.5 py-0.5 font-mono ${missing > 0 ? "text-red-300" : "text-eve-dim"}`}>
                                        {missing > 0 ? missing.toLocaleString() : "—"}
                                      </td>
                                      <td className={`px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${statusClass}`}>
                                        {status}
                                      </td>
                                      <td />
                                    </tr>
                                  );
                                })}
                              </tbody>
                            </table>
                          </section>
                        )}

                        {/* Bound EVE jobs (task_id-linked) */}
                        {taskJobs.length > 0 && (
                          <section>
                            <div className="text-[10px] uppercase tracking-wider text-eve-dim mb-1">
                              Jobs · {taskJobs.length}
                            </div>
                            <table className="w-full text-[10px]">
                              <thead className="text-eve-dim uppercase tracking-wider">
                                <tr>
                                  {/* Same packing as the materials table above:
                                      widths on the real columns, a w-full
                                      spacer at the end to eat the slack. */}
                                  <th className="px-1.5 py-0.5 text-left w-24">{t("industryLedgerJob")}</th>
                                  <th className="px-1.5 py-0.5 text-left w-16">{t("industryLedgerRuns")}</th>
                                  <th className="px-1.5 py-0.5 text-left w-24">{t("industryLedgerCost")}</th>
                                  <th className="px-1.5 py-0.5 text-left w-24">{t("industryLedgerStatus")}</th>
                                  <th className="px-1.5 py-0.5 text-left w-28">{t("industryLedgerUpdated")}</th>
                                  <th className="px-1.5 py-0.5 text-left">{t("industryLedgerActions")}</th>
                                  <th className="px-1.5 py-0.5 w-full" />
                                </tr>
                              </thead>
                              <tbody>
                                {taskJobs.map((entry) => (
                              <tr key={`job-${entry.job_id}`} className="border-t border-eve-border/20">
                                <td className="px-1.5 py-0.5 text-eve-dim">#{entry.job_id}</td>
                                <td className="px-1.5 py-0.5 text-eve-accent font-mono">{entry.runs}</td>
                                <td className="px-1.5 py-0.5 text-eve-dim font-mono">{formatISK(entry.cost_isk || 0)}</td>
                                <td className="px-1.5 py-0.5">
                                  <span className={`px-1.5 py-0.5 text-[10px] uppercase rounded-sm border ${industryJobStatusClass(entry.status)}`}>
                                    {entry.status}
                                  </span>
                                </td>
                                <td className="px-1.5 py-0.5 text-eve-dim whitespace-nowrap">{formatUtcShort(entry.updated_at)}</td>
                                <td className="px-1.5 py-0.5">
                                  <div className="inline-flex gap-1">
                                    {entry.status !== "active" && entry.status !== "completed" && entry.status !== "cancelled" && (
                                      <button
                                        type="button"
                                        onClick={() => { void handleSetLedgerJobStatus(entry.job_id, "active"); }}
                                        disabled={updatingLedgerJobId === entry.job_id || updatingLedgerJobsBulk}
                                        className="px-1.5 py-0.5 text-[10px] border border-blue-500/40 text-blue-300 rounded-sm hover:bg-blue-500/10 disabled:opacity-50"
                                      >
                                        {t("industryLedgerSetActive")}
                                      </button>
                                    )}
                                    {entry.status === "paused" ? (
                                      <button
                                        type="button"
                                        onClick={() => { void handleSetLedgerJobStatus(entry.job_id, "queued"); }}
                                        disabled={updatingLedgerJobId === entry.job_id || updatingLedgerJobsBulk}
                                        className="px-1.5 py-0.5 text-[10px] border border-cyan-500/40 text-cyan-300 rounded-sm hover:bg-cyan-500/10 disabled:opacity-50"
                                      >
                                        {t("industryLedgerResume")}
                                      </button>
                                    ) : (
                                      entry.status !== "completed" && entry.status !== "cancelled" && (
                                        <button
                                          type="button"
                                          onClick={() => { void handleSetLedgerJobStatus(entry.job_id, "paused"); }}
                                          disabled={updatingLedgerJobId === entry.job_id || updatingLedgerJobsBulk}
                                          className="px-1.5 py-0.5 text-[10px] border border-indigo-500/40 text-indigo-300 rounded-sm hover:bg-indigo-500/10 disabled:opacity-50"
                                        >
                                          {t("industryLedgerPause")}
                                        </button>
                                      )
                                    )}
                                    {entry.status !== "completed" && entry.status !== "cancelled" && (
                                      <button
                                        type="button"
                                        onClick={() => { void handleSetLedgerJobStatus(entry.job_id, "completed"); }}
                                        disabled={updatingLedgerJobId === entry.job_id || updatingLedgerJobsBulk}
                                        className="px-1.5 py-0.5 text-[10px] border border-emerald-500/40 text-emerald-300 rounded-sm hover:bg-emerald-500/10 disabled:opacity-50"
                                      >
                                        {t("industryLedgerSetCompleted")}
                                      </button>
                                    )}
                                  </div>
                                </td>
                                <td />
                              </tr>
                            ))}
                              </tbody>
                            </table>
                          </section>
                        )}
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
            {sortedTasks.length === 0 && (
              <tr>
                <td colSpan={10} className="px-2 py-4 text-center text-eve-dim text-xs">
                  {t("industryLedgerTaskBoardNoTasks")}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
