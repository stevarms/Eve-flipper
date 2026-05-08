import type { AchievementIconId } from "@/assets/achievements/achievement-icons";
import type { AchievementProgressPatch, AchievementState } from "@/lib/api";
import { achievementDefinitionById } from "./definitions";

export type AchievementEventName =
  | "scan_completed"
  | "mission_control_opened"
  | "journal_trade_created"
  | "journal_reconciled"
  | "backtest_run"
  | "route_checked"
  | "dotlan_opened"
  | "industry_analysis_run"
  | "ledger_opened"
  | "portfolio_opened"
  | "risk_opened";

export interface AchievementEventPayload {
  rowsScanned?: number;
  quantityReduced?: boolean;
  depthChangedResult?: boolean;
  negativeWorstCase?: boolean;
  walletReserveLimited?: boolean;
  exposureLimited?: boolean;
  feesViewed?: boolean;
  tooSmallToTrade?: boolean;
  capitalFrozen?: number;
  cargoLimited?: boolean;
  routeMode?: "fastest" | "safest" | "isk_hour" | string;
  gankRiskViewed?: boolean;
  profitable?: boolean;
  losing?: boolean;
  expectedVsActualCompared?: boolean;
  realBeatsExpected?: boolean;
  slippageLoss?: boolean;
  marketMoved?: boolean;
  undercutLoss?: boolean;
  previousLoss?: boolean;
  snapshotReplay?: boolean;
  paperProfitReducedByLiveDepth?: boolean;
  blueprintCoverageChecked?: boolean;
  materialDepthAware?: boolean;
  jobPlanCreated?: boolean;
  suspiciousOpportunity?: boolean;
  tooGoodToBeTrue?: boolean;
  avoidedTrade?: boolean;
  quietWin?: boolean;
  auditRun?: boolean;
}

type ProgressMode = "set" | "increment";

interface ProgressIntent {
  id: AchievementIconId;
  value?: number;
  mode?: ProgressMode;
}

export type AchievementStateMap = Partial<Record<AchievementIconId, AchievementState>>;

function currentProgress(states: AchievementStateMap, id: AchievementIconId): number {
  return Math.max(0, Number(states[id]?.progress ?? 0));
}

function isUnlocked(states: AchievementStateMap, id: AchievementIconId): boolean {
  return !!states[id]?.unlocked_at;
}

function addIntent(intents: ProgressIntent[], id: AchievementIconId, value = 1, mode: ProgressMode = "set") {
  if (!achievementDefinitionById[id]) return;
  intents.push({ id, value, mode });
}

function pushEventIntents(event: AchievementEventName, payload: AchievementEventPayload, intents: ProgressIntent[]) {
  switch (event) {
    case "scan_completed": {
      const rows = Math.max(1, Math.trunc(payload.rowsScanned ?? 1));
      addIntent(intents, "first-contact");
      addIntent(intents, "market-analyst", rows, "increment");
      addIntent(intents, "deep-scanner", rows, "increment");
      break;
    }
    case "mission_control_opened":
      addIntent(intents, "mission-controller", 1, "increment");
      addIntent(intents, "fee-awareness");
      if (payload.quantityReduced) addIntent(intents, "not-so-fast");
      if (payload.depthChangedResult) addIntent(intents, "depth-matters");
      if (payload.negativeWorstCase) addIntent(intents, "no-paper-profit");
      if (payload.walletReserveLimited) addIntent(intents, "capital-guard");
      if (payload.exposureLimited) addIntent(intents, "exposure-control");
      if (payload.tooSmallToTrade) addIntent(intents, "too-small-to-trade");
      if ((payload.capitalFrozen ?? 0) >= 1_000_000_000) addIntent(intents, "big-ticket");
      if (payload.cargoLimited) addIntent(intents, "cargo-math");
      if (payload.routeMode === "safest") addIntent(intents, "safe-route");
      if (payload.routeMode === "isk_hour") addIntent(intents, "isk-hour-mindset");
      if (payload.gankRiskViewed) addIntent(intents, "camp-check");
      break;
    case "journal_trade_created":
      addIntent(intents, "first-mission");
      addIntent(intents, "planned-operator", 1, "increment");
      addIntent(intents, "execution-discipline", 1, "increment");
      break;
    case "journal_reconciled":
      addIntent(intents, "closed-loop");
      if (payload.expectedVsActualCompared) addIntent(intents, "reality-check");
      if (payload.profitable) addIntent(intents, "green-ledger");
      if (payload.losing) addIntent(intents, "red-lesson");
      if (payload.realBeatsExpected) addIntent(intents, "clean-exit");
      if (payload.slippageLoss) addIntent(intents, "slippage-student");
      if (payload.marketMoved) addIntent(intents, "market-moved");
      if (payload.undercutLoss) addIntent(intents, "undercut-tax");
      if (payload.profitable && payload.previousLoss) addIntent(intents, "comeback-trader");
      break;
    case "backtest_run":
      addIntent(intents, "backtest-curious");
      addIntent(intents, "backtest-grinder", 1, "increment");
      if (payload.snapshotReplay) addIntent(intents, "snapshot-believer");
      if (payload.paperProfitReducedByLiveDepth) addIntent(intents, "paper-millionaire");
      break;
    case "route_checked":
      addIntent(intents, "route-planner");
      if (payload.cargoLimited) addIntent(intents, "cargo-math");
      if (payload.routeMode === "safest") addIntent(intents, "safe-route");
      if (payload.routeMode === "isk_hour") addIntent(intents, "isk-hour-mindset");
      if (payload.gankRiskViewed) addIntent(intents, "camp-check");
      break;
    case "dotlan_opened":
      addIntent(intents, "dotlan-navigator");
      break;
    case "industry_analysis_run":
      addIntent(intents, "industry-check");
      if (payload.blueprintCoverageChecked) addIntent(intents, "blueprint-mindset");
      if (payload.materialDepthAware) addIntent(intents, "material-realist");
      if (payload.jobPlanCreated) addIntent(intents, "job-planner");
      break;
    case "ledger_opened":
      addIntent(intents, "ledger-opened");
      break;
    case "portfolio_opened":
      addIntent(intents, "portfolio-view");
      break;
    case "risk_opened":
      addIntent(intents, "risk-aware");
      break;
  }

  if (payload.suspiciousOpportunity) addIntent(intents, "almost-trapped");
  if (payload.tooGoodToBeTrue) addIntent(intents, "classified-unknown-01");
  if (payload.avoidedTrade) addIntent(intents, "discipline-over-greed");
  if (payload.quietWin) addIntent(intents, "classified-unknown-03");
  if (payload.auditRun) addIntent(intents, "classified-unknown-04");
}

export function buildAchievementPatches(
  event: AchievementEventName,
  payload: AchievementEventPayload,
  states: AchievementStateMap,
): AchievementProgressPatch[] {
  const intents: ProgressIntent[] = [];
  pushEventIntents(event, payload, intents);
  if (intents.length === 0) return [];

  const nextProgress = new Map<AchievementIconId, number>();
  for (const intent of intents) {
    const definition = achievementDefinitionById[intent.id];
    const base = nextProgress.get(intent.id) ?? currentProgress(states, intent.id);
    const value = Math.max(0, Math.trunc(intent.value ?? 1));
    const progress = intent.mode === "increment" ? base + value : Math.max(base, value);
    nextProgress.set(intent.id, Math.min(definition.progressMax, progress));
  }

  const now = new Date().toISOString();
  const patches: AchievementProgressPatch[] = [];
  for (const [id, progress] of nextProgress.entries()) {
    const definition = achievementDefinitionById[id];
    const alreadyUnlocked = isUnlocked(states, id);
    patches.push({
      achievement_id: id,
      progress,
      unlocked_at: !alreadyUnlocked && progress >= definition.progressMax ? now : "",
    });
  }
  return patches;
}
