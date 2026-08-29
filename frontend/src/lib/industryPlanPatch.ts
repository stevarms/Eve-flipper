// Pure helpers for constructing and merging IndustryPlanPatch payloads.
//
// buildIndustryPlanPatch is the extracted body of buildAutoPlanPatch from
// IndustryTab.tsx — same emission rules, but with every closed-over React
// value promoted to an explicit input. Callers include the Analysis tab's
// "Seed draft" flow AND the Scanner's batch "Add to project" flow, so this
// module needs to stay side-effect-free.
//
// mergeIndustryPlanPatches folds several patches (e.g. one per Scanner row)
// into a single patch that the planner can apply as one plan.

import type {
  IndustryActivityStep,
  IndustryAnalysis,
  IndustryBlueprintPoolInput,
  IndustryCoverageResult,
  IndustryJobPlanInput,
  IndustryMaterialPlanInput,
  IndustryPlanPatch,
  IndustryTaskPlanInput,
  MaterialNode,
} from "./types";

export interface BuildIndustryPlanPatchInput {
  result: IndustryAnalysis;
  productTypeID: number;
  productName: string;
  runs: number;
  me: number;
  te: number;
  systemName: string;
  stationID: number;
  ownBlueprint: boolean;
  replace: boolean;
}

function stepRuns(step: IndustryActivityStep): number {
  if (step.activity === "invention" && step.expected_attempts) {
    return Math.max(1, Math.ceil(step.expected_attempts));
  }
  return Math.max(1, Math.ceil(step.runs || 1));
}

function stepLabel(step: IndustryActivityStep): string {
  const activity = step.activity || "industry";
  const product = step.product_name || `Type ${step.product_type_id}`;
  const base = `${activity} ${product}`;
  // For invention, tag the decryptor onto the label so the row header shows
  // "invention Zealot Blueprint · Symmetry Decryptor" — the user knows which
  // decryptor to grab in-game without expanding the task.
  if (step.activity === "invention" && step.decryptor_name) {
    return `${base} · ${step.decryptor_name}`;
  }
  return base;
}

/**
 * Component → parent edges, keyed by product type ID.
 *
 * These are the edges that cannot be recovered after the fact: a parent
 * task's material rows come from the flattened base-material bill and never
 * mention the intermediate component it consumes, so unless we write them
 * here they're gone. The invention/copy/research chain, by contrast, is
 * re-derivable from committed rows (see inferTaskPrerequisites), which is why
 * the scalar parent_task_id column is spent on components first.
 */
function componentEdges(root: MaterialNode | null | undefined): Map<number, number[]> {
  const edges = new Map<number, number[]>();
  if (!root) return edges;
  const seen = new Set<number>();
  const walk = (node: MaterialNode) => {
    if (seen.has(node.type_id)) return;
    seen.add(node.type_id);
    const built = (node.children ?? []).filter((c) => !c.is_base && c.should_build && c.blueprint);
    if (built.length > 0) {
      edges.set(node.type_id, built.map((c) => c.type_id));
    }
    for (const child of node.children ?? []) walk(child);
  };
  walk(root);
  return edges;
}

interface DirectBOMEntry {
  typeID: number;
  typeName: string;
  requiredQty: number;
  buyQty: number;
  buildQty: number;
  unitCost: number;
}

/**
 * Direct bill of materials for every produced type in the tree, keyed by
 * product type ID.
 *
 * This is what makes a component or T1 task show its own materials. The
 * flattened `flat_materials` bill is base-materials-only and describes the
 * WHOLE chain, so attributing it wholesale to the final output task (what
 * this used to do) left every intermediate task — the T1 hull feeding a T2
 * build, every sub-component — with an empty material list.
 *
 * Aggregated across every occurrence of a type, because a component shared by
 * two branches appears once per branch in the tree while `buildActivityPlan`
 * merges it into a single step (one task, combined runs). Summing the
 * occurrences is what keeps the task's bill matching its merged run count.
 *
 * Quantities stay whole-tree absolute, exactly as the analyzer sized them, so
 * summing every task's rows by type_id still reproduces the project total:
 * each base material is consumed at exactly one level.
 */
function directBOMByProduct(
  root: MaterialNode | null | undefined,
): Map<number, Map<number, DirectBOMEntry>> {
  const byProduct = new Map<number, Map<number, DirectBOMEntry>>();
  if (!root) return byProduct;
  const walk = (node: MaterialNode) => {
    const children = node.children ?? [];
    if (children.length > 0 && node.type_id > 0) {
      let bom = byProduct.get(node.type_id);
      if (!bom) {
        bom = new Map<number, DirectBOMEntry>();
        byProduct.set(node.type_id, bom);
      }
      for (const child of children) {
        if (!child.type_id || child.type_id <= 0) continue;
        const qty = Math.max(0, Math.ceil(child.quantity ?? 0));
        if (qty <= 0) continue;
        // A built child is produced by its own task, so it's a build
        // obligation rather than something to shop for. A split child is
        // part bought / part built — the analyzer guarantees
        // buy_units + build_units == quantity.
        const built = !child.is_base && child.should_build && !!child.blueprint;
        let buyQty = built ? 0 : qty;
        let buildQty = built ? qty : 0;
        if (child.should_split) {
          buyQty = Math.max(0, Math.ceil(child.buy_units ?? 0));
          buildQty = Math.max(0, Math.ceil(child.build_units ?? 0));
        }
        // buy_price is the walked cost of the FULL quantity, not per unit.
        const unitCost = qty > 0 ? (child.buy_price ?? 0) / qty : 0;
        const prev = bom.get(child.type_id);
        if (prev) {
          prev.requiredQty += qty;
          prev.buyQty += buyQty;
          prev.buildQty += buildQty;
          if (prev.unitCost <= 0) prev.unitCost = unitCost;
        } else {
          bom.set(child.type_id, {
            typeID: child.type_id,
            typeName: child.type_name || "",
            requiredQty: qty,
            buyQty,
            buildQty,
            unitCost,
          });
        }
      }
    }
    for (const child of children) walk(child);
  };
  walk(root);
  return byProduct;
}

export function buildIndustryPlanPatch(input: BuildIndustryPlanPatchInput): IndustryPlanPatch {
  const {
    result,
    productTypeID,
    productName,
    runs,
    me,
    te,
    systemName,
    stationID,
    ownBlueprint,
    replace,
  } = input;

  const topBlueprintTypeID = result.material_tree?.blueprint?.blueprint_type_id ?? 0;
  const activitySteps = result.activity_plan ?? [];
  const tasks: IndustryTaskPlanInput[] = [];
  const jobs: IndustryJobPlanInput[] = [];

  if (activitySteps.length > 0) {
    // Placeholder refs for the dependency edges. The backend remaps negative
    // task_ids to real rowids at insert (internal/db/industry_ledger.go:2599),
    // and jobs already lean on that, so parents ride the same mechanism.
    const refByKey = new Map<string, number>();
    activitySteps.forEach((step, index) => {
      refByKey.set(`${(step.activity || "manufacturing").toLowerCase()}:${step.product_type_id}`, -(index + 1));
    });
    const refFor = (activity: string, typeID: number) => refByKey.get(`${activity}:${typeID}`) ?? 0;
    const childrenByProduct = componentEdges(result.material_tree);

    // parent_task_id is scalar, and buildActivityPlan already merges a shared
    // component into one step — so a task with several prerequisites can only
    // persist one. Pick the component edge when there is one, because that is
    // the edge nothing else can reconstruct; the invention/copy/research chain
    // is inferred from committed rows anyway.
    const primaryParentRef = (step: IndustryActivityStep): number => {
      const activity = (step.activity || "manufacturing").toLowerCase();
      if (activity === "manufacturing" || activity === "reaction") {
        for (const childTypeID of childrenByProduct.get(step.product_type_id) ?? []) {
          const ref =
            refFor("manufacturing", childTypeID) || refFor("reaction", childTypeID);
          if (ref !== 0) return ref;
        }
        const bp = step.blueprint_type_id || 0;
        if (bp > 0) {
          return (
            refFor("invention", bp) ||
            refFor("research_material", bp) ||
            refFor("research_time", bp) ||
            0
          );
        }
        return 0;
      }
      if (activity === "invention") {
        // Invention consumes a T1 BPC, which the copy step produces under the
        // same typeID it started from.
        return refFor("copy", step.blueprint_type_id || 0);
      }
      return 0;
    };

    activitySteps.forEach((step, index) => {
      const targetRuns = stepRuns(step);
      const taskRef = -(index + 1);
      const parentRef = primaryParentRef(step);
      tasks.push({
        name: stepLabel(step),
        activity: step.activity || "manufacturing",
        // Self-reference is impossible here (a step is never its own
        // component) but guard anyway — the board flags self_links loudly.
        parent_task_id: parentRef === taskRef ? 0 : parentRef,
        product_type_id: step.product_type_id,
        target_runs: targetRuns,
        // Prerequisite steps (invention → sub-mfg → final mfg) should sort
        // above later steps under a priority-DESC sort. Assign in reverse
        // so index 0 (usually invention) gets the highest number.
        priority: 100 + (activitySteps.length - index),
        status: "planned",
        constraints: {
          me,
          te,
          system_name: systemName,
          station_id: stationID || 0,
          blueprint_type_id: step.blueprint_type_id || 0,
          blueprint_location_id: stationID || 0,
          duration_seconds_per_run: targetRuns > 0 ? Math.round((step.time_seconds || 0) / targetRuns) : 0,
          cost_isk_per_run: targetRuns > 0 ? (step.job_cost || 0) / targetRuns : 0,
        },
      });
      jobs.push({
        task_id: taskRef,
        facility_id: stationID || 0,
        activity: step.activity || "manufacturing",
        runs: targetRuns,
        duration_seconds: step.time_seconds ?? 0,
        cost_isk: step.job_cost ?? 0,
        status: "planned",
        started_at: "",
        finished_at: "",
        notes: "",
      });
    });
  } else {
    const taskName = `Build ${productName}`;
    tasks.push({
      name: taskName,
      activity: "manufacturing",
      product_type_id: productTypeID,
      target_runs: runs,
      priority: 100,
      status: "planned",
      constraints: {
        me,
        te,
        system_name: systemName,
        station_id: stationID || 0,
        blueprint_type_id: topBlueprintTypeID || 0,
        blueprint_location_id: stationID || 0,
        duration_seconds_per_run: runs > 0 ? Math.round((result.manufacturing_time ?? 0) / runs) : 0,
        cost_isk_per_run: runs > 0 ? (result.total_job_cost ?? 0) / runs : 0,
      },
    });
    jobs.push({
      task_id: -1,
      facility_id: stationID || 0,
      activity: "manufacturing",
      runs,
      duration_seconds: result.manufacturing_time ?? 0,
      cost_isk: result.total_job_cost ?? 0,
      status: "planned",
      started_at: "",
      finished_at: "",
      notes: "",
    });
  }

  // Expected value, captured once at plan time on the OUTPUT task only.
  //
  // Only the final saleable product carries non-zero numbers; intermediate
  // component / invention / copy tasks stay at zero. That keeps the project
  // rollup a plain SUM over every task with no is-final flag and no risk of
  // double-counting a component's notional value on top of the hull it goes
  // into.
  //
  // expected_unit_cost is the per-unit cost basis (materials + job costs at
  // plan time), which is what the sell-floor guard compares live market
  // prices against so a dipped price never gets sold below what it cost.
  if (tasks.length > 0) {
    const outputQty = Math.max(0, Math.round(result.total_quantity || 0));
    if (outputQty > 0) {
      const outputTask = tasks[tasks.length - 1];
      outputTask.expected_unit_revenue = (result.sell_revenue || 0) / outputQty;
      outputTask.expected_unit_cost = (result.optimal_build_cost || 0) / outputQty;
      outputTask.expected_output_qty = outputQty;
    }
  }

  // Materials attribution has two paths that COMPOSE:
  //
  // 1) Per-step materials — invention/copy/research steps carry their own
  //    material bill on step.materials (e.g. datacores for invention). We
  //    emit those with task_id set to the step's own task, so the task
  //    expansion in Operations shows what each step consumes. Manufacturing
  //    and reaction steps leave step.materials empty; their materials fall
  //    through to path 2.
  //
  // 2) Output-task fallback — flat_materials is a flattened bill covering
  //    every base material in the tree (datacores, decryptor cost, and
  //    ME-adjusted mfg mats all together). Whatever quantities are already
  //    attributed via path 1 are subtracted from flat_materials before the
  //    remainder is attributed to the OUTPUT (mfg) task. This prevents
  //    double-counting in material_diff while still keeping the mfg-task
  //    block-status pill reflecting its own direct BOM.
  //
  // Uses the same negative-int taskRef convention as jobs above.
  const outputTaskRef = tasks.length > 0 ? -tasks.length : -1;

  // Path 1: per-step materials (invention/copy/research). Also track the
  // per-typeID quantities already attributed so we can subtract from
  // flat_materials.
  const materials: IndustryMaterialPlanInput[] = [];
  const attributedByType = new Map<number, number>();
  activitySteps.forEach((step, index) => {
    if (!step.materials || step.materials.length === 0) return;
    const taskRef = -(index + 1);
    for (const mat of step.materials) {
      if (!mat.type_id || mat.type_id <= 0) continue;
      const qty = Math.max(0, Math.ceil(mat.quantity ?? 0));
      if (qty <= 0) continue;
      materials.push({
        task_id: taskRef,
        type_id: mat.type_id,
        type_name: mat.type_name || "",
        required_qty: qty,
        available_qty: 0,
        buy_qty: qty,
        build_qty: 0,
        unit_cost_isk: 0,
        source: "market" as const,
      });
      attributedByType.set(mat.type_id, (attributedByType.get(mat.type_id) ?? 0) + qty);
    }
  });

  // Path 2: the direct bill of materials per producing task, read off the
  // material tree. Each manufacturing/reaction step gets exactly what its own
  // job consumes — which is the only way a component or T1 task ever shows a
  // material list, and what makes its block-status pill mean anything.
  //
  // Preferred over the flat bill because flat_materials is base-materials-only
  // for the WHOLE chain: dumping it on the output task (path 3) leaves every
  // intermediate empty and tells the user to buy minerals against the task
  // that doesn't consume them.
  const bomByProduct = directBOMByProduct(result.material_tree);
  // Prices come from the flat bill where it has them; it's the same walked
  // market data and already per-unit.
  const flatUnitCost = new Map<number, number>();
  for (const m of result.flat_materials ?? []) {
    if (m.type_id > 0) flatUnitCost.set(m.type_id, m.unit_price ?? 0);
  }
  let attributedPerTask = false;
  if (bomByProduct.size > 0) {
    activitySteps.forEach((step, index) => {
      const activity = step.activity || "manufacturing";
      // Invention/copy/research consume their own bill via path 1; they
      // produce blueprints, which are not material-tree nodes.
      if (activity !== "manufacturing" && activity !== "reaction") return;
      const bom = bomByProduct.get(step.product_type_id);
      if (!bom || bom.size === 0) return;
      const taskRef = -(index + 1);
      for (const entry of bom.values()) {
        materials.push({
          task_id: taskRef,
          type_id: entry.typeID,
          type_name: entry.typeName,
          required_qty: entry.requiredQty,
          available_qty: 0,
          buy_qty: entry.buyQty,
          build_qty: entry.buildQty,
          unit_cost_isk: flatUnitCost.get(entry.typeID) ?? entry.unitCost,
          // "build" marks a row covered by another task in this project
          // rather than by the market, which is what keeps intermediates out
          // of the multibuy export (it ships buy_qty + missing_qty).
          source: entry.buildQty > 0 && entry.buyQty <= 0 ? ("build" as const) : ("market" as const),
        });
        attributedPerTask = true;
      }
    });
  }

  // Path 3: legacy flat fallback, for an analysis that arrived without a
  // material tree (nothing in-tree to attribute). Same behaviour as before:
  // flat_materials minus whatever path 1 already claimed, all on the output
  // task, so a treeless result still produces a usable shopping list.
  if (!attributedPerTask) {
    for (const m of result.flat_materials ?? []) {
      if (!m.type_id || m.type_id <= 0) continue;
      const total = Math.max(0, Math.ceil(m.quantity ?? 0));
      const alreadyAttributed = attributedByType.get(m.type_id) ?? 0;
      const remaining = total - alreadyAttributed;
      if (remaining <= 0) continue;
      materials.push({
        task_id: outputTaskRef,
        type_id: m.type_id,
        type_name: m.type_name || "",
        required_qty: remaining,
        available_qty: 0,
        buy_qty: remaining,
        build_qty: 0,
        unit_cost_isk: m.unit_price ?? 0,
        source: "market" as const,
      });
    }
  }

  // Blueprints: only the ones this row's activity plan actually needs,
  // with per-row required_runs. Merge accumulates required_runs across
  // patches; owned-inventory fields (quantity, is_bpo, available_runs)
  // are overlaid post-merge by applyCoverageToIndustryPlanPatch so they
  // never inflate.
  const blueprintMap = new Map<number, IndustryBlueprintPoolInput>();
  for (const step of activitySteps) {
    if (!step.blueprint_type_id || step.blueprint_type_id <= 0) continue;
    const requiredRuns = stepRuns(step);
    const existing = blueprintMap.get(step.blueprint_type_id);
    // T2 BPCs (produced via invention) are always copies; T1 BPs default
    // to ownBlueprint (BPO). Without this, every sub-BP in a T2 chain
    // shows as BPO which is wrong for every T2 component.
    const isBpc = Boolean(step.blueprint_is_bpc);
    const isBpo = isBpc ? false : ownBlueprint;
    blueprintMap.set(step.blueprint_type_id, {
      blueprint_type_id: step.blueprint_type_id,
      blueprint_name: step.blueprint_name || existing?.blueprint_name || "",
      location_id: stationID || 0,
      quantity: 1,
      me,
      te,
      is_bpo: isBpo,
      available_runs: isBpo ? 0 : (existing?.available_runs ?? 0) + requiredRuns,
    });
  }
  if (blueprintMap.size === 0 && topBlueprintTypeID > 0) {
    blueprintMap.set(topBlueprintTypeID, {
      blueprint_type_id: topBlueprintTypeID,
      blueprint_name: `${productName} Blueprint`,
      location_id: stationID || 0,
      quantity: 1,
      me,
      te,
      is_bpo: ownBlueprint,
      available_runs: ownBlueprint ? 0 : runs,
    });
  }
  const blueprints = Array.from(blueprintMap.values());

  return {
    replace,
    project_status: "planned",
    tasks,
    jobs,
    materials,
    blueprints,
  };
}

// mergeIndustryPlanPatches folds N patches (e.g. one per Scanner row) into a
// single patch. Job→task refs are local negative-int refs within a source
// patch, so we re-number them per source to preserve links. Materials and
// blueprints dedup by their natural key.
export function mergeIndustryPlanPatches(patches: IndustryPlanPatch[]): IndustryPlanPatch {
  if (patches.length === 0) {
    return { replace: false, project_status: "planned", tasks: [], jobs: [], materials: [], blueprints: [] };
  }
  if (patches.length === 1) {
    return patches[0];
  }

  const outTasks: IndustryTaskPlanInput[] = [];
  const outJobs: IndustryJobPlanInput[] = [];
  // Materials dedup key includes task_id now that buildIndustryPlanPatch
  // attributes each material to its owning output task. Two rows for the
  // same output product (rare — usually the scanner dedup collapses them
  // before we get here) can still land on the same (task, type) key.
  // The DB layer uses the same (task_id, type_id) as its ON CONFLICT key.
  const materialByTaskAndType = new Map<string, IndustryMaterialPlanInput>();
  const blueprintByKey = new Map<string, IndustryBlueprintPoolInput>();

  // Cross-row task dedup. Two scanner rows whose chains share a component
  // (both need Nitrogen Fuel Block, say) each carry their own task for it.
  // Emitting both produces duplicate work in Operations — in EVE you queue
  // one job for the combined runs. Key on activity + product + task name so
  // genuinely-different work stays separate (e.g. two invention tasks for
  // the same BPC under different decryptors have different names).
  const taskIndexByKey = new Map<string, number>();
  const taskRefByIndex = (i: number) => -(i + 1);
  const dedupeKeyFor = (t: IndustryTaskPlanInput) =>
    `${t.activity ?? ""}:${t.product_type_id ?? 0}:${t.name ?? ""}`;
  // Jobs are deduped alongside their task: one merged task should carry one
  // merged job, not N identical ones.
  const jobIndexByTaskAndActivity = new Map<string, number>();

  for (const patch of patches) {
    const tasks = patch.tasks ?? [];
    const jobs = patch.jobs ?? [];
    // Translate this patch's local task refs (-(i+1) at index i) to global
    // refs. A task that dedupes into an existing one maps to that one's ref,
    // so its jobs and materials attach to the surviving task.
    const taskIdRemap = new Map<number, number>();
    // parent_task_id is a local ref too, and it can point forward (a step's
    // component may sit at a later index), so it can only be translated once
    // the whole patch has been walked.
    const pendingParents: Array<{ outIdx: number; sourceParentRef: number }> = [];
    tasks.forEach((task, i) => {
      const sourceRef = -(i + 1);
      const key = dedupeKeyFor(task);
      const existingIdx = taskIndexByKey.get(key);
      if (existingIdx !== undefined) {
        // Merge: runs are additive, priority takes the more urgent (higher)
        // value so a component needed early in one chain isn't demoted by a
        // later chain that needs it less urgently.
        const existing = outTasks[existingIdx];
        // Expected value merges as a quantity-weighted average: two scanner
        // rows for the same product can carry different unit economics (a
        // different decryptor, a stale price), and the surviving task should
        // reflect the blend of what was actually committed.
        const existingQty = existing.expected_output_qty ?? 0;
        const incomingQty = task.expected_output_qty ?? 0;
        const mergedQty = existingQty + incomingQty;
        const blend = (a: number | undefined, b: number | undefined) =>
          mergedQty > 0
            ? ((a ?? 0) * existingQty + (b ?? 0) * incomingQty) / mergedQty
            : 0;
        outTasks[existingIdx] = {
          ...existing,
          target_runs: (existing.target_runs ?? 0) + (task.target_runs ?? 0),
          priority: Math.max(existing.priority ?? 0, task.priority ?? 0),
          expected_unit_revenue: blend(existing.expected_unit_revenue, task.expected_unit_revenue),
          expected_unit_cost: blend(existing.expected_unit_cost, task.expected_unit_cost),
          expected_output_qty: mergedQty,
        };
        taskIdRemap.set(sourceRef, taskRefByIndex(existingIdx));
        return;
      }
      const newIdx = outTasks.length;
      outTasks.push(task);
      taskIndexByKey.set(key, newIdx);
      taskIdRemap.set(sourceRef, taskRefByIndex(newIdx));
      const parentRef = task.parent_task_id ?? 0;
      // Only negative refs are local to this patch; a positive one is an
      // existing rowid and passes through untouched.
      if (parentRef < 0) pendingParents.push({ outIdx: newIdx, sourceParentRef: parentRef });
    });

    // Deduped tasks keep whichever parent the first patch gave them — merging
    // the prerequisite lists of a shared component isn't expressible in a
    // scalar column, and the inferred graph covers the rest.
    for (const { outIdx, sourceParentRef } of pendingParents) {
      const remapped = taskIdRemap.get(sourceParentRef) ?? 0;
      const selfRef = taskRefByIndex(outIdx);
      outTasks[outIdx] = {
        ...outTasks[outIdx],
        parent_task_id: remapped === selfRef ? 0 : remapped,
      };
    }

    for (const job of jobs) {
      const originalRef = job.task_id;
      const remapped = originalRef !== undefined ? taskIdRemap.get(originalRef) : undefined;
      const taskRef = remapped ?? originalRef;
      const jobKey = `${taskRef}:${job.activity ?? ""}`;
      const existingJobIdx = jobIndexByTaskAndActivity.get(jobKey);
      if (existingJobIdx !== undefined) {
        const existing = outJobs[existingJobIdx];
        outJobs[existingJobIdx] = {
          ...existing,
          runs: (existing.runs ?? 0) + (job.runs ?? 0),
          duration_seconds: (existing.duration_seconds ?? 0) + (job.duration_seconds ?? 0),
          cost_isk: (existing.cost_isk ?? 0) + (job.cost_isk ?? 0),
        };
        continue;
      }
      jobIndexByTaskAndActivity.set(jobKey, outJobs.length);
      outJobs.push({ ...job, task_id: taskRef });
    }

    for (const m of patch.materials ?? []) {
      const originalTaskRef = m.task_id;
      const remappedTaskRef = originalTaskRef !== undefined
        ? (taskIdRemap.get(originalTaskRef) ?? originalTaskRef)
        : 0;
      const key = `${remappedTaskRef}:${m.type_id}`;
      const existing = materialByTaskAndType.get(key);
      if (!existing) {
        materialByTaskAndType.set(key, { ...m, task_id: remappedTaskRef });
        continue;
      }
      const requiredQty = (existing.required_qty ?? 0) + (m.required_qty ?? 0);
      // available_qty is owned-assets snapshot per typeID — identical across
      // dupes in one coverage snapshot. Take max so summing doesn't inflate.
      const availableQty = Math.max(existing.available_qty ?? 0, m.available_qty ?? 0);
      const clampedAvailable = Math.min(requiredQty, availableQty);
      const buyQty = (existing.buy_qty ?? 0) + (m.buy_qty ?? 0);
      const buildQty = (existing.build_qty ?? 0) + (m.build_qty ?? 0);
      // Prefer the non-empty unit cost.
      const unitCost = existing.unit_cost_isk || m.unit_cost_isk || 0;
      materialByTaskAndType.set(key, {
        ...existing,
        task_id: remappedTaskRef,
        type_name: existing.type_name || m.type_name || "",
        required_qty: requiredQty,
        available_qty: clampedAvailable,
        buy_qty: buyQty,
        build_qty: buildQty,
        unit_cost_isk: unitCost,
        source: buyQty > 0 ? "market" : existing.source,
      });
    }

    for (const bp of patch.blueprints ?? []) {
      const key = `${bp.blueprint_type_id}-${bp.location_id}-${bp.is_bpo ? "bpo" : "bpc"}`;
      const existing = blueprintByKey.get(key);
      if (!existing) {
        blueprintByKey.set(key, { ...bp });
        continue;
      }
      blueprintByKey.set(key, {
        ...existing,
        blueprint_name: existing.blueprint_name || bp.blueprint_name || "",
        quantity: (existing.quantity ?? 0) + (bp.quantity ?? 0),
        available_runs: (existing.available_runs ?? 0) + (bp.available_runs ?? 0),
        me: Math.max(existing.me ?? 0, bp.me ?? 0),
        te: Math.max(existing.te ?? 0, bp.te ?? 0),
      });
    }
  }

  // Scheduler: first-patch-wins (a batch shares one context, so any patch's
  // scheduler is representative).
  const scheduler = patches.find((p) => p.scheduler)?.scheduler;

  return {
    replace: patches[0].replace ?? false,
    replace_blueprints: patches[0].replace_blueprints,
    project_status: patches[0].project_status ?? "planned",
    tasks: outTasks,
    jobs: outJobs,
    materials: Array.from(materialByTaskAndType.values()),
    blueprints: Array.from(blueprintByKey.values()),
    ...(scheduler ? { scheduler } : {}),
  };
}

// applyCoverageToIndustryPlanPatch overlays inventory data from a coverage
// snapshot onto a patch's materials and blueprints. Split from
// buildIndustryPlanPatch so multi-row batches (Scanner → merge → apply)
// don't double-count coverage into each source patch — which would inflate
// merged quantities N-fold, one per row in the batch.
//
// Runs post-merge for batch flows, post-build for single-item flows.
// Idempotent: applying twice with the same coverage produces the same
// result. Passing a null coverage is a no-op (returns the patch unchanged).
export function applyCoverageToIndustryPlanPatch(
  patch: IndustryPlanPatch,
  coverage: IndustryCoverageResult | null,
): IndustryPlanPatch {
  if (!coverage) return patch;

  const availByType = new Map<number, number>(
    (coverage.materials ?? []).map((m) => [m.type_id, Math.max(0, Math.ceil(m.available_qty ?? 0))]),
  );
  const bpByType = new Map<number, (typeof coverage.blueprints)[number]>(
    (coverage.blueprints ?? []).map((bp) => [bp.blueprint_type_id, bp]),
  );

  const nextMaterials: IndustryMaterialPlanInput[] = (patch.materials ?? []).map((m) => {
    const required = Math.max(0, Math.ceil(m.required_qty ?? 0));
    const rawAvailable = availByType.get(m.type_id) ?? 0;
    // Store raw stockpile count, not the clamped "usable" amount — showing
    // "have 3" when the user actually has 500 datacores is confusing.
    // buy_qty is still safe because max(0, required - rawAvailable) clamps
    // negatives to zero, so overshoots don't turn into negative purchases.
    const buy = Math.max(0, required - rawAvailable);
    return {
      ...m,
      required_qty: required,
      available_qty: rawAvailable,
      buy_qty: buy,
      source: buy > 0 ? ("market" as const) : ("stock" as const),
    };
  });

  const nextBlueprints: IndustryBlueprintPoolInput[] = (patch.blueprints ?? []).map((bp) => {
    const cov = bpByType.get(bp.blueprint_type_id);
    if (!cov || (cov.owned_qty ?? 0) <= 0) {
      return bp;
    }
    const hasBpo = (cov.bpo_qty ?? 0) > 0;
    if (hasBpo) {
      return {
        ...bp,
        blueprint_name: bp.blueprint_name || cov.blueprint_name || "",
        quantity: Math.max(1, cov.bpo_qty || 1),
        me: cov.best_me || bp.me,
        te: cov.best_te || bp.te,
        is_bpo: true,
        available_runs: 0,
      };
    }
    const hasBpc = (cov.bpc_qty ?? 0) > 0 && (cov.available_runs ?? 0) > 0;
    if (hasBpc) {
      return {
        ...bp,
        blueprint_name: bp.blueprint_name || cov.blueprint_name || "",
        quantity: Math.max(1, cov.bpc_qty || 1),
        me: cov.best_me || bp.me,
        te: cov.best_te || bp.te,
        is_bpo: false,
        available_runs: Math.max(0, cov.available_runs || 0),
      };
    }
    return bp;
  });

  return {
    ...patch,
    materials: nextMaterials,
    blueprints: nextBlueprints,
  };
}

export interface JobSplitLimits {
  /** Hard cap on runs in a single installed job. <= 0 disables the cap. */
  maxRunsPerJob: number;
  /** Cap on wall-clock length of a single installed job. <= 0 disables. */
  maxDurationHours: number;
}

/**
 * Distribute `total` across `batches` as evenly as possible, largest first.
 * 141 over 2 gives [71, 70] rather than [140, 1] — an even split means every
 * installed job finishes at roughly the same time, so the slots free up
 * together instead of leaving one straggler.
 */
function evenBatches(total: number, batches: number): number[] {
  const base = Math.floor(total / batches);
  const remainder = total % batches;
  const out: number[] = [];
  for (let i = 0; i < batches; i++) out.push(base + (i < remainder ? 1 : 0));
  return out;
}

/**
 * Split every job in a plan patch so no single installed job exceeds the
 * user's runs-per-job or hours-per-job limits.
 *
 * EVE installs jobs one slot at a time, and a 141-run invention batch is
 * not a thing you can queue in one go — it has to become N separate
 * installs. Doing that here (after merge, before apply) means the Operations
 * task expansion lists exactly the jobs the user will install, in the sizes
 * they will install them.
 *
 * Duration and cost are apportioned pro-rata off the original job's
 * per-run rates, so the project rollup totals are unchanged by splitting.
 * Jobs at or under the limits pass through untouched, with no "batch 1/1"
 * noise added to their notes.
 */
export function applyJobSplitToIndustryPlanPatch(
  patch: IndustryPlanPatch,
  limits: JobSplitLimits,
): IndustryPlanPatch {
  const jobs = patch.jobs ?? [];
  if (jobs.length === 0) return patch;

  const maxRuns = limits.maxRunsPerJob > 0 ? Math.floor(limits.maxRunsPerJob) : Infinity;
  const maxSeconds = limits.maxDurationHours > 0 ? limits.maxDurationHours * 3600 : Infinity;
  if (maxRuns === Infinity && maxSeconds === Infinity) return patch;

  const out: IndustryJobPlanInput[] = [];
  let splitAny = false;

  for (const job of jobs) {
    const runs = Math.max(0, Math.floor(job.runs ?? 0));
    const totalSeconds = Math.max(0, job.duration_seconds ?? 0);
    const totalCost = job.cost_isk ?? 0;
    const perRunSeconds = runs > 0 ? totalSeconds / runs : 0;

    // A single run is indivisible: if even one run blows the duration cap,
    // one run per job is the smallest legal batch.
    const runsByDuration =
      perRunSeconds > 0 && maxSeconds !== Infinity
        ? Math.max(1, Math.floor(maxSeconds / perRunSeconds))
        : Infinity;
    const perJob = Math.min(maxRuns, runsByDuration);

    if (runs <= 1 || perJob === Infinity || runs <= perJob) {
      out.push(job);
      continue;
    }

    splitAny = true;
    const batches = Math.ceil(runs / perJob);
    const sizes = evenBatches(runs, batches);
    sizes.forEach((size, i) => {
      const share = size / runs;
      const suffix = `batch ${i + 1}/${batches}`;
      out.push({
        ...job,
        runs: size,
        duration_seconds: Math.round(perRunSeconds * size),
        cost_isk: totalCost * share,
        notes: job.notes ? `${job.notes} · ${suffix}` : suffix,
      });
    });
  }

  if (!splitAny) return patch;
  return { ...patch, jobs: out };
}
