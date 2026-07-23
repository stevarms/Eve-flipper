import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { scanProfitableBlueprints, getStations, getStructures } from "@/lib/api";
import { useEsiFeeImport } from "@/lib/useEsiFeeImport";
import type { ProfitableScanRequest, ProfitableScanResponse, ProfitableScanRow, ProfitableScanReuseRow, StationInfo } from "@/lib/types";
import { formatISK } from "@/lib/format";
import { DECRYPTORS, effectiveInventionParams } from "@/lib/industryDecryptors";
import { useIndustrySharedPrefs } from "@/lib/useIndustrySharedPrefs";
import {
  TabSettingsPanel,
  SettingsField,
  SettingsNumberInput,
  SettingsSelect,
  SettingsCheckbox,
  SettingsGrid,
  SettingsSubsection,
} from "../TabSettingsPanel";
import { SystemAutocomplete } from "../SystemAutocomplete";
import { EmptyState } from "../EmptyState";
import { useGlobalToast } from "../Toast";
import { AddBlueprintsToProjectModal } from "./AddBlueprintsToProjectModal";
import { StructureRigPicker, computeRigTotals } from "./StructureRigPicker";
import { PricingHubPicker } from "./PricingHubPicker";
import { getStructureRigs } from "@/lib/api";
import type { StructureRig } from "@/lib/types";

const SCANNER_PERSIST_KEY = "industry-scanner";
// Display fallback for the period-days label when the backend omits the
// field (e.g. rows produced before the period-stats feature landed and
// replayed from sessionStorage). Kept in sync with the backend constant
// profitableScanPeriodDays.
const profitableScanPeriodDaysFallback = 30;
const PARAMS_LS_KEY = "eve-settings:industry-scanner";
// Keep transient scan results (rows + selection + sort + search) in
// sessionStorage so the user doesn't lose them when they switch jobs tabs.
const SCAN_STATE_SS_KEY = "eve-flipper:scanner-state";

type SortKey =
  | "selected"
  | "blueprint_name"
  | "product_name"
  | "owned_quantity"
  | "available_runs"
  | "me"
  | "te"
  | "isk_per_hour"
  | "profit"
  | "profit_percent"
  | "period_profit"
  | "period_margin"
  | "optimal_build_cost"
  | "manufacturing_time";

type SortDir = "asc" | "desc";

// Type-filter chip catalog. Each chip maps to one-or-more SDE CategoryIDs;
// the multiplex lets us group related categories under one label (Structures
// = 65 Structure + 66 Structure Module, Components = 34 Material + 35
// Component). Order is the display order in the Type chip row.
interface TypeChipDef {
  key: string;
  labelKey: string; // TranslationKey — resolved at render
  categoryIDs: number[];
}
const TYPE_CHIPS: TypeChipDef[] = [
  { key: "ships", labelKey: "industryScannerTypeChipShips", categoryIDs: [6] },
  { key: "modules", labelKey: "industryScannerTypeChipModules", categoryIDs: [7] },
  { key: "charges", labelKey: "industryScannerTypeChipCharges", categoryIDs: [8] },
  { key: "drones", labelKey: "industryScannerTypeChipDrones", categoryIDs: [18] },
  { key: "implants", labelKey: "industryScannerTypeChipImplants", categoryIDs: [20] },
  { key: "deployables", labelKey: "industryScannerTypeChipDeployables", categoryIDs: [22] },
  { key: "subsystems", labelKey: "industryScannerTypeChipSubsystems", categoryIDs: [32] },
  { key: "components", labelKey: "industryScannerTypeChipComponents", categoryIDs: [17, 34, 35] },
  { key: "structures", labelKey: "industryScannerTypeChipStructures", categoryIDs: [65, 66] },
];
const ALL_TYPE_CATEGORY_IDS: number[] = TYPE_CHIPS.flatMap((c) => c.categoryIDs);

// Scanner-only params. buildSystem, buildStationID, facilityTax,
// structureBonus, brokerFee, salesTaxPercent live in the shared prefs hook
// (used by both Analyze and Scanner), so they intentionally don't appear
// here — the scanner reads them from useIndustrySharedPrefs on render.
interface PersistedParams {
  includeCorpBlueprints: boolean;
  /** System whose region is queried for product/material market prices.
   *  Scanner-specific (Analyze uses the same system for both build + price). */
  pricingSystem: string;
  /** Specific NPC station within the pricing system, 0 = region-wide. The
   *  four major-hub presets pre-fill this to the canonical trade station. */
  pricingStationID: number;
  blueprintFilter: "bpo" | "bpc" | "both";
  // Invention discovery — the parent "Invention" chip is derived from the
  // OR of these two so the top row shows one chip; when Invention is on,
  // a sub-row exposes the T2/T3 toggles independently.
  includeT2Invention: boolean;
  includeT3Invention: boolean;
  /** When true, reaction BPs (fuel-block formulas, moon composites) score
   *  alongside mfg/invention rows in reaction mode. Off by default. */
  includeReactions: boolean;
  // When true (default), the scan expands beyond your owned BPs to include
  // every marketable SDE BP — dimmed rows tagged [unowned] surface
  // buy-and-build candidates.
  includeUnowned: boolean;
  /** Empty array = all types; otherwise SDE CategoryIDs of allowed products. */
  typeCategories: number[];
  // Client-side visibility toggles per row kind — session-persisted so tab
  // switches don't wipe the user's chip selection.
  showT1Rows: boolean;
  showT2Rows: boolean;
  showT3Rows: boolean;
  showReactionRows: boolean;
  ownedFilter: "all" | "owned" | "unowned";
  // null = no filter; numeric value (including 0 or negative) is taken literally.
  minISKPerHour: number | null;
  minProfit: number | null;
  minMarginPct: number | null;
}

const DEFAULT_PARAMS: PersistedParams = {
  includeCorpBlueprints: false,
  pricingSystem: "Jita",
  pricingStationID: 60003760, // Jita IV - Moon 4 - Caldari Navy Assembly Plant
  blueprintFilter: "bpo",
  includeT2Invention: false,
  includeT3Invention: false,
  includeReactions: false,
  includeUnowned: true,
  typeCategories: [],
  showT1Rows: true,
  showT2Rows: true,
  showT3Rows: true,
  showReactionRows: true,
  ownedFilter: "all",
  minISKPerHour: null,
  minProfit: null,
  minMarginPct: null,
};

function loadPersistedParams(): PersistedParams {
  try {
    const raw = localStorage.getItem(PARAMS_LS_KEY);
    if (!raw) return DEFAULT_PARAMS;
    const parsed = JSON.parse(raw) as Partial<PersistedParams>;
    return { ...DEFAULT_PARAMS, ...parsed };
  } catch {
    return DEFAULT_PARAMS;
  }
}

function savePersistedParams(p: PersistedParams) {
  try {
    localStorage.setItem(PARAMS_LS_KEY, JSON.stringify(p));
  } catch {
    /* ignore */
  }
}

export interface ScannerAnalysisHandoff {
  productTypeID: number;
  productName: string;
  me: number;
  te: number;
  runs: number;
  systemName: string;
  stationID: number;
  /** True if the picked station is a player structure — analysis tab needs
   *  this so its station dropdown will include structures in the fetch. */
  stationIsStructure: boolean;
  facilityTax: number;
  structureBonus: number;
  brokerFee: number;
  salesTaxPercent: number;
  /** Scanner rows are either manufacturing or invention. Explicit so analysis
   *  doesn't fall back to its default. */
  activityMode: "manufacturing" | "invention";
  /** Scanner rows are always BPs the user owns. */
  ownBlueprint: true;
  /** Match the row's BPO/BPC flavor so invention cost decisions are correct. */
  blueprintIsBPO: boolean;
  /** Auto-run handleAnalyze after the analysis state is set. */
  autoAnalyze: boolean;
}

interface Props {
  isLoggedIn: boolean;
  onProjectCreated?: (projectID: number) => void;
  /** Hand the row off to the Industry Analysis sub-tab with its parameters
   *  pre-filled. The receiver typically sets selectedItem + the per-analysis
   *  state and switches `industryInnerTab` to "analysis". */
  onViewInAnalysis?: (handoff: ScannerAnalysisHandoff) => void;
}

export function IndustryProfitableScannerPanel({ isLoggedIn, onProjectCreated, onViewInAnalysis }: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();

  const [params, setParams] = useState<PersistedParams>(() => loadPersistedParams());
  // Shared prefs (build system/station + fees) so changes made here also
  // propagate to the Analyze form and vice versa.
  const [sharedPrefs, updateSharedPrefs] = useIndustrySharedPrefs();
  const [scanning, setScanning] = useState(false);
  const [progressMsg, setProgressMsg] = useState("");

  // Rehydrate scan state from sessionStorage so tab switches don't wipe the
  // table. Reads happen lazily inside each useState initializer.
  const initialScanState = useMemo(() => {
    try {
      const raw = sessionStorage.getItem(SCAN_STATE_SS_KEY);
      if (!raw) return null;
      return JSON.parse(raw) as {
        response: ProfitableScanResponse | null;
        searchQuery: string;
        selectedIDs: string[];
        sortKey: SortKey;
        sortDir: SortDir;
      };
    } catch {
      return null;
    }
    // Initial-only read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [response, setResponse] = useState<ProfitableScanResponse | null>(
    () => initialScanState?.response ?? null,
  );
  const [error, setError] = useState<string | null>(null);
  const [sortKey, setSortKey] = useState<SortKey>(
    () => initialScanState?.sortKey ?? "isk_per_hour",
  );
  const [sortDir, setSortDir] = useState<SortDir>(
    () => initialScanState?.sortDir ?? "desc",
  );

  const NUMERIC_DEFAULT_DESC: SortKey[] = [
    "selected",
    "owned_quantity",
    "available_runs",
    "me",
    "te",
    "isk_per_hour",
    "profit",
    "profit_percent",
    "period_profit",
    "period_margin",
    "optimal_build_cost",
    "manufacturing_time",
  ];
  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "desc" ? "asc" : "desc"));
      return;
    }
    setSortKey(key);
    setSortDir(NUMERIC_DEFAULT_DESC.includes(key) ? "desc" : "asc");
  };
  const [selectedIDs, setSelectedIDs] = useState<Set<string>>(
    () => new Set(initialScanState?.selectedIDs ?? []),
  );

  // Composite so a single BPO fanning out to multiple T2 invention products
  // gets one distinct key per row.
  const rowKey = (row: ProfitableScanRow) =>
    `${row.blueprint_type_id}-${row.is_bpo ? "bpo" : "bpc"}-${row.scan_mode ?? "t1_mfg"}-${row.product_type_id}`;
  const [addToProjectOpen, setAddToProjectOpen] = useState(false);
  // Add-to-project runs state, LIFTED FROM THE MODAL so cancelling out (to
  // fix a selection or add another row) doesn't wipe manual runs edits.
  // Keyed by rowKey — stable across selection changes and modal
  // open/close cycles. Reset on new scan / clear so we never carry
  // zombie overrides from a previous scan's row set.
  const [manualRunsByRowKey, setManualRunsByRowKey] = useState<Map<string, number>>(new Map());
  const [dirtyRunsByRowKey, setDirtyRunsByRowKey] = useState<Set<string>>(new Set());
  const { importFees, loading: importingFees } = useEsiFeeImport();
  const [searchQuery, setSearchQuery] = useState(initialScanState?.searchQuery ?? "");

  // Persist transient state back to sessionStorage on every change.
  useEffect(() => {
    try {
      sessionStorage.setItem(
        SCAN_STATE_SS_KEY,
        JSON.stringify({
          response,
          searchQuery,
          selectedIDs: Array.from(selectedIDs),
          sortKey,
          sortDir,
        }),
      );
    } catch {
      // Quota exceeded or unavailable — silently skip.
    }
  }, [response, searchQuery, selectedIDs, sortKey, sortDir]);

  const handleImportFees = useCallback(() => {
    void importFees((fees) => {
      updateSharedPrefs({
        salesTaxPercent: fees.suggested_sales_tax_percent,
        brokerFee: fees.suggested_broker_fee_percent,
      });
    });
  }, [importFees, updateSharedPrefs]);

  // Station picker (mirrors the IndustryTab Analysis flow).
  const [stations, setStations] = useState<StationInfo[]>([]);
  const [structureStations, setStructureStations] = useState<StationInfo[]>([]);
  // includeStructures lives in sharedPrefs so the toggle persists across
  // page reloads and syncs between the Scanner and Analyze surfaces.
  const includeStructures = sharedPrefs.includeStructures;
  const setIncludeStructures = useCallback(
    (v: boolean) => updateSharedPrefs({ includeStructures: v }),
    [updateSharedPrefs],
  );
  const [loadingStations, setLoadingStations] = useState(false);
  const [loadingStructures, setLoadingStructures] = useState(false);
  const [systemId, setSystemId] = useState<number>(0);
  const [systemRegionId, setSystemRegionId] = useState<number>(0);
  const stationsAbortRef = useRef<AbortController | null>(null);
  const stationsRequestSeqRef = useRef(0);
  const structuresAbortRef = useRef<AbortController | null>(null);
  const structuresRequestSeqRef = useRef(0);

  // Rig catalog (loaded once via getStructureRigs). Used for the rig-derived
  // display totals next to Structure ME/Job Cost fields. Picker manages its
  // own fetch too; the module-level cache in api.ts dedupes.
  const [rigCatalog, setRigCatalog] = useState<StructureRig[]>([]);
  useEffect(() => {
    let cancelled = false;
    getStructureRigs()
      .then((r) => { if (!cancelled) setRigCatalog(r.rigs); })
      .catch(() => { /* silent — picker also handles catalog errors */ });
    return () => { cancelled = true; };
  }, []);
  // Sec of the current build system for rig-multiplier math. Falls back to
  // hisec (0.5) when we don't yet know — errs on the safe side (no advanced
  // rigs). Real value comes from getStations resp; we already read it into
  // `systemRegionId` etc., so lean on that pattern later if needed.
  const buildSystemSec = 0.5; // TODO: wire real sec once systems endpoint exposes it here
  const rigTotals = useMemo(() => {
    // Aggregate rig-derived reductions for the row-type UI display. Uses
    // "manufacturing" activity as the display baseline — reaction/invention
    // rigs would show a different figure but the field placement is under
    // the structure hull, so a single canonical number is fine.
    return computeRigTotals(rigCatalog, sharedPrefs.structureRigTypeIDs, buildSystemSec, "manufacturing");
  }, [rigCatalog, sharedPrefs.structureRigTypeIDs, buildSystemSec]);

  useEffect(() => {
    stationsAbortRef.current?.abort();
    stationsRequestSeqRef.current += 1;
    const reqSeq = stationsRequestSeqRef.current;
    const normalizedSystem = sharedPrefs.buildSystem.trim();
    if (!normalizedSystem) {
      setStations([]);
      setSystemRegionId(0);
      setSystemId(0);
      setStructureStations([]);
      setLoadingStations(false);
      return;
    }
    const controller = new AbortController();
    stationsAbortRef.current = controller;
    setLoadingStations(true);
    getStations(normalizedSystem, controller.signal)
      .then((resp) => {
        if (reqSeq !== stationsRequestSeqRef.current) return;
        setStations(resp.stations);
        setSystemRegionId(resp.region_id);
        setSystemId(resp.system_id);
      })
      .catch((e: unknown) => {
        if (reqSeq !== stationsRequestSeqRef.current) return;
        if (e instanceof Error && e.name === "AbortError") return;
        setStations([]);
        setSystemRegionId(0);
        setSystemId(0);
      })
      .finally(() => {
        if (reqSeq === stationsRequestSeqRef.current) setLoadingStations(false);
      });
  }, [sharedPrefs.buildSystem]);

  useEffect(() => {
    structuresAbortRef.current?.abort();
    structuresRequestSeqRef.current += 1;
    const reqSeq = structuresRequestSeqRef.current;
    if (!includeStructures || !systemId || !systemRegionId) {
      setStructureStations([]);
      setLoadingStructures(false);
      return;
    }
    const controller = new AbortController();
    structuresAbortRef.current = controller;
    setLoadingStructures(true);
    getStructures(systemId, systemRegionId, controller.signal)
      .then((rows) => {
        if (reqSeq !== structuresRequestSeqRef.current) return;
        setStructureStations(rows);
      })
      .catch((e: unknown) => {
        if (reqSeq !== structuresRequestSeqRef.current) return;
        if (e instanceof Error && e.name === "AbortError") return;
        setStructureStations([]);
      })
      .finally(() => {
        if (reqSeq === structuresRequestSeqRef.current) setLoadingStructures(false);
      });
  }, [includeStructures, systemId, systemRegionId]);

  const allStations = useMemo(() => {
    if (includeStructures && structureStations.length > 0) {
      return [...stations, ...structureStations];
    }
    return stations;
  }, [stations, structureStations, includeStructures]);

  // Hull-inherent bonuses by structure typeID. Rigs stack on top (via the
  // separate StructureRigPicker → rigContribution → engine math).
  const STRUCTURE_ME_BONUS_BY_TYPE: Record<number, number> = useMemo(
    () => ({
      35825: 1, // Raitaru
      35826: 1, // Azbel
      35827: 1, // Sotiyo
    }),
    [],
  );
  const STRUCTURE_JOB_COST_REDUCTION_BY_TYPE: Record<number, number> = useMemo(
    () => ({
      35825: 3, // Raitaru
      35826: 4, // Azbel
      35827: 5, // Sotiyo
    }),
    [],
  );

  // Auto-fill hull-inherent ME + job-cost bonuses and the structure type
  // ID that the rig picker uses. Clears the rig loadout when the hull
  // TRULY changes — but stays put during transient states like tab
  // switches where the stations list momentarily empties (in which case
  // `picked` would be undefined and we'd wrongly conclude "no hull").
  useEffect(() => {
    if (loadingStations || loadingStructures) return; // don't write until data settles
    let suggestedME = 0;
    let suggestedJobCost = 0;
    let suggestedType = 0;
    let stationFound = false;
    if (includeStructures && sharedPrefs.buildStationID > 0) {
      const picked = allStations.find((s) => Number(s.id) === sharedPrefs.buildStationID);
      if (picked) {
        stationFound = true;
        if (picked.is_structure && picked.type_id) {
          suggestedME = STRUCTURE_ME_BONUS_BY_TYPE[picked.type_id] ?? 0;
          suggestedJobCost = STRUCTURE_JOB_COST_REDUCTION_BY_TYPE[picked.type_id] ?? 0;
          suggestedType = picked.type_id;
        }
      }
    }
    // Skip writes when a station is selected but not yet in the list —
    // the list is still hydrating and we'd otherwise clobber good state.
    if (sharedPrefs.buildStationID > 0 && !stationFound) return;
    const patch: Partial<typeof sharedPrefs> = {};
    if (suggestedME !== sharedPrefs.structureBonus) patch.structureBonus = suggestedME;
    if (suggestedJobCost !== sharedPrefs.structureJobCostReduction) patch.structureJobCostReduction = suggestedJobCost;
    if (suggestedType !== sharedPrefs.structureTypeID) {
      patch.structureTypeID = suggestedType;
      patch.structureRigTypeIDs = []; // stale rig fit — clear
    }
    if (Object.keys(patch).length > 0) updateSharedPrefs(patch);
    // STRUCTURE_ME_BONUS_BY_TYPE + STRUCTURE_JOB_COST_REDUCTION_BY_TYPE are
    // stable (memo); deps cover the inputs that actually drive a change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sharedPrefs.buildStationID, allStations, includeStructures, loadingStations, loadingStructures]);

  // If the selected build station disappears from the list after a build-system
  // change, clear it (the user must re-pick). Skip when the ID looks like a
  // structure and this component isn't loading structures — same-shared-prefs
  // symmetry so we don't clobber an ID another surface owns.
  useEffect(() => {
    if (sharedPrefs.buildStationID <= 0) return;
    if (loadingStations) return;
    if (!includeStructures && sharedPrefs.buildStationID > 100_000_000) return;
    if (includeStructures && loadingStructures) return;
    if (includeStructures && sharedPrefs.buildStationID > 100_000_000 && structureStations.length === 0) return;
    const exists = allStations.some((s) => Number(s.id) === sharedPrefs.buildStationID);
    if (!exists) {
      updateSharedPrefs({ buildStationID: 0 });
    }
  }, [allStations, sharedPrefs.buildStationID, includeStructures, loadingStations, loadingStructures, structureStations.length, updateSharedPrefs]);

  const updateParam = <K extends keyof PersistedParams>(key: K, value: PersistedParams[K]) => {
    setParams((prev) => {
      const next = { ...prev, [key]: value };
      savePersistedParams(next);
      return next;
    });
  };

  // Build the shared scan-request body. Always sends 0 for min thresholds so
  // the backend returns everything analyzed; threshold filtering is client-side.
  // Decryptor selection is intentionally NOT sent — the backend auto-picks the
  // winning decryptor per T2 row and reports which one won.
  const buildBaseScanRequest = useCallback((): ProfitableScanRequest => {
    return {
      scope: "all",
      // Scoring baseline: 1 run per job. Per-unit profit and ISK/h are
      // invariant across run counts, and "how many to build" is a separate
      // decision made when adding to a project.
      default_bpc_runs: 1,
      include_corp_blueprints: params.includeCorpBlueprints,
      build_system_name: sharedPrefs.buildSystem,
      pricing_system_name: params.pricingSystem,
      pricing_station_id: params.pricingStationID,
      facility_tax: sharedPrefs.facilityTax,
      structure_bonus: sharedPrefs.structureBonus,
      broker_fee: sharedPrefs.brokerFee,
      sales_tax_percent: sharedPrefs.salesTaxPercent,
      runs_per_job: 1,
      blueprint_filter: params.blueprintFilter,
      include_t2_invention: params.includeT2Invention,
      include_t3_invention: params.includeT3Invention,
      include_reactions: params.includeReactions,
      type_categories: params.typeCategories,
      invention_me_base: 0,
      invention_te_base: 0,
      invention_chance_mult: 0,
      invention_output_runs: 0,
      decryptor_cost: 0,
      include_unowned: params.includeUnowned,
      unowned_default_me: 10,
      unowned_default_te: 20,
      skip_reactions: sharedPrefs.skipReactions,
      structure_rig_type_ids: sharedPrefs.structureRigTypeIDs,
      structure_type_id: sharedPrefs.structureTypeID,
      structure_job_cost_reduction: sharedPrefs.structureJobCostReduction,
      revenue_model: sharedPrefs.revenueModel,
      cost_model: sharedPrefs.costModel,
      min_isk_per_hour: 0,
      min_profit: 0,
      min_margin_percent: 0,
    };
  }, [params, sharedPrefs]);

  // AbortController for the in-flight scan. Cancelling aborts the fetch,
  // which closes the NDJSON stream. The backend handler is context-aware
  // (writeLine short-circuits on ctx.Done()), so worker goroutines drain
  // without touching a closed writer. Ref (not state) so cancel() reads
  // the current controller even if it fires the same tick a scan starts.
  const scanAbortRef = useRef<AbortController | null>(null);

  const runScan = useCallback(async (req: ProfitableScanRequest, busyLabel: string) => {
    if (!isLoggedIn) {
      addToast(t("industryScannerLoginRequired"), "warning", 2400);
      return;
    }
    // Kill any previous in-flight scan first — the user starting a new one
    // implicitly cancels the old.
    scanAbortRef.current?.abort();
    const controller = new AbortController();
    scanAbortRef.current = controller;

    setScanning(true);
    setError(null);
    setProgressMsg(busyLabel);
    try {
      const resp = await scanProfitableBlueprints(req, (m) => setProgressMsg(m), controller.signal);
      setResponse(resp);
      if (resp.stats.cap_hit > 0) {
        addToast(
          t("industryScannerCapWarning").replace("{cap}", String(resp.stats.cap_hit)),
          "warning",
          3000,
        );
      }
      for (const w of resp.warnings ?? []) {
        addToast(w, "warning", 3000);
      }
    } catch (e: unknown) {
      // AbortError is the "user cancelled" path — no toast, no error state.
      if (e instanceof Error && (e.name === "AbortError" || controller.signal.aborted)) {
        setProgressMsg("");
        return;
      }
      const msg = e instanceof Error ? e.message : "Scan failed";
      setError(msg);
      addToast(msg, "error", 3000);
    } finally {
      // Only clear busy state if THIS scan is still the current one — a
      // second scan started mid-await would have replaced the ref.
      if (scanAbortRef.current === controller) {
        setScanning(false);
        setProgressMsg("");
        scanAbortRef.current = null;
      }
    }
  }, [isLoggedIn, addToast, t]);

  const cancelScan = useCallback(() => {
    scanAbortRef.current?.abort();
    scanAbortRef.current = null;
    setScanning(false);
    setProgressMsg("");
  }, []);

  // On unmount, abort any in-flight scan so it doesn't try to setState on
  // a torn-down component (React logs a warning otherwise).
  useEffect(() => {
    return () => {
      scanAbortRef.current?.abort();
    };
  }, []);

  const handleScan = useCallback(async () => {
    setSelectedIDs(new Set());
    // New scan = fresh row set. Purge manual runs overrides so we don't
    // carry stale edits from a previous scan's rows into unrelated new
    // ones. Reset happens BEFORE the fetch so if the scan fails the user
    // still sees the reset (which matches selection also being cleared).
    setManualRunsByRowKey(new Map());
    setDirtyRunsByRowKey(new Set());
    await runScan(buildBaseScanRequest(), t("industryScannerScanning"));
  }, [buildBaseScanRequest, runScan, t]);

  const handleClearResults = useCallback(() => {
    setResponse(null);
    setSelectedIDs(new Set());
    setSearchQuery("");
    setError(null);
    setManualRunsByRowKey(new Map());
    setDirtyRunsByRowKey(new Set());
  }, []);

  const handleRefreshPrices = useCallback(async () => {
    if (!response || response.rows.length === 0) return;
    // A single source blueprint can appear as multiple rows when T2
    // invention is on (1 T1 mfg + N T2 fan-out per source). All those rows
    // share `blueprint_type_id + is_bpo` — the SOURCE BP identity. Reuse
    // groups must represent SOURCES not scanned outputs, so dedupe by that
    // key before sending. Without this, the backend re-fans-out each
    // duplicate and rows multiply on every refresh.
    const reuseMap = new Map<string, ProfitableScanReuseRow>();
    for (const r of response.rows) {
      const key = `${r.blueprint_type_id}-${r.is_bpo ? "bpo" : "bpc"}`;
      if (reuseMap.has(key)) continue;
      reuseMap.set(key, {
        blueprint_type_id: r.blueprint_type_id,
        is_bpo: r.is_bpo,
        me: r.me,
        te: r.te,
        owned_quantity: r.owned_quantity,
        available_runs: r.available_runs,
        location_ids: r.location_ids ?? [],
        owned: r.owned ?? false,
      });
    }
    const req = {
      ...buildBaseScanRequest(),
      skip_blueprint_fetch: true,
      reuse_groups: Array.from(reuseMap.values()),
    };
    await runScan(req, t("industryScannerRefreshingPrices"));
  }, [response, buildBaseScanRequest, runScan, t]);

  const sortedRows = useMemo(() => {
    if (!response) return [];
    // Apply filters client-side so threshold tweaks are instant. The scan
    // request itself sends 0 so the backend returns everything analyzed.
    const q = searchQuery.trim().toLowerCase();
    const rows = response.rows.filter((r) => {
      const mode = r.scan_mode ?? "t1_mfg";
      if (mode === "t3_invention" && !params.showT3Rows) return false;
      if (mode === "t2_invention" && !params.showT2Rows) return false;
      if (mode === "reaction" && !params.showReactionRows) return false;
      if (mode === "t1_mfg" && !params.showT1Rows) return false;
      if (params.ownedFilter === "owned" && !r.owned) return false;
      if (params.ownedFilter === "unowned" && r.owned) return false;
      if (params.minISKPerHour != null && r.isk_per_hour < params.minISKPerHour) return false;
      if (params.minProfit != null && r.profit < params.minProfit) return false;
      if (params.minMarginPct != null && r.profit_percent < params.minMarginPct) return false;
      if (q) {
        // Match against every human-readable field the row carries:
        // blueprint + product names cover the direct lookup ("ishtar"),
        // group covers the ship-class query ("heavy assault cruiser"),
        // category covers the broad-type query ("ship" / "module").
        // group_name / category_name may be missing on cached results
        // from older builds, so guard with ?? "".
        const bp = r.blueprint_name?.toLowerCase() ?? "";
        const pr = r.product_name?.toLowerCase() ?? "";
        const grp = r.group_name?.toLowerCase() ?? "";
        const cat = r.category_name?.toLowerCase() ?? "";
        const src = r.invention_source_bp_name?.toLowerCase() ?? "";
        const out = r.invention_output_bp_name?.toLowerCase() ?? "";
        if (
          !bp.includes(q) &&
          !pr.includes(q) &&
          !grp.includes(q) &&
          !cat.includes(q) &&
          !src.includes(q) &&
          !out.includes(q)
        ) return false;
      }
      return true;
    });
    const mul = sortDir === "asc" ? 1 : -1;
    if (sortKey === "selected") {
      rows.sort((a, b) => {
        const av = selectedIDs.has(rowKey(a)) ? 1 : 0;
        const bv = selectedIDs.has(rowKey(b)) ? 1 : 0;
        if (av !== bv) return mul * (av - bv);
        // Stable secondary sort: keep ISK/h-desc within each selection bucket.
        return b.isk_per_hour - a.isk_per_hour;
      });
      return rows;
    }
    rows.sort((a, b) => {
      const av = a[sortKey];
      const bv = b[sortKey];
      if (typeof av === "string" && typeof bv === "string") {
        return mul * av.localeCompare(bv);
      }
      // Undefined-aware numeric compare: undefined always sorts to the bottom
      // (independent of asc/desc) so rows without market-history period stats
      // don't get mixed in with real zero-value rows at the top of asc sorts.
      const aNum = typeof av === "number" ? av : undefined;
      const bNum = typeof bv === "number" ? bv : undefined;
      if (aNum === undefined && bNum === undefined) return 0;
      if (aNum === undefined) return 1;
      if (bNum === undefined) return -1;
      return mul * (aNum - bNum);
    });
    return rows;
  }, [response, sortKey, sortDir, selectedIDs, params.minISKPerHour, params.minProfit, params.minMarginPct, params.showT1Rows, params.showT2Rows, params.showT3Rows, params.showReactionRows, params.ownedFilter, searchQuery]);

  const handleExportCsv = useCallback(() => {
    // Export the CURRENTLY VISIBLE rows (sortedRows applies search + filters
    // + sort) so what the user sees on screen matches the file — matches
    // typical spreadsheet-app UX and makes cross-tool comparisons easy.
    // Uses \r\n line endings + a UTF-8 BOM so Excel opens it cleanly on
    // Windows without garbling non-ASCII item names.
    const rows = sortedRows;
    if (rows.length === 0) return;

    const escapeCsv = (v: string): string => {
      if (v.includes(",") || v.includes('"') || v.includes("\n") || v.includes("\r")) {
        return '"' + v.replace(/"/g, '""') + '"';
      }
      return v;
    };
    const fmtNum = (n: number | undefined | null, decimals = 2): string =>
      n == null || !Number.isFinite(n) ? "" : n.toFixed(decimals);
    const fmtInt = (n: number | undefined | null): string =>
      n == null || !Number.isFinite(n) ? "" : String(Math.round(n));
    const fmtDuration = (seconds: number | undefined | null): string => {
      if (!seconds || seconds <= 0) return "";
      const s = Math.floor(seconds);
      const d = Math.floor(s / 86400);
      const h = Math.floor((s % 86400) / 3600);
      const m = Math.floor((s % 3600) / 60);
      const ss = s % 60;
      const hms = `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(ss).padStart(2, "0")}`;
      return d > 0 ? `${d}d ${hms}` : hms;
    };
    const scanModeLabel = (mode: string | undefined): string => {
      switch (mode) {
        case "t1_mfg": return "Tech I";
        case "t2_invention": return "Tech II (invention)";
        case "t3_invention": return "Tech III (invention)";
        case "reaction": return "Reaction";
        default: return mode ?? "";
      }
    };

    const headers = [
      "Product", "Blueprint", "Group", "Category", "Meta",
      "ME", "TE", "Runs", "Quantity",
      "ROI %", "Build Cost", "Materials", "Job Cost", "Invention Cost", "BP Amortization",
      "Sell Revenue", "Profit", "ISK/Hour",
      "Manufacturing Time", "Duration (sec)",
      "Daily Volume", "Period Profit", "Period ROI %",
      "Owned", "Owned Qty", "Available Runs",
      "Decryptor", "Invention Probability %", "Expected Attempts",
    ];

    const bpAmortByRow = (r: ProfitableScanRow): number => {
      // total_material_cost already includes invention datacores per the
      // engine's semantic, and total_job_cost already includes the
      // invention install. So the leftover between optimal_build_cost and
      // (materials + job) is blueprint amortization only — no double-count.
      const optimal = r.optimal_build_cost ?? 0;
      const mats = r.total_material_cost ?? 0;
      const job = r.total_job_cost ?? 0;
      return Math.max(0, optimal - mats - job);
    };

    const csvRows = rows.map((r) => [
      r.product_name ?? "",
      r.blueprint_name ?? "",
      r.group_name ?? "",
      r.category_name ?? "",
      scanModeLabel(r.scan_mode),
      String(r.me),
      String(r.te),
      String(r.runs),
      fmtInt(r.total_quantity),
      fmtNum(r.profit_percent, 2),
      fmtNum(r.optimal_build_cost, 2),
      fmtNum(r.total_material_cost, 2),
      fmtNum(r.total_job_cost, 2),
      fmtNum(r.invention_cost, 2),
      fmtNum(bpAmortByRow(r), 2),
      fmtNum(r.sell_revenue, 2),
      fmtNum(r.profit, 2),
      fmtNum(r.isk_per_hour, 2),
      fmtDuration(r.manufacturing_time),
      fmtInt(r.manufacturing_time),
      fmtInt(r.product_daily_volume),
      fmtNum(r.period_profit, 2),
      fmtNum(r.period_margin, 2),
      r.owned ? "true" : "false",
      fmtInt(r.owned_quantity),
      fmtInt(r.available_runs),
      r.best_decryptor_key ?? "",
      fmtNum((r.invention_probability ?? 0) * 100, 2),
      fmtNum(r.expected_attempts, 2),
    ]);

    const csv = "﻿" + [headers, ...csvRows]
      .map((row) => row.map((v) => escapeCsv(String(v))).join(","))
      .join("\r\n");
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8;" });
    const url = URL.createObjectURL(blob);
    const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
    const a = document.createElement("a");
    a.href = url;
    a.download = `industry-scan-${stamp}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [sortedRows]);

  const toggleSelect = (key: string) => {
    setSelectedIDs((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (selectedIDs.size === sortedRows.length && sortedRows.length > 0) {
      setSelectedIDs(new Set());
    } else {
      setSelectedIDs(new Set(sortedRows.map(rowKey)));
    }
  };

  // Derive selections from the FULL scan response, not the current filtered
  // view. Otherwise: pick 5 items → type in the search box → the ones filtered
  // out silently disappear from the "Add to project" bundle. Selections must
  // persist regardless of what's currently visible.
  const selectedRows: ProfitableScanRow[] = useMemo(() => {
    if (!response) return [];
    return response.rows.filter((r) => selectedIDs.has(rowKey(r)));
  }, [response, selectedIDs]);

  const selectionTotals = useMemo(() => {
    let capital = 0;
    let profit = 0;
    for (const r of selectedRows) {
      capital += r.optimal_build_cost || 0;
      profit += r.profit || 0;
    }
    return { capital, profit };
  }, [selectedRows]);

  if (!isLoggedIn) {
    return (
      <div className="m-2">
        <div className="bg-eve-panel border border-eve-border rounded-sm p-4 text-xs text-eve-dim">
          {t("industryScannerLoginRequired")}
        </div>
      </div>
    );
  }

  return (
    <div className="m-2 space-y-2">
      <TabSettingsPanel
        title={t("industryScannerTitle")}
        hint={t("industryScannerIntro")}
        icon="⚙"
        defaultExpanded={true}
        persistKey={SCANNER_PERSIST_KEY}
      >
        {/* ==== Item & Build ====
            Layout: (1) "Include" chip row — three toggles for what feeds the
            scan pool (Corp BPs / T2 invention fan-out / Unowned SDE BPs). (2)
            "Kind" chip row — BPO/BPC/Both. (3) A numbers grid for the counts
            that don't fit chips (max BPs, runs/job, default BPC runs, build
            mode). No more Scope selector (always "all" characters) and no
            more decryptor picker (backend auto-picks the winning decryptor
            per T2 row). */}
        <SettingsSubsection
          title={t("industrySectionItemBuild")}
          persistKey="industry-scanner-item-build"
          first
        >

          {/* Include chips — orthogonal toggles for pool composition. The
              parent "Invention" chip is the OR of T2 + T3. Toggling it off
              turns both off; toggling on when both were off defaults to T2. */}
          <div className="mb-2">
            <div className="text-[10px] text-eve-dim mb-1">{t("industryScannerIncludeGroupLabel")}</div>
            <div className="flex flex-wrap gap-1">
              <ToggleChip
                active={params.includeCorpBlueprints}
                onClick={() => updateParam("includeCorpBlueprints", !params.includeCorpBlueprints)}
                label={t("industryScannerIncludeCorpChip")}
                title={t("industryScannerIncludeCorpHint")}
              />
              <ToggleChip
                active={params.includeT2Invention || params.includeT3Invention}
                onClick={() => {
                  const on = params.includeT2Invention || params.includeT3Invention;
                  if (on) {
                    // Turn both off.
                    setParams((prev) => {
                      const next = { ...prev, includeT2Invention: false, includeT3Invention: false };
                      savePersistedParams(next);
                      return next;
                    });
                  } else {
                    // Enable T2 as the default when the parent chip is turned on.
                    updateParam("includeT2Invention", true);
                  }
                }}
                label={t("industryScannerIncludeInventionChip")}
                title={t("industryScannerIncludeT2InventionHint")}
              />
              <ToggleChip
                active={params.includeReactions}
                onClick={() => updateParam("includeReactions", !params.includeReactions)}
                label={t("industryScannerIncludeReactionsChip")}
                title={t("industryScannerIncludeReactionsHint")}
              />
              <ToggleChip
                active={params.includeUnowned}
                onClick={() => updateParam("includeUnowned", !params.includeUnowned)}
                label={t("industryScannerIncludeUnownedChip")}
                title={t("industryScannerIncludeUnownedHint")}
              />
            </div>
            {(params.includeT2Invention || params.includeT3Invention) && (
              <div className="mt-1 pl-3 flex flex-wrap gap-1 items-center">
                <span className="text-[10px] text-eve-dim mr-1">
                  {t("industryScannerInventionTierGroupLabel")}
                </span>
                <ToggleChip
                  active={params.includeT2Invention}
                  onClick={() => updateParam("includeT2Invention", !params.includeT2Invention)}
                  label={t("industryScannerIncludeT2Chip")}
                />
                <ToggleChip
                  active={params.includeT3Invention}
                  onClick={() => updateParam("includeT3Invention", !params.includeT3Invention)}
                  label={t("industryScannerIncludeT3Chip")}
                  title={t("industryScannerIncludeT3InventionHint")}
                />
              </div>
            )}
          </div>

          {/* Blueprint kind chips — mutually exclusive. */}
          <div className="mb-2">
            <div className="text-[10px] text-eve-dim mb-1">{t("industryScannerBPFilterLabel")}</div>
            <div className="flex flex-wrap gap-1">
              {(["bpo", "bpc", "both"] as const).map((k) => {
                const active = params.blueprintFilter === k;
                const labelKey =
                  k === "bpo"
                    ? "industryScannerBPFilterBPO"
                    : k === "bpc"
                      ? "industryScannerBPFilterBPC"
                      : "industryScannerBPFilterBoth";
                return (
                  <ToggleChip
                    key={k}
                    active={active}
                    onClick={() => updateParam("blueprintFilter", k)}
                    label={t(labelKey)}
                  />
                );
              })}
            </div>
          </div>

          {/* Type filter — multi-select over product categoryIDs. Empty
              typeCategories = include all; that's the default so first-time
              users see the same results they got before this filter existed. */}
          <div className="mb-2">
            <div className="text-[10px] text-eve-dim mb-1 flex items-center gap-2">
              <span>{t("industryScannerTypeFilterLabel")}</span>
              {params.typeCategories.length > 0 && (
                <button
                  type="button"
                  onClick={() => updateParam("typeCategories", [])}
                  className="text-[10px] text-eve-dim hover:text-eve-text underline"
                >
                  {t("industryScannerTypeFilterReset")}
                </button>
              )}
            </div>
            <div className="flex flex-wrap gap-1">
              {TYPE_CHIPS.map((chip) => {
                // Chip is "on" when the filter is empty (all types) OR every
                // categoryID for this chip is in the whitelist. Toggling adds
                // or removes this chip's category IDs from the whitelist.
                const allOn = params.typeCategories.length === 0;
                const active =
                  allOn ||
                  chip.categoryIDs.every((id) => params.typeCategories.includes(id));
                return (
                  <ToggleChip
                    key={chip.key}
                    active={active}
                    onClick={() => {
                      let next: number[];
                      if (allOn) {
                        // Transition from "all" → "everything except this chip".
                        // Starting explicit whitelist = all chips minus this one.
                        next = ALL_TYPE_CATEGORY_IDS.filter(
                          (id) => !chip.categoryIDs.includes(id),
                        );
                      } else if (active) {
                        next = params.typeCategories.filter(
                          (id) => !chip.categoryIDs.includes(id),
                        );
                      } else {
                        next = [...params.typeCategories, ...chip.categoryIDs];
                      }
                      // Collapse back to "all types" when every chip would be
                      // active — avoids sending an unnecessarily large whitelist.
                      const isAllActive = ALL_TYPE_CATEGORY_IDS.every((id) =>
                        next.includes(id),
                      );
                      updateParam("typeCategories", isAllActive ? [] : next);
                    }}
                    label={t(chip.labelKey as Parameters<typeof t>[0])}
                  />
                );
              })}
            </div>
          </div>

          {/* Numbers grid — everything that doesn't fit a chip. */}
          <SettingsGrid cols={4}>
            <SettingsField label={t("industryBuildMode")}>
              <SettingsSelect
                value={sharedPrefs.buildMode}
                onChange={(v) => updateSharedPrefs({ buildMode: v as "auto" | "buy_all" | "build_all" })}
                options={[
                  { value: "auto", label: t("industryBuildModeAuto") },
                  { value: "buy_all", label: t("industryBuildModeBuyAll") },
                  { value: "build_all", label: t("industryBuildModeBuildAll") },
                ]}
              />
            </SettingsField>
          </SettingsGrid>
        </SettingsSubsection>

        {/* ==== Sell / market side (pricing region + revenue model). Sits
            above the build side so the mental flow reads "here's what I'm
            selling, here's how I'm selling it → now here's how I'm building
            it". The revenue model dropdown lives here (not in build fees)
            because it's a property of the sale, not the build. ==== */}
        <SettingsSubsection
          title={t("industrySectionPricing")}
          persistKey="industry-scanner-pricing"
        >
          <PricingHubPicker
            systemName={params.pricingSystem}
            stationID={params.pricingStationID}
            onChange={(sys, stationID) => {
              setParams((prev) => {
                const next: PersistedParams = {
                  ...prev,
                  pricingSystem: sys,
                  pricingStationID: stationID,
                };
                savePersistedParams(next);
                return next;
              });
            }}
            isLoggedIn={isLoggedIn}
          />
          <div className="mt-1 text-[10px] text-eve-dim">
            {params.pricingStationID > 0
              ? `Pricing from station ${params.pricingStationID} (${params.pricingSystem || "unknown"} region).`
              : params.pricingSystem.trim()
                ? `Pricing region-wide in ${params.pricingSystem}.`
                : "Pricing falls back to the build system's region."}
          </div>
          <div className="mt-3">
            <SettingsGrid cols={2}>
              <SettingsField label={t("industryProductDestinationLabel")} hint={t("industryProductDestinationHint")}>
                <SettingsSelect
                  value={sharedPrefs.revenueModel}
                  onChange={(v) => updateSharedPrefs({ revenueModel: v as "sell_to_sell" | "sell_to_buy" })}
                  options={[
                    { value: "sell_to_sell", label: t("industryOrderSideSellOrders") },
                    { value: "sell_to_buy", label: t("industryOrderSideBuyOrders") },
                  ]}
                />
              </SettingsField>
              <SettingsField label={t("industryMaterialSourceLabel")} hint={t("industryMaterialSourceHint")}>
                <SettingsSelect
                  value={sharedPrefs.costModel}
                  onChange={(v) => updateSharedPrefs({ costModel: v as "buy_to_sell" | "buy_to_buy" })}
                  options={[
                    { value: "buy_to_sell", label: t("industryOrderSideSellOrders") },
                    { value: "buy_to_buy", label: t("industryOrderSideBuyOrders") },
                  ]}
                />
              </SettingsField>
            </SettingsGrid>
          </div>
        </SettingsSubsection>

        {/* ==== Location & Fees ==== */}
        <SettingsSubsection
          title={t("industrySectionLocationFees")}
          persistKey="industry-scanner-location-fees"
        >
          <SettingsGrid cols={3}>
            <SettingsField label={t("industryScannerBuildSystemLabel")}>
              <SystemAutocomplete
                value={sharedPrefs.buildSystem}
                onChange={(v) => updateSharedPrefs({ buildSystem: v })}
                showLocationButton={false}
                isLoggedIn={isLoggedIn}
                suppressInternalHint
              />
            </SettingsField>
            <SettingsField label={t("stationSelect")}>
              {loadingStations || loadingStructures ? (
                <div className="h-[34px] flex items-center text-xs text-eve-dim">
                  {loadingStructures ? t("loadingStructures") : t("loadingStations")}
                </div>
              ) : allStations.length === 0 ? (
                <div className="h-[34px] flex items-center text-xs text-eve-dim">
                  {stations.length === 0 && !isLoggedIn
                    ? t("noNpcStationsLoginHint")
                    : stations.length === 0 && isLoggedIn && !includeStructures
                      ? t("noNpcStationsToggleHint")
                      : includeStructures
                        ? t("noStationsOrInaccessible")
                        : t("noStations")}
                </div>
              ) : (
                <SettingsSelect
                  value={sharedPrefs.buildStationID}
                  onChange={(v) => updateSharedPrefs({ buildStationID: Number(v) })}
                  options={[
                    { value: 0, label: t("allStations") },
                    ...allStations.map((st) => ({
                      value: st.id,
                      label: st.is_structure ? `🏗️ ${st.name}` : st.name,
                    })),
                  ]}
                />
              )}
            </SettingsField>
            <SettingsField label={t("includeStructures")}>
              <SettingsCheckbox
                checked={includeStructures}
                onChange={setIncludeStructures}
              />
            </SettingsField>
          </SettingsGrid>
          {(() => {
            if (loadingStations) return <div className="mt-2 text-[10px] text-eve-dim">{t("loadingStations")}</div>;
            if (loadingStructures) return <div className="mt-2 text-[10px] text-eve-dim">{t("loadingStructures")}</div>;
            if (includeStructures) {
              return (
                <div className="mt-2 text-[10px] text-eve-dim">
                  {structureStations.length > 0
                    ? `${structureStations.length} accessible structure(s) resolved for this system.`
                    : "Private/corp structures depend on ESI ACL visibility; if none appear, verify character access and scopes."}
                </div>
              );
            }
            if (stations.length === 0 && sharedPrefs.buildSystem.trim()) {
              return (
                <div className="mt-2 text-[10px] text-amber-400/80">
                  {!isLoggedIn ? t("noNpcStationsLoginHint") : t("noNpcStationsToggleHint")}
                </div>
              );
            }
            return null;
          })()}

          <div className="mt-3">
            <SettingsGrid cols={4}>
              <SettingsField label={t("industryScannerFacilityTaxLabel")} hint={t("industryFacilityTaxHint")}>
                <SettingsNumberInput
                  value={sharedPrefs.facilityTax}
                  onChange={(v) => updateSharedPrefs({ facilityTax: v })}
                  min={0}
                  max={100}
                  step={0.01}
                />
              </SettingsField>
              <SettingsField
                label={t("industryStructureInherentMELabel")}
                hint={t("industryStructureInherentMEHint")}
                belowChildren={rigTotals.me > 0 ? (
                  <span title={t("industryStructureRigDerivedTooltip")}>
                    {t("industryStructureRigDerivedLabel")}: up to −{rigTotals.me.toFixed(2)}%
                  </span>
                ) : undefined}
              >
                <SettingsNumberInput
                  value={sharedPrefs.structureBonus}
                  onChange={(v) => updateSharedPrefs({ structureBonus: v })}
                  disabled
                  title={t("industryStructureInherentMEHint")}
                  min={-100}
                  max={100}
                  step={0.01}
                />
              </SettingsField>
              <SettingsField
                label={t("industryStructureInherentJobCostLabel")}
                hint={t("industryStructureInherentJobCostHint")}
                belowChildren={rigTotals.cost > 0 ? (
                  <span title={t("industryStructureRigDerivedTooltip")}>
                    {t("industryStructureRigDerivedLabel")}: up to −{rigTotals.cost.toFixed(2)}%
                  </span>
                ) : undefined}
              >
                <SettingsNumberInput
                  value={sharedPrefs.structureJobCostReduction}
                  onChange={(v) => updateSharedPrefs({ structureJobCostReduction: v })}
                  disabled
                  title={t("industryStructureInherentJobCostHint")}
                  min={0}
                  max={100}
                  step={0.01}
                />
              </SettingsField>
              <SettingsField label={t("industryScannerBrokerFeeLabel")} hint={t("industryBrokerFeeHint")}>
                <SettingsNumberInput
                  value={sharedPrefs.brokerFee}
                  onChange={(v) => updateSharedPrefs({ brokerFee: v })}
                  min={0}
                  max={100}
                  step={0.01}
                />
              </SettingsField>
              <SettingsField label={t("industryScannerSalesTaxLabel")} hint={t("industrySalesTaxHint")}>
                <SettingsNumberInput
                  value={sharedPrefs.salesTaxPercent}
                  onChange={(v) => updateSharedPrefs({ salesTaxPercent: v })}
                  min={0}
                  max={100}
                  step={0.01}
                />
              </SettingsField>
            </SettingsGrid>
            {isLoggedIn && (
              <div className="mt-2 flex items-center gap-2">
                <button
                  type="button"
                  onClick={handleImportFees}
                  disabled={importingFees}
                  className="px-2 py-1 text-[11px] font-semibold rounded-sm border border-eve-accent text-eve-accent
                             hover:bg-eve-accent/10 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                >
                  {importingFees ? t("industryScannerImportFeesPending") : t("industryScannerImportFeesBtn")}
                </button>
                <span className="text-[10px] text-eve-dim">Pulls broker fee + sales tax from Accounting / Broker Relations skill levels of the active character.</span>
              </div>
            )}
            {sharedPrefs.structureTypeID > 0 && (
              <div className="mt-3">
                <StructureRigPicker
                  structureTypeID={sharedPrefs.structureTypeID}
                  selectedRigTypeIDs={sharedPrefs.structureRigTypeIDs}
                  onChange={(ids) => updateSharedPrefs({ structureRigTypeIDs: ids })}
                  systemSecurity={buildSystemSec}
                />
              </div>
            )}
          </div>
        </SettingsSubsection>
      </TabSettingsPanel>

      {/* Filters live in their own collapsible panel outside the Settings
          panel so the user can hide the (mostly-static) settings once they're
          tuned while still adjusting result-table thresholds live. Separate
          persistKey means each panel remembers its own open/closed state. */}
      <TabSettingsPanel
        title={t("industrySectionFilters")}
        icon="🔍"
        defaultExpanded={true}
        persistKey={SCANNER_PERSIST_KEY + ":filters"}
      >
        <SettingsGrid cols={3}>
          <SettingsField label={t("industryScannerMinISKPerHourLabel")}>
            <NullableNumberInput
              value={params.minISKPerHour}
              onChange={(v) => updateParam("minISKPerHour", v)}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerMinProfitLabel")}>
            <NullableNumberInput
              value={params.minProfit}
              onChange={(v) => updateParam("minProfit", v)}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerMinMarginLabel")}>
            <NullableNumberInput
              value={params.minMarginPct}
              onChange={(v) => updateParam("minMarginPct", v)}
              step={0.1}
            />
          </SettingsField>
        </SettingsGrid>
      </TabSettingsPanel>

      {/* Scan / Refresh live outside the collapsible params panel so the user
          can trigger them even while the parameters section is collapsed. */}
      <div className="flex items-center gap-3 px-1">
        {scanning ? (
          <button
            type="button"
            onClick={cancelScan}
            title={t("industryScannerCancelHint")}
            className="px-3 py-1.5 text-xs font-semibold rounded-sm border border-red-500/60 text-red-300
                       hover:bg-red-500/10 transition-colors"
          >
            {t("industryScannerCancelBtn")}
          </button>
        ) : (
          <button
            type="button"
            onClick={handleScan}
            className="px-3 py-1.5 text-xs font-semibold rounded-sm border border-eve-accent text-eve-accent
                       hover:bg-eve-accent/10 transition-colors"
          >
            {t("industryScannerScanBtn")}
          </button>
        )}
        <button
          type="button"
          onClick={handleRefreshPrices}
          disabled={scanning || !response || response.rows.length === 0}
          title="Re-runs the analyzer over the same blueprints with fresh ESI prices — no blueprint refetch."
          className="px-3 py-1.5 text-xs rounded-sm border border-eve-border text-eve-dim
                     hover:text-eve-accent hover:border-eve-accent disabled:opacity-50
                     disabled:cursor-not-allowed transition-colors"
        >
          {scanning ? t("industryScannerRefreshingPrices") : t("industryScannerRefreshPricesBtn")}
        </button>
        {scanning && progressMsg && (
          <span className="text-xs text-eve-dim">{progressMsg}</span>
        )}
        {error && !scanning && (
          <span className="text-xs text-red-300">{error}</span>
        )}
      </div>

      {!response && !scanning && (
        <div className="bg-eve-panel border border-eve-border rounded-sm p-4">
          <EmptyState reason="no_scan_yet" hints={[t("industryScannerNoScanYet")]} />
        </div>
      )}

      {response && (
        <div className="bg-eve-panel border border-eve-border rounded-sm">
          <div className="px-3 py-2 border-b border-eve-border/50 flex items-center justify-between gap-2 flex-wrap">
            <div className="text-[11px] text-eve-dim">
              {t("industryScannerStatsLine")
                .replace("{groups}", String(response.stats.owned_blueprint_groups))
                .replace("{analyzed}", String(response.stats.analyzed))
                .replace("{filtered}", String(Math.max(0, response.rows.length - sortedRows.length)))
                .replace("{errors}", String(response.stats.errors))}
            </div>
            <div className="flex items-center gap-2">
              <div className="relative">
                <input
                  type="text"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  placeholder={t("industryScannerSearchPlaceholder")}
                  className="pl-2 pr-7 py-1 text-[11px] bg-eve-input border border-eve-border rounded-sm text-eve-text
                             focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                             w-56 transition-colors"
                />
                {searchQuery && (
                  <button
                    type="button"
                    onClick={() => setSearchQuery("")}
                    aria-label="Clear search"
                    title="Clear search"
                    className="absolute right-1.5 top-1/2 -translate-y-1/2 text-eve-dim hover:text-eve-text text-xs leading-none px-1"
                  >
                    ×
                  </button>
                )}
              </div>
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  onClick={() => updateParam("showT1Rows", !params.showT1Rows)}
                  title={t("industryScannerScanModeT1")}
                  className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                    params.showT1Rows
                      ? "border-emerald-400/60 text-emerald-300 bg-emerald-400/10"
                      : "border-eve-border text-eve-dim hover:text-eve-text"
                  }`}
                >
                  {t("industryScannerScanModeT1")}
                </button>
                <button
                  type="button"
                  onClick={() => updateParam("showT2Rows", !params.showT2Rows)}
                  title={t("industryScannerScanModeT2")}
                  className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                    params.showT2Rows
                      ? "border-violet-400/60 text-violet-300 bg-violet-400/10"
                      : "border-eve-border text-eve-dim hover:text-eve-text"
                  }`}
                >
                  {t("industryScannerScanModeT2")}
                </button>
                <button
                  type="button"
                  onClick={() => updateParam("showT3Rows", !params.showT3Rows)}
                  title={t("industryScannerScanModeT3")}
                  className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                    params.showT3Rows
                      ? "border-sky-400/60 text-sky-300 bg-sky-400/10"
                      : "border-eve-border text-eve-dim hover:text-eve-text"
                  }`}
                >
                  {t("industryScannerScanModeT3")}
                </button>
                <button
                  type="button"
                  onClick={() => updateParam("showReactionRows", !params.showReactionRows)}
                  title={t("industryScannerScanModeReaction")}
                  className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                    params.showReactionRows
                      ? "border-amber-400/60 text-amber-300 bg-amber-400/10"
                      : "border-eve-border text-eve-dim hover:text-eve-text"
                  }`}
                >
                  {t("industryScannerScanModeReaction")}
                </button>
              </div>
              {/* Owned filter — All / Owned / Unowned. Radio-style, only one active. */}
              <div className="flex items-center gap-1">
                {(["all", "owned", "unowned"] as const).map((k) => {
                  const active = params.ownedFilter === k;
                  const labelKey =
                    k === "all"
                      ? "industryScannerOwnedFilterAll"
                      : k === "owned"
                        ? "industryScannerOwnedFilterOwned"
                        : "industryScannerOwnedFilterUnowned";
                  return (
                    <button
                      key={k}
                      type="button"
                      onClick={() => updateParam("ownedFilter", k)}
                      className={`px-2 py-0.5 text-[10px] rounded-sm border transition-colors ${
                        active
                          ? "border-eve-accent/60 text-eve-accent bg-eve-accent/10"
                          : "border-eve-border text-eve-dim hover:text-eve-text"
                      }`}
                    >
                      {t(labelKey)}
                    </button>
                  );
                })}
              </div>
              <span className="text-[11px] text-eve-dim">
                {t("industryScannerSelectedSummary")
                  .replace("{count}", String(selectedIDs.size))
                  .replace("{capital}", formatISK(selectionTotals.capital))
                  .replace("{profit}", formatISK(selectionTotals.profit))}
              </span>
              <button
                type="button"
                onClick={() => setAddToProjectOpen(true)}
                disabled={selectedIDs.size === 0}
                className="px-2 py-1 text-[11px] font-semibold rounded-sm border border-eve-accent text-eve-accent
                           hover:bg-eve-accent/10 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
              >
                {t("industryScannerAddToProject")}
              </button>
              <button
                type="button"
                onClick={handleExportCsv}
                disabled={sortedRows.length === 0}
                title={t("industryScannerExportCsvTitle")}
                className="px-2 py-1 text-[11px] rounded-sm border border-eve-border text-eve-dim
                           hover:text-eve-accent hover:border-eve-accent/40 disabled:opacity-40
                           disabled:cursor-not-allowed transition-colors"
              >
                {t("industryScannerExportCsv")}
              </button>
              <button
                type="button"
                onClick={handleClearResults}
                title={t("industryScannerClearResultsTitle")}
                className="px-2 py-1 text-[11px] rounded-sm border border-eve-border text-eve-dim
                           hover:text-red-300 hover:border-red-500/40 transition-colors"
              >
                {t("industryScannerClearResults")}
              </button>
            </div>
          </div>

          {sortedRows.length === 0 ? (
            <div className="p-4 text-xs text-eve-dim">{t("industryScannerNoResults")}</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead className="bg-eve-dark/60 text-eve-dim">
                  <tr>
                    <th className="px-2 py-1.5 text-left w-14">
                      <div className="flex items-center gap-1">
                        <input
                          type="checkbox"
                          checked={selectedIDs.size === sortedRows.length && sortedRows.length > 0}
                          onChange={toggleSelectAll}
                          title="Select all / clear all"
                        />
                        <button
                          type="button"
                          onClick={() => toggleSort("selected")}
                          className={`text-[9px] leading-none hover:text-eve-text transition-colors ${
                            sortKey === "selected" ? "text-eve-accent" : ""
                          }`}
                          title="Sort by selected"
                        >
                          {sortKey === "selected" ? (sortDir === "desc" ? "▼" : "▲") : "↕"}
                        </button>
                      </div>
                    </th>
                    <SortableHeader sortKey="blueprint_name" align="left" label={t("industryScannerColBlueprint")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="product_name" align="left" label={t("industryScannerColProduct")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="owned_quantity" align="right" label={t("industryScannerColOwned")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="available_runs" align="right" label={t("industryScannerColRunsAvail")} active={sortKey} dir={sortDir} onClick={toggleSort} titleText={t("industryScannerColRunsAvailTooltip")} />
                    <SortableHeader sortKey="me" align="right" label={t("industryScannerColME")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="te" align="right" label={t("industryScannerColTE")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="isk_per_hour" align="right" label={t("industryScannerColISKHour")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="profit" align="right" label={t("industryScannerColProfit")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="profit_percent" align="right" label={t("industryScannerColMargin")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="period_profit" align="right" label={t("industryScannerColPeriodProfit")} active={sortKey} dir={sortDir} onClick={toggleSort} titleText={t("industryScannerColPeriodProfitTooltip")} />
                    <SortableHeader sortKey="period_margin" align="right" label={t("industryScannerColPeriodMargin")} active={sortKey} dir={sortDir} onClick={toggleSort} titleText={t("industryScannerColPeriodMarginTooltip")} />
                    <SortableHeader sortKey="optimal_build_cost" align="right" label={t("industryScannerColCapital")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="manufacturing_time" align="right" label={t("industryScannerColTime")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <th className="px-2 py-1.5 text-right w-10" aria-label={t("industryScannerColActions")} title={t("industryScannerColActions")} />
                  </tr>
                </thead>
                <tbody>
                  {sortedRows.map((row) => {
                    const k = rowKey(row);
                    const checked = selectedIDs.has(k);
                    const hours = row.manufacturing_time / 3600;
                    const isT2 = row.scan_mode === "t2_invention";
                    const isT3 = row.scan_mode === "t3_invention";
                    const isInvention = isT2 || isT3;
                    const capUnlimited = (row.attempts_cap ?? -1) < 0;
                    const capLabel = capUnlimited
                      ? t("industryScannerAttemptsCapUnlimited")
                      : String(row.attempts_cap ?? 0);
                    const inventionTooltip = isInvention
                      ? `Invention: ${((row.invention_probability ?? 0) * 100).toFixed(1)}% × ${(row.expected_attempts ?? 0).toFixed(1)} attempts (cap ${capLabel})`
                      : undefined;
                    const isUnowned = row.owned === false;

                    // Per-row profit-math breakdown for the Profit/Margin/ISK-h
                    // tooltips. Native title attribute handles multi-line via \n
                    // — plaintext keeps it drama-free and works everywhere.
                    // Falls back gracefully when the extra fields aren't set
                    // (older cached scan results).
                    const totalUnits = row.total_quantity ?? row.runs * (row.output_qty_per_run ?? 1);
                    const matCost = row.total_material_cost ?? Math.max(0, row.optimal_build_cost - (row.total_job_cost ?? 0) - (row.invention_cost ?? 0));
                    const jobCost = row.total_job_cost ?? 0;
                    const invCost = row.invention_cost ?? 0;
                    const unitPrice = row.unit_sell_price ?? (totalUnits > 0 ? row.sell_revenue / totalUnits : 0);
                    const profitTooltipLines: string[] = [];
                    profitTooltipLines.push(`═ ${row.product_name || "Product"} ═`);
                    profitTooltipLines.push(`Runs: ${row.runs} × ${row.output_qty_per_run ?? 1} = ${totalUnits.toLocaleString()} units`);
                    profitTooltipLines.push("");
                    profitTooltipLines.push(`Sell revenue:   ${formatISK(row.sell_revenue)}`);
                    if (unitPrice > 0) {
                      profitTooltipLines.push(`  ${totalUnits.toLocaleString()} × ${formatISK(unitPrice)}/unit (after tax + broker fee)`);
                    }
                    profitTooltipLines.push("");
                    profitTooltipLines.push(`Build cost:     ${formatISK(row.optimal_build_cost)}`);
                    if (matCost > 0) profitTooltipLines.push(`  Materials:    ${formatISK(matCost)}`);
                    if (jobCost > 0) profitTooltipLines.push(`  Job cost:     ${formatISK(jobCost)}`);
                    if (invCost > 0) profitTooltipLines.push(`  (of which invention: ${formatISK(invCost)})`);
                    profitTooltipLines.push("");
                    profitTooltipLines.push(`Profit:         ${formatISK(row.profit)}`);
                    profitTooltipLines.push(`ROI:            ${row.profit_percent.toFixed(1)}%`);
                    if (row.manufacturing_time > 0) {
                      const hoursDisp = row.manufacturing_time / 3600;
                      profitTooltipLines.push(`Time:           ${hoursDisp.toFixed(1)}h`);
                      profitTooltipLines.push(`ISK/hour:       ${formatISK(row.isk_per_hour)}`);
                    }
                    if (isInvention) {
                      profitTooltipLines.push("");
                      profitTooltipLines.push(`Invention: ${((row.invention_probability ?? 0) * 100).toFixed(1)}% × ${(row.expected_attempts ?? 0).toFixed(1)} attempts (cap ${capLabel})`);
                      if (row.best_decryptor_key) {
                        profitTooltipLines.push(`Best decryptor: ${row.best_decryptor_key}`);
                      }
                    }
                    const profitTooltip = profitTooltipLines.join("\n");
                    return (
                      <tr
                        key={k}
                        className={`border-t border-eve-border/30 hover:bg-eve-accent/5 ${
                          checked ? "bg-eve-accent/10" : ""
                        } ${isUnowned ? "opacity-60" : ""}`}
                      >
                        <td className="px-2 py-1">
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => toggleSelect(k)}
                          />
                        </td>
                        <td className="px-2 py-1 font-medium text-eve-text" title={inventionTooltip}>
                          {isInvention ? (
                            <span>
                              <span className="text-eve-dim">
                                {row.invention_source_bp_name || row.blueprint_name}
                              </span>
                              <span className="text-eve-dim/60"> → </span>
                              <span className="text-eve-text">
                                {row.invention_output_bp_name || `${row.product_name} Blueprint`}
                              </span>
                            </span>
                          ) : (
                            row.blueprint_name
                          )}
                          {row.is_bpo ? (
                            <span className="ml-1 text-[10px] text-emerald-300">[BPO]</span>
                          ) : (
                            <span className="ml-1 text-[10px] text-amber-300">[BPC]</span>
                          )}
                          {isUnowned && (
                            <span className="ml-1 text-[10px] text-slate-400">
                              {t("industryScannerUnownedBadge")}
                            </span>
                          )}
                          {row.scan_mode === "reaction" && (
                            <span className="ml-1 text-[10px] text-amber-300">
                              [REACT]
                            </span>
                          )}
                          {isInvention && (
                            <span
                              className={`ml-1 text-[10px] ${
                                row.attempts_cap_exceeded
                                  ? "text-amber-400"
                                  : isT3
                                    ? "text-sky-300"
                                    : "text-violet-300"
                              }`}
                              title={
                                row.attempts_cap_exceeded
                                  ? t("industryScannerAttemptsCapExceeded")
                                  : undefined
                              }
                            >
                              [{isT3 ? "T3" : "T2"} INV{row.attempts_cap_exceeded ? "!" : ""}]
                            </span>
                          )}
                          {isInvention && row.best_decryptor_key && (() => {
                            const dec = DECRYPTORS[row.best_decryptor_key as keyof typeof DECRYPTORS];
                            const label = dec ? dec.name : row.best_decryptor_key;
                            const isNone = row.best_decryptor_key === "none";
                            return (
                              <span
                                className={`ml-1 text-[10px] ${
                                  isNone ? "text-slate-400" : "text-sky-300"
                                }`}
                                title={t("industryScannerBestDecryptorTooltip").replace(
                                  "{name}",
                                  label,
                                )}
                              >
                                [{isNone ? t("industryScannerBestDecryptorNone") : label}]
                              </span>
                            );
                          })()}
                        </td>
                        <td className="px-2 py-1 text-eve-dim">{row.product_name}</td>
                        <td className="px-2 py-1 text-right font-mono">{row.owned_quantity}</td>
                        <td className="px-2 py-1 text-right font-mono text-eve-dim">
                          {/* BPOs are unlimited by definition; invention and unowned
                              rows have no "stock" concept — dash out those cells. */}
                          {row.is_bpo
                            ? "∞"
                            : row.owned === false
                              ? "—"
                              : isInvention
                                ? "—"
                                : row.available_runs.toLocaleString()}
                        </td>
                        <td className="px-2 py-1 text-right font-mono">{row.me}</td>
                        <td className="px-2 py-1 text-right font-mono">{row.te}</td>
                        <td
                          className={`px-2 py-1 text-right font-mono cursor-help ${
                            row.isk_per_hour >= 0 ? "text-emerald-300" : "text-red-300"
                          }`}
                          title={profitTooltip}
                        >
                          {formatISK(row.isk_per_hour)}
                        </td>
                        <td
                          className={`px-2 py-1 text-right font-mono cursor-help ${
                            row.profit >= 0 ? "text-emerald-300" : "text-red-300"
                          }`}
                          title={profitTooltip}
                        >
                          {formatISK(row.profit)}
                        </td>
                        <td
                          className={`px-2 py-1 text-right font-mono cursor-help ${
                            row.profit_percent >= 0 ? "text-eve-text" : "text-red-300"
                          }`}
                          title={profitTooltip}
                        >
                          {row.profit_percent.toFixed(1)}%
                        </td>
                        <td
                          className={`px-2 py-1 text-right font-mono ${
                            row.period_profit === undefined
                              ? "text-eve-dim"
                              : row.period_profit >= 0
                                ? "text-emerald-300"
                                : "text-red-300"
                          }`}
                          title={
                            row.product_daily_volume !== undefined
                              ? t("industryScannerPeriodProfitCellTooltip")
                                  .replace("{days}", String(row.period_days ?? profitableScanPeriodDaysFallback))
                                  .replace("{volume}", String(row.product_daily_volume))
                              : undefined
                          }
                        >
                          {row.period_profit === undefined ? "—" : formatISK(row.period_profit)}
                        </td>
                        <td
                          className={`px-2 py-1 text-right font-mono ${
                            row.period_margin === undefined
                              ? "text-eve-dim"
                              : row.period_margin >= 0
                                ? "text-eve-text"
                                : "text-red-300"
                          }`}
                        >
                          {row.period_margin === undefined ? "—" : `${row.period_margin.toFixed(1)}%`}
                        </td>
                        <td className="px-2 py-1 text-right font-mono cursor-help" title={profitTooltip}>
                          {formatISK(row.optimal_build_cost)}
                        </td>
                        <td className="px-2 py-1 text-right font-mono text-eve-dim">
                          {hours >= 1 ? `${hours.toFixed(1)}h` : `${Math.round(row.manufacturing_time / 60)}m`}
                        </td>
                        <td className="px-1 py-1 text-right">
                          {onViewInAnalysis && (
                            <button
                              type="button"
                              onClick={() => {
                                // The analysis tab uses one station ID for
                                // both cost index AND pricing. Hand off the
                                // BUILD station so structure ME bonus carries
                                // through; user can switch pricing-side inside
                                // the analysis tab if they want hub prices.
                                const picked = allStations.find(
                                  (s) => Number(s.id) === sharedPrefs.buildStationID,
                                );
                                const isT2Handoff = row.scan_mode === "t2_invention";
                                const inv = effectiveInventionParams(sharedPrefs.decryptor);
                                // Decryptor + fees are already in shared
                                // prefs, so the handoff no longer needs to
                                // thread invention-derived numbers through —
                                // Analyze re-derives them from the same
                                // shared decryptor selection.
                                onViewInAnalysis({
                                  productTypeID: row.product_type_id,
                                  productName: row.product_name,
                                  // For T2 rows the ME/TE that drive analysis
                                  // are the *invented T2 BPC's*, not the T1
                                  // source. Pass the decryptor-adjusted values.
                                  me: isT2Handoff ? inv.meBase : row.me,
                                  te: isT2Handoff ? inv.teBase : row.te,
                                  runs: row.runs,
                                  systemName: sharedPrefs.buildSystem,
                                  stationID: sharedPrefs.buildStationID,
                                  stationIsStructure: Boolean(picked?.is_structure),
                                  facilityTax: sharedPrefs.facilityTax,
                                  structureBonus: sharedPrefs.structureBonus,
                                  brokerFee: sharedPrefs.brokerFee,
                                  salesTaxPercent: sharedPrefs.salesTaxPercent,
                                  activityMode: isT2Handoff ? "invention" : "manufacturing",
                                  ownBlueprint: true,
                                  blueprintIsBPO: row.is_bpo,
                                  autoAnalyze: true,
                                });
                              }}
                              title={t("industryScannerViewInAnalysis")}
                              aria-label={t("industryScannerViewInAnalysis")}
                              className="px-1.5 py-0.5 text-[11px] rounded-sm border border-eve-border/60 text-eve-dim
                                         hover:text-eve-accent hover:border-eve-accent transition-colors"
                            >
                              ↗
                            </button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <AddBlueprintsToProjectModal
        open={addToProjectOpen}
        onClose={() => setAddToProjectOpen(false)}
        rows={selectedRows}
        // Fallback default when a row has no market volume history; the
        // modal drives real per-row values via daily_volume × target_days.
        runsPerJob={1}
        // Persist runs edits across modal open/close cycles within the
        // same scan so cancelling to fix a selection doesn't lose the
        // user's manual runs numbers. Parent resets these on new scan
        // or clear results.
        rowKeyFor={rowKey}
        manualRunsByRowKey={manualRunsByRowKey}
        dirtyRunsByRowKey={dirtyRunsByRowKey}
        onManualRunsChange={(rk, runs) => {
          setManualRunsByRowKey((prev) => {
            const next = new Map(prev);
            next.set(rk, runs);
            return next;
          });
          setDirtyRunsByRowKey((prev) => {
            if (prev.has(rk)) return prev;
            const next = new Set(prev);
            next.add(rk);
            return next;
          });
        }}
        analysisContext={{
          systemName: sharedPrefs.buildSystem,
          stationID: sharedPrefs.buildStationID,
          facilityTax: sharedPrefs.facilityTax,
          structureBonus: sharedPrefs.structureBonus,
          brokerFee: sharedPrefs.brokerFee,
          salesTaxPercent: sharedPrefs.salesTaxPercent,
          decryptorKey: sharedPrefs.decryptor,
          decryptorCost: sharedPrefs.decryptorCost,
          // Scanner batch add-to-project uses the same build-mode preference
          // the Analyze tab does — both surfaces read from the shared prefs
          // hook so the value is always in sync.
          buildMode: sharedPrefs.buildMode,
        }}
        onSuccess={(projectID, count, summary) => {
          setAddToProjectOpen(false);
          const detail = summary
            ? ` (tasks:${summary.tasks_inserted} jobs:${summary.jobs_inserted} bp:${summary.blueprints_upserted})`
            : "";
          addToast(
            t("industryScannerAddToProjectSuccess").replace("{count}", String(count)) + detail,
            "success",
            3600,
          );
          setSelectedIDs(new Set());
          // Rows just got committed to a project — flush their overrides so
          // if the user re-selects them they start from defaults again.
          setManualRunsByRowKey(new Map());
          setDirtyRunsByRowKey(new Set());
          if (onProjectCreated) onProjectCreated(projectID);
        }}
      />
    </div>
  );
}

interface SortableHeaderProps {
  sortKey: SortKey;
  label: string;
  align: "left" | "right";
  active: SortKey;
  dir: SortDir;
  onClick: (key: SortKey) => void;
  /** Optional hover tooltip explaining the column. When absent falls back to
   *  the generic "Click to sort" text. */
  titleText?: string;
}

interface NullableNumberInputProps {
  value: number | null;
  onChange: (v: number | null) => void;
  step?: number;
  placeholder?: string;
}

function NullableNumberInput({
  value,
  onChange,
  step = 1,
  placeholder = "any",
}: NullableNumberInputProps) {
  // Local string state so the user can type intermediate values like "-" or
  // empty without losing focus or having the parent commit prematurely.
  const [draft, setDraft] = useState<string>(value == null ? "" : String(value));
  const [focused, setFocused] = useState(false);

  // Sync from parent when not editing.
  useEffect(() => {
    if (!focused) {
      setDraft(value == null ? "" : String(value));
    }
  }, [focused, value]);

  const commit = (raw: string) => {
    const trimmed = raw.trim();
    if (trimmed === "") {
      onChange(null);
      return;
    }
    const parsed = parseFloat(trimmed);
    if (!Number.isFinite(parsed)) {
      // Reset to last committed.
      setDraft(value == null ? "" : String(value));
      return;
    }
    onChange(parsed);
  };

  return (
    <div className="relative">
      <input
        type="text"
        inputMode="decimal"
        value={draft}
        placeholder={placeholder}
        onChange={(e) => {
          const raw = e.target.value;
          setDraft(raw);
          // Live commit when the value parses cleanly so filters apply
          // immediately. Empty string commits to null.
          if (raw.trim() === "") {
            onChange(null);
            return;
          }
          const parsed = parseFloat(raw);
          if (Number.isFinite(parsed)) {
            onChange(parsed);
          }
        }}
        onFocus={() => setFocused(true)}
        onBlur={(e) => {
          setFocused(false);
          commit(e.target.value);
        }}
        step={step}
        className="w-44 px-2 py-1 pr-6 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm font-mono
                   focus:outline-none focus:border-eve-accent focus:ring-1 focus:ring-eve-accent/30
                   transition-colors"
      />
      {value != null && (
        <button
          type="button"
          onClick={() => {
            setDraft("");
            onChange(null);
          }}
          aria-label="Clear filter"
          title="Clear filter"
          className="absolute right-1.5 top-1/2 -translate-y-1/2 text-eve-dim hover:text-eve-text text-xs leading-none px-1"
        >
          ×
        </button>
      )}
    </div>
  );
}

interface ToggleChipProps {
  active: boolean;
  onClick: () => void;
  label: string;
  title?: string;
}

function ToggleChip({ active, onClick, label, title }: ToggleChipProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={`px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
        active
          ? "border-eve-accent/60 text-eve-accent bg-eve-accent/10"
          : "border-eve-border text-eve-dim hover:text-eve-text"
      }`}
    >
      {label}
    </button>
  );
}

function SortableHeader({ sortKey, label, align, active, dir, onClick, titleText }: SortableHeaderProps) {
  const isActive = active === sortKey;
  const arrow = isActive ? (dir === "desc" ? " ▼" : " ▲") : "";
  return (
    <th className={`px-2 py-1.5 ${align === "right" ? "text-right" : "text-left"}`}>
      <button
        type="button"
        onClick={() => onClick(sortKey)}
        className={`inline-flex items-center gap-1 ${
          align === "right" ? "justify-end w-full" : ""
        } hover:text-eve-text transition-colors ${isActive ? "text-eve-accent" : ""}`}
        title={titleText ?? "Click to sort"}
      >
        {label}
        <span className="text-[9px]">{arrow || "↕"}</span>
      </button>
    </th>
  );
}
