import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/lib/i18n";
import { scanProfitableBlueprints, getStations, getStructures, getCharacterMarketFees } from "@/lib/api";
import type { ProfitableScanRequest, ProfitableScanResponse, ProfitableScanRow, ProfitableScanReuseRow, StationInfo } from "@/lib/types";
import { formatISK } from "@/lib/format";
import {
  TabSettingsPanel,
  SettingsField,
  SettingsNumberInput,
  SettingsSelect,
  SettingsCheckbox,
  SettingsGrid,
} from "../TabSettingsPanel";
import { SystemAutocomplete } from "../SystemAutocomplete";
import { EmptyState } from "../EmptyState";
import { useGlobalToast } from "../Toast";
import { AddBlueprintsToProjectModal } from "./AddBlueprintsToProjectModal";

const SCANNER_PERSIST_KEY = "industry-scanner";
const PARAMS_LS_KEY = "eve-settings:industry-scanner";
// Keep transient scan results (rows + selection + sort + search) in
// sessionStorage so the user doesn't lose them when they switch jobs tabs.
const SCAN_STATE_SS_KEY = "eve-flipper:scanner-state";

type SortKey =
  | "selected"
  | "blueprint_name"
  | "product_name"
  | "owned_quantity"
  | "me"
  | "te"
  | "isk_per_hour"
  | "profit"
  | "profit_percent"
  | "optimal_build_cost"
  | "manufacturing_time";

type SortDir = "asc" | "desc";

// Quick-pick presets for the most common pricing hubs. The user can also type
// any system into the pricing-system autocomplete — these just one-click the
// canonical NPC station + system for each major trade hub.
interface PricingHubPreset {
  key: string;
  shortLabel: string;
  systemName: string;
  stationID: number;
}
const PRICING_HUB_PRESETS: PricingHubPreset[] = [
  { key: "jita", shortLabel: "Jita", systemName: "Jita", stationID: 60003760 },
  { key: "amarr", shortLabel: "Amarr", systemName: "Amarr", stationID: 60008494 },
  { key: "dodixie", shortLabel: "Dodixie", systemName: "Dodixie", stationID: 60011866 },
  { key: "rens", shortLabel: "Rens", systemName: "Rens", stationID: 60004588 },
  { key: "hek", shortLabel: "Hek", systemName: "Hek", stationID: 60005686 },
];

interface PersistedParams {
  scope: "single" | "all";
  defaultBPCRuns: number;
  includeCorpBlueprints: boolean;
  buildSystem: string;
  /** Station/structure where the user manufactures — drives Structure ME
   *  Bonus auto-fill (and later Job Cost Bonus). NOT used for price lookup. */
  buildStationID: number;
  /** System whose region is queried for product/material market prices. */
  pricingSystem: string;
  /** Specific NPC station within the pricing system, 0 = region-wide. The
   *  four major-hub presets pre-fill this to the canonical trade station. */
  pricingStationID: number;
  facilityTax: number;
  structureBonus: number;
  brokerFee: number;
  salesTaxPercent: number;
  runsPerJob: number;
  maxBlueprints: number;
  blueprintFilter: "bpo" | "bpc" | "both";
  // null = no filter; numeric value (including 0 or negative) is taken literally.
  minISKPerHour: number | null;
  minProfit: number | null;
  minMarginPct: number | null;
}

const DEFAULT_PARAMS: PersistedParams = {
  scope: "all",
  defaultBPCRuns: 1,
  includeCorpBlueprints: false,
  buildSystem: "Botane",
  buildStationID: 0,
  pricingSystem: "Jita",
  pricingStationID: 60003760, // Jita IV - Moon 4 - Caldari Navy Assembly Plant
  facilityTax: 0,
  structureBonus: 0,
  brokerFee: 3,
  salesTaxPercent: 4.5,
  runsPerJob: 1,
  maxBlueprints: 500,
  blueprintFilter: "bpo",
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
  /** Scanner only ever scans manufacturing; pass it explicitly so analysis
   *  doesn't fall back to its default. */
  activityMode: "manufacturing";
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
    "me",
    "te",
    "isk_per_hour",
    "profit",
    "profit_percent",
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

  const rowKey = (row: ProfitableScanRow) =>
    `${row.blueprint_type_id}-${row.is_bpo ? "bpo" : "bpc"}`;
  const [addToProjectOpen, setAddToProjectOpen] = useState(false);
  const [importingFees, setImportingFees] = useState(false);
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

  const handleImportFees = useCallback(async () => {
    if (importingFees) return;
    setImportingFees(true);
    try {
      const fees = await getCharacterMarketFees();
      setParams((prev) => {
        const next = {
          ...prev,
          salesTaxPercent: fees.suggested_sales_tax_percent,
          brokerFee: fees.suggested_broker_fee_percent,
        };
        savePersistedParams(next);
        return next;
      });
      addToast(
        t("industryScannerImportFeesSuccess")
          .replace("{tax}", fees.suggested_sales_tax_percent.toFixed(2))
          .replace("{fee}", fees.suggested_broker_fee_percent.toFixed(2))
          .replace("{acc}", String(fees.accounting_level))
          .replace("{br}", String(fees.broker_relations_level)),
        "success",
        2800,
      );
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "Import failed";
      addToast(msg, "error", 3000);
    } finally {
      setImportingFees(false);
    }
  }, [importingFees, addToast, t]);

  // Station picker (mirrors the IndustryTab Analysis flow).
  const [stations, setStations] = useState<StationInfo[]>([]);
  const [structureStations, setStructureStations] = useState<StationInfo[]>([]);
  const [includeStructures, setIncludeStructures] = useState(false);
  const [loadingStations, setLoadingStations] = useState(false);
  const [loadingStructures, setLoadingStructures] = useState(false);
  const [systemId, setSystemId] = useState<number>(0);
  const [systemRegionId, setSystemRegionId] = useState<number>(0);
  const stationsAbortRef = useRef<AbortController | null>(null);
  const stationsRequestSeqRef = useRef(0);
  const structuresAbortRef = useRef<AbortController | null>(null);
  const structuresRequestSeqRef = useRef(0);

  useEffect(() => {
    stationsAbortRef.current?.abort();
    stationsRequestSeqRef.current += 1;
    const reqSeq = stationsRequestSeqRef.current;
    const normalizedSystem = params.buildSystem.trim();
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
  }, [params.buildSystem]);

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

  // Built-in ME bonus by Engineering Complex type. Rigs add more, but we don't
  // have rig data over the wire — user can override after auto-fill.
  // Source: EVE community wiki structure bonuses.
  const STRUCTURE_ME_BONUS_BY_TYPE: Record<number, number> = useMemo(
    () => ({
      35825: 1, // Raitaru
      35826: 1, // Azbel
      35827: 1, // Sotiyo
    }),
    [],
  );

  // Auto-fill Structure ME Bonus %. Rules:
  //  - "Include player structures" off, OR no station selected, OR station is
  //    an NPC station → bonus = 0 (NPC stations give no ME role bonus).
  //  - Player structure selected → bonus = base structure-type bonus from the
  //    lookup table (Raitaru/Azbel/Sotiyo = 1). Rigs not modeled — user can
  //    override after auto-fill.
  useEffect(() => {
    let suggested = 0;
    if (includeStructures && params.buildStationID > 0) {
      const picked = allStations.find((s) => Number(s.id) === params.buildStationID);
      if (picked?.is_structure && picked.type_id) {
        suggested = STRUCTURE_ME_BONUS_BY_TYPE[picked.type_id] ?? 0;
      }
    }
    if (suggested !== params.structureBonus) {
      setParams((prev) => {
        const next = { ...prev, structureBonus: suggested };
        savePersistedParams(next);
        return next;
      });
    }
    // STRUCTURE_ME_BONUS_BY_TYPE is stable (memo); deps cover the inputs that
    // actually drive a change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params.buildStationID, allStations, includeStructures]);

  // If the selected build station disappears from the list after a build-system
  // change, clear it (the user must re-pick).
  useEffect(() => {
    if (params.buildStationID <= 0) return;
    const exists = allStations.some((s) => Number(s.id) === params.buildStationID);
    if (!exists) {
      setParams((prev) => {
        const next = { ...prev, buildStationID: 0 };
        savePersistedParams(next);
        return next;
      });
    }
  }, [allStations, params.buildStationID]);

  const updateParam = <K extends keyof PersistedParams>(key: K, value: PersistedParams[K]) => {
    setParams((prev) => {
      const next = { ...prev, [key]: value };
      savePersistedParams(next);
      return next;
    });
  };

  // Build the shared scan-request body. Always sends 0 for min thresholds so
  // the backend returns everything analyzed; threshold filtering is client-side.
  const buildBaseScanRequest = useCallback((): ProfitableScanRequest => ({
    scope: params.scope,
    default_bpc_runs: params.defaultBPCRuns,
    include_corp_blueprints: params.includeCorpBlueprints,
    build_system_name: params.buildSystem,
    pricing_system_name: params.pricingSystem,
    pricing_station_id: params.pricingStationID,
    facility_tax: params.facilityTax,
    structure_bonus: params.structureBonus,
    broker_fee: params.brokerFee,
    sales_tax_percent: params.salesTaxPercent,
    runs_per_job: params.runsPerJob,
    max_blueprints: params.maxBlueprints,
    blueprint_filter: params.blueprintFilter,
    min_isk_per_hour: 0,
    min_profit: 0,
    min_margin_percent: 0,
  }), [params]);

  const runScan = useCallback(async (req: ProfitableScanRequest, busyLabel: string) => {
    if (!isLoggedIn) {
      addToast(t("industryScannerLoginRequired"), "warning", 2400);
      return;
    }
    setScanning(true);
    setError(null);
    setProgressMsg(busyLabel);
    try {
      const resp = await scanProfitableBlueprints(req, (m) => setProgressMsg(m));
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
      const msg = e instanceof Error ? e.message : "Scan failed";
      setError(msg);
      addToast(msg, "error", 3000);
    } finally {
      setScanning(false);
      setProgressMsg("");
    }
  }, [isLoggedIn, addToast, t]);

  const handleScan = useCallback(async () => {
    setSelectedIDs(new Set());
    await runScan(buildBaseScanRequest(), t("industryScannerScanning"));
  }, [buildBaseScanRequest, runScan, t]);

  const handleClearResults = useCallback(() => {
    setResponse(null);
    setSelectedIDs(new Set());
    setSearchQuery("");
    setError(null);
  }, []);

  const handleRefreshPrices = useCallback(async () => {
    if (!response || response.rows.length === 0) return;
    // Preserve the user's selection across the refresh — same rows come back.
    const reuse: ProfitableScanReuseRow[] = response.rows.map((r) => ({
      blueprint_type_id: r.blueprint_type_id,
      is_bpo: r.is_bpo,
      me: r.me,
      te: r.te,
      owned_quantity: r.owned_quantity,
      available_runs: r.available_runs,
      location_ids: r.location_ids ?? [],
    }));
    const req = {
      ...buildBaseScanRequest(),
      skip_blueprint_fetch: true,
      reuse_groups: reuse,
    };
    await runScan(req, t("industryScannerRefreshingPrices"));
  }, [response, buildBaseScanRequest, runScan, t]);

  const sortedRows = useMemo(() => {
    if (!response) return [];
    // Apply filters client-side so threshold tweaks are instant. The scan
    // request itself sends 0 so the backend returns everything analyzed.
    const q = searchQuery.trim().toLowerCase();
    const rows = response.rows.filter((r) => {
      if (params.minISKPerHour != null && r.isk_per_hour < params.minISKPerHour) return false;
      if (params.minProfit != null && r.profit < params.minProfit) return false;
      if (params.minMarginPct != null && r.profit_percent < params.minMarginPct) return false;
      if (q) {
        const bp = r.blueprint_name?.toLowerCase() ?? "";
        const pr = r.product_name?.toLowerCase() ?? "";
        if (!bp.includes(q) && !pr.includes(q)) return false;
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
      if (typeof av === "number" && typeof bv === "number") {
        return mul * (av - bv);
      }
      return 0;
    });
    return rows;
  }, [response, sortKey, sortDir, selectedIDs, params.minISKPerHour, params.minProfit, params.minMarginPct, searchQuery]);

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

  const selectedRows: ProfitableScanRow[] = useMemo(
    () => sortedRows.filter((r) => selectedIDs.has(rowKey(r))),
    [sortedRows, selectedIDs],
  );

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
        <SettingsGrid cols={4}>
          <SettingsField label={t("industryScannerScopeLabel")}>
            <SettingsSelect
              value={params.scope}
              onChange={(v) => updateParam("scope", v as "single" | "all")}
              options={[
                { value: "all", label: t("industryScannerScopeAll") },
                { value: "single", label: t("industryScannerScopeSingle") },
              ]}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerDefaultRunsLabel")}>
            <SettingsNumberInput
              value={params.defaultBPCRuns}
              onChange={(v) => updateParam("defaultBPCRuns", v)}
              min={1}
              max={1000}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerRunsPerJobLabel")}>
            <SettingsNumberInput
              value={params.runsPerJob}
              onChange={(v) => updateParam("runsPerJob", v)}
              min={1}
              max={10000}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerIncludeCorpLabel")}>
            <SettingsCheckbox
              checked={params.includeCorpBlueprints}
              onChange={(v) => updateParam("includeCorpBlueprints", v)}
              label={t("industryScannerIncludeCorpHint")}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerMaxBlueprintsLabel")}>
            <SettingsNumberInput
              value={params.maxBlueprints}
              onChange={(v) => updateParam("maxBlueprints", v)}
              min={1}
              max={20000}
            />
          </SettingsField>
          <SettingsField label={t("industryScannerBPFilterLabel")}>
            <SettingsSelect
              value={params.blueprintFilter}
              onChange={(v) => updateParam("blueprintFilter", v as PersistedParams["blueprintFilter"])}
              options={[
                { value: "bpo", label: t("industryScannerBPFilterBPO") },
                { value: "bpc", label: t("industryScannerBPFilterBPC") },
                { value: "both", label: t("industryScannerBPFilterBoth") },
              ]}
            />
          </SettingsField>
        </SettingsGrid>

        <div className="mt-3">
          <SettingsGrid cols={3}>
            <SettingsField label={t("industryScannerBuildSystemLabel")}>
              <SystemAutocomplete
                value={params.buildSystem}
                onChange={(v) => updateParam("buildSystem", v)}
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
                  value={params.buildStationID}
                  onChange={(v) => updateParam("buildStationID", Number(v))}
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
            // Single status line below the grid so column heights stay even.
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
            if (stations.length === 0 && params.buildSystem.trim()) {
              return (
                <div className="mt-2 text-[10px] text-amber-400/80">
                  {!isLoggedIn ? t("noNpcStationsLoginHint") : t("noNpcStationsToggleHint")}
                </div>
              );
            }
            return null;
          })()}
        </div>

        {/* Pricing controls — decoupled from build location so the user can,
            e.g., build in Botane while reading Jita prices for the product. */}
        <div className="mt-3">
          <SettingsGrid cols={2}>
            <SettingsField label={t("industryScannerPricingSystemLabel")}>
              <SystemAutocomplete
                value={params.pricingSystem}
                onChange={(v) => {
                  // Typing a non-hub system clears the canonical station ID so
                  // the backend falls back to region-wide pricing for that
                  // system. Matching a preset still keeps its known station ID.
                  const trimmed = v.trim();
                  const preset = PRICING_HUB_PRESETS.find(
                    (h) => h.systemName.toLowerCase() === trimmed.toLowerCase(),
                  );
                  setParams((prev) => {
                    const next: PersistedParams = {
                      ...prev,
                      pricingSystem: v,
                      pricingStationID: preset ? preset.stationID : 0,
                    };
                    savePersistedParams(next);
                    return next;
                  });
                }}
                showLocationButton={false}
                isLoggedIn={isLoggedIn}
                suppressInternalHint
              />
            </SettingsField>
            <SettingsField label={t("industryScannerPricingHubsLabel")}>
              <div className="flex flex-wrap gap-1">
                {PRICING_HUB_PRESETS.map((hub) => {
                  const active =
                    params.pricingSystem.trim().toLowerCase() === hub.systemName.toLowerCase();
                  return (
                    <button
                      key={hub.key}
                      type="button"
                      onClick={() => {
                        setParams((prev) => {
                          const next: PersistedParams = {
                            ...prev,
                            pricingSystem: hub.systemName,
                            pricingStationID: hub.stationID,
                          };
                          savePersistedParams(next);
                          return next;
                        });
                      }}
                      className={`px-2 py-1 text-[11px] rounded-sm border transition-colors ${
                        active
                          ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                          : "border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80"
                      }`}
                    >
                      {hub.shortLabel}
                    </button>
                  );
                })}
              </div>
            </SettingsField>
          </SettingsGrid>
          <div className="mt-1 text-[10px] text-eve-dim">
            {params.pricingStationID > 0
              ? `Pricing from station ${params.pricingStationID} (${params.pricingSystem || "unknown"} region).`
              : params.pricingSystem.trim()
                ? `Pricing region-wide in ${params.pricingSystem}.`
                : "Pricing falls back to the build system's region."}
          </div>
        </div>

        <div className="mt-3">
          <SettingsGrid cols={4}>
            <SettingsField label={t("industryScannerFacilityTaxLabel")}>
              <SettingsNumberInput
                value={params.facilityTax}
                onChange={(v) => updateParam("facilityTax", v)}
                min={0}
                max={100}
                step={0.01}
              />
            </SettingsField>
            <SettingsField label={t("industryScannerStructureBonusLabel")}>
              <SettingsNumberInput
                value={params.structureBonus}
                onChange={(v) => updateParam("structureBonus", v)}
                min={-100}
                max={100}
                step={0.01}
              />
            </SettingsField>
            <SettingsField label={t("industryScannerBrokerFeeLabel")}>
              <SettingsNumberInput
                value={params.brokerFee}
                onChange={(v) => updateParam("brokerFee", v)}
                min={0}
                max={100}
                step={0.01}
              />
            </SettingsField>
            <SettingsField label={t("industryScannerSalesTaxLabel")}>
              <SettingsNumberInput
                value={params.salesTaxPercent}
                onChange={(v) => updateParam("salesTaxPercent", v)}
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
        </div>

        <div className="mt-3">
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
        </div>

        <div className="mt-3 flex items-center gap-3">
          <button
            type="button"
            onClick={handleScan}
            disabled={scanning}
            className="px-3 py-1.5 text-xs font-semibold rounded-sm border border-eve-accent text-eve-accent
                       hover:bg-eve-accent/10 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {scanning ? t("industryScannerScanning") : t("industryScannerScanBtn")}
          </button>
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
      </TabSettingsPanel>

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
                    <SortableHeader sortKey="me" align="right" label={t("industryScannerColME")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="te" align="right" label={t("industryScannerColTE")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="isk_per_hour" align="right" label={t("industryScannerColISKHour")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="profit" align="right" label={t("industryScannerColProfit")} active={sortKey} dir={sortDir} onClick={toggleSort} />
                    <SortableHeader sortKey="profit_percent" align="right" label={t("industryScannerColMargin")} active={sortKey} dir={sortDir} onClick={toggleSort} />
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
                    return (
                      <tr
                        key={k}
                        className={`border-t border-eve-border/30 hover:bg-eve-accent/5 ${
                          checked ? "bg-eve-accent/10" : ""
                        }`}
                      >
                        <td className="px-2 py-1">
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => toggleSelect(k)}
                          />
                        </td>
                        <td className="px-2 py-1 font-medium text-eve-text">
                          {row.blueprint_name}
                          {row.is_bpo ? (
                            <span className="ml-1 text-[10px] text-emerald-300">[BPO]</span>
                          ) : (
                            <span className="ml-1 text-[10px] text-amber-300">[BPC]</span>
                          )}
                        </td>
                        <td className="px-2 py-1 text-eve-dim">{row.product_name}</td>
                        <td className="px-2 py-1 text-right font-mono">{row.owned_quantity}</td>
                        <td className="px-2 py-1 text-right font-mono">{row.me}</td>
                        <td className="px-2 py-1 text-right font-mono">{row.te}</td>
                        <td className="px-2 py-1 text-right font-mono text-emerald-300">
                          {formatISK(row.isk_per_hour)}
                        </td>
                        <td className="px-2 py-1 text-right font-mono text-emerald-300">
                          {formatISK(row.profit)}
                        </td>
                        <td className="px-2 py-1 text-right font-mono">
                          {row.profit_percent.toFixed(1)}%
                        </td>
                        <td className="px-2 py-1 text-right font-mono">
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
                                  (s) => Number(s.id) === params.buildStationID,
                                );
                                onViewInAnalysis({
                                  productTypeID: row.product_type_id,
                                  productName: row.product_name,
                                  me: row.me,
                                  te: row.te,
                                  runs: row.runs,
                                  systemName: params.buildSystem,
                                  stationID: params.buildStationID,
                                  stationIsStructure: Boolean(picked?.is_structure),
                                  facilityTax: params.facilityTax,
                                  structureBonus: params.structureBonus,
                                  brokerFee: params.brokerFee,
                                  salesTaxPercent: params.salesTaxPercent,
                                  activityMode: "manufacturing",
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
        runsPerJob={params.runsPerJob}
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
        className="w-full px-3 py-1.5 pr-7 bg-eve-input border border-eve-border rounded-sm text-eve-text text-sm font-mono
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

function SortableHeader({ sortKey, label, align, active, dir, onClick }: SortableHeaderProps) {
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
        title="Click to sort"
      >
        {label}
        <span className="text-[9px]">{arrow || "↕"}</span>
      </button>
    </th>
  );
}
