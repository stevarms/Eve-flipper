import type {
  IndustryCoverageMaterialRow,
  IndustryMaterialDiff,
  IndustryPlanPatch,
  IndustryProjectSnapshot,
  IndustryTaskRecord,
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
  /**
   * Many-to-many prerequisites. `parent_by_task` is the persisted scalar
   * column (one edge per task, best-effort); this is the real graph, merging
   * that column with edges inferred from the committed rows. Callers asking
   * "can this start?" want this one.
   */
  prereqs_by_task: Record<number, number[]>;
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
 * Recover the prerequisite graph from committed task rows.
 *
 * The planner only persists a scalar `parent_task_id`, and older projects
 * don't have even that — so the edges that matter most (the
 * copy → invention → manufacturing chain) have to be re-derived from what
 * every task already carries: its `product_type_id` and its
 * `constraints.blueprint_type_id`.
 *
 *   invention → manufacturing   mfg's blueprint IS the invention's product
 *                               (invention emits the T2 *blueprint* typeID,
 *                               internal/engine/industry.go:1693)
 *   copy      → invention       invention's source blueprint IS the copy's
 *                               product (copies keep the BPO typeID, :1894)
 *   research  → manufacturing   research's product IS the blueprint itself
 *                               (:1991)
 *
 * Cancelled tasks are never prerequisites: cancelling the invention step is
 * exactly how the user says "I already have that blueprint".
 *
 * Component → parent edges can't be recovered this way — a parent's material
 * rows are flattened to base materials and never mention the intermediate —
 * so those come from `parent_task_id`, written at plan time.
 */
export function inferTaskPrerequisites(
  snapshot: IndustryProjectSnapshot | null,
): Record<number, number[]> {
  const edges: Record<number, number[]> = {};
  if (!snapshot) return edges;

  const add = (childID: number, parentID: number) => {
    if (!childID || !parentID || childID === parentID) return;
    const list = (edges[childID] ??= []);
    if (!list.includes(parentID)) list.push(parentID);
  };

  // Producers indexed by what they hand downstream. A cancelled producer is
  // omitted so it can't block anything.
  const byProduct = new Map<string, number[]>();
  for (const t of snapshot.tasks) {
    if ((t.status || "") === "cancelled") continue;
    const product = Number(t.product_type_id) || 0;
    if (product <= 0) continue;
    const key = `${(t.activity || "").toLowerCase()}:${product}`;
    const list = byProduct.get(key);
    if (list) list.push(t.id);
    else byProduct.set(key, [t.id]);
  }
  const producersOf = (activity: string, typeID: number) =>
    byProduct.get(`${activity}:${typeID}`) ?? [];

  for (const t of snapshot.tasks) {
    if ((t.status || "") === "cancelled") continue;
    const activity = (t.activity || "").toLowerCase();
    const blueprintID = taskConstraintNumber(t.constraints, "blueprint_type_id");
    if (blueprintID <= 0) continue;

    if (activity === "manufacturing") {
      for (const id of producersOf("invention", blueprintID)) add(t.id, id);
      for (const id of producersOf("research_material", blueprintID)) add(t.id, id);
      for (const id of producersOf("research_time", blueprintID)) add(t.id, id);
    } else if (activity === "invention") {
      for (const id of producersOf("copy", blueprintID)) add(t.id, id);
    }
  }

  return edges;
}

/**
 * Per-task block status derived from the committed plan snapshot.
 *
 *   ready    all materials fully covered AND every prerequisite completed
 *   soft     every material has some stock but at least one is short, OR a
 *            prerequisite task hasn't completed yet
 *   hard     at least one required material is completely absent
 *   unknown  task has no material plan rows (rare — legacy pre-plan tasks)
 *
 * A pending prerequisite is deliberately SOFT, not hard. The graph is
 * inferred (see inferTaskPrerequisites) and the user may legitimately know
 * better — they might already hold the invented BPC, or want to stage the
 * mfg job anyway. Amber says "check this" without refusing to offer it.
 */
export type TaskBlockLevel = "ready" | "soft" | "hard" | "unknown";

export interface TaskBlockStatus {
  level: TaskBlockLevel;
  materialsMissingCount: number;
  materialsHardCount: number;
  /** Task IDs of incomplete prerequisites, nearest first. */
  parentBlocking: number[];
  /** Convenience count of parentBlocking, for callers that only need "is it waiting". */
  prereqPending: number;
  reason: string;
}

/**
 * Walk the prerequisite DAG from `taskID` and collect every upstream task
 * that hasn't completed. Cancelled prerequisites don't block — a cancelled
 * invention step means the user sourced the blueprint some other way.
 */
function collectPendingPrereqs(
  taskID: number,
  prereqsByTask: Record<number, number[]>,
  statusByID: Record<number, string>,
): number[] {
  const pending: number[] = [];
  const seen = new Set<number>([taskID]);
  const queue = [...(prereqsByTask[taskID] ?? [])];
  while (queue.length > 0) {
    const cursor = queue.shift() as number;
    if (seen.has(cursor)) continue;
    seen.add(cursor);
    const status = statusByID[cursor];
    if (status === undefined) continue;
    if (status !== "completed" && status !== "cancelled") pending.push(cursor);
    for (const up of prereqsByTask[cursor] ?? []) if (!seen.has(up)) queue.push(up);
  }
  return pending;
}

export function deriveTaskBlockStatus(
  taskID: number,
  snapshot: IndustryProjectSnapshot | null,
  prereqsByTask: Record<number, number[]>,
): TaskBlockStatus {
  if (!snapshot) {
    return {
      level: "unknown",
      materialsMissingCount: 0,
      materialsHardCount: 0,
      parentBlocking: [],
      prereqPending: 0,
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
  // "Can I actually start work right now?" is the block question.
  // Reads look at available_qty ONLY — buy_qty / build_qty describe plan
  // intent (what the pipeline says to source), but they don't put material
  // in your hangar. A material with 0 in stock blocks the task even if the
  // plan says "buy 5 of these".
  const materials = snapshot.materials.filter((m) => m.task_id === taskID);
  let hardCount = 0;        // available === 0 (nothing to work with)
  let notStockedCount = 0;  // available < required (short)
  for (const m of materials) {
    const required = m.required_qty ?? 0;
    if (required <= 0) continue;
    const available = m.available_qty ?? 0;
    if (available >= required) continue;
    notStockedCount++;
    // A row with build_qty is sourced from another task in this project, not
    // from the market. Counting it hard would paint every parent assembly
    // red for components it is about to build — and the prerequisite walk
    // below already reports that as "waiting on", which is the useful framing.
    if (available <= 0 && (m.build_qty ?? 0) <= 0) hardCount++;
  }

  // Prerequisite blocking: walk the upstream DAG collecting anything that
  // hasn't finished. A single task can have several prerequisites (an
  // assembly waits on every component), so this is a BFS, not a parent walk.
  const statusByID: Record<number, string> = {};
  const nameByID: Record<number, string> = {};
  for (const t of snapshot.tasks) {
    statusByID[t.id] = t.status || "planned";
    nameByID[t.id] = t.name || `task ${t.id}`;
  }
  const parentBlocking = collectPendingPrereqs(taskID, prereqsByTask, statusByID);
  // Name the nearest one or two — "waiting on 4 tasks" tells the user nothing
  // actionable, "waiting on: Invent Quake XL BP" tells them exactly what to
  // go install.
  const prereqReason = () => {
    const names = parentBlocking.slice(0, 2).map((id) => nameByID[id] ?? `task ${id}`);
    const more = parentBlocking.length - names.length;
    return `waiting on: ${names.join(", ")}${more > 0 ? ` +${more} more` : ""}`;
  };

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
  // Fallback: no per-task rows for this task (legacy projects with all
  // materials tagged task_id: 0). Falls back to project-level material_diff.
  if (materials.length === 0) {
    // Some activities legitimately have no material bill — copy jobs consume
    // only time (barring rare corner cases), and research_material /
    // research_time consume only time too. Falling back to project-level
    // material_diff for those wrongly reports them "hard blocked" whenever
    // the DOWNSTREAM mfg has any unmet material, which is user-visible as
    // "copy job says blocked but I have the BPO". For those activities,
    // block only on parent completion.
    const taskRec = snapshot.tasks.find((t) => t.id === taskID);
    const activity = (taskRec?.activity ?? "").toLowerCase();
    const noMaterialsByDesign =
      activity === "copy" || activity === "research_material" || activity === "research_time";
    if (noMaterialsByDesign) {
      if (parentBlocking.length > 0) {
        return {
          level: "soft",
          materialsMissingCount: 0,
          materialsHardCount: 0,
          parentBlocking,
          prereqPending: parentBlocking.length,
          reason: prereqReason(),
        };
      }
      return {
        level: "ready",
        materialsMissingCount: 0,
        materialsHardCount: 0,
        parentBlocking: [],
        prereqPending: 0,
        reason: `${activity} step — no materials to stage`,
      };
    }
    const projectDiff = snapshot.material_diff ?? [];
    let hardCountDiff = 0;
    let notStockedCount = 0;
    for (const m of projectDiff) {
      const required = m.required_qty ?? 0;
      if (required <= 0) continue;
      const available = m.available_qty ?? 0;
      if (available < required) notStockedCount++;
      if (available <= 0) hardCountDiff++;
    }
    if (projectDiff.length > 0) {
      if (hardCountDiff > 0) {
        const bits = [`${hardCountDiff} project material${hardCountDiff === 1 ? "" : "s"} with none in stock`];
        if (parentBlocking.length > 0) bits.push(prereqReason());
        return {
          level: "hard",
          materialsMissingCount: hardCountDiff,
          materialsHardCount: hardCountDiff,
          parentBlocking,
          prereqPending: parentBlocking.length,
          reason: bits.join(" · ") + " (project-level)",
        };
      }
      if (notStockedCount > 0 || parentBlocking.length > 0) {
        const bits: string[] = [];
        if (parentBlocking.length > 0) bits.push(prereqReason());
        if (notStockedCount > 0) {
          bits.push(`${notStockedCount} material${notStockedCount === 1 ? "" : "s"} partially stocked (project-level)`);
        }
        return {
          level: "soft",
          materialsMissingCount: notStockedCount,
          materialsHardCount: 0,
          parentBlocking,
          prereqPending: parentBlocking.length,
          reason: bits.join(" · "),
        };
      }
      return {
        level: "ready",
        materialsMissingCount: 0,
        materialsHardCount: 0,
        parentBlocking: [],
        prereqPending: 0,
        reason: "everything is in stock",
      };
    }
    if (parentBlocking.length === 0) {
      return {
        level: "unknown",
        materialsMissingCount: 0,
        materialsHardCount: 0,
        parentBlocking,
        prereqPending: 0,
        reason: "no material plan for this task",
      };
    }
  }

  if (hardCount > 0) {
    const bits = [`${hardCount} material${hardCount === 1 ? "" : "s"} with none in stock`];
    if (parentBlocking.length > 0) bits.push(prereqReason());
    return {
      level: "hard",
      materialsMissingCount: hardCount,
      materialsHardCount: hardCount,
      parentBlocking,
      prereqPending: parentBlocking.length,
      reason: bits.join(" · "),
    };
  }
  if (notStockedCount > 0 || parentBlocking.length > 0) {
    const bits: string[] = [];
    if (parentBlocking.length > 0) bits.push(prereqReason());
    if (notStockedCount > 0) {
      bits.push(`${notStockedCount} material${notStockedCount === 1 ? "" : "s"} partially stocked`);
    }
    return {
      level: "soft",
      materialsMissingCount: notStockedCount,
      materialsHardCount: 0,
      parentBlocking,
      prereqPending: parentBlocking.length,
      reason: bits.join(" · "),
    };
  }
  return {
    level: "ready",
    materialsMissingCount: 0,
    materialsHardCount: 0,
    parentBlocking: [],
    prereqPending: 0,
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
    // Match the "can I start work right now?" block semantic:
    //   covered  = available >= required (stocked, ready to go)
    //   partial  = some in stock but short (0 < available < required)
    //   missing  = nothing in stock (available === 0, even if plan says
    //              to buy — buy_qty is intent, not inventory)
    const status: IndustryCoverageMaterialRow["status"] =
      available >= required ? "covered" : available > 0 ? "partial" : "missing";
    return {
      type_id: m.type_id,
      type_name: m.type_name,
      required_qty: required,
      available_qty: available,
      missing_qty: missing,
      coverage_pct: coveragePct,
      status,
      unit_volume: m.unit_volume,
    };
  });
}

/**
 * Plan-time value rollup for a committed project.
 *
 * Revenue comes from the expected_unit_revenue / expected_output_qty snapped
 * onto tasks at commit time (see industryPlanPatch). Only the saleable output
 * task of each chain carries non-zero values, so a plain sum over every task
 * is the project total with no double-counting of intermediate components.
 *
 * Costs are read back off the committed rows rather than the plan-time cost
 * basis, so they track edits (rebalance, recalc-remaining, manual qty fixes)
 * that happen after the plan was applied:
 *   materialCost = Σ materials.unit_cost_isk × required_qty
 *   jobCost      = Σ jobs.cost_isk
 *
 * remainingBuyCost prices the *shortfall* — what's still to be acquired —
 * by joining material_diff.missing_qty against the per-type unit cost on the
 * material plan rows.
 */
export interface IndustryProjectValuation {
  expectedRevenue: number;
  materialCost: number;
  jobCost: number;
  totalCost: number;
  expectedProfit: number;
  /** profit / revenue, 0 when there's no revenue estimate. */
  marginPct: number;
  remainingBuyCost: number;
  /**
   * False for projects committed before expected-value capture existed —
   * the UI should say "not estimated" instead of showing a confident 0 ISK.
   */
  hasRevenueEstimate: boolean;
}

export function deriveProjectValuation(
  snapshot: IndustryProjectSnapshot | null | undefined,
): IndustryProjectValuation {
  const empty: IndustryProjectValuation = {
    expectedRevenue: 0,
    materialCost: 0,
    jobCost: 0,
    totalCost: 0,
    expectedProfit: 0,
    marginPct: 0,
    remainingBuyCost: 0,
    hasRevenueEstimate: false,
  };
  if (!snapshot) return empty;

  let expectedRevenue = 0;
  let hasRevenueEstimate = false;
  for (const task of snapshot.tasks ?? []) {
    if (task.status === "cancelled") continue;
    const qty = task.expected_output_qty ?? 0;
    const unit = task.expected_unit_revenue ?? 0;
    if (qty > 0 && unit > 0) {
      expectedRevenue += unit * qty;
      hasRevenueEstimate = true;
    }
  }

  let materialCost = 0;
  const unitCostByType = new Map<number, number>();
  for (const m of snapshot.materials ?? []) {
    const unit = m.unit_cost_isk ?? 0;
    materialCost += unit * Math.max(0, m.required_qty ?? 0);
    // Same type can appear on several tasks; keep the highest quoted unit
    // cost so the shortfall estimate errs toward over- rather than
    // under-budgeting.
    if (unit > (unitCostByType.get(m.type_id) ?? 0)) {
      unitCostByType.set(m.type_id, unit);
    }
  }

  let jobCost = 0;
  for (const j of snapshot.jobs ?? []) {
    if (j.status === "cancelled" || j.status === "failed") continue;
    jobCost += j.cost_isk ?? 0;
  }

  let remainingBuyCost = 0;
  for (const d of snapshot.material_diff ?? []) {
    const missing = Math.max(0, d.missing_qty ?? 0);
    if (missing <= 0) continue;
    remainingBuyCost += missing * (unitCostByType.get(d.type_id) ?? 0);
  }

  const totalCost = materialCost + jobCost;
  const expectedProfit = expectedRevenue - totalCost;
  return {
    expectedRevenue,
    materialCost,
    jobCost,
    totalCost,
    expectedProfit,
    marginPct: expectedRevenue > 0 ? expectedProfit / expectedRevenue : 0,
    remainingBuyCost,
    hasRevenueEstimate,
  };
}

/**
 * Canonical Operations ordering, shared by the runbook card and the task
 * list so "step 3 of 9" in one always points at the same row in the other.
 *
 * dep depth asc → activity prerequisite order → priority desc → id asc.
 * Prerequisites (copy/invention/reaction) bubble above their manufacturing
 * consumers even when parent links aren't populated, which is the norm for
 * scanner-committed plans where every task lands at depth 1.
 */
function operationsActivityOrder(activity: string): number {
  switch (activity) {
    case "copy":
      return 0;
    case "invention":
      return 1;
    case "reaction":
      return 2;
    case "manufacturing":
    default:
      return 3;
  }
}

export function sortOperationsTasks(
  tasks: IndustryTaskRecord[],
  depthByTask: Record<number, number>,
): IndustryTaskRecord[] {
  const arr = [...tasks];
  arr.sort((a, b) => {
    const da = depthByTask[a.id] ?? 1;
    const db = depthByTask[b.id] ?? 1;
    if (da !== db) return da - db;
    const aa = operationsActivityOrder(a.activity);
    const ab = operationsActivityOrder(b.activity);
    if (aa !== ab) return aa - ab;
    const pa = a.priority || 0;
    const pb = b.priority || 0;
    if (pa !== pb) return pb - pa;
    return a.id - b.id;
  });
  return arr;
}

/**
 * Split an Operations task name into its base label and the decryptor
 * suffix that stepLabel appends for invention ("invention Zealot Blueprint ·
 * Symmetry Decryptor"). Returns an empty decryptor for every other activity.
 */
export function splitTaskDecryptorSuffix(
  name: string,
  activity: string,
): { base: string; decryptor: string } {
  const sep = " · ";
  if (activity !== "invention") return { base: name, decryptor: "" };
  const idx = name.lastIndexOf(sep);
  if (idx < 0) return { base: name, decryptor: "" };
  return { base: name.slice(0, idx), decryptor: name.slice(idx + sep.length) };
}
