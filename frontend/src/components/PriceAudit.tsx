import { useCallback, useEffect, useMemo, useState } from "react";
import {
  getStations,
  hubAllocate,
  priceAudit,
  type FlowMetric,
  type HubAllocateResult,
  type HubAllocateStrategy,
  type HubStationMeta,
  type PriceAuditRow,
} from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { formatISK } from "@/lib/format";
import type { StationInfo } from "@/lib/types";
import { useGlobalToast } from "./Toast";
import { SystemAutocomplete } from "./SystemAutocomplete";
import { STATION_TRADING_HUBS } from "@/lib/tradeHubs";

// formatISK abbreviates and truncates decimals for values under 1000
// (7.62 → "7.6"). Prices in this tab often live in that range, and losing
// the second decimal matters when the whole point is a 0.01 ISK undercut.
// This helper always keeps two decimals for values below 1000 and delegates
// to formatISK for larger values where the K/M/B suffix is more useful.
function formatPrice(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (Math.abs(value) >= 1000) return formatISK(value);
  return value.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
}

interface Props {
  isLoggedIn: boolean;
}

interface ParsedItem {
  name: string;
  qty: number;
}

type Mode = "single" | "allocate";

// Parse a paste of items. Handles three EVE inventory export formats:
//
//   1. List-view copy (TAB-separated):
//        "Tritanium\t1000\tMineral\t0.01 m3"
//      Qty is always column 2; extra columns are ignored.
//
//   2. Detailed-view copy (space-separated with metadata trail):
//        "Common Moon Mining Crystal Type A II 90 Mining Crystal ... 63,510,932.70 ISK"
//      Name is everything before the first purely-numeric token; qty is
//      that token; everything after (type / category / size / meta /
//      estimated price / ISK) is discarded.
//
//   3. Hand-typed:
//        "tritanium 1000"        → { name: "tritanium",     qty: 1000 }
//        "Compressed Tritanium"  → { name: "Compressed Tritanium", qty: 1 }
//
// EVE item names can contain alphanumeric tokens ("250mm", "10MN") but
// never a token that is *purely* digits — Roman numerals ("II", "IV") are
// alphabetic. So "first pure-digit token = qty" is a safe heuristic.
function parseItems(text: string): ParsedItem[] {
  const lines = text.split(/\r?\n/);
  const out: ParsedItem[] = [];
  for (const raw of lines) {
    const trimmed = raw.trim();
    if (!trimmed) continue;

    // 1. TAB-separated wins when tabs are present.
    if (trimmed.includes("\t")) {
      const parts = trimmed.split(/\t+/);
      const name = parts[0].trim();
      if (!name) continue;
      let qty = 1;
      if (parts.length > 1) {
        const qtyStr = parts[1].replace(/[,\s]/g, "");
        const parsed = Number(qtyStr);
        if (Number.isFinite(parsed) && parsed > 0) qty = Math.floor(parsed);
      }
      out.push({ name, qty });
      continue;
    }

    // 2/3. Whitespace-separated. Walk tokens and find the first that is
    // purely digits (with optional thousands commas).
    const tokens = trimmed.split(/\s+/);
    let qtyIdx = -1;
    for (let i = 1; i < tokens.length; i++) {
      if (/^\d[\d,]*$/.test(tokens[i])) {
        qtyIdx = i;
        break;
      }
    }
    if (qtyIdx === -1) {
      // No qty token — whole line is the name at qty 1.
      out.push({ name: trimmed, qty: 1 });
      continue;
    }
    const name = tokens.slice(0, qtyIdx).join(" ");
    const qtyNum = Math.floor(Number(tokens[qtyIdx].replace(/,/g, "")));
    const qty = Number.isFinite(qtyNum) && qtyNum > 0 ? qtyNum : 1;
    if (name) out.push({ name, qty });
  }
  return out;
}

const DEFAULT_HUB = STATION_TRADING_HUBS[0]; // Jita IV-4
const HUBS_STORAGE_KEY = "price_audit.hubs";
const DAYS_STORAGE_KEY = "price_audit.days_of_stock";
const MODE_STORAGE_KEY = "price_audit.mode";
const STRATEGY_STORAGE_KEY = "price_audit.strategy";
const HUB_PERCENTS_STORAGE_KEY = "price_audit.hub_percents";
const HISTORY_DAYS_STORAGE_KEY = "price_audit.history_days";
const FLOW_METRIC_STORAGE_KEY = "price_audit.flow_metric";
const NO_UNALLOCATED_STORAGE_KEY = "price_audit.no_unallocated";
const HUB_CAPS_STORAGE_KEY = "price_audit.hub_caps";

function loadSelectedHubs(): Set<string> {
  if (typeof window === "undefined") {
    // Sensible default for the user described in memory (Jita/Dodixie/Hek).
    return new Set(["jita", "dodixie", "hek"]);
  }
  try {
    const raw = window.localStorage.getItem(HUBS_STORAGE_KEY);
    if (!raw) return new Set(["jita", "dodixie", "hek"]);
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      return new Set(parsed.filter((k): k is string => typeof k === "string"));
    }
  } catch {
    /* ignore */
  }
  return new Set(["jita", "dodixie", "hek"]);
}

function saveSelectedHubs(keys: Set<string>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(HUBS_STORAGE_KEY, JSON.stringify([...keys]));
  } catch {
    /* ignore */
  }
}

function loadDaysOfStock(): number {
  if (typeof window === "undefined") return 7;
  const raw = window.localStorage.getItem(DAYS_STORAGE_KEY);
  const n = Number(raw);
  return Number.isFinite(n) && n > 0 ? n : 7;
}

function saveDaysOfStock(days: number): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(DAYS_STORAGE_KEY, String(days));
  } catch {
    /* ignore */
  }
}

function loadMode(): Mode {
  if (typeof window === "undefined") return "single";
  return window.localStorage.getItem(MODE_STORAGE_KEY) === "allocate"
    ? "allocate"
    : "single";
}

function saveMode(mode: Mode): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(MODE_STORAGE_KEY, mode);
  } catch {
    /* ignore */
  }
}

function loadStrategy(): HubAllocateStrategy {
  if (typeof window === "undefined") return "balanced";
  const raw = window.localStorage.getItem(STRATEGY_STORAGE_KEY);
  if (raw === "profit" || raw === "balanced" || raw === "volume" || raw === "percent") {
    return raw;
  }
  return "balanced";
}

function saveStrategy(v: HubAllocateStrategy): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STRATEGY_STORAGE_KEY, v);
  } catch {
    /* ignore */
  }
}

function loadHubPercents(): Record<string, number> {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(HUB_PERCENTS_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const out: Record<string, number> = {};
      for (const [k, v] of Object.entries(parsed)) {
        const n = Number(v);
        if (Number.isFinite(n) && n >= 0) out[k] = n;
      }
      return out;
    }
  } catch {
    /* ignore */
  }
  return {};
}

function saveHubPercents(percents: Record<string, number>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(HUB_PERCENTS_STORAGE_KEY, JSON.stringify(percents));
  } catch {
    /* ignore */
  }
}

function loadHistoryDays(): number {
  if (typeof window === "undefined") return 7;
  const n = Number(window.localStorage.getItem(HISTORY_DAYS_STORAGE_KEY));
  return Number.isFinite(n) && n > 0 ? n : 7;
}

function saveHistoryDays(days: number): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(HISTORY_DAYS_STORAGE_KEY, String(days));
  } catch {
    /* ignore */
  }
}

function loadFlowMetric(): FlowMetric {
  if (typeof window === "undefined") return "median";
  const raw = window.localStorage.getItem(FLOW_METRIC_STORAGE_KEY);
  return raw === "mean" ? "mean" : "median";
}

function saveFlowMetric(v: FlowMetric): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(FLOW_METRIC_STORAGE_KEY, v);
  } catch {
    /* ignore */
  }
}

function loadNoUnallocated(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(NO_UNALLOCATED_STORAGE_KEY) === "1";
}

function saveNoUnallocated(v: boolean): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(NO_UNALLOCATED_STORAGE_KEY, v ? "1" : "0");
  } catch {
    /* ignore */
  }
}

// hub caps keyed by hub short-key ("jita","dodixie",...) so entries survive
// station-id changes if a hub ever moves.
function loadHubCaps(): Record<string, number> {
  if (typeof window === "undefined") return {};
  try {
    const raw = window.localStorage.getItem(HUB_CAPS_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const out: Record<string, number> = {};
      for (const [k, v] of Object.entries(parsed)) {
        const n = Number(v);
        if (Number.isFinite(n) && n >= 0) out[k] = n;
      }
      return out;
    }
  } catch {
    /* ignore */
  }
  return {};
}

function saveHubCaps(caps: Record<string, number>): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(HUB_CAPS_STORAGE_KEY, JSON.stringify(caps));
  } catch {
    /* ignore */
  }
}

export function PriceAudit({ isLoggedIn: _isLoggedIn }: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();

  const [mode, setMode] = useState<Mode>(loadMode);
  const [systemName, setSystemName] = useState(DEFAULT_HUB.systemName);
  const [stationId, setStationId] = useState<number>(DEFAULT_HUB.stationID);
  const [stations, setStations] = useState<StationInfo[]>([]);
  const [pasteText, setPasteText] = useState("");
  const [singleResults, setSingleResults] = useState<PriceAuditRow[]>([]);
  const [allocateResults, setAllocateResults] = useState<HubAllocateResult[]>(
    [],
  );
  const [allocateStations, setAllocateStations] = useState<HubStationMeta[]>([]);
  const [fetching, setFetching] = useState(false);
  const [stationName, setStationName] = useState<string>(
    "Jita IV - Moon 4 - Caldari Navy Assembly Plant",
  );

  const [selectedHubKeys, setSelectedHubKeys] = useState<Set<string>>(
    () => loadSelectedHubs(),
  );
  const [daysOfStock, setDaysOfStock] = useState<number>(() => loadDaysOfStock());
  const [strategy, setStrategy] = useState<HubAllocateStrategy>(() => loadStrategy());
  const [hubPercents, setHubPercents] = useState<Record<string, number>>(
    () => loadHubPercents(),
  );
  const [historyDays, setHistoryDays] = useState<number>(() => loadHistoryDays());
  const [flowMetric, setFlowMetric] = useState<FlowMetric>(() => loadFlowMetric());
  const [noUnallocated, setNoUnallocated] = useState<boolean>(() => loadNoUnallocated());
  const [hubCaps, setHubCaps] = useState<Record<string, number>>(() => loadHubCaps());

  useEffect(() => saveMode(mode), [mode]);
  useEffect(() => saveSelectedHubs(selectedHubKeys), [selectedHubKeys]);
  useEffect(() => saveDaysOfStock(daysOfStock), [daysOfStock]);
  useEffect(() => saveStrategy(strategy), [strategy]);
  useEffect(() => saveHubPercents(hubPercents), [hubPercents]);
  useEffect(() => saveHistoryDays(historyDays), [historyDays]);
  useEffect(() => saveFlowMetric(flowMetric), [flowMetric]);
  useEffect(() => saveNoUnallocated(noUnallocated), [noUnallocated]);
  useEffect(() => saveHubCaps(hubCaps), [hubCaps]);

  const parsedItems = useMemo(() => parseItems(pasteText), [pasteText]);
  const parsedCount = parsedItems.length;

  const selectedHubs = useMemo(
    () => STATION_TRADING_HUBS.filter((h) => selectedHubKeys.has(h.key)),
    [selectedHubKeys],
  );

  // --- Single-station derived state ---
  const resolvedRows = useMemo(
    () => singleResults.filter((r) => !r.unresolved),
    [singleResults],
  );
  const unresolvedRows = useMemo(
    () => singleResults.filter((r) => r.unresolved),
    [singleResults],
  );
  const fallbackCount = useMemo(
    () =>
      resolvedRows.filter(
        (r) => r.source === "region" || r.source === "avg",
      ).length,
    [resolvedRows],
  );
  const copyableCount = useMemo(
    () =>
      resolvedRows.filter(
        (r) => r.suggested_price != null && r.suggested_price > 0,
      ).length,
    [resolvedRows],
  );

  // --- Allocate-mode derived state: bucket allocations by station ---
  type HubBucket = {
    stationID: number;
    stationName: string;
    systemName: string;
    total: number;
    usedM3: number;
    capM3: number; // 0 = uncapped
    rows: Array<{
      typeName: string;
      qty: number;
      totalQty: number;
      price: number;
      source: string;
      dailyFlow: number;
      dailyVolumes?: number[];
      unitVolume: number;
    }>;
  };
  const allocateBuckets = useMemo<HubBucket[]>(() => {
    const byStation = new Map<number, HubBucket>();
    // Cap lookup by station ID from the response so we don't need to re-derive it.
    const capByStation = new Map<number, number>();
    for (const s of allocateStations) {
      if (s.volume_cap && s.volume_cap > 0) {
        capByStation.set(s.id, s.volume_cap);
      }
    }
    for (const r of allocateResults) {
      if (r.unresolved) continue;
      const name = r.type_name?.trim() || r.name.trim();
      for (const a of r.allocations) {
        if (a.qty <= 0 || a.price == null || a.price <= 0) continue;
        let bucket = byStation.get(a.station_id);
        if (!bucket) {
          bucket = {
            stationID: a.station_id,
            stationName: a.station_name,
            systemName: a.system_name,
            total: 0,
            usedM3: 0,
            capM3: capByStation.get(a.station_id) ?? 0,
            rows: [],
          };
          byStation.set(a.station_id, bucket);
        }
        const unitVol = a.unit_volume ?? 0;
        bucket.rows.push({
          typeName: name,
          qty: a.qty,
          totalQty: r.qty,
          price: a.price,
          source: a.source,
          dailyFlow: a.daily_flow,
          dailyVolumes: a.daily_volumes,
          unitVolume: unitVol,
        });
        bucket.total += a.qty * a.price;
        bucket.usedM3 += a.qty * unitVol;
      }
    }
    // Order buckets by user's hub-selection order so Jita comes first, etc.
    const order = new Map(STATION_TRADING_HUBS.map((h, i) => [h.stationID, i]));
    return [...byStation.values()].sort((a, b) => {
      const oa = order.get(a.stationID) ?? 99;
      const ob = order.get(b.stationID) ?? 99;
      return oa - ob;
    });
  }, [allocateResults, allocateStations]);
  const allocateUnresolved = useMemo(
    () => allocateResults.filter((r) => r.unresolved),
    [allocateResults],
  );
  const allocateUnallocated = useMemo(
    () =>
      allocateResults.filter((r) => !r.unresolved && r.unallocated > 0),
    [allocateResults],
  );

  const loadStationsForSystem = useCallback(async (system: string) => {
    if (!system.trim()) {
      setStations([]);
      return;
    }
    try {
      const resp = await getStations(system.trim());
      setStations(resp.stations);
    } catch {
      setStations([]);
    }
  }, []);

  const setSystemAndReload = useCallback(
    (s: string) => {
      setSystemName(s);
      void loadStationsForSystem(s);
    },
    [loadStationsForSystem],
  );

  const handleFetch = useCallback(async () => {
    if (parsedCount === 0) {
      addToast(t("priceAuditNoItems"), "error", 2400);
      return;
    }
    if (mode === "single" && !stationId) {
      addToast(t("priceAuditNoStation"), "error", 2400);
      return;
    }
    if (mode === "allocate" && selectedHubs.length === 0) {
      addToast(t("priceAuditNoHubs"), "error", 2400);
      return;
    }
    setFetching(true);
    try {
      if (mode === "single") {
        const resp = await priceAudit({
          station_id: stationId,
          items: parsedItems.map(({ name, qty }) => ({ name, qty })),
        });
        setSingleResults(resp.results);
        setAllocateResults([]);
        setAllocateStations([]);
        if (resp.station_name) setStationName(resp.station_name);
        const unresolved = resp.results.filter((r) => r.unresolved).length;
        if (unresolved > 0) {
          addToast(
            t("priceAuditUnresolved", { count: unresolved }),
            "info",
            3000,
          );
        }
      } else {
        // Build percentage weights indexed by station_id string (backend key type).
        const hubPercentsForRequest: Record<string, number> = {};
        if (strategy === "percent") {
          for (const hub of selectedHubs) {
            const key = String(hub.stationID);
            const v = hubPercents[hub.key];
            hubPercentsForRequest[key] = Number.isFinite(v) && v > 0 ? v : 0;
          }
        }
        // Volume caps: hub key → m³ from state, remap to station_id keys
        // for the wire format.
        const hubCapsForRequest: Record<string, number> = {};
        for (const hub of selectedHubs) {
          const v = hubCaps[hub.key];
          if (Number.isFinite(v) && v > 0) {
            hubCapsForRequest[String(hub.stationID)] = v;
          }
        }
        const resp = await hubAllocate({
          station_ids: selectedHubs.map((h) => h.stationID),
          days_of_stock: daysOfStock,
          items: parsedItems.map(({ name, qty }) => ({ name, qty })),
          strategy,
          hub_percents: strategy === "percent" ? hubPercentsForRequest : undefined,
          history_days: historyDays,
          flow_metric: flowMetric,
          no_unallocated: noUnallocated,
          hub_caps: Object.keys(hubCapsForRequest).length > 0 ? hubCapsForRequest : undefined,
        });
        setAllocateResults(resp.results);
        setAllocateStations(resp.stations);
        setSingleResults([]);
        const unresolved = resp.results.filter((r) => r.unresolved).length;
        if (unresolved > 0) {
          addToast(
            t("priceAuditUnresolved", { count: unresolved }),
            "info",
            3000,
          );
        }
      }
    } catch (err: any) {
      addToast(err?.message ?? "Fetch failed", "error", 3000);
    } finally {
      setFetching(false);
    }
  }, [
    addToast,
    daysOfStock,
    flowMetric,
    historyDays,
    hubCaps,
    hubPercents,
    mode,
    noUnallocated,
    parsedCount,
    parsedItems,
    selectedHubs,
    stationId,
    strategy,
    t,
  ]);

  const removeSingleRow = useCallback((typeId: number, name: string) => {
    setSingleResults((prev) =>
      prev.filter((r) => {
        if (r.type_id != null && typeId > 0) return r.type_id !== typeId;
        return r.name !== name;
      }),
    );
  }, []);

  const handleCopySingle = useCallback(async () => {
    const lines: string[] = [];
    for (const r of resolvedRows) {
      if (r.suggested_price == null || r.suggested_price <= 0) continue;
      const name = r.type_name?.trim() || r.name.trim();
      if (!name) continue;
      lines.push(`${name}\t${r.suggested_price.toFixed(2)}`);
    }
    if (lines.length === 0) {
      addToast(t("priceAuditNothingToCopy"), "error", 2400);
      return;
    }
    try {
      await navigator.clipboard.writeText(lines.join("\n"));
      addToast(t("priceAuditCopied", { count: lines.length }), "success", 2400);
    } catch {
      addToast(t("priceAuditCopyFailed"), "error", 2400);
    }
  }, [addToast, resolvedRows, t]);

  const handleCopyBucket = useCallback(
    async (bucket: HubBucket) => {
      const lines = bucket.rows.map(
        (r) => `${r.typeName}\t${r.price.toFixed(2)}`,
      );
      if (lines.length === 0) {
        addToast(t("priceAuditNothingToCopy"), "error", 2400);
        return;
      }
      try {
        await navigator.clipboard.writeText(lines.join("\n"));
        addToast(
          t("priceAuditCopiedForHub", {
            count: lines.length,
            hub: bucket.systemName,
          }),
          "success",
          2400,
        );
      } catch {
        addToast(t("priceAuditCopyFailed"), "error", 2400);
      }
    },
    [addToast, t],
  );

  // Format the raw daily-volume samples for the Daily Flow hover tooltip.
  // Shows the last N days newest-first so users can eyeball spikes and zeros.
  const formatFlowTooltip = useCallback(
    (volumes: number[] | undefined, computed: number): string => {
      if (!volumes || volumes.length === 0) {
        return t("priceAuditDailyFlowHint");
      }
      // Backend sends oldest-first — reverse for newest-first tooltip reading.
      const rev = [...volumes].reverse();
      const list = rev
        .map((v) => v.toLocaleString(undefined, { maximumFractionDigits: 0 }))
        .join(", ");
      return t("priceAuditDailyFlowTooltip", {
        metric: flowMetric === "median" ? "median" : "mean",
        window: String(volumes.length),
        computed: computed.toLocaleString(undefined, {
          maximumFractionDigits: 1,
        }),
        values: list,
      });
    },
    [flowMetric, t],
  );

  const sourceBadge = (source: string) => {
    if (source === "station") {
      return (
        <span
          title={t("priceAuditSourceStationHint")}
          className="ml-1 inline-flex items-center px-1 py-px rounded-[2px] border border-emerald-500/50 bg-emerald-500/10 text-emerald-300 text-[9px] leading-none font-medium uppercase"
        >
          {t("priceAuditSourceStation")}
        </span>
      );
    }
    if (source === "region") {
      return (
        <span
          title={t("priceAuditSourceRegionHint")}
          className="ml-1 inline-flex items-center px-1 py-px rounded-[2px] border border-amber-500/50 bg-amber-500/10 text-amber-300 text-[9px] leading-none font-medium uppercase"
        >
          {t("priceAuditSourceRegion")}
        </span>
      );
    }
    if (source === "avg") {
      return (
        <span
          title={t("priceAuditSourceAvgHint")}
          className="ml-1 inline-flex items-center px-1 py-px rounded-[2px] border border-red-500/50 bg-red-500/10 text-red-300 text-[9px] leading-none font-medium uppercase"
        >
          {t("priceAuditSourceAvg")}
        </span>
      );
    }
    return null;
  };

  const toggleHub = useCallback((key: string) => {
    setSelectedHubKeys((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  return (
    <div className="flex-1 flex flex-col min-h-0 p-3 gap-3">
      {/* Top: mode toggle + inputs */}
      <div className="shrink-0 rounded-sm border border-eve-border/60 bg-gradient-to-br from-eve-panel to-eve-dark/40 p-3">
        <div className="flex items-center gap-4 mb-3">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">
            {t("priceAuditMode")}
          </span>
          {(["single", "allocate"] as Mode[]).map((m) => (
            <label
              key={m}
              className="flex items-center gap-1 cursor-pointer select-none"
            >
              <input
                type="radio"
                checked={mode === m}
                onChange={() => setMode(m)}
                className="accent-eve-accent"
              />
              <span className="text-xs text-eve-text">
                {m === "single"
                  ? t("priceAuditModeSingle")
                  : t("priceAuditModeAllocate")}
              </span>
            </label>
          ))}
        </div>

        {mode === "single" ? (
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-[220px]">
              <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
                {t("system")}
              </label>
              <SystemAutocomplete
                value={systemName}
                onChange={setSystemAndReload}
              />
            </div>
            <div className="min-w-[260px]">
              <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
                {t("colStationName")}
              </label>
              <select
                value={stationId}
                onChange={(e) => {
                  const id = Number(e.target.value);
                  setStationId(id);
                  const match = stations.find((s) => s.id === id);
                  if (match) setStationName(match.name);
                }}
                className="w-full h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs"
              >
                {stations.length === 0 && (
                  <option value={stationId}>
                    {stationName || `#${stationId}`}
                  </option>
                )}
                {stations.map((st) => (
                  <option key={st.id} value={st.id}>
                    {st.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-wrap items-center gap-1">
              <span className="text-[10px] uppercase tracking-wider text-eve-dim mr-1">
                {t("tradeHubs")}
              </span>
              {STATION_TRADING_HUBS.map((hub) => {
                const active =
                  systemName.toLowerCase() === hub.systemName.toLowerCase() &&
                  stationId === hub.stationID;
                return (
                  <button
                    key={hub.key}
                    type="button"
                    onClick={() => {
                      setStationId(hub.stationID);
                      setSystemAndReload(hub.systemName);
                    }}
                    className={`px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
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
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-3">
              <span className="text-[10px] uppercase tracking-wider text-eve-dim">
                {t("priceAuditStrategy")}
              </span>
              {(
                [
                  ["profit", "priceAuditStrategyProfit"],
                  ["balanced", "priceAuditStrategyBalanced"],
                  ["volume", "priceAuditStrategyVolume"],
                  ["percent", "priceAuditStrategyPercent"],
                ] as const
              ).map(([key, label]) => {
                const active = strategy === key;
                return (
                  <button
                    key={key}
                    type="button"
                    onClick={() => setStrategy(key)}
                    title={t((label + "Hint") as any)}
                    className={`px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
                      active
                        ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                        : "border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80"
                    }`}
                  >
                    {t(label)}
                  </button>
                );
              })}
              <label
                className="ml-2 flex items-center gap-1 cursor-pointer select-none"
                title={t("priceAuditNoUnallocatedHint")}
              >
                <input
                  type="checkbox"
                  checked={noUnallocated}
                  onChange={(e) => setNoUnallocated(e.target.checked)}
                  className="accent-eve-accent"
                />
                <span className="text-[11px] text-eve-text">
                  {t("priceAuditNoUnallocated")}
                </span>
              </label>
            </div>

            <div className="flex flex-wrap items-end gap-4">
              <div>
                <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
                  {t("priceAuditHubs")}
                </label>
                <div className="flex flex-wrap items-center gap-1">
                  {STATION_TRADING_HUBS.map((hub) => {
                    const active = selectedHubKeys.has(hub.key);
                    return (
                      <button
                        key={hub.key}
                        type="button"
                        onClick={() => toggleHub(hub.key)}
                        className={`px-2 py-0.5 text-[11px] rounded-sm border transition-colors ${
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
              </div>

              {selectedHubs.length > 0 && (
                <div>
                  <label
                    className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
                    title={t("priceAuditHubCapsHint")}
                  >
                    {t("priceAuditHubCaps")}
                  </label>
                  <div className="flex flex-wrap items-center gap-2">
                    {selectedHubs.map((hub) => (
                      <div key={hub.key} className="flex items-center gap-1">
                        <span className="text-[11px] text-eve-dim">
                          {hub.shortLabel}
                        </span>
                        <input
                          type="number"
                          min={0}
                          step={100}
                          value={hubCaps[hub.key] ?? ""}
                          placeholder="∞"
                          onChange={(e) => {
                            const raw = e.target.value.trim();
                            if (raw === "") {
                              setHubCaps((prev) => {
                                if (!(hub.key in prev)) return prev;
                                const next = { ...prev };
                                delete next[hub.key];
                                return next;
                              });
                              return;
                            }
                            const v = Number(raw);
                            setHubCaps((prev) => ({
                              ...prev,
                              [hub.key]:
                                Number.isFinite(v) && v >= 0 ? v : 0,
                            }));
                          }}
                          className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
                        />
                        <span className="text-[11px] text-eve-dim">m³</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {strategy === "percent" ? (
                <div className="flex-1 min-w-0">
                  <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
                    {t("priceAuditHubPercents")}
                  </label>
                  <div className="flex flex-wrap items-center gap-2">
                    {selectedHubs.map((hub) => (
                      <div key={hub.key} className="flex items-center gap-1">
                        <span className="text-[11px] text-eve-dim">
                          {hub.shortLabel}
                        </span>
                        <input
                          type="number"
                          min={0}
                          max={100}
                          step={1}
                          value={hubPercents[hub.key] ?? ""}
                          onChange={(e) => {
                            const v = Number(e.target.value);
                            setHubPercents((prev) => ({
                              ...prev,
                              [hub.key]:
                                Number.isFinite(v) && v >= 0 ? v : 0,
                            }));
                          }}
                          className="w-16 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
                        />
                        <span className="text-[11px] text-eve-dim">%</span>
                      </div>
                    ))}
                    {selectedHubs.length === 0 && (
                      <span className="text-[11px] text-eve-dim italic">
                        {t("priceAuditNoHubs")}
                      </span>
                    )}
                  </div>
                </div>
              ) : (
                <div>
                  <label
                    className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
                    title={t("priceAuditDaysOfStockHint")}
                  >
                    {t("priceAuditDaysOfStock")}
                  </label>
                  <input
                    type="number"
                    min={1}
                    max={90}
                    value={daysOfStock}
                    onChange={(e) => {
                      const v = Math.max(
                        1,
                        Math.min(90, Number(e.target.value) || 7),
                      );
                      setDaysOfStock(v);
                    }}
                    className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
                  />
                </div>
              )}

              {strategy !== "percent" && (
                <>
                  <div>
                    <label
                      className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
                      title={t("priceAuditFlowWindowHint")}
                    >
                      {t("priceAuditFlowWindow")}
                    </label>
                    <input
                      type="number"
                      min={1}
                      max={365}
                      value={historyDays}
                      onChange={(e) => {
                        const v = Math.max(
                          1,
                          Math.min(365, Number(e.target.value) || 7),
                        );
                        setHistoryDays(v);
                      }}
                      className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
                    />
                  </div>
                  <div>
                    <label
                      className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
                      title={t("priceAuditFlowMetricHint")}
                    >
                      {t("priceAuditFlowMetric")}
                    </label>
                    <div className="flex gap-1">
                      {(["median", "mean"] as FlowMetric[]).map((m) => (
                        <button
                          key={m}
                          type="button"
                          onClick={() => setFlowMetric(m)}
                          title={
                            m === "median"
                              ? t("priceAuditFlowMetricMedianHint")
                              : t("priceAuditFlowMetricMeanHint")
                          }
                          className={`px-2 h-8 text-[11px] rounded-sm border transition-colors ${
                            flowMetric === m
                              ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                              : "border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80"
                          }`}
                        >
                          {m === "median"
                            ? t("priceAuditFlowMetricMedian")
                            : t("priceAuditFlowMetricMean")}
                        </button>
                      ))}
                    </div>
                  </div>
                </>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Body: paste (left) + results (right) */}
      <div className="flex-1 min-h-0 grid grid-cols-1 lg:grid-cols-[minmax(280px,360px)_1fr] gap-3">
        {/* Left: paste */}
        <div className="flex flex-col min-h-0 rounded-sm border border-eve-border/60 bg-eve-panel/40 p-3">
          <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium mb-1">
            {t("priceAuditPasteLabel")}
          </label>
          <p className="text-[11px] text-eve-dim mb-2">
            {t("priceAuditPasteHint")}
          </p>
          <textarea
            value={pasteText}
            onChange={(e) => setPasteText(e.target.value)}
            className="flex-1 min-h-0 p-2 rounded-sm border border-eve-border bg-eve-input text-eve-text font-mono text-xs resize-none"
            placeholder={"Tritanium\t1000\nPyerite\t500"}
          />
          <div className="mt-2 flex items-center justify-between text-[11px] text-eve-dim">
            <span>{t("priceAuditParsedCount", { count: parsedCount })}</span>
            <button
              type="button"
              onClick={() => void handleFetch()}
              disabled={fetching || parsedCount === 0}
              className="px-3 py-1 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {fetching ? t("priceAuditFetching") : t("priceAuditFetchBtn")}
            </button>
          </div>
        </div>

        {/* Right: results */}
        <div className="flex flex-col min-h-0 rounded-sm border border-eve-border/60 bg-eve-panel/40">
          {mode === "single" ? (
            singleResults.length === 0 ? (
              <div className="flex-1 flex items-center justify-center text-sm text-eve-dim p-6 text-center">
                {t("priceAuditEmptyState")}
              </div>
            ) : (
              <>
                <div className="flex-1 min-h-0 overflow-auto">
                  <table className="w-full text-xs">
                    <thead className="sticky top-0 bg-eve-dark z-10">
                      <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
                        <th className="px-3 py-2 text-left font-medium">
                          {t("colItem")}
                        </th>
                        <th className="px-3 py-2 text-right font-medium">
                          {t("colQty")}
                        </th>
                        <th className="px-3 py-2 text-right font-medium">
                          {t("colStationSellPrice")}
                        </th>
                        <th className="px-3 py-2 text-right font-medium">
                          {t("colSuggestedPrice")}
                        </th>
                        <th
                          style={{ width: 32, minWidth: 32, maxWidth: 32 }}
                          className="px-1 py-2"
                        />
                      </tr>
                    </thead>
                    <tbody>
                      {resolvedRows.map((r, i) => (
                        <tr
                          key={`${r.type_id ?? r.name}-${i}`}
                          className={`border-b border-eve-border/50 hover:bg-eve-accent/5 ${
                            i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"
                          }`}
                        >
                          <td className="px-3 py-1.5 text-eve-text">
                            {r.type_name ?? r.name}
                            {sourceBadge(r.source)}
                          </td>
                          <td className="px-3 py-1.5 text-right font-mono text-eve-accent">
                            {r.qty.toLocaleString()}
                          </td>
                          <td className="px-3 py-1.5 text-right font-mono text-eve-dim">
                            {formatPrice(r.low_sell)}
                          </td>
                          <td className="px-3 py-1.5 text-right font-mono text-eve-accent">
                            {formatPrice(r.suggested_price)}
                          </td>
                          <td className="px-1 py-1.5 text-center">
                            <button
                              type="button"
                              onClick={() =>
                                removeSingleRow(r.type_id ?? 0, r.name)
                              }
                              title={t("priceAuditRemoveRow")}
                              className="text-eve-dim hover:text-red-300 transition-colors"
                            >
                              ×
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>

                  {unresolvedRows.length > 0 && (
                    <div className="border-t border-red-500/40 bg-red-950/20 p-2">
                      <div className="text-[10px] uppercase tracking-wider text-red-300 mb-1">
                        {t("priceAuditUnresolvedHeader", {
                          count: unresolvedRows.length,
                        })}
                      </div>
                      <ul className="text-[11px] font-mono text-red-200 space-y-0.5">
                        {unresolvedRows.map((r, i) => (
                          <li key={`u-${i}`}>{r.name}</li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>

                <div className="shrink-0 flex items-center justify-between gap-3 px-3 py-2 border-t border-eve-border/60">
                  <div className="text-[11px] text-eve-dim">
                    {fallbackCount > 0 && (
                      <span className="text-amber-300">
                        {t("priceAuditFallbackWarning", { count: fallbackCount })}
                      </span>
                    )}
                  </div>
                  <button
                    type="button"
                    onClick={() => void handleCopySingle()}
                    disabled={copyableCount === 0}
                    className="px-3 py-1 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs disabled:opacity-40 disabled:cursor-not-allowed"
                  >
                    📋 {t("priceAuditCopyBtn", { count: copyableCount })}
                  </button>
                </div>
              </>
            )
          ) : /* mode === "allocate" */ allocateResults.length === 0 ? (
            <div className="flex-1 flex items-center justify-center text-sm text-eve-dim p-6 text-center">
              {t("priceAuditEmptyStateAllocate")}
            </div>
          ) : (
            <div className="flex-1 min-h-0 overflow-auto">
              {allocateBuckets.map((bucket) => (
                <div
                  key={bucket.stationID}
                  className="border-b border-eve-border/60 last:border-b-0"
                >
                  <div className="sticky top-0 z-10 flex items-center justify-between gap-2 px-3 py-2 bg-eve-dark border-b border-eve-border/60">
                    <div className="min-w-0">
                      <div className="flex items-baseline gap-3 flex-wrap">
                        <span className="text-xs font-semibold text-eve-accent uppercase tracking-wider">
                          {bucket.systemName}
                        </span>
                        <span
                          className="text-[11px] font-mono text-eve-text"
                          title={t("priceAuditHubTotalHint")}
                        >
                          {t("priceAuditHubTotal", {
                            value: formatISK(bucket.total),
                          })}
                        </span>
                        {bucket.capM3 > 0 ? (
                          <span
                            className={`text-[11px] font-mono ${
                              bucket.usedM3 > bucket.capM3 * 0.98
                                ? "text-amber-300"
                                : "text-eve-dim"
                            }`}
                            title={t("priceAuditHubCapUsedHint")}
                          >
                            {t("priceAuditHubCapUsed", {
                              used: bucket.usedM3.toLocaleString(undefined, {
                                maximumFractionDigits: 0,
                              }),
                              cap: bucket.capM3.toLocaleString(undefined, {
                                maximumFractionDigits: 0,
                              }),
                            })}
                          </span>
                        ) : bucket.usedM3 > 0 ? (
                          <span
                            className="text-[11px] font-mono text-eve-dim"
                            title={t("priceAuditHubVolumeHint")}
                          >
                            {t("priceAuditHubVolume", {
                              used: bucket.usedM3.toLocaleString(undefined, {
                                maximumFractionDigits: 0,
                              }),
                            })}
                          </span>
                        ) : null}
                      </div>
                      <div className="text-[10px] text-eve-dim truncate">
                        {bucket.stationName}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => void handleCopyBucket(bucket)}
                      className="shrink-0 px-3 py-1 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs"
                    >
                      📋{" "}
                      {t("priceAuditCopyBucketBtn", {
                        hub: bucket.systemName,
                        count: bucket.rows.length,
                      })}
                    </button>
                  </div>
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border/60">
                        <th className="px-3 py-1.5 text-left font-medium">
                          {t("colItem")}
                        </th>
                        <th className="px-3 py-1.5 text-right font-medium">
                          {t("colQty")}
                        </th>
                        <th
                          className="px-3 py-1.5 text-right font-medium"
                          title={t("priceAuditDailyFlowHint")}
                        >
                          {t("priceAuditDailyFlow")}
                        </th>
                        <th className="px-3 py-1.5 text-right font-medium">
                          {t("colSuggestedPrice")}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {bucket.rows.map((row, i) => (
                        <tr
                          key={`${bucket.stationID}-${row.typeName}-${i}`}
                          className={`border-b border-eve-border/50 hover:bg-eve-accent/5 ${
                            i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"
                          }`}
                        >
                          <td className="px-3 py-1.5 text-eve-text">
                            {row.typeName}
                            {sourceBadge(row.source)}
                          </td>
                          <td
                            className="px-3 py-1.5 text-right font-mono text-eve-accent cursor-help"
                            title={t("priceAuditAllocShareTooltip", {
                              qty: row.qty.toLocaleString(),
                              total: row.totalQty.toLocaleString(),
                              pct:
                                row.totalQty > 0
                                  ? (
                                      (row.qty / row.totalQty) *
                                      100
                                    ).toLocaleString(undefined, {
                                      maximumFractionDigits: 1,
                                    })
                                  : "0",
                            })}
                          >
                            {row.qty.toLocaleString()}
                            {row.totalQty > 0 && row.totalQty !== row.qty && (
                              <span className="ml-1 text-eve-dim text-[10px]">
                                (
                                {(
                                  (row.qty / row.totalQty) *
                                  100
                                ).toLocaleString(undefined, {
                                  maximumFractionDigits: 0,
                                })}
                                %)
                              </span>
                            )}
                          </td>
                          <td
                            className="px-3 py-1.5 text-right font-mono text-eve-dim cursor-help"
                            title={formatFlowTooltip(row.dailyVolumes, row.dailyFlow)}
                          >
                            {row.dailyFlow > 0
                              ? row.dailyFlow.toLocaleString(undefined, {
                                  maximumFractionDigits: 1,
                                })
                              : "—"}
                          </td>
                          <td className="px-3 py-1.5 text-right font-mono text-eve-accent">
                            {formatPrice(row.price)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ))}

              {(allocateUnallocated.length > 0 ||
                allocateUnresolved.length > 0) && (
                <div className="border-t border-red-500/40 bg-red-950/20 p-2">
                  {allocateUnallocated.length > 0 && (
                    <>
                      <div className="text-[10px] uppercase tracking-wider text-red-300 mb-1">
                        {t("priceAuditUnallocatedHeader", {
                          count: allocateUnallocated.length,
                        })}
                      </div>
                      <ul className="text-[11px] font-mono text-red-200 space-y-0.5 mb-2">
                        {allocateUnallocated.map((r, i) => (
                          <li key={`una-${i}`}>
                            {r.type_name ?? r.name}: {r.unallocated.toLocaleString()}{" "}
                            {t("priceAuditUnallocatedUnit")}
                          </li>
                        ))}
                      </ul>
                    </>
                  )}
                  {allocateUnresolved.length > 0 && (
                    <>
                      <div className="text-[10px] uppercase tracking-wider text-red-300 mb-1">
                        {t("priceAuditUnresolvedHeader", {
                          count: allocateUnresolved.length,
                        })}
                      </div>
                      <ul className="text-[11px] font-mono text-red-200 space-y-0.5">
                        {allocateUnresolved.map((r, i) => (
                          <li key={`unr-${i}`}>{r.name}</li>
                        ))}
                      </ul>
                    </>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
