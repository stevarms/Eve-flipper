import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CopyButton } from "@/components/ui/CopyButton";
import { ItemRef } from "@/components/ui/ItemRef";
import {
  analyzeLPStore,
  getLPBalances,
  getLPBPCPrices,
  getLPCorporations,
  setLPBPCPrice,
  type LPBalancesResponse,
  type LPCorporation,
  type LPMethod,
  type LPOfferRow,
  type LPStreamMessage,
} from "@/lib/api";
import { formatISK } from "@/lib/format";
import { useI18n, type TranslationKey } from "@/lib/i18n";
import { loadScannerPricingLocation } from "@/lib/industryScannerSettings";
import { lpBreakdownLines, lpMethodsWithValues, type LPValueMethod } from "@/lib/lpBreakdown";
import { computeLPBasket } from "@/lib/lpBasket";
import { useIndustrySharedPrefs } from "@/lib/useIndustrySharedPrefs";

// LP Store: every offer in one loyalty-point store, valued per LP each way it
// can be realised, and a basket that adds up what a set of redemptions costs
// and copies everything it needs to buy as one multibuy.

const PANEL = "rounded-sm border border-eve-border/60 bg-eve-panel/40";
const TH = "px-2 py-1.5 text-left font-medium";
const THR = "px-2 py-1.5 text-right font-medium";
const TD = "px-2 py-1.5";
const TDR = "px-2 py-1.5 text-right font-mono";
const BTN =
  "px-2.5 py-1 rounded-sm border border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80 transition-colors text-[11px] disabled:opacity-40 disabled:cursor-not-allowed";
const BTN_ACCENT =
  "px-3 py-1 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs disabled:opacity-40 disabled:cursor-not-allowed";
const INPUT = "h-7 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono";

const PREFS_KEY = "eve-flipper:lp-store:prefs:v1";
// Bump the version whenever LPOfferRow gains or changes a field: a session
// restored from an older version would hand the table rows missing it.
const STATE_KEY = "eve-flipper:lp-store:state:v2";

type Filter = "all" | "sellable" | "blueprints";

interface Prefs {
  corporationID: number;
  filter: Filter;
  minISKPerLP: number | null;
  /** Hides offers whose item (or a blueprint's product) trades less than this per day. */
  minVolume: number | null;
  typedLP: number | null;
  includeBuild: boolean;
  sortKey: SortKey;
  sortDir: "asc" | "desc";
}

interface SessionState {
  corporationID: number;
  rows: LPOfferRow[];
  selection: [number, number][];
  warnings: string[];
}

const DEFAULT_PREFS: Prefs = { corporationID: 1000180, filter: "all", minISKPerLP: null, minVolume: null, typedLP: null, includeBuild: false, sortKey: "best", sortDir: "desc" };

function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    return raw ? { ...DEFAULT_PREFS, ...(JSON.parse(raw) as Partial<Prefs>) } : DEFAULT_PREFS;
  } catch {
    return DEFAULT_PREFS;
  }
}

function savePrefs(p: Prefs) {
  try {
    localStorage.setItem(PREFS_KEY, JSON.stringify(p));
  } catch {
    /* storage unavailable: the prefs just do not stick */
  }
}

function loadSession(): SessionState | null {
  try {
    const raw = sessionStorage.getItem(STATE_KEY);
    return raw ? (JSON.parse(raw) as SessionState) : null;
  } catch {
    return null;
  }
}

function saveSession(s: SessionState) {
  try {
    sessionStorage.setItem(STATE_KEY, JSON.stringify(s));
  } catch {
    /* a big store can exceed the quota; losing the cache only costs a re-scan */
  }
}

const METHOD_LABEL: Record<Exclude<LPMethod, "">, TranslationKey> = {
  sell: "lpMethodSell",
  list: "lpMethodList",
  sell_bpc: "lpMethodSellBPC",
  build_sell: "lpMethodBuildSell",
  build_list: "lpMethodBuildList",
};

function formatPerLP(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return "—";
  return Math.round(v).toLocaleString();
}

function formatUnits(v: number): string {
  if (!Number.isFinite(v)) return "—";
  if (v >= 100) return Math.round(v).toLocaleString();
  return v.toLocaleString(undefined, { maximumFractionDigits: 1 });
}

type SortKey =
  | "offer"
  | "category"
  | "lp"
  | "cost"
  | "instant"
  | "listed"
  | "bpc"
  | "build_instant"
  | "build_listed"
  | "best"
  | "volume";

// Text columns sort A-Z first; number columns biggest first, since the
// question they answer is "which is highest".
const TEXT_SORTS: SortKey[] = ["offer", "category"];

function sortValue(r: LPOfferRow, key: SortKey): number | string | null {
  switch (key) {
    case "offer":
      return r.type_name.toLowerCase();
    case "category":
      // Category first, then group, so implants cluster and within them the
      // same kind of implant sits together.
      return r.category ? `${r.category} ${r.group}`.toLowerCase() : null;
    case "lp":
      return r.lp_cost;
    case "cost":
      return r.unpriced ? null : r.cost;
    case "instant":
      return r.instant;
    case "listed":
      return r.listed;
    case "bpc":
      return r.bpc_sale;
    case "build_instant":
      return r.build_instant;
    case "build_listed":
      return r.build_listed;
    case "best":
      return r.unpriced ? null : r.best;
    case "volume":
      return r.avg_daily_volume > 0 ? r.avg_daily_volume : null;
  }
}

/** Everything a search matches: the name, the product, and what kind of thing it is. */
function searchText(r: LPOfferRow): string {
  return [r.type_name, r.product_name, r.category, r.group, ...(r.market_path ?? [])].join(" ").toLowerCase();
}

function requiredItemsMultibuy(r: LPOfferRow): string {
  return r.required_items.map((ri) => `${ri.type_name} ${ri.quantity}`).join("\n");
}

function buildMaterialsMultibuy(r: LPOfferRow): string {
  return (r.build_materials ?? []).map((m) => `${m.type_name} ${m.quantity}`).join("\n");
}

interface Props {
  isLoggedIn: boolean;
  onError?: (msg: string) => void;
}

export function LPStoreTab({ isLoggedIn, onError }: Props) {
  const { t } = useI18n();
  const [sharedPrefs] = useIndustrySharedPrefs();
  const [prefs, setPrefsState] = useState<Prefs>(loadPrefs);
  const setPrefs = useCallback((patch: Partial<Prefs>) => {
    setPrefsState((prev) => {
      const next = { ...prev, ...patch };
      savePrefs(next);
      return next;
    });
  }, []);

  const initial = useMemo(loadSession, []);
  const [rows, setRows] = useState<LPOfferRow[]>(initial?.corporationID === prefs.corporationID ? initial.rows : []);
  const [selection, setSelection] = useState<Map<number, number>>(
    new Map(initial?.corporationID === prefs.corporationID ? initial.selection : []),
  );
  const [warnings, setWarnings] = useState<string[]>(initial?.warnings ?? []);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState("");
  const [search, setSearch] = useState("");
  const [detail, setDetail] = useState<number | null>(null);
  const [notice, setNotice] = useState("");

  const [corporations, setCorporations] = useState<LPCorporation[]>([]);
  const [balances, setBalances] = useState<LPBalancesResponse | null>(null);
  const [overrides, setOverrides] = useState<Record<number, number>>({});
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    getLPCorporations().then(setCorporations).catch(() => setCorporations([]));
  }, []);

  useEffect(() => {
    if (!isLoggedIn) {
      setBalances({ available: false, reason: "not_logged_in" });
      setOverrides({});
      return;
    }
    getLPBalances()
      .then(setBalances)
      .catch(() => setBalances({ available: false, reason: "esi" }));
    getLPBPCPrices()
      .then(setOverrides)
      .catch(() => setOverrides({}));
  }, [isLoggedIn]);

  useEffect(() => {
    saveSession({ corporationID: prefs.corporationID, rows, selection: [...selection.entries()], warnings });
  }, [prefs.corporationID, rows, selection, warnings]);

  useEffect(() => () => abortRef.current?.abort(), []);

  // The picker: militias first, then every other store the character holds LP with.
  const storeOptions = useMemo(() => {
    const byID = new Map<number, { id: number; name: string; lp: number | null }>();
    for (const c of corporations) byID.set(c.corporation_id, { id: c.corporation_id, name: c.name, lp: null });
    if (balances?.available) {
      for (const b of balances.balances) {
        const cur = byID.get(b.corporation_id);
        byID.set(b.corporation_id, { id: b.corporation_id, name: cur?.name ?? b.name, lp: b.loyalty_points });
      }
    }
    if (!byID.has(prefs.corporationID)) {
      byID.set(prefs.corporationID, { id: prefs.corporationID, name: `Corporation ${prefs.corporationID}`, lp: null });
    }
    return [...byID.values()];
  }, [corporations, balances, prefs.corporationID]);

  const characterLP = useMemo(() => {
    if (!balances?.available) return null;
    return balances.balances.find((b) => b.corporation_id === prefs.corporationID)?.loyalty_points ?? 0;
  }, [balances, prefs.corporationID]);
  const lpBalance = characterLP ?? prefs.typedLP;

  const applyMessage = useCallback((msg: LPStreamMessage) => {
    switch (msg.type) {
      case "progress":
        setProgress(msg.message);
        break;
      case "offers":
        setRows(msg.rows);
        break;
      case "row":
        setRows((prev) => prev.map((r) => (r.offer_id === msg.row.offer_id ? msg.row : r)));
        break;
      case "warning":
        setWarnings((prev) => [...prev, msg.message]);
        break;
      case "done":
        setProgress("");
        break;
    }
  }, []);

  const analyze = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const pricing = loadScannerPricingLocation();
    setRunning(true);
    setWarnings([]);
    setNotice("");
    setProgress(t("lpStarting"));
    try {
      await analyzeLPStore(
        {
          corporation_id: prefs.corporationID,
          build_system_name: sharedPrefs.buildSystem,
          pricing_system_name: pricing.pricingSystem,
          pricing_station_id: pricing.pricingStationID,
          facility_tax: sharedPrefs.facilityTax,
          structure_bonus: sharedPrefs.structureBonus,
          broker_fee: sharedPrefs.brokerFee,
          sales_tax_percent: sharedPrefs.salesTaxPercent,
          structure_rig_type_ids: sharedPrefs.structureRigTypeIDs,
          structure_type_id: sharedPrefs.structureTypeID,
          structure_job_cost_reduction: sharedPrefs.structureJobCostReduction,
          skip_reactions: sharedPrefs.skipReactions,
          cost_model: sharedPrefs.costModel,
          build_mode: sharedPrefs.buildMode,
        },
        applyMessage,
        controller.signal,
      );
    } catch (e) {
      if (!controller.signal.aborted) onError?.(e instanceof Error ? e.message : String(e));
    } finally {
      if (abortRef.current === controller) {
        setRunning(false);
        setProgress("");
      }
    }
  }, [prefs.corporationID, sharedPrefs, applyMessage, onError, t]);

  const changeStore = (id: number) => {
    abortRef.current?.abort();
    setPrefs({ corporationID: id });
    setRows([]);
    setSelection(new Map());
    setWarnings([]);
    setDetail(null);
  };

  const saveOverride = useCallback(
    async (typeID: number, price: number | null) => {
      try {
        setOverrides(await setLPBPCPrice(typeID, price));
        // The table's values came from the old price; the next analysis uses the new one.
        setNotice(t("lpOverrideSavedReanalyze"));
      } catch (e) {
        onError?.(e instanceof Error ? e.message : String(e));
      }
    },
    [onError, t],
  );

  const visibleRows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return rows.filter((r) => {
      if (prefs.filter === "sellable" && r.is_blueprint) return false;
      if (prefs.filter === "blueprints" && !r.is_blueprint) return false;
      if (prefs.minISKPerLP != null && (r.best == null || r.best < prefs.minISKPerLP)) return false;
      if (prefs.minVolume != null && r.avg_daily_volume < prefs.minVolume) return false;
      if (q && !searchText(r).includes(q)) return false;
      return true;
    });
  }, [rows, prefs.filter, prefs.minISKPerLP, prefs.minVolume, search]);

  // Sorted by the chosen column. Rows with no value sort last whichever way
  // round, so an ascending sort does not open on a screen of dashes.
  const sortedRows = useMemo(() => {
    const dir = prefs.sortDir === "asc" ? 1 : -1;
    return [...visibleRows].sort((a, b) => {
      const va = sortValue(a, prefs.sortKey);
      const vb = sortValue(b, prefs.sortKey);
      if (va == null && vb == null) return a.type_name.localeCompare(b.type_name);
      if (va == null) return 1;
      if (vb == null) return -1;
      const cmp = typeof va === "string" ? va.localeCompare(vb as string) : va - (vb as number);
      return cmp * dir || a.type_name.localeCompare(b.type_name);
    });
  }, [visibleRows, prefs.sortKey, prefs.sortDir]);

  const toggleSort = (key: SortKey) => {
    if (prefs.sortKey === key) setPrefs({ sortDir: prefs.sortDir === "asc" ? "desc" : "asc" });
    else setPrefs({ sortKey: key, sortDir: TEXT_SORTS.includes(key) ? "asc" : "desc" });
  };

  const basket = useMemo(
    () => computeLPBasket(rows, selection, { includeBuild: prefs.includeBuild, balance: lpBalance }),
    [rows, selection, prefs.includeBuild, lpBalance],
  );

  const setCount = (offerID: number, count: number) =>
    setSelection((prev) => {
      const next = new Map(prev);
      if (count > 0) next.set(offerID, Math.floor(count));
      else next.delete(offerID);
      return next;
    });

  // The full worked sum, one line per step (see lib/lpBreakdown).
  const valueTitle = (r: LPOfferRow, v: number | null, method: LPValueMethod): string => {
    if (r.unpriced) return t("lpUnpricedHint");
    if (v == null) return t("lpTipNoValue");
    return lpBreakdownLines(r, method, t, formatISK).join("\n");
  };

  const valueCell = (r: LPOfferRow, v: number | null, pending: boolean, method: LPValueMethod) => {
    if (r.unpriced) return <span className="text-eve-warning" title={t("lpUnpricedHint")}>?</span>;
    if (v == null && pending && running) return <span className="text-eve-dim" title={t("lpTipPending")}>…</span>;
    const isBest = r.best_method === method;
    const tone = v != null && v < 0 ? "text-eve-error" : isBest ? "text-eve-accent" : "";
    return (
      <span className={`${tone} ${isBest ? "font-semibold" : ""}`} title={valueTitle(r, v, method)}>
        {formatPerLP(v)}
      </span>
    );
  };

  const costTitle = (r: LPOfferRow): string => {
    const lines = [t("lpTipCostISK", { isk: formatISK(r.isk_cost) })];
    for (const ri of r.required_items) {
      lines.push(
        ri.priced
          ? t("lpTipCostItem", { qty: ri.quantity, item: ri.type_name, each: formatISK(ri.unit_price), total: formatISK(ri.unit_price * ri.quantity) })
          : t("lpTipCostItemUnpriced", { qty: ri.quantity, item: ri.type_name }),
      );
    }
    lines.push(r.unpriced ? t("lpUnpricedHint") : t("lpTipCostTotal", { total: formatISK(r.cost) }));
    return lines.join("\n");
  };

  const volumeTitle = (r: LPOfferRow): string => {
    if (r.avg_daily_volume <= 0) return t("lpTipNoVolume");
    return t("lpTipVolume", {
      perDay: formatUnits(r.avg_daily_volume),
      units: r.units_per_redemption.toLocaleString(),
      days: formatUnits(r.units_per_redemption / r.avg_daily_volume),
    });
  };

  const sortHeader = (key: SortKey, label: string, right: boolean, hint?: string, unit?: string) => {
    const active = prefs.sortKey === key;
    return (
      <th className={right ? THR : TH} title={hint} aria-sort={active ? (prefs.sortDir === "asc" ? "ascending" : "descending") : "none"}>
        <button
          type="button"
          className={`uppercase tracking-wider hover:text-eve-text ${active ? "text-eve-accent" : ""}`}
          onClick={() => toggleSort(key)}
          title={t("lpTipSort")}
        >
          {label}
          {active && <span className="ml-0.5">{prefs.sortDir === "asc" ? "▲" : "▼"}</span>}
          {unit && <span className="block text-[9px] normal-case tracking-normal text-eve-dim">{unit}</span>}
        </button>
      </th>
    );
  };

  const offerRow = (r: LPOfferRow) => {
    const count = selection.get(r.offer_id) ?? 0;
    const buildPending = r.is_blueprint && !r.build_error;
    return (
      <Fragment key={r.offer_id}>
        <tr
          className={`border-b border-eve-border/40 cursor-pointer ${count > 0 ? "bg-eve-accent/10" : "hover:bg-eve-panel"}`}
          onClick={() => setDetail(detail === r.offer_id ? null : r.offer_id)}
        >
          <td className="px-2 py-1.5 w-6" onClick={(e) => e.stopPropagation()}>
            <input
              type="checkbox"
              checked={count > 0}
              onChange={(e) => setCount(r.offer_id, e.target.checked ? 1 : 0)}
              aria-label={t("lpSelectOffer", { item: r.type_name })}
              title={t("lpTipSelect")}
            />
          </td>
          <td className="px-1 py-1.5 w-14" onClick={(e) => e.stopPropagation()}>
            {count > 0 && (
              <CountInput
                count={count}
                label={t("lpRedeemCount", { item: r.type_name })}
                onChange={(n) => setCount(r.offer_id, n)}
              />
            )}
          </td>
          <td className={TD}>
            <div className="flex flex-col">
              <ItemRef typeId={r.type_id} name={r.type_name} market={!r.is_blueprint} copyName />
              <span className="text-[10px] text-eve-dim" title={t("lpTipOfferLabel")}>
                {offerLabel(r)}
              </span>
            </div>
          </td>
          <td className={TD} title={(r.market_path ?? []).join(" › ")}>
            {r.category ? (
              <div className="flex flex-col">
                <span className="text-eve-text">{r.category}</span>
                {r.group && <span className="text-[10px] text-eve-dim">{r.group}</span>}
              </div>
            ) : (
              <span className="text-eve-dim">—</span>
            )}
          </td>
          <td
            className={TDR}
            title={
              lpBalance != null && r.lp_cost > 0
                ? `${t("lpColLPHint")}\n${t("lpTipAffordable", { count: Math.floor(lpBalance / r.lp_cost).toLocaleString() })}`
                : t("lpColLPHint")
            }
          >
            {r.lp_cost.toLocaleString()}
          </td>
          <td className={TDR} title={costTitle(r)}>
            {r.unpriced ? "?" : formatISK(r.cost)}
          </td>
          <td className={TDR}>{r.is_blueprint ? <span title={t("lpTipNotSellable")}>—</span> : valueCell(r, r.instant, false, "sell")}</td>
          <td className={TDR}>{r.is_blueprint ? <span title={t("lpTipNotSellable")}>—</span> : valueCell(r, r.listed, false, "list")}</td>
          <td className={TDR}>
            {!r.is_blueprint ? (
              <span title={t("lpTipNotBlueprint")}>—</span>
            ) : r.bpc_sale == null && !running && !r.unpriced ? (
              <span className="text-eve-dim text-[10px]" title={bpcTitle(r)}>
                {t("lpNoContracts")}
              </span>
            ) : (
              valueCell(r, r.bpc_sale, true, "sell_bpc")
            )}
          </td>
          <td className={TDR}>
            {!r.is_blueprint ? (
              <span title={t("lpTipNotBlueprint")}>—</span>
            ) : r.build_error ? (
              <span className="text-eve-warning" title={t("lpBuildFailed", { error: r.build_error })}>!</span>
            ) : (
              valueCell(r, r.build_instant, buildPending, "build_sell")
            )}
          </td>
          <td className={TDR}>
            {!r.is_blueprint ? (
              <span title={t("lpTipNotBlueprint")}>—</span>
            ) : r.build_error ? (
              <span className="text-eve-warning" title={t("lpBuildFailed", { error: r.build_error })}>!</span>
            ) : (
              valueCell(r, r.build_listed, buildPending, "build_list")
            )}
          </td>
          <td className={TDR}>
            {r.best != null && r.best_method && !r.unpriced ? (
              <span title={`${t("lpTipBest", { method: t(METHOD_LABEL[r.best_method]) })}\n${valueTitle(r, r.best, r.best_method)}`}>
                <span className={`font-semibold ${r.best < 0 ? "text-eve-error" : "text-eve-accent"}`}>{formatPerLP(r.best)}</span>
                <span className="block text-[10px] text-eve-dim">
                  {t(METHOD_LABEL[r.best_method])} · {t("lpPerRedemption", { isk: formatISK(r.best * r.lp_cost) })}
                </span>
              </span>
            ) : r.unpriced ? (
              <span className="text-eve-warning" title={t("lpUnpricedHint")}>?</span>
            ) : running ? (
              <span className="text-eve-dim" title={t("lpTipPending")}>…</span>
            ) : (
              <span title={t("lpTipNoValue")}>—</span>
            )}
          </td>
          <td className={TDR} title={volumeTitle(r)}>
            {r.avg_daily_volume > 0 ? formatUnits(r.avg_daily_volume) : "—"}
          </td>
        </tr>
        {detail === r.offer_id && (
          <tr className="bg-eve-dark/80 border-b border-eve-border/40">
            <td colSpan={13} className="px-4 py-3">
              <OfferDetail
                row={r}
                isLoggedIn={isLoggedIn}
                override={overrides[r.type_id]}
                onSaveOverride={(price) => void saveOverride(r.type_id, price)}
              />
            </td>
          </tr>
        )}
      </Fragment>
    );
  };

  function offerLabel(r: LPOfferRow): string {
    const parts: string[] = [];
    if (r.is_blueprint) parts.push(r.runs === 1 ? t("lpOneRun") : t("lpRuns", { runs: r.runs }));
    else if (r.quantity > 1) parts.push(`× ${r.quantity.toLocaleString()}`);
    if (r.isk_cost > 0) parts.push(`${formatISK(r.isk_cost)} ISK`);
    for (const ri of r.required_items) parts.push(`${ri.quantity}× ${ri.type_name}`);
    return parts.join(" · ");
  }

  function bpcTitle(r: LPOfferRow): string {
    if (r.bpc_override) return t("lpBPCOverrideTitle", { price: formatISK(r.bpc_per_run) });
    if (r.bpc_samples > 0) return t("lpBPCSamplesTitle", { price: formatISK(r.bpc_per_run), count: r.bpc_samples });
    return t("lpNoContractsHint");
  }

  const selectedCount = [...selection.values()].filter((c) => c > 0).length;

  return (
    <div className="flex flex-col gap-2 p-2 min-h-0 flex-1 overflow-hidden">
      {/* Header: which store, how much LP, and where prices and build settings come from. */}
      <div className={`${PANEL} px-3 py-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs`}>
        <label className="flex items-center gap-2">
          <span className="text-eve-dim">{t("lpStore")}</span>
          <select
            title={t("lpTipStore")}
            className={`${INPUT} min-w-[14rem]`}
            value={prefs.corporationID}
            onChange={(e) => changeStore(Number(e.target.value))}
            disabled={running}
          >
            {storeOptions.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
                {o.lp != null ? ` — ${o.lp.toLocaleString()} LP` : ""}
              </option>
            ))}
          </select>
        </label>

        <label className="flex items-center gap-2">
          <span className="text-eve-dim">LP</span>
          {characterLP != null ? (
            <span className="font-mono text-eve-text" title={t("lpBalanceFromCharacter")}>
              {characterLP.toLocaleString()}
            </span>
          ) : (
            <input
              type="number"
              min={0}
              className={`${INPUT} w-28 text-right`}
              value={prefs.typedLP ?? ""}
              placeholder={t("lpTypeYourLP")}
              title={balanceHint(balances, t)}
              onChange={(e) => setPrefs({ typedLP: e.target.value === "" ? null : Number(e.target.value) })}
            />
          )}
        </label>

        <span className="text-[11px] text-eve-dim" title={t("lpSettingsFromScannerHint")}>
          {t("lpSettingsFromScanner", {
            pricing: loadScannerPricingLocation().pricingSystem,
            build: sharedPrefs.buildSystem || "—",
          })}
        </span>

        <span className="ml-auto flex items-center gap-2">
          {progress && <span className="text-[11px] text-eve-dim">{progress}</span>}
          {running ? (
            <button className={BTN} onClick={() => abortRef.current?.abort()} title={t("lpTipCancel")}>
              {t("lpCancel")}
            </button>
          ) : (
            <button className={BTN_ACCENT} onClick={() => void analyze()} title={t("lpTipAnalyze")}>
              {rows.length > 0 ? t("lpReanalyze") : t("lpAnalyze")}
            </button>
          )}
        </span>
      </div>

      {notice && <div className="px-1 text-[11px] text-eve-accent">{notice}</div>}

      {warnings.length > 0 && (
        <div className="rounded-sm border border-eve-warning/40 bg-eve-warning/5 px-3 py-2 text-[11px] text-eve-warning space-y-0.5">
          {warnings.map((w, i) => (
            <div key={i}>{w}</div>
          ))}
        </div>
      )}

      {/* The tally. Always mounted, so nothing jumps under the cursor when the first box is ticked. */}
      <div className={`${PANEL} px-3 py-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs`}>
        <span className="text-eve-dim" title={t("lpTipSelectedOffers")}>
          {t("lpSelectedOffers", { count: selectedCount })}
        </span>
        <span title={t("lpTipTallyLP")}>
          <span className="text-eve-dim">LP </span>
          <span className={`font-mono ${basket.overBalance ? "text-eve-error font-semibold" : "text-eve-text"}`}>
            {basket.lp.toLocaleString()}
            {lpBalance != null && <span className="text-eve-dim"> / {lpBalance.toLocaleString()}</span>}
          </span>
        </span>
        <span title={t("lpTipISKNeeded")}>
          <span className="text-eve-dim">{t("lpISKNeeded")} </span>
          <span className="font-mono text-eve-text">{formatISK(basket.isk)}</span>
        </span>
        <span title={t("lpTipExpectedProfit")}>
          <span className="text-eve-dim">{t("lpExpectedProfit")} </span>
          <span className={`font-mono ${basket.profit < 0 ? "text-eve-error" : "text-eve-profit"}`}>{formatISK(basket.profit)}</span>
          {basket.lp > 0 && <span className="text-eve-dim"> ({formatPerLP(basket.blendedISKPerLP)} ISK/LP)</span>}
        </span>
        {basket.unvalued > 0 && (
          <span className="text-eve-warning" title={t("lpUnvaluedHint")}>
            {t("lpUnvalued", { count: basket.unvalued })}
          </span>
        )}
        <span className="ml-auto flex items-center gap-2">
          <label className="flex items-center gap-1 text-[11px] text-eve-dim" title={t("lpIncludeBuildHint")}>
            <input
              type="checkbox"
              checked={prefs.includeBuild}
              onChange={(e) => setPrefs({ includeBuild: e.target.checked })}
            />
            {t("lpIncludeBuild")}
          </label>
          <span className="text-[11px] text-eve-dim" title={t("lpTipCopyMultibuy")}>
            {t("lpCopyMultibuy")}
          </span>
          <CopyButton text={basket.multibuy} label={t("lpCopyMultibuy")} disabled={!basket.multibuy} />
          <button className={BTN} disabled={selectedCount === 0} onClick={() => setSelection(new Map())} title={t("lpTipClear")}>
            {t("lpClearSelection")}
          </button>
        </span>
        {basket.warnings.length > 0 && (
          <div className="w-full text-[11px] text-eve-warning">
            {basket.warnings.map((w) => (
              <div key={w.offer_id}>
                {w.days == null
                  ? t("lpNoVolumeWarning", { item: w.name })
                  : t("lpLiquidityWarning", { item: w.name, days: Math.round(w.days) })}
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2 text-xs">
        {(["all", "sellable", "blueprints"] as Filter[]).map((f) => (
          <button
            key={f}
            onClick={() => setPrefs({ filter: f })}
            title={t(f === "all" ? "lpTipFilterAll" : f === "sellable" ? "lpTipFilterSellable" : "lpTipFilterBlueprints")}
            className={`px-2 py-0.5 rounded-sm border text-[11px] ${
              prefs.filter === f ? "border-eve-accent text-eve-accent bg-eve-accent/10" : "border-eve-border text-eve-dim hover:text-eve-text"
            }`}
          >
            {t(f === "all" ? "lpFilterAll" : f === "sellable" ? "lpFilterSellable" : "lpFilterBlueprints")}
          </button>
        ))}
        <label className="flex items-center gap-1 text-eve-dim" title={t("lpTipMinISKPerLP")}>
          {t("lpMinISKPerLP")}
          <input
            type="number"
            className={`${INPUT} w-20 text-right`}
            value={prefs.minISKPerLP ?? ""}
            onChange={(e) => setPrefs({ minISKPerLP: e.target.value === "" ? null : Number(e.target.value) })}
          />
        </label>
        <label className="flex items-center gap-1 text-eve-dim" title={t("lpMinVolumeHint")}>
          {t("lpMinVolume")}
          <input
            type="number"
            min={0}
            className={`${INPUT} w-20 text-right`}
            value={prefs.minVolume ?? ""}
            onChange={(e) => setPrefs({ minVolume: e.target.value === "" ? null : Number(e.target.value) })}
          />
        </label>
        <input
          className={`${INPUT} w-48`}
          placeholder={t("lpSearch")}
          title={t("lpTipSearch")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <span className="text-[11px] text-eve-dim ml-auto">
          {t("lpOfferCount", { shown: visibleRows.length, total: rows.length })}
        </span>
      </div>

      <div className={`${PANEL} overflow-auto flex-1 min-h-0`}>
        {rows.length === 0 ? (
          <div className="px-3 py-8 text-center text-xs text-eve-dim">{running ? progress || t("lpStarting") : t("lpEmpty")}</div>
        ) : (
          <table className="w-full text-xs">
            <thead className="sticky top-0 bg-eve-dark z-10">
              <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
                <th className="px-2 py-1.5 w-6"></th>
                <th className="px-1 py-1.5 w-14"></th>
                {sortHeader("offer", t("lpColItem"), false, t("lpColItemHint"))}
                {sortHeader("category", t("lpColCategory"), false, t("lpColCategoryHint"))}
                {sortHeader("lp", "LP", true, t("lpColLPHint"))}
                {sortHeader("cost", t("lpColCost"), true, t("lpColCostHint"))}
                {sortHeader("instant", t("lpColInstant"), true, t("lpColInstantHint"), "ISK/LP")}
                {sortHeader("listed", t("lpColListed"), true, t("lpColListedHint"), "ISK/LP")}
                {sortHeader("bpc", t("lpColBPC"), true, t("lpColBPCHint"), "ISK/LP")}
                {sortHeader("build_instant", t("lpColBuildInstant"), true, t("lpColBuildInstantHint"), "ISK/LP")}
                {sortHeader("build_listed", t("lpColBuildListed"), true, t("lpColBuildListedHint"), "ISK/LP")}
                {sortHeader("best", t("lpColBest"), true, t("lpColBestHint"), "ISK/LP")}
                {sortHeader("volume", t("lpColVolume"), true, t("lpColVolumeHint"), t("lpUnitsPerDay"))}
              </tr>
            </thead>
            <tbody>
              {sortedRows.map((r) => offerRow(r))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// The redemption count keeps its own draft: clearing the box to type a new
// number must not read as 0, which would deselect the row and take the box
// away mid-edit. Every valid number updates the tally as it is typed.
function CountInput({ count, label, onChange }: { count: number; label: string; onChange: (n: number) => void }) {
  const [draft, setDraft] = useState(String(count));
  useEffect(() => setDraft(String(count)), [count]);
  return (
    <input
      type="number"
      min={1}
      step={1}
      className={`${INPUT} w-14 text-right`}
      value={draft}
      aria-label={label}
      title={label}
      onChange={(e) => {
        setDraft(e.target.value);
        const n = Math.floor(Number(e.target.value));
        if (e.target.value.trim() !== "" && Number.isFinite(n) && n >= 1) onChange(n);
      }}
      onBlur={() => setDraft(String(count))}
    />
  );
}

// The worked sum for each way this offer can be realised, best first, one
// tab each -- the same lines the table's tooltips show, easier to read here.
function BreakdownPanel({ row }: { row: LPOfferRow }) {
  const { t } = useI18n();
  const methods = lpMethodsWithValues(row);
  const [method, setMethod] = useState<LPValueMethod | null>(methods[0] ?? null);
  const active = method && methods.includes(method) ? method : (methods[0] ?? null);
  if (!active) return null;
  return (
    <div className="space-y-1 md:col-span-2">
      <div className="flex items-center gap-2">
        <span className="text-eve-dim uppercase tracking-wider text-[10px]">{t("lpDetailBreakdown")}</span>
        {methods.map((m) => (
          <button
            key={m}
            type="button"
            onClick={() => setMethod(m)}
            title={t("lpTipBreakdownTab")}
            className={`px-1.5 py-0.5 rounded-sm border text-[10px] ${
              m === active ? "border-eve-accent text-eve-accent bg-eve-accent/10" : "border-eve-border text-eve-dim hover:text-eve-text"
            }`}
          >
            {t(METHOD_LABEL[m])}
          </button>
        ))}
      </div>
      <div className="font-mono text-[11px] leading-5 whitespace-pre">
        {lpBreakdownLines(row, active, t, formatISK).map((line, i) => (
          <div key={i}>{line}</div>
        ))}
      </div>
    </div>
  );
}

function balanceHint(b: LPBalancesResponse | null, t: ReturnType<typeof useI18n>["t"]): string {
  if (!b || b.available) return "";
  if (b.reason === "missing_scope") return t("lpBalanceMissingScope");
  if (b.reason === "not_logged_in") return t("lpBalanceLoggedOut");
  return t("lpBalanceUnavailable");
}

function OfferDetail({
  row,
  isLoggedIn,
  override,
  onSaveOverride,
}: {
  row: LPOfferRow;
  isLoggedIn: boolean;
  override: number | undefined;
  onSaveOverride: (price: number | null) => void;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(override != null ? String(override) : "");
  useEffect(() => setDraft(override != null ? String(override) : ""), [override]);

  const reqMultibuy = requiredItemsMultibuy(row);
  const buildMultibuy = buildMaterialsMultibuy(row);

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-x-8 gap-y-3 text-[11px]">
      <div className="space-y-1">
        <div className="text-eve-dim uppercase tracking-wider text-[10px]">{t("lpDetailCost")}</div>
        <div className="flex justify-between">
          <span>ISK</span>
          <span className="font-mono">{formatISK(row.isk_cost)}</span>
        </div>
        {row.required_items.map((ri) => (
          <div key={ri.type_id} className="flex justify-between gap-4">
            <span>
              {ri.quantity}× {ri.type_name}
            </span>
            <span className={`font-mono ${ri.priced ? "" : "text-eve-warning"}`}>
              {ri.priced ? `${formatISK(ri.unit_price)} → ${formatISK(ri.unit_price * ri.quantity)}` : t("lpNotForSale")}
            </span>
          </div>
        ))}
        <div className="flex justify-between border-t border-eve-border/40 pt-1">
          <span className="text-eve-dim">{t("lpDetailTotal")}</span>
          <span className="font-mono">{row.unpriced ? "?" : formatISK(row.cost)}</span>
        </div>
        {reqMultibuy && (
          <div className="flex items-center gap-2 pt-1">
            <CopyButton text={reqMultibuy} label={t("lpCopyRequiredItems")} />
            <span className="text-eve-dim">{t("lpCopyRequiredItems")}</span>
          </div>
        )}
      </div>

      <div className="space-y-1">
        <div className="text-eve-dim uppercase tracking-wider text-[10px]">
          {row.is_blueprint ? t("lpDetailProductMarket", { product: row.product_name }) : t("lpDetailMarket")}
        </div>
        <div className="flex justify-between">
          <span title={t("lpTipDetailBid")}>{t("lpDetailBid")}</span>
          <span className="font-mono">{row.unit_bid > 0 ? formatISK(row.unit_bid) : "—"}</span>
        </div>
        <div className="flex justify-between">
          <span title={t("lpTipDetailAsk")}>{t("lpDetailAsk")}</span>
          <span className="font-mono">{row.unit_ask > 0 ? formatISK(row.unit_ask) : "—"}</span>
        </div>
        <div className="flex justify-between">
          <span title={t("lpColVolumeHint")}>{t("lpDetailVolume")}</span>
          <span className="font-mono">
            {row.avg_daily_volume > 0 ? formatUnits(row.avg_daily_volume) : "—"}
            <span className="text-eve-dim"> · {t("lpDetailUnitsPerRedemption", { units: row.units_per_redemption })}</span>
          </span>
        </div>
        {row.is_blueprint && row.build_cost > 0 && (
          <div className="flex justify-between">
            <span title={t("lpTipDetailBuildCost")}>{t("lpDetailBuildCost")}</span>
            <span className="font-mono">
              {formatISK(row.build_cost)}
              <span className="text-eve-dim"> ({t("lpDetailJobCost", { cost: formatISK(row.build_job_cost) })})</span>
            </span>
          </div>
        )}
      </div>

      <BreakdownPanel row={row} />

      {row.is_blueprint && (
        <div className="space-y-1">
          <div className="text-eve-dim uppercase tracking-wider text-[10px]">{t("lpDetailBlueprint")}</div>
          <div>{t("lpDetailBlueprintRuns", { runs: row.runs, product: row.product_name })}</div>
          <div>
            {row.bpc_override
              ? t("lpBPCOverrideTitle", { price: formatISK(row.bpc_per_run) })
              : row.bpc_samples > 0
                ? t("lpBPCSamplesTitle", { price: formatISK(row.bpc_per_run), count: row.bpc_samples })
                : t("lpNoContractsHint")}
          </div>
          {isLoggedIn ? (
            <div className="flex items-center gap-2">
              <span className="text-eve-dim" title={t("lpTipYourPrice")}>
                {t("lpYourPricePerRun")}
              </span>
              <input
                type="number"
                min={0}
                className={`${INPUT} w-32 text-right`}
                value={draft}
                placeholder="—"
                onChange={(e) => setDraft(e.target.value)}
              />
              <button
                className={BTN}
                onClick={() => {
                  const v = Number(draft);
                  onSaveOverride(draft.trim() === "" || !Number.isFinite(v) || v <= 0 ? null : v);
                }}
              >
                {t("lpSave")}
              </button>
              {override != null && (
                <button className={BTN} onClick={() => onSaveOverride(null)}>
                  {t("lpClear")}
                </button>
              )}
            </div>
          ) : (
            <div className="text-eve-dim">{t("lpOverrideNeedsLogin")}</div>
          )}
          {row.build_error && <div className="text-eve-warning">{t("lpBuildFailed", { error: row.build_error })}</div>}
          {buildMultibuy && (
            <div className="flex items-center gap-2 pt-1">
              <CopyButton text={buildMultibuy} label={t("lpCopyBuildMaterials")} />
              <span className="text-eve-dim">{t("lpCopyBuildMaterials")}</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
