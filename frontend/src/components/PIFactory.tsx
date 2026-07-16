import { useCallback, useEffect, useMemo, useState, type DragEvent } from "react";
import {
  getPISchematics,
  getStations,
  piFactoryPlan,
  type PIFactoryConfig,
  type PIFactoryResponse,
  type PISchematicSummary,
} from "@/lib/api";
import { useI18n } from "@/lib/i18n";
import { formatISK } from "@/lib/format";
import { useEsiFeeImport } from "@/lib/useEsiFeeImport";
import type { StationInfo } from "@/lib/types";
import { useGlobalToast } from "./Toast";
import { SystemAutocomplete } from "./SystemAutocomplete";
import { STATION_TRADING_HUBS } from "@/lib/tradeHubs";
import { TabHelp } from "./TabHelp";

interface Props {
  isLoggedIn: boolean;
}

const DEFAULT_HUB = STATION_TRADING_HUBS[0]; // Jita IV-4
const PORTFOLIO_KEY = "pi_factory.portfolio";
const SETTINGS_KEY = "pi_factory.settings";

interface PersistedSettings {
  systemName: string;
  stationId: number;
  pocoTaxPct: number;
  salesTaxPct: number;
  brokerFeePct: number;
  bufferDays: number;
  launchpadM3: number;
}

const DEFAULT_SETTINGS: PersistedSettings = {
  systemName: DEFAULT_HUB.systemName,
  stationId: DEFAULT_HUB.stationID,
  pocoTaxPct: 15,
  salesTaxPct: 4.5,
  brokerFeePct: 3,
  bufferDays: 7,
  launchpadM3: 10000,
};

function loadPortfolio(): PIFactoryConfig[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(PORTFOLIO_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter(
        (p): p is PIFactoryConfig =>
          typeof p?.id === "string" &&
          typeof p?.name === "string" &&
          typeof p?.schematic_id === "number" &&
          typeof p?.factory_count === "number",
      )
      .slice(0, 100);
  } catch {
    return [];
  }
}

function savePortfolio(list: PIFactoryConfig[]): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(PORTFOLIO_KEY, JSON.stringify(list));
  } catch {
    /* ignore */
  }
}

function loadSettings(): PersistedSettings {
  if (typeof window === "undefined") return DEFAULT_SETTINGS;
  try {
    const raw = window.localStorage.getItem(SETTINGS_KEY);
    if (!raw) return DEFAULT_SETTINGS;
    const parsed = JSON.parse(raw);
    return { ...DEFAULT_SETTINGS, ...parsed };
  } catch {
    return DEFAULT_SETTINGS;
  }
}

function saveSettings(s: PersistedSettings): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
}

function makeId(): string {
  return `f_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

export function PIFactory({ isLoggedIn }: Props) {
  const { t } = useI18n();
  const { addToast } = useGlobalToast();
  const { importFees, loading: importingFees } = useEsiFeeImport();

  const [settings, setSettings] = useState<PersistedSettings>(() => loadSettings());
  const [portfolio, setPortfolio] = useState<PIFactoryConfig[]>(() => loadPortfolio());
  const [schematics, setSchematics] = useState<PISchematicSummary[]>([]);
  const [schematicFilter, setSchematicFilter] = useState("");
  const [stations, setStations] = useState<StationInfo[]>([]);
  const [stationName, setStationName] = useState<string>(
    "Jita IV - Moon 4 - Caldari Navy Assembly Plant",
  );
  const [planResp, setPlanResp] = useState<PIFactoryResponse | null>(null);
  const [fetching, setFetching] = useState(false);

  useEffect(() => saveSettings(settings), [settings]);
  useEffect(() => savePortfolio(portfolio), [portfolio]);

  // Load schematic catalog once on mount.
  useEffect(() => {
    void (async () => {
      try {
        const list = await getPISchematics();
        setSchematics(list);
      } catch (err: any) {
        addToast(
          err?.message ?? t("piFactorySchematicsLoadFailed"),
          "error",
          3000,
        );
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Load stations when the system changes so the user can pick a specific
  // NPC station beyond the hub quickselect.
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
      setSettings((prev) => ({ ...prev, systemName: s }));
      void loadStationsForSystem(s);
    },
    [loadStationsForSystem],
  );

  const schematicById = useMemo(() => {
    const m = new Map<number, PISchematicSummary>();
    for (const s of schematics) m.set(s.id, s);
    return m;
  }, [schematics]);

  const filteredSchematics = useMemo(() => {
    const q = schematicFilter.trim().toLowerCase();
    if (!q) return schematics;
    return schematics.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        (s.output_tier ?? "").toLowerCase().includes(q),
    );
  }, [schematicFilter, schematics]);

  const addFactory = useCallback(
    (schemId: number) => {
      const schem = schematicById.get(schemId);
      if (!schem) return;
      const item: PIFactoryConfig = {
        id: makeId(),
        name: schem.name,
        schematic_id: schemId,
        factory_count: 4,
      };
      setPortfolio((prev) => [...prev, item]);
    },
    [schematicById],
  );

  const removeFactory = useCallback((id: string) => {
    setPortfolio((prev) => prev.filter((p) => p.id !== id));
  }, []);

  // Drag-to-reorder state. draggedId is the source card; drop target is
  // determined at drop time from the target card + mouse Y (upper half →
  // insert BEFORE target, lower half → insert AFTER).
  const [draggedId, setDraggedId] = useState<string | null>(null);

  const reorderFactory = useCallback(
    (fromId: string, toId: string, position: "before" | "after") => {
      if (fromId === toId) return;
      setPortfolio((prev) => {
        const fromIdx = prev.findIndex((p) => p.id === fromId);
        const toIdx = prev.findIndex((p) => p.id === toId);
        if (fromIdx < 0 || toIdx < 0) return prev;
        const next = prev.slice();
        const [moved] = next.splice(fromIdx, 1);
        // Recompute the target index after the removal so it still points
        // at the same visual slot.
        let insertIdx = next.findIndex((p) => p.id === toId);
        if (insertIdx < 0) return prev;
        if (position === "after") insertIdx += 1;
        next.splice(insertIdx, 0, moved);
        return next;
      });
    },
    [],
  );

  const updateFactory = useCallback(
    (id: string, patch: Partial<PIFactoryConfig>) => {
      setPortfolio((prev) =>
        prev.map((p) => (p.id === id ? { ...p, ...patch } : p)),
      );
    },
    [],
  );

  const handleFetch = useCallback(async () => {
    if (portfolio.length === 0) {
      addToast(t("piFactoryNoFactories"), "error", 2400);
      return;
    }
    if (!settings.stationId) {
      addToast(t("piFactoryNoStation"), "error", 2400);
      return;
    }
    setFetching(true);
    try {
      const resp = await piFactoryPlan({
        station_id: settings.stationId,
        poco_tax_percent: settings.pocoTaxPct,
        sales_tax_percent: settings.salesTaxPct,
        broker_fee_percent: settings.brokerFeePct,
        buffer_days: settings.bufferDays,
        factories: portfolio,
      });
      setPlanResp(resp);
      if (resp.station_name) setStationName(resp.station_name);
    } catch (err: any) {
      addToast(err?.message ?? "Fetch failed", "error", 3000);
    } finally {
      setFetching(false);
    }
  }, [addToast, portfolio, settings, t]);

  const handleImportFees = useCallback(() => {
    void importFees((fees) =>
      setSettings((prev) => ({
        ...prev,
        salesTaxPct: fees.suggested_sales_tax_percent,
        brokerFeePct: fees.suggested_broker_fee_percent,
      })),
    );
  }, [importFees]);

  const handleCopyShopping = useCallback(async () => {
    if (!planResp || planResp.shopping.length === 0) {
      addToast(t("piFactoryNothingToCopy"), "error", 2400);
      return;
    }
    const lines = planResp.shopping
      .filter((r) => r.qty_buffer > 0)
      .map((r) => `${r.type_name}\t${r.qty_buffer}`);
    if (lines.length === 0) {
      addToast(t("piFactoryNothingToCopy"), "error", 2400);
      return;
    }
    try {
      await navigator.clipboard.writeText(lines.join("\n"));
      addToast(
        t("piFactoryShoppingCopied", { count: lines.length }),
        "success",
        2400,
      );
    } catch {
      addToast(t("piFactoryCopyFailed"), "error", 2400);
    }
  }, [addToast, planResp, t]);

  const resultById = useMemo(() => {
    const m = new Map<string, PIFactoryResponse["results"][number]>();
    if (planResp) {
      for (const r of planResp.results) m.set(r.id, r);
    }
    return m;
  }, [planResp]);

  const aggregates = useMemo(() => {
    if (!planResp) return null;
    let build = 0,
      taxes = 0,
      rev = 0,
      fees = 0,
      net = 0,
      savings = 0,
      inputSale = 0,
      outputSale = 0;
    for (const r of planResp.results) {
      if (r.unresolved) continue;
      build += r.input_cost_per_day;
      taxes += r.poco_tax_per_day;
      rev += r.gross_rev_per_day;
      fees += r.sales_fees_per_day;
      net += r.net_profit_per_day;
      savings += r.savings_vs_buy_per_day;
      inputSale += r.input_sale_value_per_day;
      outputSale += r.output_sale_value_per_day;
    }
    return { build, taxes, rev, fees, net, savings, inputSale, outputSale };
  }, [planResp]);

  return (
    <div className="flex-1 flex flex-col min-h-0 p-3 gap-3">
      {/* Top: settings row */}
      <div className="shrink-0 rounded-sm border border-eve-border/60 bg-gradient-to-br from-eve-panel to-eve-dark/40 p-3">
        <div className="flex items-center justify-between gap-2 mb-2">
          <div className="flex items-center gap-2">
            <span className="text-eve-accent text-base">🏭</span>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-eve-text">
              {t("tabPIFactory")}
            </h3>
          </div>
          <TabHelp
            stepKeys={[
              "helpPIFactoryStep1",
              "helpPIFactoryStep2",
              "helpPIFactoryStep3",
              "helpPIFactoryStep4",
            ]}
            wikiSlug="PI-Factory"
          />
        </div>
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-[220px]">
            <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
              {t("system")}
            </label>
            <SystemAutocomplete
              value={settings.systemName}
              onChange={setSystemAndReload}
            />
          </div>
          <div className="min-w-[240px]">
            <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
              {t("colStationName")}
            </label>
            <select
              value={settings.stationId}
              onChange={(e) => {
                const id = Number(e.target.value);
                setSettings((prev) => ({ ...prev, stationId: id }));
                const match = stations.find((s) => s.id === id);
                if (match) setStationName(match.name);
              }}
              className="w-full h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs"
            >
              {stations.length === 0 && (
                <option value={settings.stationId}>
                  {stationName || `#${settings.stationId}`}
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
                settings.systemName.toLowerCase() ===
                  hub.systemName.toLowerCase() &&
                settings.stationId === hub.stationID;
              return (
                <button
                  key={hub.key}
                  type="button"
                  onClick={() => {
                    setSettings((prev) => ({
                      ...prev,
                      systemName: hub.systemName,
                      stationId: hub.stationID,
                    }));
                    void loadStationsForSystem(hub.systemName);
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
          <div>
            <label
              className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
              title={t("piFactoryPocoTaxHint")}
            >
              {t("piFactoryPocoTax")}
            </label>
            <input
              type="number"
              min={0}
              max={100}
              step={0.5}
              value={settings.pocoTaxPct}
              onChange={(e) =>
                setSettings((prev) => ({
                  ...prev,
                  pocoTaxPct: Math.max(0, Math.min(100, Number(e.target.value) || 0)),
                }))
              }
              className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
            />
          </div>
          <div>
            <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
              {t("piFactorySalesTax")}
            </label>
            <input
              type="number"
              min={0}
              max={100}
              step={0.1}
              value={settings.salesTaxPct}
              onChange={(e) =>
                setSettings((prev) => ({
                  ...prev,
                  salesTaxPct: Math.max(0, Math.min(100, Number(e.target.value) || 0)),
                }))
              }
              className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
            />
          </div>
          <div>
            <label className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1">
              {t("piFactoryBrokerFee")}
            </label>
            <div className="flex items-center gap-1">
              <input
                type="number"
                min={0}
                max={100}
                step={0.1}
                value={settings.brokerFeePct}
                onChange={(e) =>
                  setSettings((prev) => ({
                    ...prev,
                    brokerFeePct: Math.max(0, Math.min(100, Number(e.target.value) || 0)),
                  }))
                }
                className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
              />
              {isLoggedIn && (
                <button
                  type="button"
                  onClick={handleImportFees}
                  disabled={importingFees}
                  title={t("esiFeeSyncHint")}
                  className="h-8 px-2 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-[10px] uppercase tracking-wider disabled:opacity-40 disabled:cursor-not-allowed"
                >
                  {importingFees ? t("esiFeeSyncPending") : t("esiFeeSync")}
                </button>
              )}
            </div>
          </div>
          <div>
            <label
              className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
              title={t("piFactoryBufferDaysHint")}
            >
              {t("piFactoryBufferDays")}
            </label>
            <input
              type="number"
              min={1}
              max={90}
              step={1}
              value={settings.bufferDays}
              onChange={(e) =>
                setSettings((prev) => ({
                  ...prev,
                  bufferDays: Math.max(1, Math.min(90, Number(e.target.value) || 7)),
                }))
              }
              className="w-20 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
            />
          </div>
          <div>
            <label
              className="text-[11px] uppercase tracking-wider text-eve-dim font-medium block mb-1"
              title={t("piFactoryLaunchpadHint")}
            >
              {t("piFactoryLaunchpad")}
            </label>
            <input
              type="number"
              min={0}
              step={500}
              value={settings.launchpadM3}
              onChange={(e) =>
                setSettings((prev) => ({
                  ...prev,
                  launchpadM3: Math.max(0, Number(e.target.value) || 0),
                }))
              }
              className="w-24 h-8 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
            />
          </div>
          <button
            type="button"
            onClick={() => void handleFetch()}
            disabled={fetching || portfolio.length === 0}
            className="ml-auto px-3 py-1.5 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {fetching ? t("piFactoryFetching") : t("piFactoryFetchBtn")}
          </button>
        </div>
      </div>

      {/* Body: schematic picker (left) + factories (right) */}
      <div className="flex-1 min-h-0 grid grid-cols-1 lg:grid-cols-[minmax(240px,300px)_1fr] gap-3">
        {/* Left: schematic picker only — the portfolio itself lives as the
            factory cards on the right, so we don't duplicate that list here. */}
        <div className="flex flex-col min-h-0 rounded-sm border border-eve-border/60 bg-eve-panel/40 p-2">
          <div className="text-[10px] uppercase tracking-wider text-eve-dim px-1 mb-1 shrink-0">
            {t("piFactoryAddSchematic")}
          </div>
          <input
            type="text"
            value={schematicFilter}
            onChange={(e) => setSchematicFilter(e.target.value)}
            placeholder={t("piFactorySchematicFilter")}
            className="shrink-0 w-full h-7 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs mb-1"
          />
          <div className="flex-1 min-h-0 flex flex-col gap-0.5 overflow-auto">
            {filteredSchematics.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => addFactory(s.id)}
                className="text-left px-2 py-1 rounded-sm hover:bg-eve-accent/10 text-[11px] text-eve-text flex items-center justify-between gap-2"
              >
                <span className="truncate">{s.name}</span>
                {s.output_tier && (
                  <span className="shrink-0 text-[9px] text-eve-dim uppercase">
                    {s.output_tier}
                  </span>
                )}
              </button>
            ))}
            {filteredSchematics.length === 0 && (
              <div className="p-2 text-center text-[11px] text-eve-dim">
                {schematics.length === 0
                  ? t("piFactoryLoadingSchematics")
                  : t("piFactoryNoMatchingSchematics")}
              </div>
            )}
          </div>
        </div>

        {/* Right: factory cards + shopping list */}
        <div className="flex flex-col min-h-0 gap-4 overflow-auto">
          {portfolio.length === 0 ? (
            <div className="flex-1 flex items-center justify-center text-sm text-eve-dim p-6 text-center">
              {t("piFactoryEmptyState")}
            </div>
          ) : (
            <>
              {aggregates && (
                <div className="shrink-0 rounded-sm border border-eve-border/60 bg-eve-panel/40 p-3 grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-8 gap-3 text-[11px]">
                  <Stat
                    label={t("piFactoryTotalBuild")}
                    value={formatISK(aggregates.build)}
                    hint={t("piFactoryTotalBuildHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalTaxes")}
                    value={formatISK(aggregates.taxes)}
                    hint={t("piFactoryTotalTaxesHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalRev")}
                    value={formatISK(aggregates.rev)}
                    hint={t("piFactoryTotalRevHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalFees")}
                    value={formatISK(aggregates.fees)}
                    hint={t("piFactoryTotalFeesHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalNet")}
                    value={formatISK(aggregates.net)}
                    tone={aggregates.net >= 0 ? "good" : "bad"}
                    hint={t("piFactoryTotalNetHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalInputSale")}
                    value={formatISK(aggregates.inputSale)}
                    hint={t("piFactoryTotalInputSaleHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalOutputSale")}
                    value={formatISK(aggregates.outputSale)}
                    hint={t("piFactoryTotalOutputSaleHint")}
                  />
                  <Stat
                    label={t("piFactoryTotalSavings")}
                    value={formatISK(aggregates.savings)}
                    tone={aggregates.savings >= 0 ? "good" : "bad"}
                    hint={t("piFactoryTotalSavingsHint")}
                  />
                </div>
              )}

              {portfolio.map((p) => (
                <FactoryCard
                  key={p.id}
                  cfg={p}
                  result={resultById.get(p.id)}
                  launchpadM3={settings.launchpadM3}
                  salesTaxPct={settings.salesTaxPct}
                  brokerFeePct={settings.brokerFeePct}
                  isDragging={draggedId === p.id}
                  onUpdate={(patch) => updateFactory(p.id, patch)}
                  onRemove={() => removeFactory(p.id)}
                  onDragStart={() => setDraggedId(p.id)}
                  onDragEnd={() => setDraggedId(null)}
                  onDropReorder={(position) => {
                    if (draggedId) {
                      reorderFactory(draggedId, p.id, position);
                    }
                    setDraggedId(null);
                  }}
                />
              ))}

              {planResp && planResp.shopping.length > 0 && (
                <div className="shrink-0 rounded-sm border border-eve-border/60 bg-eve-panel/40 p-3">
                  <div className="flex items-center justify-between gap-2 mb-2">
                    <div>
                      <div className="text-xs font-semibold text-eve-accent uppercase tracking-wider">
                        {t("piFactoryShoppingList")}
                      </div>
                      <div className="text-[10px] text-eve-dim">
                        {t("piFactoryShoppingSubtitle", {
                          days: String(planResp.buffer_days),
                          station: stationName,
                        })}
                        {(() => {
                          const totalM3 = planResp.shopping.reduce(
                            (s, r) => s + (r.volume_buffer ?? 0),
                            0,
                          );
                          if (totalM3 <= 0) return null;
                          return (
                            <>
                              {" · "}
                              <span title={t("piFactoryShoppingTotalVolumeHint")}>
                                {t("piFactoryShoppingTotalVolume", {
                                  m3: totalM3.toLocaleString(undefined, {
                                    maximumFractionDigits: 0,
                                  }),
                                })}
                              </span>
                            </>
                          );
                        })()}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => void handleCopyShopping()}
                      className="px-3 py-1 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs"
                    >
                      📋{" "}
                      {t("priceAuditCopyBtn", {
                        count: planResp.shopping.length,
                      })}
                    </button>
                  </div>
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
                        <th className="px-2 py-1 text-left font-medium">
                          {t("colItem")}
                        </th>
                        <th className="px-2 py-1 text-right font-medium">
                          {t("piFactoryQtyPerDay")}
                        </th>
                        <th className="px-2 py-1 text-right font-medium">
                          {t("piFactoryBufferQty")}
                        </th>
                        <th
                          className="px-2 py-1 text-right font-medium"
                          title={t("piFactoryBufferVolumeHint")}
                        >
                          m³
                        </th>
                        <th className="px-2 py-1 text-right font-medium">
                          {t("colStationSellPrice")}
                        </th>
                        <th className="px-2 py-1 text-right font-medium">
                          {t("piFactoryBufferCost")}
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {planResp.shopping.map((r, i) => (
                        <tr
                          key={`${r.type_id}-${i}`}
                          className={`border-b border-eve-border/50 ${i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"}`}
                        >
                          <td className="px-2 py-1 text-eve-text">
                            {r.type_name}
                          </td>
                          <td className="px-2 py-1 text-right font-mono text-eve-dim">
                            {r.qty_per_day.toLocaleString(undefined, {
                              maximumFractionDigits: 1,
                            })}
                          </td>
                          <td className="px-2 py-1 text-right font-mono text-eve-accent">
                            {r.qty_buffer.toLocaleString()}
                          </td>
                          <td className="px-2 py-1 text-right font-mono text-eve-dim">
                            {r.volume_buffer
                              ? r.volume_buffer.toLocaleString(undefined, {
                                  maximumFractionDigits: 1,
                                })
                              : "—"}
                          </td>
                          <td className="px-2 py-1 text-right font-mono text-eve-dim">
                            {r.sell_price != null
                              ? formatISK(r.sell_price)
                              : "—"}
                          </td>
                          <td className="px-2 py-1 text-right font-mono text-eve-accent">
                            {r.cost_buffer != null
                              ? formatISK(r.cost_buffer)
                              : "—"}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  tone,
  hint,
  labelHint,
  valueHint,
}: {
  label: string;
  value: string;
  tone?: "good" | "bad";
  /** Alias for labelHint — kept for callers not yet split. */
  hint?: string;
  labelHint?: string;
  valueHint?: string;
}) {
  const cls =
    tone === "good"
      ? "text-emerald-300"
      : tone === "bad"
        ? "text-red-300"
        : "text-eve-accent";
  const lHint = labelHint ?? hint;
  return (
    <div>
      <div
        className={`text-[10px] uppercase tracking-wider text-eve-dim ${lHint ? "cursor-help" : ""}`}
        title={lHint}
      >
        {label}
      </div>
      <div
        className={`font-mono text-sm ${cls} ${valueHint ? "cursor-help" : ""}`}
        title={valueHint}
      >
        {value}
      </div>
    </div>
  );
}

// Colour keyed to the output tier so cards for different PI tiers read
// as visually distinct rows in the portfolio. Neutral fallback for
// unresolved / unknown tiers.
const TIER_STYLES: Record<string, { stripe: string; badge: string }> = {
  P1: {
    stripe: "bg-emerald-500/70",
    badge: "border-emerald-500/60 bg-emerald-500/10 text-emerald-300",
  },
  P2: {
    stripe: "bg-sky-500/70",
    badge: "border-sky-500/60 bg-sky-500/10 text-sky-300",
  },
  P3: {
    stripe: "bg-violet-500/70",
    badge: "border-violet-500/60 bg-violet-500/10 text-violet-300",
  },
  P4: {
    stripe: "bg-amber-500/70",
    badge: "border-amber-500/60 bg-amber-500/10 text-amber-300",
  },
};

function FactoryCard({
  cfg,
  result,
  launchpadM3,
  salesTaxPct,
  brokerFeePct,
  isDragging,
  onUpdate,
  onRemove,
  onDragStart,
  onDragEnd,
  onDropReorder,
}: {
  cfg: PIFactoryConfig;
  result?: PIFactoryResponse["results"][number];
  launchpadM3: number;
  salesTaxPct: number;
  brokerFeePct: number;
  isDragging: boolean;
  onUpdate: (patch: Partial<PIFactoryConfig>) => void;
  onRemove: () => void;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDropReorder: (position: "before" | "after") => void;
}) {
  const { t } = useI18n();
  const daysPerLaunchpad =
    result && result.input_volume_per_day > 0 && launchpadM3 > 0
      ? launchpadM3 / result.input_volume_per_day
      : null;
  const tier = result?.output_tier ?? "";
  const tierStyle = TIER_STYLES[tier];

  // Sum of input sell prices × qty — used for the "Sell inputs instead"
  // value-hint arithmetic breakdown.
  const inputSellSum = result
    ? result.inputs.reduce(
        (s, r) => s + (r.sell_price ?? 0) * r.qty_per_day,
        0,
      )
    : 0;
  // Show sales and broker rates separately in tooltips so the user
  // recognizes their own configured numbers ("3.5999%" not "4.5999%").
  const salesLabel = `${salesTaxPct.toLocaleString(undefined, { maximumFractionDigits: 4 })}%`;
  const brokerLabel = `${brokerFeePct.toLocaleString(undefined, { maximumFractionDigits: 4 })}%`;
  const feeBreakdown = `${salesLabel} sales − ${brokerLabel} broker`;

  // Track which half of the card the pointer is over so the drop indicator
  // shows on the correct edge.
  const [dropEdge, setDropEdge] = useState<"top" | "bottom" | null>(null);

  const handleDragOver = (e: DragEvent<HTMLDivElement>) => {
    // Required — without preventDefault the browser rejects the drop.
    e.preventDefault();
    if (isDragging) {
      // Don't show an indicator on the card being dragged itself.
      setDropEdge(null);
      return;
    }
    const rect = e.currentTarget.getBoundingClientRect();
    const midY = rect.top + rect.height / 2;
    setDropEdge(e.clientY < midY ? "top" : "bottom");
  };

  return (
    <div
      draggable
      onDragStart={(e) => {
        // Firefox requires setData for the drag to be recognized.
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", cfg.id);
        onDragStart();
      }}
      onDragEnd={() => {
        setDropEdge(null);
        onDragEnd();
      }}
      onDragOver={handleDragOver}
      onDragLeave={() => setDropEdge(null)}
      onDrop={(e) => {
        e.preventDefault();
        const position = dropEdge === "top" ? "before" : "after";
        setDropEdge(null);
        onDropReorder(position);
      }}
      className={`relative shrink-0 overflow-hidden rounded-sm border-2 border-eve-border bg-eve-dark/60 shadow-md shadow-black/20 p-3 pl-4 transition-opacity ${
        isDragging ? "opacity-40" : ""
      }`}
    >
      {/* Tier-colored left stripe. Neutral when tier isn't known yet
          (e.g. before Fetch prices, or an unresolved schematic). */}
      <div
        className={`absolute inset-y-0 left-0 w-1 ${tierStyle?.stripe ?? "bg-eve-border"}`}
      />
      {/* Drop indicator — thin accent bar on the edge the drop would target. */}
      {dropEdge === "top" && (
        <div className="absolute inset-x-0 top-0 h-0.5 bg-eve-accent shadow-eve-glow" />
      )}
      {dropEdge === "bottom" && (
        <div className="absolute inset-x-0 bottom-0 h-0.5 bg-eve-accent shadow-eve-glow" />
      )}
      <div className="flex items-center justify-between gap-3 mb-2">
        <div className="min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-bold text-eve-text uppercase tracking-wider truncate">
              {result?.schematic_name ?? cfg.name}
            </span>
            {tier && tierStyle && (
              <span
                className={`shrink-0 inline-flex items-center px-1.5 py-0.5 rounded-sm border text-[10px] leading-none font-semibold uppercase tracking-wider ${tierStyle.badge}`}
              >
                {tier}
              </span>
            )}
          </div>
          {result && (
            <div
              className="text-[10px] text-eve-dim cursor-help"
              title={t("piFactoryCycleInfoHint")}
            >
              {t("piFactoryCycleInfo", {
                cycle:
                  result.cycle_time_sec >= 3600
                    ? `${(result.cycle_time_sec / 3600).toFixed(1)}h`
                    : `${(result.cycle_time_sec / 60).toFixed(0)}m`,
                cyclesDay: result.cycles_per_day.toFixed(1),
              })}
            </div>
          )}
        </div>
        <div className="flex items-center gap-2">
          <label className="text-[10px] uppercase tracking-wider text-eve-dim">
            {t("piFactoryFactories")}
          </label>
          <input
            type="number"
            min={1}
            max={100}
            value={cfg.factory_count}
            onChange={(e) =>
              onUpdate({
                factory_count: Math.max(
                  1,
                  Math.min(100, Number(e.target.value) || 1),
                ),
              })
            }
            className="w-16 h-7 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono"
          />
          <button
            type="button"
            onClick={onRemove}
            title={t("piFactoryRemove")}
            className="h-7 w-7 flex items-center justify-center rounded-sm border border-eve-border text-eve-dim hover:text-red-300 hover:border-red-400/60 transition-colors text-sm"
          >
            ×
          </button>
        </div>
      </div>

      {result?.unresolved ? (
        <div className="text-[11px] text-red-300">
          {result.unresolved_reason ?? t("piFactoryUnresolved")}
        </div>
      ) : !result ? (
        <div className="text-[11px] text-eve-dim italic">
          {t("piFactoryPendingFetch")}
        </div>
      ) : (
        <>
          <table className="w-full text-xs" style={{ tableLayout: "fixed" }}>
            {/* Fixed widths so all factory cards line up column-for-column,
                regardless of item-name length or per-cell number magnitude. */}
            <colgroup>
              <col />
              <col style={{ width: 100 }} />
              <col style={{ width: 90 }} />
              <col style={{ width: 100 }} />
              <col style={{ width: 110 }} />
            </colgroup>
            <thead>
              <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
                <th className="px-2 py-1 text-left font-medium truncate">
                  {t("piFactoryInput")}
                </th>
                <th
                  className="px-2 py-1 text-right font-medium"
                  title={t("piFactoryQtyPerDayHint")}
                >
                  {t("piFactoryQtyPerDay")}
                </th>
                <th
                  className="px-2 py-1 text-right font-medium"
                  title={t("piFactoryVolumePerDayHint")}
                >
                  m³/day
                </th>
                <th
                  className="px-2 py-1 text-right font-medium"
                  title={t("piFactorySellPriceHint")}
                >
                  {t("colStationSellPrice")}
                </th>
                <th
                  className="px-2 py-1 text-right font-medium"
                  title={t("piFactoryCostPerDayHint")}
                >
                  {t("piFactoryCostPerDay")}
                </th>
              </tr>
            </thead>
            <tbody>
              {result.inputs.map((r, i) => (
                <tr key={`${r.type_id}-${i}`}>
                  <td className="px-2 py-1 text-eve-text truncate" title={r.type_name}>
                    {r.type_name}
                  </td>
                  <td
                    className="px-2 py-1 text-right font-mono text-eve-dim"
                    title={t("piFactoryQtyPerDayHint")}
                  >
                    {r.qty_per_day.toLocaleString(undefined, {
                      maximumFractionDigits: 1,
                    })}
                  </td>
                  <td
                    className="px-2 py-1 text-right font-mono text-eve-dim"
                    title={
                      r.unit_volume
                        ? t("piFactoryUnitVolumeHint", {
                            v: r.unit_volume.toLocaleString(undefined, {
                              maximumFractionDigits: 3,
                            }),
                          })
                        : t("piFactoryVolumePerDayHint")
                    }
                  >
                    {r.volume_per_day
                      ? r.volume_per_day.toLocaleString(undefined, {
                          maximumFractionDigits: 1,
                        })
                      : "—"}
                  </td>
                  <td
                    className="px-2 py-1 text-right font-mono text-eve-dim"
                    title={t("piFactorySellPriceHint")}
                  >
                    {r.sell_price ? formatISK(r.sell_price) : "—"}
                  </td>
                  <td
                    className="px-2 py-1 text-right font-mono text-eve-accent"
                    title={t("piFactoryCostPerDayHint")}
                  >
                    {formatISK(r.cost_per_day ?? 0)}
                  </td>
                </tr>
              ))}
              <tr
                className="border-t border-eve-border/60"
                title={t("piFactoryOutputRowHint")}
              >
                <td
                  className="px-2 py-1 text-eve-accent font-semibold truncate"
                  title={result.output.type_name}
                >
                  → {result.output.type_name}
                </td>
                <td className="px-2 py-1 text-right font-mono text-eve-accent">
                  {result.output.qty_per_day.toLocaleString(undefined, {
                    maximumFractionDigits: 1,
                  })}
                </td>
                <td className="px-2 py-1 text-right font-mono text-eve-dim">
                  {result.output.volume_per_day
                    ? result.output.volume_per_day.toLocaleString(undefined, {
                        maximumFractionDigits: 1,
                      })
                    : "—"}
                </td>
                <td
                  className="px-2 py-1 text-right font-mono text-eve-dim"
                  title={t("piFactorySellPriceHint")}
                >
                  {result.output.sell_price
                    ? formatISK(result.output.sell_price)
                    : "—"}
                </td>
                <td
                  className="px-2 py-1 text-right font-mono text-eve-accent"
                  title={t("piFactoryGrossRevHint")}
                >
                  {formatISK(result.gross_rev_per_day)}
                </td>
              </tr>
            </tbody>
          </table>

          <div
            className="mt-2 grid gap-x-4 gap-y-2 text-[11px]"
            style={{
              // Auto-fill so each cell takes at least ~135px but grows to
              // fill remaining space. Wider than a rigid 7-column layout
              // which was truncating the launchpad-fill and savings cells.
              gridTemplateColumns: "repeat(auto-fill, minmax(135px, 1fr))",
            }}
          >
            <MetricCell
              label={t("piFactoryPocoTaxes")}
              value={formatISK(result.poco_tax_per_day)}
              tone="dim"
              labelHint={t("piFactoryPocoTaxesHint")}
              valueHint={`${formatISK(result.poco_tax_per_day)} = ${formatISK(result.poco_import_per_day)} import + ${formatISK(result.poco_export_per_day)} export`}
            />
            <MetricCell
              label={t("piFactoryInputCost")}
              value={formatISK(result.input_cost_per_day)}
              tone="dim"
              labelHint={t("piFactoryInputCostHint")}
              valueHint={`${formatISK(result.input_cost_per_day)} = Σ (input qty/day × sell price at hub) across ${result.inputs.length} inputs`}
            />
            <MetricCell
              label={t("piFactoryInputSale")}
              value={formatISK(result.input_sale_value_per_day)}
              tone="dim"
              labelHint={t("piFactoryInputSaleHint")}
              valueHint={`${formatISK(result.input_sale_value_per_day)} = ${formatISK(inputSellSum)} raw × (1 − ${feeBreakdown})`}
            />
            <MetricCell
              label={t("piFactoryOutputSale")}
              value={formatISK(result.output_sale_value_per_day)}
              tone="dim"
              labelHint={t("piFactoryOutputSaleHint")}
              valueHint={`${formatISK(result.output_sale_value_per_day)} = ${formatISK(result.gross_rev_per_day)} × (1 − ${feeBreakdown}) − ${formatISK(result.poco_export_per_day)} POCO export`}
            />
            <MetricCell
              label={t("piFactoryNet")}
              value={formatISK(result.net_profit_per_day)}
              tone={result.net_profit_per_day >= 0 ? "good" : "bad"}
              labelHint={t("piFactoryNetHint")}
              valueHint={`${formatISK(result.net_profit_per_day)} = ${formatISK(result.gross_rev_per_day)} − ${formatISK(result.sales_fees_per_day)} (${feeBreakdown}) − ${formatISK(result.input_cost_per_day)} inputs − ${formatISK(result.poco_tax_per_day)} POCO`}
            />
            <MetricCell
              label={t("piFactorySavingsVsBuy")}
              value={formatISK(result.savings_vs_buy_per_day)}
              tone={result.savings_vs_buy_per_day >= 0 ? "good" : "bad"}
              labelHint={t("piFactorySavingsVsBuyHint")}
              valueHint={`${formatISK(result.savings_vs_buy_per_day)} = ${formatISK(result.buy_output_cost_per_day)} buy-price − ${formatISK(result.input_cost_per_day + result.poco_tax_per_day)} build cost (inputs + POCO)`}
            />
            <MetricCell
              label={t("piFactoryLaunchpadFill")}
              value={
                daysPerLaunchpad != null
                  ? t("piFactoryLaunchpadFillValueShort", {
                      days: daysPerLaunchpad.toLocaleString(undefined, {
                        maximumFractionDigits: 1,
                      }),
                    })
                  : "—"
              }
              tone="dim"
              labelHint={t("piFactoryLaunchpadFillHint")}
              valueHint={
                daysPerLaunchpad != null
                  ? `${daysPerLaunchpad.toLocaleString(undefined, { maximumFractionDigits: 1 })} d = ${launchpadM3.toLocaleString()} m³ launchpad ÷ ${result.input_volume_per_day.toLocaleString(undefined, { maximumFractionDigits: 0 })} m³/day input burn`
                  : undefined
              }
            />
          </div>
        </>
      )}
    </div>
  );
}

function MetricCell({
  label,
  value,
  tone,
  hint,
  labelHint,
  valueHint,
}: {
  label: string;
  value: string;
  tone: "good" | "bad" | "dim";
  /** Alias for labelHint — kept for callers not yet split. */
  hint?: string;
  labelHint?: string;
  valueHint?: string;
}) {
  const cls =
    tone === "good"
      ? "text-emerald-300"
      : tone === "bad"
        ? "text-red-300"
        : "text-eve-text";
  const lHint = labelHint ?? hint;
  return (
    <div>
      <div
        className={`text-[10px] uppercase tracking-wider text-eve-dim ${lHint ? "cursor-help" : ""}`}
        title={lHint}
      >
        {label}
      </div>
      <div
        className={`font-mono ${cls} ${valueHint ? "cursor-help" : ""}`}
        title={valueHint}
      >
        {value}
      </div>
    </div>
  );
}
