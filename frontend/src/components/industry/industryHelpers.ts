import type {
  IndustryCoverageMaterialRow,
  IndustryMaterialDiff,
  IndustryPlanPatch,
  IndustryProjectSnapshot,
} from "@/lib/types";

export type IndustryPlannerWarningSource = "preview" | "apply" | "gate";

export interface IndustryPlannerWarningEvent {
  id: number;
  source: IndustryPlannerWarningSource;
  message: string;
  created_at: string;
}

export interface IndustryTaskDependencyRow {
  child_id: number;
  child_name: string;
  child_status: string;
  parent_id: number;
  parent_name: string;
  parent_status: string;
  parent_missing: boolean;
}

export interface IndustryTaskDependencyBoard {
  total_tasks: number;
  total_edges: number;
  roots: number;
  leaves: number;
  max_depth: number;
  critical_path_sec: number;
  orphans: number;
  cycles: number;
  self_links: number;
  depth_by_task: Record<number, number>;
  parent_by_task: Record<number, number>;
  parent_missing_by_task: Record<number, boolean>;
  critical_task_ids: Set<number>;
  rows: IndustryTaskDependencyRow[];
}

export function formatDuration(seconds: number): string {
  if (seconds <= 0) return "—";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  const parts: string[] = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0) parts.push(`${h}h`);
  if (m > 0) parts.push(`${m}m`);
  if (parts.length === 0) parts.push(`${s}s`);
  return parts.join(" ");
}

export function formatUtcShort(value: string): string {
  const trimmed = value?.trim();
  if (!trimmed) return "—";
  const date = new Date(trimmed);
  if (Number.isNaN(date.getTime())) return trimmed;
  const yyyy = date.getUTCFullYear();
  const mm = String(date.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(date.getUTCDate()).padStart(2, "0");
  const hh = String(date.getUTCHours()).padStart(2, "0");
  const mi = String(date.getUTCMinutes()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd} ${hh}:${mi} UTC`;
}

export function industryJobStatusClass(status: string): string {
  switch (status) {
    case "completed":
      return "bg-emerald-500/20 text-emerald-400 border-emerald-500/30";
    case "active":
      return "bg-blue-500/20 text-blue-400 border-blue-500/30";
    case "queued":
    case "planned":
      return "bg-amber-500/20 text-amber-400 border-amber-500/30";
    case "paused":
      return "bg-indigo-500/20 text-indigo-400 border-indigo-500/30";
    case "failed":
      return "bg-red-500/20 text-red-400 border-red-500/30";
    case "cancelled":
      return "bg-eve-dim/20 text-eve-dim border-eve-dim/30";
    default:
      return "bg-eve-dim/20 text-eve-dim border-eve-dim/30";
  }
}

export function industryTaskStatusClass(status: string): string {
  switch (status) {
    case "completed":
      return "bg-emerald-500/20 text-emerald-400 border-emerald-500/30";
    case "active":
      return "bg-blue-500/20 text-blue-400 border-blue-500/30";
    case "ready":
      return "bg-amber-500/20 text-amber-400 border-amber-500/30";
    case "planned":
      return "bg-eve-dim/20 text-eve-dim border-eve-dim/30";
    case "blocked":
      return "bg-red-500/20 text-red-400 border-red-500/30";
    case "paused":
      return "bg-indigo-500/20 text-indigo-400 border-indigo-500/30";
    case "cancelled":
      return "bg-zinc-500/20 text-zinc-300 border-zinc-500/30";
    default:
      return "bg-eve-dim/20 text-eve-dim border-eve-dim/30";
  }
}

function stableSerialize(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) {
    return `[${value.map((item) => stableSerialize(item)).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return `{${keys.map((key) => `${JSON.stringify(key)}:${stableSerialize(record[key])}`).join(",")}}`;
}

export function planPatchSignature(patch: IndustryPlanPatch | null): string {
  if (!patch) return "";
  return stableSerialize(patch);
}

export function taskConstraintRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return { ...(value as Record<string, unknown>) };
}

export function taskConstraintNumber(value: unknown, key: string): number {
  const constraints = taskConstraintRecord(value);
  const raw = constraints[key];
  if (typeof raw === "number" && Number.isFinite(raw)) return raw;
  if (typeof raw === "string") {
    const parsed = Number(raw.trim());
    if (Number.isFinite(parsed)) return parsed;
  }
  return 0;
}

export function industryPlannerWarningSourceClass(source: IndustryPlannerWarningSource): string {
  switch (source) {
    case "preview":
      return "border-yellow-500/40 text-yellow-300 bg-yellow-500/10";
    case "apply":
      return "border-amber-500/40 text-amber-300 bg-amber-500/10";
    case "gate":
      return "border-red-500/40 text-red-300 bg-red-500/10";
    default:
      return "border-eve-border text-eve-dim bg-eve-dark/30";
  }
}

/**
 * Per-task block status derived from the committed plan snapshot.
 *
 *   ready    all materials fully covered AND all parent tasks completed
 *   soft     every material has some stock but at least one is short
 *   hard     at least one required material is completely absent, OR
 *            at least one parent task hasn't completed yet
 *   unknown  task has no material plan rows (rare — legacy pre-plan tasks)
 */
export type TaskBlockLevel = "ready" | "soft" | "hard" | "unknown";

export interface TaskBlockStatus {
  level: TaskBlockLevel;
  materialsMissingCount: number;
  materialsHardCount: number;
  parentBlocking: number[];
  reason: string;
}

export function deriveTaskBlockStatus(
  taskID: number,
  snapshot: IndustryProjectSnapshot | null,
  parentByTask: Record<number, number>,
): TaskBlockStatus {
  if (!snapshot) {
    return {
      level: "unknown",
      materialsMissingCount: 0,
      materialsHardCount: 0,
      parentBlocking: [],
      reason: "no snapshot loaded",
    };
  }

  // Per-task material plan rows carry a task_id — see IndustryMaterialPlanRecord.
  // The project-level material_diff is aggregated by type across all tasks,
  // so we use the raw plan rows here to answer per-task blocking.
  //
  // Three states (in escalating severity):
  //   HARD  — at least one material has missing_qty > 0 (plan can't source it)
  //   SOFT  — plan covers everything but at least one material isn't in stock
  //           right now (needs a market run or upstream build first)
  //   READY — every material's available_qty >= required_qty already
  const materials = snapshot.materials.filter((m) => m.task_id === taskID);
  let missingCount = 0;
  let notStockedCount = 0;
  for (const m of materials) {
    const required = m.required_qty ?? 0;
    if (required <= 0) continue;
    const available = m.available_qty ?? 0;
    const buy = m.buy_qty ?? 0;
    const build = m.build_qty ?? 0;
    const missing = Math.max(0, required - (available + buy + build));
    if (missing > 0) missingCount++;
    if (available < required) notStockedCount++;
  }

  // Parent-task blocking: walk parentByTask up until we hit a root,
  // collecting any parent whose status isn't completed.
  const parentBlocking: number[] = [];
  const tasksByID: Record<number, string> = {};
  for (const t of snapshot.tasks) {
    tasksByID[t.id] = t.status || "planned";
  }
  {
    let cursor = parentByTask[taskID] ?? 0;
    const seen = new Set<number>();
    while (cursor > 0 && !seen.has(cursor)) {
      seen.add(cursor);
      const parentStatus = tasksByID[cursor];
      if (parentStatus && parentStatus !== "completed") {
        parentBlocking.push(cursor);
      }
      cursor = parentByTask[cursor] ?? 0;
    }
  }

  // Fallback: when no per-task material rows are present (backend applied
  // the plan without tagging task_id on IndustryMaterialPlanRecord rows —
  // observed on projects committed via the inline scanner-batch flow),
  // fall back to project-level material_diff. Every task shares the same
  // answer, but it's correct: "the project has these gaps globally."
  //
  // Three states, in escalating severity:
  //   HARD  — at least one material has missing_qty > 0 (plan can't source it)
  //   SOFT  — plan covers everything but at least one material isn't in stock
  //           yet (buy_qty > 0 or build_qty > 0 → market run or upstream build
  //           required first)
  //   READY — every material's available_qty >= required_qty right now
  if (materials.length === 0) {
    const projectDiff = snapshot.material_diff ?? [];
    let missingCountDiff = 0;
    let notStockedCount = 0;
    for (const m of projectDiff) {
      const required = m.required_qty ?? 0;
      if (required <= 0) continue;
      if ((m.missing_qty ?? 0) > 0) missingCountDiff++;
      if ((m.available_qty ?? 0) < required) notStockedCount++;
    }
    if (projectDiff.length > 0) {
      const hardOnParents = parentBlocking.length > 0;
      if (missingCountDiff > 0 || hardOnParents) {
        const bits: string[] = [];
        if (missingCountDiff > 0) bits.push(`${missingCountDiff} project material${missingCountDiff === 1 ? "" : "s"} missing`);
        if (hardOnParents) bits.push(`${parentBlocking.length} parent task${parentBlocking.length === 1 ? "" : "s"} incomplete`);
        return {
          level: "hard",
          materialsMissingCount: missingCountDiff,
          materialsHardCount: missingCountDiff,
          parentBlocking,
          reason: bits.join(" · ") + " (project-level)",
        };
      }
      if (notStockedCount > 0) {
        return {
          level: "soft",
          materialsMissingCount: 0,
          materialsHardCount: 0,
          parentBlocking: [],
          reason: `${notStockedCount} material${notStockedCount === 1 ? "" : "s"} need buying/building first (project-level)`,
        };
      }
      return {
        level: "ready",
        materialsMissingCount: 0,
        materialsHardCount: 0,
        parentBlocking: [],
        reason: "everything is in stock",
      };
    }
    if (parentBlocking.length === 0) {
      return {
        level: "unknown",
        materialsMissingCount: 0,
        materialsHardCount: 0,
        parentBlocking,
        reason: "no material plan for this task",
      };
    }
  }

  const hardOnParents = parentBlocking.length > 0;
  if (missingCount > 0 || hardOnParents) {
    const bits: string[] = [];
    if (missingCount > 0) bits.push(`${missingCount} material${missingCount === 1 ? "" : "s"} missing`);
    if (hardOnParents) bits.push(`${parentBlocking.length} parent task${parentBlocking.length === 1 ? "" : "s"} incomplete`);
    return {
      level: "hard",
      materialsMissingCount: missingCount,
      materialsHardCount: missingCount,
      parentBlocking,
      reason: bits.join(" · "),
    };
  }
  if (notStockedCount > 0) {
    return {
      level: "soft",
      materialsMissingCount: 0,
      materialsHardCount: 0,
      parentBlocking: [],
      reason: `${notStockedCount} material${notStockedCount === 1 ? "" : "s"} need buying/building first`,
    };
  }
  return {
    level: "ready",
    materialsMissingCount: 0,
    materialsHardCount: 0,
    parentBlocking: [],
    reason: "everything is in stock",
  };
}

/**
 * Adapt the project-level IndustryMaterialDiff[] into the shape the shared
 * MaterialsPreviewPanel expects. Same data, different derivation:
 *   coverage_pct = available / required (clamped 0..1)
 *   status       = "covered" if missing===0, else "partial" if available>0, else "missing"
 */
export function materialDiffToCoverageRows(
  diff: IndustryMaterialDiff[] | undefined | null,
): IndustryCoverageMaterialRow[] {
  if (!Array.isArray(diff) || diff.length === 0) return [];
  return diff.map((m) => {
    const required = Math.max(0, m.required_qty ?? 0);
    const available = Math.max(0, m.available_qty ?? 0);
    const missing = Math.max(0, m.missing_qty ?? 0);
    const coveragePct = required > 0 ? Math.max(0, Math.min(1, available / required)) : 1;
    // Match the block-derivation semantic — the preview pill should read
    // the same way the task block pill does:
    //   covered  = every unit already in stock (available >= required)
    //   partial  = plan covers the gap (buy or build) but not stocked yet
    //   missing  = plan can't source it (missing_qty > 0)
    const status: IndustryCoverageMaterialRow["status"] =
      missing > 0 ? "missing" : available >= required ? "covered" : "partial";
    return {
      type_id: m.type_id,
      type_name: m.type_name,
      required_qty: required,
      available_qty: available,
      missing_qty: missing,
      coverage_pct: coveragePct,
      status,
    };
  });
}
