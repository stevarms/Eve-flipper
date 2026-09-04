import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getCharacterInfo, getPLEXDashboard, type CharacterScope, type PLEXDashboardParams } from "../lib/api";
import { type TranslationKey, useI18n } from "../lib/i18n";
import { formatISK } from "../lib/format";
import { useTheme } from "../lib/useTheme";
import type { PLEXDashboard, ArbitragePath, ScanParams } from "../lib/types";
import { normalizeTaxProfile, type TaxProfile } from "../lib/taxProfile";
import { usePlexAlerts, PlexAlertPanel } from "./PlexAlerts";
import { SignalCard, GlobalPriceCard, ArbitrageRow, SpreadRow, MarketDepthCard, InjectionTiersCard } from "./plex-tab/PlexMarketCards";
import { SPFarmCard } from "./plex-tab/SPFarmCard";
import { ArbHistoryChart, PLEXChart } from "./plex-tab/PlexCharts";
import { ArbitrageModal } from "./plex-tab/PlexArbitrageModal";
import { OmegaComparatorCard, CrossHubCard } from "./plex-tab/PlexAnalyticsCards";

type PlexSubTab = "market" | "spfarm" | "analytics";
const SKILL_ACCOUNTING = 16622;
const SKILL_BROKER_RELATIONS = 3446;

/**
 * The arbitrage matrix is three different businesses, not two.
 *
 * It used to be split "NES" vs "Spread", where NES was everything that was not
 * a spread — which quietly filed `market_process` (buy a Skill Extractor off
 * the *market*, extract, sell the Injector) under a heading that says you must
 * spend real money at the New Eden Store. It is in fact the most accessible
 * play on the screen: no NES purchase, no PLEX.
 */
type ArbTab = "nes" | "market" | "spread";
const ARB_TABS: readonly ArbTab[] = ["nes", "market", "spread"];

const ARB_TAB_LABEL: Record<ArbTab, TranslationKey> = {
  nes: "plexArbTabNES",
  market: "plexArbTabMarket",
  spread: "plexArbTabSpread",
};

const ARB_TAB_HINT: Record<ArbTab, TranslationKey> = {
  nes: "plexArbNESHint",
  market: "mmMarketArbHint",
  spread: "mmSpreadHint",
};

/** Which tab an engine path type belongs to. Unknown types fall to NES. */
function arbTabFor(type: string): ArbTab {
  if (type === "spread") return "spread";
  if (type === "market_process") return "market";
  return "nes";
}

/**
 * Is anything here worth doing, and how much is on the table?
 *
 * Every path is listed whether viable or not — a dead path is information —
 * so without this line you have to read six rows to learn there is nothing to
 * do. Paths with no market data are excluded from the denominator: "0 / 4
 * viable" reads as a bad market when it actually means a failed fetch.
 */
function ArbSummary({ paths }: { paths: ArbitragePath[] }) {
  const { t } = useI18n();
  const priced = paths.filter((p) => !p.no_data);
  if (priced.length === 0) return null;

  const viable = priced.filter((p) => p.viable);
  const bestROI = Math.max(...priced.map((p) => p.roi));
  const potential = viable.reduce((sum, p) => sum + p.profit_isk, 0);
  const tone = viable.length > 0 ? "text-eve-success" : "text-eve-error";

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 mb-2 text-[10px] text-eve-dim">
      <span>
        {t("mmViablePaths")}:{" "}
        <span className={`font-mono font-semibold ${tone}`}>
          {viable.length} / {priced.length}
        </span>
      </span>
      <span>
        {t("mmBestROI")}:{" "}
        <span className={`font-mono font-semibold ${bestROI > 0 ? "text-eve-success" : "text-eve-error"}`}>
          {bestROI > 0 ? "+" : ""}
          {bestROI.toFixed(1)}%
        </span>
      </span>
      {potential > 0 && (
        <span>
          {t("mmTotalPotential")}:{" "}
          <span className="font-mono font-semibold text-eve-success">{formatISK(potential)}</span>
        </span>
      )}
    </div>
  );
}

interface PlexTabProps {
  isLoggedIn?: boolean;
  activeCharacterId?: CharacterScope;
  taxProfile?: Partial<ScanParams>;
  onTaxProfileChange?: (profile: TaxProfile) => void;
}

/** Format seconds as M:SS */
function formatCountdown(sec: number): string {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}

export function PlexTab({ isLoggedIn = false, activeCharacterId, taxProfile, onTaxProfileChange }: PlexTabProps) {
  const { t } = useI18n();
  const { themeKey } = useTheme();
  const tax = useMemo(() => normalizeTaxProfile(taxProfile ?? {}), [taxProfile]);
  const salesTax = tax.split_trade_fees ? tax.sell_sales_tax_percent : tax.sales_tax_percent;
  const brokerFee = tax.split_trade_fees ? tax.sell_broker_fee_percent : tax.broker_fee_percent;
  const [dashboard, setDashboard] = useState<PLEXDashboard | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [nesExtractor, setNesExtractor] = useState(293);
  const [nesOmega, setNesOmega] = useState(500);
  const [omegaUSD, setOmegaUSD] = useState(14.99);
  const [showNES, setShowNES] = useState(false);
  const [esiFeesLoading, setEsiFeesLoading] = useState(false);
  const [esiFeesMsg, setEsiFeesMsg] = useState<string | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [autoInterval, setAutoInterval] = useState(5); // minutes
  const [countdown, setCountdown] = useState(0); // seconds remaining

  const abortRef = useRef<AbortController | null>(null);
  const autoTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchData = useCallback(async () => {
    // Abort any in-flight request
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    setLoading(true);
    setError("");
    try {
      const params: PLEXDashboardParams = {
        salesTax, brokerFee,
        nesExtractor, nesOmega, omegaUSD,
      };
      const data = await getPLEXDashboard(params, controller.signal);
      setDashboard(data);
    } catch (e: unknown) {
      if (e instanceof Error && e.name === "AbortError") return;
      setError(e instanceof Error ? e.message : "Failed to load PLEX data");
    } finally {
      setLoading(false);
    }
  }, [salesTax, brokerFee, nesExtractor, nesOmega, omegaUSD]);

  // Fetch on mount
  useEffect(() => {
    fetchData();
    return () => abortRef.current?.abort();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Auto-refresh timer
  useEffect(() => {
    // Clear previous timers
    if (autoTimerRef.current) { clearInterval(autoTimerRef.current); autoTimerRef.current = null; }
    if (countdownRef.current) { clearInterval(countdownRef.current); countdownRef.current = null; }
    setCountdown(0);

    if (!autoRefresh) return;

    const intervalMs = autoInterval * 60 * 1000;
    setCountdown(autoInterval * 60);

    // Countdown ticker (every second)
    countdownRef.current = setInterval(() => {
      setCountdown(prev => prev > 0 ? prev - 1 : 0);
    }, 1000);

    // Fetch timer
    autoTimerRef.current = setInterval(() => {
      fetchData();
      setCountdown(autoInterval * 60); // reset countdown after fetch
    }, intervalMs);

    return () => {
      if (autoTimerRef.current) clearInterval(autoTimerRef.current);
      if (countdownRef.current) clearInterval(countdownRef.current);
    };
  }, [autoRefresh, autoInterval, fetchData]);

  const [selectedArb, setSelectedArb] = useState<ArbitragePath | null>(null);
  const [arbTab, setArbTab] = useState<ArbTab>("nes");

  const arbGroups = useMemo(() => {
    const groups: Record<ArbTab, ArbitragePath[]> = { nes: [], market: [], spread: [] };
    for (const arb of dashboard?.arbitrage ?? []) groups[arbTabFor(arb.type)].push(arb);
    return groups;
  }, [dashboard]);
  const [showAlerts, setShowAlerts] = useState(false);
  const [subTab, setSubTab] = useState<PlexSubTab>("market");

  // PLEX alerts (Browser Notification API)
  usePlexAlerts(dashboard);

  const signal = dashboard?.signal;
  const ind = dashboard?.indicators;

  const updateSellTax = useCallback((nextSalesTax: number) => {
    onTaxProfileChange?.({
      ...tax,
      sales_tax_percent: nextSalesTax,
      sell_sales_tax_percent: nextSalesTax,
    });
  }, [onTaxProfileChange, tax]);

  const updateSellBrokerFee = useCallback((nextBrokerFee: number) => {
    onTaxProfileChange?.({
      ...tax,
      broker_fee_percent: nextBrokerFee,
      sell_broker_fee_percent: nextBrokerFee,
    });
  }, [onTaxProfileChange, tax]);

  const handleFetchEsiFees = useCallback(async () => {
    if (!isLoggedIn || !onTaxProfileChange) return;
    setEsiFeesLoading(true);
    setEsiFeesMsg(null);
    try {
      const info = await getCharacterInfo(activeCharacterId);
      const skills = info.skills?.skills ?? [];
      const accounting = skills.find((s) => s.skill_id === SKILL_ACCOUNTING)?.active_skill_level ?? 0;
      const brokerRelations = skills.find((s) => s.skill_id === SKILL_BROKER_RELATIONS)?.active_skill_level ?? 0;
      const nextSalesTax = parseFloat((8 * (1 - 0.11 * accounting)).toFixed(2));
      const nextBrokerFee = parseFloat(Math.max(0, 3 - brokerRelations * 0.3).toFixed(2));
      onTaxProfileChange({
        ...tax,
        sales_tax_percent: nextSalesTax,
        broker_fee_percent: nextBrokerFee,
        buy_broker_fee_percent: nextBrokerFee,
        sell_broker_fee_percent: nextBrokerFee,
        buy_sales_tax_percent: 0,
        sell_sales_tax_percent: nextSalesTax,
      });
      setEsiFeesMsg(t("plexSpfarmEsiFeesLoaded", { accounting, tax: nextSalesTax, broker: brokerRelations, fee: nextBrokerFee }));
    } catch {
      setEsiFeesMsg(t("plexSpfarmEsiError"));
    } finally {
      setEsiFeesLoading(false);
    }
  }, [activeCharacterId, isLoggedIn, onTaxProfileChange, tax, t]);

  return (
    <div className="flex flex-col gap-3 h-full overflow-y-auto pr-1 scrollbar-thin">
      {/* Top bar: controls */}
      <div className="flex items-center gap-3 flex-wrap shrink-0">
        <h2 className="text-sm font-semibold text-eve-accent uppercase tracking-wider">{t("plexTitle")}</h2>
        <div className="flex items-center gap-2 text-xs">
          <label className="text-eve-dim">{t("paramsTax")}</label>
          <input
            type="number"
            step="0.1"
            min="0"
            max="100"
            value={salesTax}
            onChange={(e) => updateSellTax(parseFloat(e.target.value) || 0)}
            disabled={!onTaxProfileChange}
            className="w-16 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-xs text-eve-text"
          />
          <label className="text-eve-dim">{t("paramsBrokerFee")}</label>
          <input
            type="number"
            step="0.1"
            min="0"
            max="100"
            value={brokerFee}
            onChange={(e) => updateSellBrokerFee(parseFloat(e.target.value) || 0)}
            disabled={!onTaxProfileChange}
            className="w-16 px-1.5 py-1 bg-eve-input border border-eve-border rounded-sm text-xs text-eve-text"
          />
          <button
            type="button"
            disabled={!isLoggedIn || !onTaxProfileChange || esiFeesLoading}
            onClick={() => void handleFetchEsiFees()}
            title={isLoggedIn ? t("plexSpfarmEsiSkillsTitleLoggedIn") : t("plexSpfarmEsiSkillsTitleLoggedOut")}
            className="flex items-center gap-1 px-2 py-1 rounded-sm text-[11px] border border-eve-accent/40 text-eve-accent bg-eve-accent/10 hover:bg-eve-accent/20 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          >
            {esiFeesLoading ? <span className="animate-pulse">⟳</span> : "⚡"}
            {esiFeesLoading ? t("plexLoading") : t("plexSpfarmEsiSkills")}
          </button>
          {esiFeesMsg && <span className="text-[11px] text-eve-dim">{esiFeesMsg}</span>}
        </div>
        <button
          onClick={() => setShowNES((v) => !v)}
          className={`px-2 py-1 rounded-sm text-[10px] font-semibold uppercase tracking-wider border transition-all ${showNES ? "border-eve-accent/50 bg-eve-accent/10 text-eve-accent" : "border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text"}`}
        >
          NES ▾
        </button>
        <button
          onClick={fetchData}
          disabled={loading}
          className="px-3 py-1.5 rounded-sm text-xs font-semibold uppercase tracking-wider bg-eve-accent text-eve-dark hover:bg-eve-accent-hover shadow-eve-glow disabled:opacity-50 disabled:cursor-not-allowed transition-all"
        >
          {loading ? t("plexLoading") : t("plexRefresh")}
        </button>
        {/* Auto-refresh toggle */}
        <div className="flex items-center gap-1.5">
          <button
            onClick={() => setAutoRefresh(v => !v)}
            className={`px-2 py-1 rounded-sm text-[10px] font-semibold uppercase tracking-wider border transition-all ${autoRefresh ? "border-eve-success/50 bg-eve-success/10 text-eve-success" : "border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text"}`}
            title={t("plexAutoRefreshHint")}
          >
            {autoRefresh ? `⟳ ${formatCountdown(countdown)}` : t("plexAutoRefresh")}
          </button>
          {autoRefresh && (
            <select
              value={autoInterval}
              onChange={(e) => setAutoInterval(Number(e.target.value))}
              className="px-1 py-0.5 bg-eve-input border border-eve-border rounded-sm text-[10px] text-eve-text"
            >
              <option value={1}>1 {t("plexMin")}</option>
              <option value={2}>2 {t("plexMin")}</option>
              <option value={5}>5 {t("plexMin")}</option>
              <option value={10}>10 {t("plexMin")}</option>
              <option value={15}>15 {t("plexMin")}</option>
              <option value={30}>30 {t("plexMin")}</option>
              <option value={60}>60 {t("plexMin")}</option>
            </select>
          )}
        </div>
        {/* Alert bell */}
        <div className="relative">
          <button
            onClick={() => setShowAlerts(v => !v)}
            className={`px-2 py-1 rounded-sm text-[10px] font-semibold border transition-all ${showAlerts ? "border-eve-warning/50 bg-eve-warning/10 text-eve-warning" : "border-eve-border bg-eve-panel text-eve-dim hover:text-eve-text"}`}
            title={t("plexAlerts")}
          >
            🔔
          </button>
          {showAlerts && <PlexAlertPanel onClose={() => setShowAlerts(false)} />}
        </div>
        {error && <span className="text-xs text-eve-error">{error}</span>}
      </div>

      {/* NES price overrides (collapsible) */}
      {showNES && (
        <div className="flex items-center gap-3 flex-wrap shrink-0 px-2 py-1.5 bg-eve-panel/50 border border-eve-border/50 rounded-sm">
          <span className="text-[10px] text-eve-dim uppercase tracking-wider font-medium">{t("plexNESPrices")}</span>
          <div className="flex items-center gap-1.5 text-xs">
            <label className="text-eve-dim">Extractor</label>
            <input type="number" min="1" value={nesExtractor} onChange={(e) => setNesExtractor(parseInt(e.target.value) || 0)}
              className="w-16 px-1.5 py-0.5 bg-eve-input border border-eve-border rounded-sm text-xs text-eve-text font-mono" />
            <span className="text-eve-dim text-[10px]">PLEX</span>
          </div>
          <div className="flex items-center gap-1.5 text-xs">
            <label className="text-eve-dim">Omega</label>
            <input type="number" min="1" value={nesOmega} onChange={(e) => setNesOmega(parseInt(e.target.value) || 0)}
              className="w-16 px-1.5 py-0.5 bg-eve-input border border-eve-border rounded-sm text-xs text-eve-text font-mono" />
            <span className="text-eve-dim text-[10px]">PLEX</span>
          </div>
          <span className="text-[10px] text-eve-dim italic">{t("plexNESHint")}</span>
        </div>
      )}

      {!dashboard && !loading && !error && (
        <div className="flex-1 flex items-center justify-center text-eve-dim text-sm">{t("plexEmpty")}</div>
      )}

      {dashboard && (
        <>
          {/* Sub-tab navigation */}
          <nav className="shrink-0 flex border-b border-eve-border">
            {(["market", "spfarm", "analytics"] as PlexSubTab[]).map(st => {
              const labels: Record<PlexSubTab, string> = {
                market: t("plexSubMarket"),
                spfarm: t("plexSubSPFarm"),
                analytics: t("plexSubAnalytics"),
              };
              return (
                <button
                  key={st}
                  onClick={() => setSubTab(st)}
                  className={`px-4 py-2 text-xs font-semibold uppercase tracking-wider border-b-2 transition-colors ${subTab === st ? "text-eve-accent border-eve-accent" : "text-eve-dim border-transparent hover:text-eve-text"}`}
                >
                  {labels[st]}
                </button>
              );
            })}
          </nav>

          {/* ==================== MARKET SUB-TAB ==================== */}
          {subTab === "market" && (
            <>
              {/* Signal + Global PLEX Price */}
              <div className="grid grid-cols-1 lg:grid-cols-[280px_1fr] gap-3 shrink-0">
                {signal && <SignalCard signal={signal} indicators={ind} />}
                <GlobalPriceCard price={dashboard.plex_price} indicators={ind} />
              </div>

              {/* Price Chart */}
              <div className="bg-eve-dark border border-eve-border rounded-sm p-3 shrink-0">
                <h3 className="text-xs font-semibold text-eve-dim uppercase tracking-wider mb-2">{t("plexPriceChart")}</h3>
                <PLEXChart history={dashboard.history} overlays={dashboard.chart_overlays} themeKey={themeKey} />
              </div>

              {/* Arbitrage Matrix (full width) */}
              <div className="bg-eve-dark border border-eve-border rounded-sm p-3 shrink-0">
                <div className="flex items-center gap-0 mb-2 flex-wrap">
                  {ARB_TABS.map((tab) => (
                    <button
                      key={tab}
                      onClick={() => setArbTab(tab)}
                      className={`px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wider border-b-2 transition-colors ${arbTab === tab ? "text-eve-accent border-eve-accent" : "text-eve-dim border-transparent hover:text-eve-text"}`}
                    >
                      {t(ARB_TAB_LABEL[tab])}
                      <span className="ml-1.5 font-normal opacity-60">{arbGroups[tab].length}</span>
                    </button>
                  ))}
                </div>

                {/* What this table is a model of, in one line. */}
                <p className="text-[10px] text-eve-dim mb-2">{t(ARB_TAB_HINT[arbTab])}</p>

                <ArbSummary paths={arbGroups[arbTab]} />

                <div className="overflow-x-auto table-scroll-wrapper table-scroll-container">
                  {arbTab === "spread" ? (
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="text-eve-dim border-b border-eve-border">
                          <th className="text-left py-1.5 px-2 font-medium">{t("mmItem")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("mmBuyOrder")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("mmSellOrder")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("mmSpreadISK")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("plexProfit")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">ROI</th>
                        </tr>
                      </thead>
                      <tbody>
                        {arbGroups.spread.map((arb, i) => (
                          <SpreadRow key={`spread-${i}`} arb={arb} onClick={() => setSelectedArb(arb)} />
                        ))}
                      </tbody>
                    </table>
                  ) : (
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="text-eve-dim border-b border-eve-border">
                          <th className="text-left py-1.5 px-2 font-medium">{t("plexPath")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">PLEX</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("plexCost")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("plexRevenue")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">{t("plexProfit")}</th>
                          <th className="text-right py-1.5 px-2 font-medium">ROI</th>
                        </tr>
                      </thead>
                      <tbody>
                        {arbGroups[arbTab].map((arb, i) => (
                          <ArbitrageRow key={`${arbTab}-${i}`} arb={arb} onClick={() => setSelectedArb(arb)} />
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>

                {arbTab === "spread" && (
                  <div className="mt-3 border-t border-eve-border/30 pt-2.5">
                    <h4 className="text-[10px] font-semibold text-eve-dim uppercase tracking-wider mb-1.5">
                      {t("mmTipsTitle")}
                    </h4>
                    <div className="space-y-1 text-[11px] text-eve-dim leading-relaxed">
                      <p>• {t("plexTipSpread1")}</p>
                      <p>• {t("plexTipSpread2")}</p>
                      <p>• {t("plexTipSpread3")}</p>
                      <p>• {t("mmTip4")}</p>
                      <p>• {t("mmTip5")}</p>
                      <p>• {t("mmTip6")}</p>
                    </div>
                  </div>
                )}
              </div>

              {/* Cross-Hub Arbitrage */}
              {dashboard.cross_hub && dashboard.cross_hub.length > 0 && (
                <CrossHubCard items={dashboard.cross_hub} />
              )}
            </>
          )}

          {/* ==================== SP FARM SUB-TAB ==================== */}
          {subTab === "spfarm" && (
            <>
              {/* SP Farm Calculator (full width) */}
              <SPFarmCard farm={dashboard.sp_farm} plexPrice={dashboard.plex_price} salesTax={salesTax} brokerFee={brokerFee} isLoggedIn={isLoggedIn} activeCharacterId={activeCharacterId} />

              {/* Injection Tiers */}
              <div className="grid grid-cols-1 gap-3 shrink-0">
                {dashboard.injection_tiers && dashboard.injection_tiers.length > 0 && (
                  <InjectionTiersCard tiers={dashboard.injection_tiers} />
                )}
              </div>
            </>
          )}

          {/* ==================== ANALYTICS SUB-TAB ==================== */}
          {subTab === "analytics" && (
            <>
              {/* Historical Arb Chart + Market Depth */}
              <div className="grid grid-cols-1 lg:grid-cols-[1fr_340px] gap-3 shrink-0">
                {dashboard.arb_history && (
                  <ArbHistoryChart data={dashboard.arb_history} themeKey={themeKey} />
                )}
                {dashboard.market_depth && (
                  <MarketDepthCard depth={dashboard.market_depth} />
                )}
              </div>

              {/* Omega Comparator */}
              <OmegaComparatorCard
                omega={dashboard.omega_comparison ?? null}
                omegaUSD={omegaUSD}
                onOmegaUSDChange={setOmegaUSD}
                plexPrice={dashboard.plex_price.sell_price}
                nesOmega={nesOmega}
              />
            </>
          )}
        </>
      )}

      {/* Arbitrage detail modal */}
      {selectedArb && (
        <ArbitrageModal arb={selectedArb} onClose={() => setSelectedArb(null)} />
      )}
    </div>
  );
}
