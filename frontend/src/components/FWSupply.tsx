import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CopyButton } from "@/components/ui/CopyButton";
import { CopyPrice } from "@/components/ui/CopyPrice";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ItemRef } from "@/components/ui/ItemRef";
import { LoadingBlock } from "@/components/ui/LoadingBlock";
import {
  applyFWCampaignOrders,
  bulkFWLots,
  createFWCampaign,
  deleteFWCampaign,
  deleteFWLot,
  generateFWPlan,
  getAuthStatus,
  getFWCampaign,
  getFWCampaignOrders,
  getFWPlan,
  listFWCampaigns,
  saveFWLot,
  setFWLotState,
  updateFWCampaign,
  type FWCampaign,
  type FWCampaignPatch,
  type FWLot,
  type FWOrdersResponse,
  type FWPlanResponse,
  type FWShipmentLine,
  type FWStagingCandidate,
  type FWSupplyRow,
} from "@/lib/api";
import { formatISK, formatMargin } from "@/lib/format";
import { sortGapRows, type GapSortKey } from "@/lib/fwGapSort";
import { useI18n } from "@/lib/i18n";
import { formatGridPrice, priceStep } from "@/lib/pricing";
import type { AuthCharacter } from "@/lib/types";

/* FWSupply — buy in a trade hub, sell in a faction warfare hub.
 *
 * The tab renders four views over one campaign: the staging ring (where to
 * sell), the gap table (what is missing and what of it is being shipped), the
 * lots (what the campaign has actually spent) and the orders (what the market
 * did with it).
 *
 * There was a fifth. A shipment line is a gap row plus a shipped quantity — the
 * Go type embeds FWSupplyRow — so the shipping list was the gap table with
 * fewer columns and a filter, and keeping the two apart meant the rows the
 * budget left behind were on a different screen from the rows it funded. They
 * are the same question, so they are one table.
 *
 * Two rules from the backend show through the UI and are worth stating here,
 * because rendering them wrong is how they get lost:
 *
 * A warning is not an absence. Every panel that can be built from a partial
 * fetch renders the warnings above it rather than beside it, because an
 * unread market book looks exactly like an empty one — every item a gap with
 * no competition, which is precisely the shape of the opportunity this tool
 * exists to find.
 *
 * The seller's panel stands alone. Nothing in it needs the buying character's
 * session: prices come from the public book and quantities from the plan. The
 * whole point of the split is that the Jita alt never has to travel or log in
 * for the daily relist round.
 */

interface Props {
  isLoggedIn: boolean;
  onError?: (msg: string) => void;
}

type View = "ring" | "gaps" | "lots" | "orders";

/* What the gap table lists. `shipping` is the buy list, `actionable` every
 * gap and thin row with the shipped ones marked, `all` the covered rows too.
 *
 * The default is `actionable` rather than `shipping`, because the rows the
 * budget could not fund are the ones worth seeing: a list that shows only what
 * it bought cannot tell you what it left. */
type GapsShow = "shipping" | "actionable" | "all";

const MILITIAS: { id: number; name: string }[] = [
  { id: 500001, name: "Caldari State" },
  { id: 500002, name: "Minmatar Republic" },
  { id: 500003, name: "Amarr Empire" },
  { id: 500004, name: "Gallente Federation" },
];

const SHIP_PROFILES = ["fast_frigate", "sunesis", "blockade_runner", "deep_space_transport", "freighter"];

const LOT_STATES = ["planned", "bought", "in_transit", "at_dest", "listed", "sold", "pulled"];

// The minimum-lot floor by unit price, mirrored from FWMinLotBands
// (fw_supply.go) -- keep the two in step if the bands ever change. The engine
// uses these to stretch a *sized* gap or thin row; here they cap a manual
// quantity's starting guess for a row the engine never sized at all, so a
// checked "covered" row with a real price above it opens with a lot-sized
// number instead of an empty box.
const FW_MIN_LOT_BANDS: { underISK: number; floor: number }[] = [
  { underISK: 100_000, floor: 50 },
  { underISK: 1_000_000, floor: 30 },
  { underISK: 1_500_000, floor: 15 },
  { underISK: 5_000_000, floor: 5 },
];

function fwFloorForPrice(price: number): number | null {
  for (const band of FW_MIN_LOT_BANDS) {
    if (price < band.underISK) return band.floor;
  }
  return null;
}

// The handoff, in order. A lot at the end of it has nowhere to advance to by
// button: `sold` and `pulled` are outcomes, and both are chosen explicitly.
const NEXT_STATE: Record<string, string> = {
  planned: "bought",
  bought: "in_transit",
  in_transit: "at_dest",
  at_dest: "listed",
};

const CEILING_CATEGORIES = ["ship", "module", "ammo", "drone", "consumable"];

function militiaName(id: number): string {
  return MILITIAS.find((m) => m.id === id)?.name ?? `faction ${id}`;
}

// Prices in a thin book are routinely three digits, and the 0.01 ISK undercut
// is the whole trade — so anything under 1000 keeps both decimals.
function formatPrice(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value) || value === 0) return "—";
  if (Math.abs(value) >= 1000) return formatISK(value);
  return value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function formatQty(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  return Math.round(value).toLocaleString();
}

function formatRate(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value >= 100) return Math.round(value).toLocaleString();
  if (value >= 10) return value.toFixed(0);
  return value.toFixed(1);
}

// Cover is the ranking number, and its top end is not interesting: a station
// holding four years of a type is as covered as one holding one.
function formatCover(days: number | null | undefined): string {
  if (days == null || !Number.isFinite(days)) return "∞";
  if (days >= 365) return "365+";
  if (days >= 10) return days.toFixed(0);
  return days.toFixed(1);
}

function formatMarkup(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value) || value <= 0) return "—";
  return `${value.toFixed(2)}×`;
}

/** A margin of zero is what an unpriced row reports, and it is not 0.0%. */
function formatPct(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value) || value === 0) return "\u2014";
  return formatMargin(value);
}

function formatM3(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value >= 1000) return `${Math.round(value).toLocaleString()} m³`;
  return `${value.toFixed(1)} m³`;
}

function formatSecurity(sec: number): string {
  return sec.toFixed(1);
}

function securityTone(sec: number): string {
  if (sec >= 0.5) return "text-eve-success";
  if (sec > 0) return "text-eve-warning";
  return "text-eve-danger";
}

function verdictTone(verdict: string): string {
  switch (verdict) {
    case "gap":
      return "text-eve-success";
    case "thin":
      return "text-eve-warning";
    case "unpriceable":
      return "text-eve-danger";
    default:
      return "text-eve-dim";
  }
}

function dangerTone(verdict: string | undefined): string {
  switch (verdict) {
    case "red":
      return "text-eve-danger";
    case "yellow":
      return "text-eve-warning";
    case "green":
      return "text-eve-success";
    default:
      return "text-eve-dim";
  }
}

function ageLabel(seconds: number | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return "—";
  if (seconds < 90) return `${Math.round(seconds)}s`;
  if (seconds < 5400) return `${Math.round(seconds / 60)}m`;
  if (seconds < 172800) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

const PANEL = "rounded-sm border border-eve-border/60 bg-eve-panel/40";
const TH = "px-3 py-1.5 text-left font-medium";
const THR = "px-3 py-1.5 text-right font-medium";
const TD = "px-3 py-1.5";
const TDR = "px-3 py-1.5 text-right font-mono";
const BTN =
  "px-2.5 py-1 rounded-sm border border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80 transition-colors text-[11px] disabled:opacity-40 disabled:cursor-not-allowed";
const BTN_ACCENT =
  "px-3 py-1 rounded-sm border border-eve-accent/60 text-eve-accent hover:bg-eve-accent/10 transition-colors text-xs disabled:opacity-40 disabled:cursor-not-allowed";
const INPUT = "h-7 px-2 rounded-sm border border-eve-border bg-eve-input text-eve-text text-xs font-mono";

/** WarningList renders what the tool could not see. Above the data, never
 *  beside it — a missing book is the one failure that looks like a result. */
function WarningList({ title, items, tone = "warning" }: { title: string; items: string[]; tone?: "warning" | "danger" }) {
  if (items.length === 0) return null;
  const border = tone === "danger" ? "border-eve-danger/50" : "border-eve-warning/50";
  const text = tone === "danger" ? "text-eve-danger" : "text-eve-warning";
  return (
    <div className={`rounded-sm border ${border} bg-eve-panel/40 px-3 py-2`}>
      <div className={`text-[11px] uppercase tracking-wider ${text}`}>{title}</div>
      <ul className="mt-1 space-y-0.5">
        {items.map((w, i) => (
          <li key={i} className="text-[11px] text-eve-text leading-relaxed">
            • {w}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return (
    <div className="px-3 py-2 rounded-sm border border-eve-border/60 bg-eve-panel/60 min-w-[7rem]">
      <div className="text-[10px] uppercase tracking-wider text-eve-dim">{label}</div>
      <div className={`text-sm font-mono ${tone ?? "text-eve-text"}`}>{value}</div>
    </div>
  );
}

export function FWSupply({ isLoggedIn, onError }: Props) {
  const { t } = useI18n();

  const [campaigns, setCampaigns] = useState<FWCampaign[]>([]);
  const [campaign, setCampaign] = useState<FWCampaign | null>(null);
  const [planResp, setPlanResp] = useState<FWPlanResponse | null>(null);
  const [orders, setOrders] = useState<FWOrdersResponse | null>(null);

  const [view, setView] = useState<View>("ring");
  const [loading, setLoading] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [ordersLoading, setOrdersLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [showSettings, setShowSettings] = useState(false);
  const [draft, setDraft] = useState<FWCampaignPatch>({});
  // For the "sell under" picker -- fees follow whoever lists, so the campaign
  // names one of the user's own linked characters rather than the active
  // session character, since a campaign's seller need not be who is logged in
  // right now.
  const [characters, setCharacters] = useState<AuthCharacter[]>([]);
  const [gapsShow, setGapsShow] = useState<GapsShow>("actionable");
  const [confirmDeleteCampaign, setConfirmDeleteCampaign] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);

  const onErrorRef = useRef(onError);
  onErrorRef.current = onError;

  const fail = useCallback((e: unknown, fallback: string) => {
    const msg = e instanceof Error ? e.message : fallback;
    setError(msg);
    onErrorRef.current?.(msg);
  }, []);

  const plan = planResp?.has_plan ? planResp.plan : undefined;

  const loadCampaigns = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await listFWCampaigns();
      setCampaigns(list);
      return list;
    } catch (e) {
      fail(e, "Could not load campaigns");
      return [];
    } finally {
      setLoading(false);
    }
  }, [fail]);

  // Selecting a campaign reads its record and its cached plan. Never the
  // generator: that walks a week of killmails, and opening a tab is not a
  // request to do so.
  const selectCampaign = useCallback(
    async (id: number) => {
      setLoading(true);
      setError(null);
      setOrders(null);
      setDraft({});
      try {
        const [detail, cached] = await Promise.all([getFWCampaign(id), getFWPlan(id)]);
        setCampaign(detail);
        setPlanResp(cached);
      } catch (e) {
        fail(e, "Could not load the campaign");
      } finally {
        setLoading(false);
      }
    },
    [fail],
  );

  useEffect(() => {
    if (!isLoggedIn) return;
    let cancelled = false;
    void (async () => {
      const list = await loadCampaigns();
      if (cancelled || list.length === 0) return;
      await selectCampaign(list[0].campaign_id);
    })();
    return () => {
      cancelled = true;
    };
  }, [isLoggedIn, loadCampaigns, selectCampaign]);

  useEffect(() => {
    if (!isLoggedIn) return;
    void getAuthStatus()
      .then((s) => setCharacters(s.characters ?? []))
      .catch(() => setCharacters([]));
  }, [isLoggedIn]);

  const patchCampaign = useCallback(
    async (patch: FWCampaignPatch) => {
      if (!campaign) return;
      setBusy(true);
      setError(null);
      try {
        const updated = await updateFWCampaign(campaign.campaign_id, patch);
        setCampaign(updated);
        setCampaigns((prev) => prev.map((c) => (c.campaign_id === updated.campaign_id ? { ...c, ...updated } : c)));
        // A patch that changes the premise drops the cached plan server-side,
        // so the tab re-reads rather than showing a table measured against a
        // station or a cover target the campaign has left.
        setPlanResp(await getFWPlan(updated.campaign_id));
      } catch (e) {
        fail(e, "Could not save the campaign");
      } finally {
        setBusy(false);
      }
    },
    [campaign, fail],
  );

  // The one handler behind every per-item override, row buttons and settings
  // chips alike: a type cannot be both included and excluded, so setting one
  // clears it from the other list in the same patch rather than two requests
  // that could land out of order.
  const toggleTypeOverride = useCallback(
    async (typeId: number, typeName: string, kind: "include" | "exclude") => {
      if (!campaign) return;
      const included = campaign.included_types ?? [];
      const excluded = campaign.excluded_types ?? [];
      if (kind === "include") {
        const already = included.some((o) => o.type_id === typeId);
        await patchCampaign({
          included_types: already
            ? included.filter((o) => o.type_id !== typeId)
            : [...included, { type_id: typeId, type_name: typeName }],
          excluded_types: excluded.filter((o) => o.type_id !== typeId),
        });
      } else {
        const already = excluded.some((o) => o.type_id === typeId);
        await patchCampaign({
          excluded_types: already
            ? excluded.filter((o) => o.type_id !== typeId)
            : [...excluded, { type_id: typeId, type_name: typeName }],
          included_types: included.filter((o) => o.type_id !== typeId),
        });
      }
    },
    [campaign, patchCampaign],
  );

  const handleGenerate = useCallback(async () => {
    if (!campaign) return;
    setGenerating(true);
    setError(null);
    try {
      setPlanResp(await generateFWPlan(campaign.campaign_id));
      setCampaign(await getFWCampaign(campaign.campaign_id));
    } catch (e) {
      fail(e, "Could not generate the plan");
    } finally {
      setGenerating(false);
    }
  }, [campaign, fail]);

  // The drawer's version of the toggle: patch, then actually regenerate rather
  // than read the cache patchCampaign already refreshed. A stored preference
  // that does not reprice until some later, unprompted Regenerate click is
  // indistinguishable from a toggle that did nothing -- this is the fix for
  // that. It is safe to make automatic because the demand fetch it triggers is
  // skipped whenever its cache is fresh (fw_demand.go), so in the common case
  // this costs a market refresh, not a killmail walk.
  const toggleTypeOverrideAndRegenerate = useCallback(
    async (typeId: number, typeName: string, kind: "include" | "exclude") => {
      await toggleTypeOverride(typeId, typeName, kind);
      await handleGenerate();
    },
    [toggleTypeOverride, handleGenerate],
  );

  const handleCreate = useCallback(
    async (militia: number) => {
      setBusy(true);
      setError(null);
      try {
        const created = await createFWCampaign({ militia_faction_id: militia });
        await loadCampaigns();
        await selectCampaign(created.campaign_id);
      } catch (e) {
        fail(e, "Could not create the campaign");
      } finally {
        setBusy(false);
      }
    },
    [fail, loadCampaigns, selectCampaign],
  );

  // The server refuses an unconfirmed delete, and it is right to: the lots are
  // the only record of what was bought and what it cost. So this runs only from
  // the dialog below, which names the campaign and how many lots go with it.
  const handleDeleteCampaign = useCallback(async () => {
    if (!campaign) return;
    setConfirmDeleteCampaign(false);
    setBusy(true);
    try {
      await deleteFWCampaign(campaign.campaign_id);
      setCampaign(null);
      setPlanResp(null);
      setOrders(null);
      const list = await loadCampaigns();
      if (list.length > 0) await selectCampaign(list[0].campaign_id);
    } catch (e) {
      fail(e, "Could not delete the campaign");
    } finally {
      setBusy(false);
    }
  }, [campaign, fail, loadCampaigns, selectCampaign]);

  const loadOrders = useCallback(async () => {
    if (!campaign) return;
    setOrdersLoading(true);
    setError(null);
    try {
      setOrders(await getFWCampaignOrders(campaign.campaign_id));
    } catch (e) {
      fail(e, "Could not read the order book");
    } finally {
      setOrdersLoading(false);
    }
  }, [campaign, fail]);

  const handleApply = useCallback(
    async (lotIDs?: number[]) => {
      if (!campaign) return;
      setBusy(true);
      try {
        const result = await applyFWCampaignOrders(campaign.campaign_id, lotIDs);
        setCampaign((prev) => (prev ? { ...prev, lots: result.lots, budget: result.budget } : prev));
        await loadOrders();
      } catch (e) {
        fail(e, "Could not apply the fills");
      } finally {
        setBusy(false);
      }
    },
    [campaign, fail, loadOrders],
  );

  const handleLotState = useCallback(
    async (lotID: number, state: string) => {
      if (!campaign) return;
      setBusy(true);
      try {
        setCampaign(await setFWLotState(campaign.campaign_id, lotID, state));
      } catch (e) {
        fail(e, "Could not advance the lot");
      } finally {
        setBusy(false);
      }
    },
    [campaign, fail],
  );

  const handleLotDelete = useCallback(
    async (lotID: number) => {
      if (!campaign) return;
      setBusy(true);
      try {
        setCampaign(await deleteFWLot(campaign.campaign_id, lotID));
      } catch (e) {
        fail(e, "Could not delete the lot");
      } finally {
        setBusy(false);
      }
    },
    [campaign, fail],
  );

  // One request for the whole selection, not a loop. The budget is derived from
  // the lots, so a loop that failed at the fourth of ten would leave headroom
  // describing a position the campaign was never in -- and no way to see from
  // the screen which four had landed.
  const handleLotBulk = useCallback(
    async (lotIDs: number[], action: "state" | "delete", state?: string) => {
      if (!campaign || lotIDs.length === 0) return;
      setBusy(true);
      try {
        setCampaign(await bulkFWLots(campaign.campaign_id, lotIDs, action, state));
      } catch (e) {
        // The server applies the batch in one transaction, so a failure here
        // means nothing moved. Saying so is the difference between retrying and
        // hunting for what half-applied.
        fail(e, action === "delete" ? "Could not delete the lots — nothing was changed" : "Could not move the lots — nothing was changed");
      } finally {
        setBusy(false);
      }
    },
    [campaign, fail],
  );

  // Recording a lot is the moment a plan becomes capital: the quantity and the
  // Jita price are what was actually bought, and from here the budget is
  // measured from the lot, never from the plan. The rows are whatever the
  // caller selected -- GapsPanel's checkboxes, not a budget trim -- so this
  // reads suggested_qty, the full row's own number, rather than a trimmed one.
  const recordLots = useCallback(
    async (rows: FWSupplyRow[]) => {
      if (!campaign || rows.length === 0) return;
      setBusy(true);
      try {
        let latest = campaign;
        for (const row of rows) {
          latest = await saveFWLot(campaign.campaign_id, {
            type_id: row.type_id,
            type_name: row.type_name,
            qty: row.suggested_qty,
            qty_remaining: row.suggested_qty,
            unit_cost_isk: row.jita_best_sell,
            listed_price: row.suggested_price,
            state: "bought",
          });
        }
        setCampaign(latest);
      } catch (e) {
        fail(e, "Could not record the lot");
      } finally {
        setBusy(false);
      }
    },
    [campaign, fail],
  );

  const rows = plan?.rows ?? [];

  const planWarnings = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const w of [...(planResp?.warnings ?? []), ...(plan?.warnings ?? [])]) {
      if (!w || seen.has(w)) continue;
      seen.add(w);
      out.push(w);
    }
    return out;
  }, [planResp, plan]);

  if (!isLoggedIn) {
    return (
      <div className="flex-1 flex items-center justify-center p-8 text-sm text-eve-dim">{t("fwLoginRequired")}</div>
    );
  }

  const budget = campaign?.budget;

  return (
    <div className="flex-1 flex flex-col gap-3 p-4 overflow-auto">
      {/* Header: which campaign, how old its plan is, and the one expensive button. */}
      <div className="flex items-start justify-between flex-wrap gap-2">
        <div>
          <h2 className="text-lg font-semibold text-eve-accent">{t("fwTitle")}</h2>
          <p className="text-xs text-eve-dim mt-1">{t("fwDesc")}</p>
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <select
            className={INPUT}
            value={campaign?.campaign_id ?? ""}
            onChange={(e) => {
              const id = Number(e.target.value);
              if (id > 0) void selectCampaign(id);
            }}
          >
            {campaigns.length === 0 && <option value="">{t("fwNoCampaigns")}</option>}
            {campaigns.map((c) => (
              <option key={c.campaign_id} value={c.campaign_id}>
                {c.name} — {militiaName(c.militia_faction_id)}
              </option>
            ))}
          </select>
          <select
            className={INPUT}
            value=""
            disabled={busy}
            onChange={(e) => {
              const militia = Number(e.target.value);
              if (militia > 0) void handleCreate(militia);
            }}
          >
            <option value="">{t("fwNewCampaign")}</option>
            {MILITIAS.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
          <button className={BTN} onClick={() => setShowSettings((s) => !s)} disabled={!campaign}>
            {t("fwSettings")}
          </button>
          <button className={BTN_ACCENT} onClick={() => void handleGenerate()} disabled={!campaign || generating}>
            {generating ? t("fwGenerating") : planResp?.has_plan ? t("fwRegenerate") : t("fwGeneratePlan")}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-sm border border-eve-danger/50 bg-eve-danger/5 px-3 py-2 text-xs text-eve-danger">
          {error}
        </div>
      )}

      {loading && <LoadingBlock label={t("fwLoading")} />}

      {!loading && campaigns.length === 0 && (
        <div className={`${PANEL} px-3 py-6 text-center text-xs text-eve-dim`}>{t("fwNoCampaignsHint")}</div>
      )}

      {campaign && (
        <>
          {/* Budget strip — capital at cost. Listed value is shown and does not
              gate, because what the stock is asking is not what it cost. */}
          <div className="flex flex-wrap items-stretch gap-2">
            <div className="px-3 py-2 rounded-sm border border-eve-border/60 bg-eve-panel/60">
              <div className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwBudget")}</div>
              <input
                className={`${INPUT} w-36 mt-0.5`}
                type="number"
                defaultValue={campaign.budget_isk}
                key={`budget-${campaign.campaign_id}-${campaign.budget_isk}`}
                onBlur={(e) => {
                  const v = Number(e.target.value);
                  if (Number.isFinite(v) && v !== campaign.budget_isk) void patchCampaign({ budget_isk: v });
                }}
              />
            </div>
            <Stat label={t("fwCommitted")} value={formatISK(budget?.committed_isk ?? 0)} />
            <Stat
              label={t("fwHeadroom")}
              value={formatISK(budget?.headroom_isk ?? 0)}
              tone={(budget?.headroom_isk ?? 0) < 0 ? "text-eve-danger" : "text-eve-success"}
            />
            <Stat label={t("fwListedValue")} value={formatISK(budget?.listed_value_isk ?? 0)} />
            <Stat
              label={t("fwDestination")}
              value={campaign.dest_station_name || (campaign.dest_station_id > 0 ? String(campaign.dest_station_id) : t("fwNoDestination"))}
            />
            <Stat label={t("fwSource")} value={campaign.source_station_name || String(campaign.source_station_id)} />
            <Stat
              label={t("fwPlanAge")}
              value={planResp?.has_plan ? ageLabel(planResp.age_seconds) : t("fwNoPlan")}
            />
          </div>

          {/* Committed ISK by who is holding it. Stranded capital is invisible
              on a total: a lot sitting in `bought` for a week reads the same as
              one on the market until it is broken out by owner and state. */}
          {(budget?.by_owner?.length ?? 0) + (budget?.by_state?.length ?? 0) > 0 && (
            <div className="flex flex-wrap gap-3 text-[11px] text-eve-dim">
              {(budget?.by_owner ?? []).length > 0 && (
                <div className="flex flex-wrap items-center gap-2">
                  <span className="uppercase tracking-wider">{t("fwByOwner")}</span>
                  {(budget?.by_owner ?? []).map((o) => (
                    <span key={`${o.owner_kind}-${o.owner_id}`} className="px-2 py-0.5 rounded-sm border border-eve-border/60">
                      {o.owner_name || `${o.owner_kind} ${o.owner_id}`}: <span className="font-mono text-eve-text">{formatISK(o.committed_isk)}</span>
                    </span>
                  ))}
                </div>
              )}
              {(budget?.by_state ?? []).length > 0 && (
                <div className="flex flex-wrap items-center gap-2">
                  <span className="uppercase tracking-wider">{t("fwByState")}</span>
                  {(budget?.by_state ?? []).map((s) => (
                    <span key={s.state} className="px-2 py-0.5 rounded-sm border border-eve-border/60">
                      {s.state}: <span className="font-mono text-eve-text">{formatISK(s.committed_isk)}</span>
                    </span>
                  ))}
                </div>
              )}
            </div>
          )}

          {showSettings && (
            <SettingsPanel
              campaign={campaign}
              draft={draft}
              setDraft={setDraft}
              busy={busy}
              characters={characters}
              onToggleTypeOverride={toggleTypeOverride}
              onSave={() => {
                const patch = draft;
                setDraft({});
                void patchCampaign(patch);
              }}
              onDelete={() => setConfirmDeleteCampaign(true)}
            />
          )}

          <WarningList title={t("fwOccupancyDrift")} items={planResp?.occupancy_drift ?? []} tone="danger" />
          <WarningList title={t("fwWarnings")} items={planWarnings} />

          {plan && (
            <div className="text-[11px] text-eve-dim">
              {t("fwDemandSummary", {
                militia: plan.militia_name,
                kills: plan.demand.in_warzone_kills.toLocaleString(),
                fetched: plan.demand.fetched_kills.toLocaleString(),
                days: Math.round((plan.demand.window_seconds || 0) / 86400),
                types: plan.demand.destroyed_types.toLocaleString(),
                frontline: plan.frontline_systems.toLocaleString(),
                radius: plan.ring_radius,
              })}
              {plan.demand.truncated ? ` — ${t("fwDemandTruncated")}` : ""}
              {/* The long window gets its own sentence rather than more
                  parameters in the first one, because it has a figure the short
                  window does not: how much of what was asked for was actually
                  covered. A quarter that walked eighty days is not a quarter,
                  and the rates are already scaled to the eighty -- so they are
                  right, and they look whole, which is why the span is printed. */}
              {plan.demand.long_window_seconds > 0 && (
                <div>
                  {t("fwDemandSummaryLong", {
                    days: Math.round(plan.demand.long_window_seconds / 86400),
                    covered: Math.round((plan.demand.long_covered_seconds || 0) / 86400),
                    kills: plan.demand.long_in_warzone_kills.toLocaleString(),
                    fetched: plan.demand.long_fetched_kills.toLocaleString(),
                    types: plan.demand.long_destroyed_types.toLocaleString(),
                    sized: plan.demand.size_against === "long" ? t("fwSizeAgainstLong") : t("fwSizeAgainstShort"),
                  })}
                  {plan.demand.long_truncated ? ` — ${t("fwDemandTruncated")}` : ""}
                </div>
              )}
              {/* The resolved rate pair floor prices were actually computed
                  with, confirmed by a real number rather than only by a
                  warning's absence -- choosing a seller character in Settings
                  should visibly change this line. */}
              {plan.fees && (
                <div>
                  {t("fwFeeProfile", {
                    tax: plan.fees.sales_tax_percent.toFixed(2),
                    broker: plan.fees.broker_fee_percent.toFixed(2),
                    source:
                      plan.fees.source === "skills"
                        ? t("fwFeeSourceSkills", {
                            accounting: String(plan.fees.accounting_level ?? 0),
                            broker: String(plan.fees.broker_relations_level ?? 0),
                          })
                        : plan.fees.source === "config"
                          ? t("fwFeeSourceConfig")
                          : t("fwFeeSourceDefault"),
                  })}
                </div>
              )}
            </div>
          )}

          {/* Views */}
          <div className="flex items-center gap-1 flex-wrap">
            {(["ring", "gaps", "lots", "orders"] as View[]).map((v) => (
              <button
                key={v}
                onClick={() => {
                  setView(v);
                  if (v === "orders" && !orders && !ordersLoading) void loadOrders();
                }}
                className={`px-3 py-1 rounded-sm border text-xs transition-colors ${
                  view === v
                    ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                    : "border-eve-border text-eve-dim hover:text-eve-text hover:border-eve-border/80"
                }`}
              >
                {t(
                  v === "ring"
                    ? "fwViewRing"
                    : v === "gaps"
                      ? "fwViewGaps"
                      : v === "lots"
                        ? "fwViewLots"
                        : "fwViewOrders",
                )}
              </button>
            ))}
          </div>

          {!planResp?.has_plan && view !== "lots" && view !== "orders" && (
            <div className={`${PANEL} px-3 py-6 text-center text-xs text-eve-dim`}>{t("fwNoPlanHint")}</div>
          )}

          {view === "ring" && plan && (
            <RingPanel
              ring={plan.ring ?? []}
              campaign={campaign}
              busy={busy}
              onSetDestination={(stationID) => void patchCampaign({ dest_station_id: stationID })}
              onTogglePin={(systemID) => {
                const pinned = campaign.pinned_systems ?? [];
                void patchCampaign({
                  pinned_systems: pinned.includes(systemID) ? pinned.filter((s) => s !== systemID) : [...pinned, systemID],
                });
              }}
              onToggleExclude={(systemID) => {
                const excluded = campaign.excluded_systems ?? [];
                void patchCampaign({
                  excluded_systems: excluded.includes(systemID)
                    ? excluded.filter((s) => s !== systemID)
                    : [...excluded, systemID],
                });
              }}
            />
          )}

          {view === "gaps" && plan && (
            <GapsPanel
              plan={plan}
              campaign={campaign}
              onToggleTypeOverride={toggleTypeOverrideAndRegenerate}
              rows={rows}
              busy={busy}
              generating={generating}
              show={gapsShow}
              setShow={setGapsShow}
              expanded={expanded}
              setExpanded={setExpanded}
              onRecord={(rs) => void recordLots(rs)}
              ladderNote={
                (plan.ladder?.bands ?? []).some((b) => b.samples >= 5)
                  ? t("fwLadderDerived", { station: plan.destination?.station_name ?? "" })
                  : t("fwLadderUncalibrated")
              }
            />
          )}

          {view === "lots" && (
            <LotsPanel
              lots={campaign.lots ?? []}
              busy={busy}
              onAdvance={(lotID, state) => void handleLotState(lotID, state)}
              onDelete={(lotID) => void handleLotDelete(lotID)}
              onBulk={(ids, action, state) => void handleLotBulk(ids, action, state)}
            />
          )}

          {view === "orders" && (
            <OrdersPanel
              orders={orders}
              loading={ordersLoading}
              busy={busy}
              onRefresh={() => void loadOrders()}
              onApply={(lotIDs) => void handleApply(lotIDs)}
            />
          )}

          {plan?.occupancy && plan.occupancy.length > 0 && view === "ring" && (
            <OccupancyPanel occupancy={plan.occupancy} />
          )}
        </>
      )}

      {/* The count is the point of the sentence: a campaign with lots is a
          record of ISK that left the wallet, and a campaign with none is just a
          set of settings. Both are worth confirming; only one is worth
          hesitating over, and the number is what tells them apart. */}
      {confirmDeleteCampaign && campaign && (
        <ConfirmDialog
          open={true}
          title={t("fwConfirmDeleteCampaignTitle")}
          message={t("fwConfirmDeleteCampaign", {
            name: campaign.name || String(campaign.campaign_id),
            count: (campaign.lots ?? []).length,
          })}
          confirmText={t("fwDelete")}
          variant="danger"
          onConfirm={() => void handleDeleteCampaign()}
          onClose={() => setConfirmDeleteCampaign(false)}
        />
      )}
    </div>
  );
}

/* ---------------------------------------------------------------- settings */

function SettingsPanel({
  campaign,
  draft,
  setDraft,
  busy,
  characters,
  onToggleTypeOverride,
  onSave,
  onDelete,
}: {
  campaign: FWCampaign;
  draft: FWCampaignPatch;
  setDraft: (d: FWCampaignPatch) => void;
  busy: boolean;
  characters: AuthCharacter[];
  onToggleTypeOverride: (typeId: number, typeName: string, kind: "include" | "exclude") => void;
  onSave: () => void;
  onDelete: () => void;
}) {
  const { t } = useI18n();
  const num = (key: keyof FWCampaignPatch, label: string, step = 1, disabled = false, hint?: string) => (
    <label className={`flex flex-col gap-1 ${disabled ? "opacity-40" : ""}`} title={hint}>
      <span className="text-[10px] uppercase tracking-wider text-eve-dim">{label}</span>
      <input
        className={`${INPUT} w-28`}
        type="number"
        step={step}
        disabled={disabled}
        value={String((draft[key] as number | undefined) ?? (campaign[key as keyof FWCampaign] as number))}
        onChange={(e) => setDraft({ ...draft, [key]: Number(e.target.value) })}
      />
    </label>
  );

  // max_trips = 0 already means "no cargo bound" in the engine
  // (fw_budget.go: remainingM3 stays +Inf), so this checkbox is the label that
  // option never had. m3 and trips keep being reported when it is on; they just
  // stop trimming, and the shipment says so in a note.
  const noHaulerLimit = (draft.max_trips ?? campaign.max_trips) === 0;

  // The second demand window, and which of the two rates sizes the lots. Both
  // are stored on the campaign; the pair may disagree -- the window off with
  // "long" still remembered -- and the server resolves that toward the window
  // that always exists. So the control shows the stored preference even while it
  // is inert, and switching the window off deliberately does not rewrite it.
  const longWindow = draft.long_demand_window_seconds ?? campaign.long_demand_window_seconds ?? 0;
  const sizeAgainst = (draft.size_against ?? campaign.size_against) === "long" ? "long" : "short";

  // Fees follow whoever lists, so this names one of the user's own linked
  // characters -- Accounting and Broker Relations read from ESI the moment a
  // plan is generated, exactly like every other realized-profit surface in the
  // app. "None" keeps the configured/default rates, which is what every
  // campaign predating this control already does.
  const sellerCharacterID =
    (draft.seller_owner_kind ?? campaign.seller_owner_kind) === "character"
      ? (draft.seller_owner_id ?? campaign.seller_owner_id)
      : 0;

  const ceilings = { ...(campaign.category_ceilings ?? {}), ...(draft.category_ceilings ?? {}) };

  return (
    <div className={`${PANEL} p-3 flex flex-col gap-3`}>
      <div className="flex flex-wrap gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwName")}</span>
          <input
            className={`${INPUT} w-48`}
            value={draft.name ?? campaign.name}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          />
        </label>
        {num("target_cover_days", t("fwCoverTarget"), 1, false, t("fwCoverTargetHint"))}
        {num("covered_multiple", t("fwCoveredMultiple"), 0.5, false, t("fwCoveredMultipleHint"))}
        {num("min_margin_pct", t("fwMinMargin"), 1, false, t("fwMinMarginHint"))}
        {num("freight_isk_per_m3", t("fwFreight"), 50, false, t("fwFreightHint"))}
        {num("step_over_days_cover", t("fwStepOver"), 0.1, false, t("fwStepOverHint"))}
        {num("max_jumps_from_front", t("fwRadius"), 1, false, t("fwRadiusSettingHint"))}
        {num("max_trips", t("fwMaxTrips"), 1, noHaulerLimit, t("fwMaxTripsHint"))}
        <label className={`flex flex-col gap-1 ${noHaulerLimit ? "opacity-40" : ""}`} title={t("fwShipProfileHint")}>
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwShipProfile")}</span>
          <select
            className={`${INPUT} w-44`}
            disabled={noHaulerLimit}
            value={draft.ship_profile ?? campaign.ship_profile}
            onChange={(e) => setDraft({ ...draft, ship_profile: e.target.value })}
          >
            {SHIP_PROFILES.map((p) => (
              <option key={p} value={p}>
                {p.replace(/_/g, " ")}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-2 self-end pb-1 text-[11px] text-eve-text">
          <input
            type="checkbox"
            checked={noHaulerLimit}
            onChange={(e) =>
              setDraft({
                ...draft,
                max_trips: e.target.checked ? 0 : campaign.max_trips > 0 ? campaign.max_trips : 1,
              })
            }
          />
          <span title={t("fwNoHaulerLimitHint")}>{t("fwNoHaulerLimit")}</span>
        </label>
      </div>

      {/* The second demand window is off by default and stays a choice, because
          what it costs is not small: the first generation at ninety days is
          roughly three hundred requests. After that it is a cached fold of about
          two thousand rows, refreshed on a schedule that scales with the window.
          The figures sit on the options themselves -- the cost is the whole
          reason this is not simply always on. */}
      <div className="flex flex-wrap gap-3 items-end">
        <label className="flex flex-col gap-1">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwLongWindow")}</span>
          <select
            className={`${INPUT} w-56`}
            value={String(longWindow)}
            onChange={(e) => setDraft({ ...draft, long_demand_window_seconds: Number(e.target.value) })}
          >
            <option value="0">{t("fwLongWindowOff")}</option>
            <option value="2592000">{t("fwLongWindow30")}</option>
            <option value="7776000">{t("fwLongWindow90")}</option>
          </select>
        </label>
        <label className={`flex flex-col gap-1 ${longWindow > 0 ? "" : "opacity-40"}`}>
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwSizeAgainst")}</span>
          <div className="flex items-center gap-3 py-1">
            {(["short", "long"] as const).map((v) => (
              <label key={v} className="flex items-center gap-1 text-[11px] text-eve-text">
                <input
                  type="radio"
                  name="fw-size-against"
                  disabled={longWindow <= 0}
                  checked={sizeAgainst === v}
                  onChange={() => setDraft({ ...draft, size_against: v })}
                />
                <span>{v === "short" ? t("fwSizeAgainstShort") : t("fwSizeAgainstLong")}</span>
              </label>
            ))}
          </div>
        </label>
        <span className="text-[11px] text-eve-dim max-w-[30rem] pb-1">{t("fwLongWindowHint")}</span>
      </div>

      {/* Fees are computed once, at plan time, from whoever this names -- not
          previewed here, so this control and the number in the demand summary
          never disagree about which rates are in play. */}
      <div className="flex flex-wrap gap-3 items-end">
        <label className="flex flex-col gap-1">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwSellUnder")}</span>
          <select
            className={`${INPUT} w-56`}
            value={String(sellerCharacterID)}
            onChange={(e) => {
              const id = Number(e.target.value);
              setDraft({ ...draft, seller_owner_kind: id > 0 ? "character" : "", seller_owner_id: id });
            }}
          >
            <option value="0">{t("fwSellUnderNone")}</option>
            {characters.map((c) => (
              <option key={c.character_id} value={c.character_id}>
                {c.character_name}
              </option>
            ))}
          </select>
        </label>
        <span className="text-[11px] text-eve-dim max-w-[28rem] pb-1">{t("fwSellUnderHint")}</span>
      </div>

      {/* Per-item overrides: your judgment beating the algorithm's. An excluded
          type stops producing a row to un-exclude it from, so this is the only
          place either list can be managed once set -- the name was captured at
          the moment of the toggle, so removing a chip needs no lookup. */}
      {(campaign.included_types ?? []).length > 0 && (
        <div className="flex flex-wrap gap-2 items-center">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwAlwaysShipList")}</span>
          {(campaign.included_types ?? []).map((o) => (
            <span
              key={o.type_id}
              className="flex items-center gap-1 px-2 py-0.5 rounded-sm border border-eve-border/60 text-[11px]"
            >
              {o.type_name}
              <button
                className="text-eve-dim hover:text-eve-danger transition-colors"
                onClick={() => onToggleTypeOverride(o.type_id, o.type_name, "include")}
                aria-label={t("fwRemoveOverride")}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      {(campaign.excluded_types ?? []).length > 0 && (
        <div className="flex flex-wrap gap-2 items-center">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwNeverShipList")}</span>
          {(campaign.excluded_types ?? []).map((o) => (
            <span
              key={o.type_id}
              className="flex items-center gap-1 px-2 py-0.5 rounded-sm border border-eve-border/60 text-[11px]"
            >
              {o.type_name}
              <button
                className="text-eve-dim hover:text-eve-danger transition-colors"
                onClick={() => onToggleTypeOverride(o.type_id, o.type_name, "exclude")}
                aria-label={t("fwRemoveOverride")}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}

      {/* The ceilings are the non-gouging guard, and they are yours to move:
          the derived ladder is clamped by them, never the other way round. */}
      <div className="flex flex-wrap gap-3 items-end">
        <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwCeilings")}</span>
        {CEILING_CATEGORIES.map((cat) => (
          <label key={cat} className="flex flex-col gap-1">
            <span className="text-[10px] text-eve-dim">{cat}</span>
            <input
              className={`${INPUT} w-20`}
              type="number"
              step={0.05}
              value={String(ceilings[cat] ?? "")}
              onChange={(e) =>
                setDraft({ ...draft, category_ceilings: { ...ceilings, [cat]: Number(e.target.value) } })
              }
            />
          </label>
        ))}
      </div>

      <div className="flex items-center gap-2">
        <button className={BTN_ACCENT} onClick={onSave} disabled={busy || Object.keys(draft).length === 0}>
          {t("fwSave")}
        </button>
        <button className={BTN} onClick={() => setDraft({})} disabled={Object.keys(draft).length === 0}>
          {t("fwCancel")}
        </button>
        <span className="flex-1" />
        <button
          className="px-2.5 py-1 rounded-sm border border-eve-danger/50 text-eve-danger hover:bg-eve-danger/10 transition-colors text-[11px]"
          onClick={onDelete}
          disabled={busy}
        >
          {t("fwDeleteCampaign")}
        </button>
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------- ring */

function RingPanel({
  ring,
  campaign,
  busy,
  onSetDestination,
  onTogglePin,
  onToggleExclude,
}: {
  ring: FWStagingCandidate[];
  campaign: FWCampaign;
  busy: boolean;
  onSetDestination: (stationID: number) => void;
  onTogglePin: (systemID: number) => void;
  onToggleExclude: (systemID: number) => void;
}) {
  const { t } = useI18n();
  if (ring.length === 0) {
    return <div className={`${PANEL} px-3 py-6 text-center text-xs text-eve-dim`}>{t("fwRingEmpty")}</div>;
  }
  const excluded = new Set(campaign.excluded_systems ?? []);
  return (
    <div className={`${PANEL} overflow-auto`}>
      <div className="px-3 py-2 text-[11px] text-eve-dim border-b border-eve-border/60">{t("fwRingHint")}</div>
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-eve-dark z-10">
          <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
            <th className={TH}>{t("fwColStation")}</th>
            <th className={THR}>{t("fwColSec")}</th>
            <th className={THR}>{t("fwColJumpsFront")}</th>
            <th className={THR}>{t("fwColJumpsSource")}</th>
            <th className={THR}>{t("fwColLowsec")}</th>
            <th className={THR}>{t("fwColOrders")}</th>
            <th className={THR}>{t("fwColTypes")}</th>
            <th className={THR}>{t("fwColOverlap")}</th>
            <th className={THR}>{t("fwColScore")}</th>
            <th className={THR}></th>
          </tr>
        </thead>
        <tbody>
          {ring.map((c, i) => {
            const isDest = c.station_id === campaign.dest_station_id;
            return (
              <tr
                key={c.station_id}
                className={`border-b border-eve-border/50 hover:bg-eve-accent/5 ${
                  i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"
                } ${isDest ? "outline outline-1 outline-eve-accent/40" : ""}`}
              >
                <td className={TD}>
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-eve-text">{c.station_name || `station ${c.station_id}`}</span>
                    {c.is_bulwark && (
                      <span className="px-1.5 py-0.5 rounded-sm border border-eve-accent/50 text-eve-accent text-[10px]">
                        {t("fwBadgeBulwark")}
                      </span>
                    )}
                    {c.pinned && (
                      <span className="px-1.5 py-0.5 rounded-sm border border-eve-border text-eve-dim text-[10px]">
                        {t("fwBadgePinned")}
                      </span>
                    )}
                    {!c.in_ring && (
                      <span className="px-1.5 py-0.5 rounded-sm border border-eve-border text-eve-dim text-[10px]">
                        {t("fwBadgeOutsideRing")}
                      </span>
                    )}
                    {c.highsec_route && (
                      <span className="px-1.5 py-0.5 rounded-sm border border-eve-success/40 text-eve-success text-[10px]">
                        {t("fwBadgeHighsecRoute")}
                      </span>
                    )}
                  </div>
                  <div className="text-[10px] text-eve-dim">{c.system_name}</div>
                </td>
                <td className={`${TDR} ${securityTone(c.security)}`}>{formatSecurity(c.security)}</td>
                <td className={TDR}>{c.jumps_to_front}</td>
                <td className={TDR}>{c.jumps_from_source > 0 ? c.jumps_from_source : "—"}</td>
                <td className={`${TDR} ${c.lowsec_jumps_from_source > 0 ? "text-eve-warning" : "text-eve-dim"}`}>
                  {c.lowsec_jumps_from_source}
                </td>
                <td className={TDR}>{c.player_orders.toLocaleString()}</td>
                <td className={TDR}>{c.player_types.toLocaleString()}</td>
                <td className={TDR}>{c.fw_item_overlap.toLocaleString()}</td>
                <td className={TDR}>{c.score.toFixed(1)}</td>
                <td className="px-3 py-1.5 text-right whitespace-nowrap">
                  <button className={BTN} disabled={busy || isDest} onClick={() => onSetDestination(c.station_id)}>
                    {isDest ? t("fwIsDestination") : t("fwSetDestination")}
                  </button>{" "}
                  <button className={BTN} disabled={busy} onClick={() => onTogglePin(c.system_id)}>
                    {c.pinned ? t("fwUnpin") : t("fwPin")}
                  </button>{" "}
                  <button className={BTN} disabled={busy} onClick={() => onToggleExclude(c.system_id)}>
                    {excluded.has(c.system_id) ? t("fwInclude") : t("fwExclude")}
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/* -------------------------------------------------------------------- gaps */

/* GapsPanel is the whole buy-and-sell view: what is missing, what of it fits the
 * budget, what it costs and what it should be listed at.
 *
 * It used to be two panels. A shipment line is a supply row plus a shipped
 * quantity, so the shipping list was this table with fewer columns, and the
 * split put the rows the budget funded on a different screen from the rows it
 * could not. Those are the same list.
 */

export function GapsPanel({
  plan,
  campaign,
  onToggleTypeOverride,
  rows,
  busy,
  generating,
  show,
  setShow,
  expanded,
  setExpanded,
  onRecord,
  ladderNote,
}: {
  plan: NonNullable<FWPlanResponse["plan"]>;
  campaign: FWCampaign;
  onToggleTypeOverride: (typeId: number, typeName: string, kind: "include" | "exclude") => void;
  rows: FWSupplyRow[];
  busy: boolean;
  generating: boolean;
  show: GapsShow;
  setShow: (v: GapsShow) => void;
  expanded: number | null;
  setExpanded: (v: number | null) => void;
  onRecord: (rows: FWSupplyRow[]) => void;
  ladderNote: string;
}) {
  const { t } = useI18n();
  const shipment = plan.shipment;
  const route = plan.route;

  // No long window, no column. A column of zeroes is not a trend, and it would
  // read as one -- an unmeasured rate and a measured zero render identically.
  const hasLongWindow = (plan.demand.long_window_seconds ?? 0) > 0;

  // The button's label reflects the campaign's current setting, not the row's
  // own Included flag -- those can disagree for one regeneration, right after
  // a toggle, since the setting takes effect on the next regenerate rather
  // than rewriting the cached plan in place.
  const includedTypeIds = useMemo(
    () => new Set((campaign.included_types ?? []).map((o) => o.type_id)),
    [campaign.included_types],
  );

  // Selection replaces the budget's own trim as what drives the action bar,
  // and it is entirely yours: every row is checkable, whatever its verdict,
  // because the cover model and the price model answer different questions.
  // A row the cover math calls "covered" -- 500k units stocked against a slow
  // destruction rate -- can still be sitting under a competitor listing far
  // above what it costs to land, and that is a real trade the checkbox has to
  // be able to reach even though nothing recommended it.
  //
  // manualQty is the escape hatch for a row the engine never sized, or sized
  // to more than you actually want to carry: the cover model saying "covered"
  // is not the same claim as "there is no trade here," and 69 Caracals is a
  // real suggestion you are still allowed to find impractical to haul. Every
  // checked row's quantity is yours to edit -- not just the ones the engine
  // left at zero.
  //
  // Quantity only, never price -- a price the engine never reached has no
  // fee-adjusted margin to show either, and inventing one would be exactly
  // the fabrication this app refuses to do with real ISK on the line. A
  // manual quantity against a real jita_best_sell still prices the buy side
  // honestly; it just cannot show a profit it cannot compute.
  const [selected, setSelected] = useState<Set<number>>(new Set());
  // "" is a real state, distinct from absent: it means you have cleared the
  // field on purpose (about to type a replacement) and the input must stay
  // empty rather than snapping back to the engine's own number -- which is
  // exactly what happens if a cleared field falls through to "no override" on
  // a row whose suggested_qty is nonzero, one keystroke before you get to type
  // the number you actually wanted. Absent means "still the engine's/the
  // prefill's own"; "" means "empty, on the way to something else"; a number
  // means the number.
  const [manualQty, setManualQty] = useState<Map<number, number | "">>(new Map());
  // The opening guess for a row sized at zero: the rate the row is actually
  // sized by, times the campaign's own cover target, capped at the same
  // minimum-lot floor the engine stretches a sized row to. Not kills -- a
  // handful of killmails can each carry a fit's whole ammo hold, so 2 losses
  // and 2,278 units a day are the same two killmails, and kills is the wrong
  // one of those two numbers to guess a lot size from. Rate x days is the
  // same "how much moves before it needs topping up" question the engine
  // itself asks of a gap row; the floor is the same cap it stretches a
  // sized row to, applied here to a guess instead of a deficit.
  const prefillQty = (r: FWSupplyRow) => {
    const floor = fwFloorForPrice(r.jita_best_sell);
    if (floor == null) return 0;
    const rate = r.sized_by === "long" ? r.daily_destroyed_long : r.daily_destroyed;
    return Math.min(Math.round(rate * campaign.target_cover_days), floor);
  };
  const defaultQty = (r: FWSupplyRow) => (r.suggested_qty > 0 ? r.suggested_qty : prefillQty(r));
  // undefined means no override is in play at all -- the caller falls back to
  // the row's own total (cost_isk, profit_isk, ...) rather than recomputing
  // one from a per-unit rate that would not match it exactly once freight,
  // fees and rounding are in the picture.
  const manualOverrideQty = (r: FWSupplyRow): number | undefined => {
    const m = manualQty.get(r.type_id);
    if (m === undefined) return undefined;
    return m === "" ? 0 : m;
  };
  const effectiveQty = (r: FWSupplyRow) => manualOverrideQty(r) ?? defaultQty(r);
  const hasRealNumbers = (r: FWSupplyRow) => effectiveQty(r) > 0;
  const rowCostISK = (r: FWSupplyRow) => {
    const m = manualOverrideQty(r);
    return m !== undefined ? m * r.jita_best_sell : r.cost_isk;
  };
  const rowLandedISK = (r: FWSupplyRow) => {
    const m = manualOverrideQty(r);
    return m !== undefined ? m * r.landed_cost : r.landed_cost * r.suggested_qty;
  };
  const rowProfitISK = (r: FWSupplyRow) => {
    const m = manualOverrideQty(r);
    return m !== undefined ? m * r.unit_profit_isk : r.profit_isk;
  };
  const rowM3 = (r: FWSupplyRow) => {
    const m = manualOverrideQty(r);
    return m !== undefined ? m * r.volume_m3 : r.cargo_m3;
  };
  const toggleSelected = (typeId: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(typeId)) {
        next.delete(typeId);
        // Start clean next time this row is checked, rather than carrying a
        // stale edit (or a stale "cleared") across an uncheck and a recheck.
        setManualQty((m) => {
          if (!m.has(typeId)) return m;
          const nextM = new Map(m);
          nextM.delete(typeId);
          return nextM;
        });
      } else {
        next.add(typeId);
      }
      return next;
    });
  };
  const setQtyOverride = (typeId: number, raw: string) => {
    setManualQty((prev) => {
      const next = new Map(prev);
      if (raw === "") {
        next.set(typeId, "");
        return next;
      }
      const n = Number(raw);
      next.set(typeId, Number.isFinite(n) && n > 0 ? n : "");
      return next;
    });
  };

  // sortKey null is the server's own ranking: gap, then thin, then unpriceable,
  // then covered, and within each the deepest hole first. Making a column the
  // default would silently throw that away, and it is the one ordering that
  // encodes "ship this first" -- so it stays the default and the `ranked` chip
  // is how you get back to it.
  const [sortKey, setSortKey] = useState<GapSortKey | null>(null);
  const [sortDir, setSortDir] = useState<"asc" | "desc">("desc");
  const toggleSort = (key: GapSortKey) => {
    if (sortKey === key) {
      setSortDir(sortDir === "desc" ? "asc" : "desc");
      return;
    }
    setSortKey(key);
    setSortDir("desc");
  };

  // shippedLines and shipByType are what the automatic budget-trim WOULD have
  // funded. They no longer drive any action -- multibuy, the price list and
  // recording all read the checkboxes now -- they only feed the "ship" badge
  // and the "shipping" filter, both signals to read rather than gates.
  const shippedLines = useMemo(() => (plan.shipment?.lines ?? []).filter((l) => l.ship_qty > 0), [plan.shipment]);
  const shipByType = useMemo(() => {
    const m = new Map<number, FWShipmentLine>();
    for (const l of shippedLines) m.set(l.type_id, l);
    return m;
  }, [shippedLines]);

  const actionableCount = useMemo(
    () => rows.filter((r) => r.verdict === "gap" || r.verdict === "thin").length,
    [rows],
  );

  const visible = useMemo(() => {
    const filtered = rows.filter((r) => {
      if (show === "shipping") return shipByType.has(r.type_id);
      if (show === "actionable") return r.verdict === "gap" || r.verdict === "thin";
      return true;
    });
    return sortGapRows(filtered, sortKey, sortDir);
  }, [rows, show, shipByType, sortKey, sortDir]);

  // The action bar's whole input: exactly the rows you checked, not a trim.
  // Anything checked with no suggested quantity is still yours to have
  // checked -- it just cannot contribute a multibuy line the game will
  // accept, so multibuy and recording both read the narrower list, at
  // whichever quantity is actually in play for it.
  const selectedRows = useMemo(() => rows.filter((r) => selected.has(r.type_id)), [rows, selected]);
  const sizedSelected = useMemo(() => selectedRows.filter(hasRealNumbers), [selectedRows, manualQty]);
  const multibuy = useMemo(
    () => sizedSelected.map((r) => `${r.type_name} ${effectiveQty(r)}`).join("\n"),
    [sizedSelected, manualQty],
  );
  // Same grid-snapping the seller paste has always used: a reference price is
  // jita x markup and lands on a number EVE's client will not take, so the
  // step this reproduces is what makes the pasted list usable at all. A
  // manual quantity never manufactures a price -- if the engine reached one,
  // this is it; if it did not, there is nothing honest to paste here.
  const pricedSelected = useMemo(() => sizedSelected.filter((r) => r.suggested_price > 0), [sizedSelected]);
  const priceList = useMemo(
    () =>
      pricedSelected
        .map((r) => `${r.type_name}\t${formatGridPrice(r.suggested_price, priceStep(r.suggested_price))}`)
        .join("\n"),
    [pricedSelected],
  );
  // Advisory, not a gate: a box you checked is already the explicit choice a
  // silent auto-trim used to make for you, so going over headroom is worth
  // naming, not worth refusing.
  const selectedCostISK = useMemo(
    () => selectedRows.reduce((sum, r) => sum + rowCostISK(r), 0),
    [selectedRows, manualQty],
  );
  const selectedLandedISK = useMemo(
    () => selectedRows.reduce((sum, r) => sum + rowLandedISK(r), 0),
    [selectedRows, manualQty],
  );
  const selectedProfitISK = useMemo(
    () => selectedRows.reduce((sum, r) => sum + rowProfitISK(r), 0),
    [selectedRows, manualQty],
  );
  const selectedM3 = useMemo(() => selectedRows.reduce((sum, r) => sum + rowM3(r), 0), [selectedRows, manualQty]);
  const selectedMarginPct = selectedLandedISK > 0 ? (selectedProfitISK / selectedLandedISK) * 100 : 0;
  const overBudgetBy = Math.max(0, selectedCostISK - (campaign.budget?.headroom_isk ?? 0));

  const visibleIds = useMemo(() => visible.map((r) => r.type_id), [visible]);
  const allVisibleSelected = visibleIds.length > 0 && visibleIds.every((id) => selected.has(id));
  const toggleSelectAllVisible = () => {
    setSelected((prev) => {
      if (allVisibleSelected) {
        const next = new Set(prev);
        for (const id of visibleIds) next.delete(id);
        return next;
      }
      return new Set([...prev, ...visibleIds]);
    });
  };

  const SortTH = ({
    k,
    label,
    hint,
    right = true,
  }: {
    k: GapSortKey;
    label: string;
    hint?: string;
    right?: boolean;
  }) => (
    <th className={right ? THR : TH}>
      <button
        className={`hover:text-eve-text transition-colors ${sortKey === k ? "text-eve-accent" : ""}`}
        onClick={() => toggleSort(k)}
        title={hint}
      >
        {label}
        {sortKey === k ? (sortDir === "asc" ? " ↑" : " ↓") : ""}
      </button>
    </th>
  );

  const SHOWS: GapsShow[] = ["shipping", "actionable", "all"];
  const showCount = (v: GapsShow) =>
    v === "shipping" ? shippedLines.length : v === "actionable" ? actionableCount : rows.length;

  return (
    <div className="flex flex-col gap-3">
      <WarningList title={t("fwShipmentNotes")} items={shipment.notes ?? []} />

      {/* The handoff: what leaves Jita, what it is worth, and the road -- for
          what you have actually checked, not for what a budget trim picked.
          Shown even with nothing selected yet, because the road and the gank
          check are destination facts, not shipment ones. */}
      <div className={`${PANEL} p-3`}>
        <div className="text-xs text-eve-accent mb-2">{t("fwHandoff")}</div>
        <div className="flex flex-wrap gap-2">
          <Stat label={t("fwTotalCost")} value={formatISK(selectedCostISK)} />
          {/* Landed is cost plus freight, and it is what the profit is measured
              against -- the budget still counts the stock alone, because
              freight buys nothing the campaign can sell back. */}
          <Stat label={t("fwTotalLanded")} value={formatISK(selectedLandedISK)} />
          <Stat
            label={t("fwTotalProfit")}
            value={formatISK(selectedProfitISK)}
            tone={selectedProfitISK > 0 ? "text-profit" : undefined}
          />
          <Stat label={t("fwBlendedMargin")} value={formatPct(selectedMarginPct)} />
          <Stat label={t("fwTotalM3")} value={formatM3(selectedM3)} />
          <Stat label={t("fwCollateral")} value={formatISK(selectedCostISK)} />
          <Stat
            label={t("fwRouteJumps")}
            value={route ? `${route.jumps} (${route.lowsec_jumps} ${t("fwLowsecShort")})` : "—"}
          />
          <Stat
            label={t("fwRouteRisk")}
            value={route?.checked ? route.verdict || "—" : t("fwRouteUnchecked")}
            tone={route?.checked ? dangerTone(route.verdict) : "text-eve-warning"}
          />
        </div>
        {route?.checked && (route.hot_systems?.length ?? 0) > 0 && (
          <div className="mt-2 text-[11px] text-eve-warning">
            {t("fwHotSystems")}: {(route.hot_systems ?? []).join(", ")}
          </div>
        )}
        {/* Advisory: a box you checked is already your explicit choice, so
            going over headroom is worth naming rather than worth refusing. */}
        {overBudgetBy > 0 && (
          <div className="mt-2 text-[11px] text-eve-warning">{t("fwOverBudget", { amount: formatISK(overBudgetBy) })}</div>
        )}
        {/* What the automatic budget-trim would have funded, for comparison --
            a signal to read, not what "Record selected" acts on. */}
        {shipment.dropped > 0 && (
          <div className="mt-2 text-[11px] text-eve-dim" title={t("fwShipmentTrimHint")}>
            {t("fwShipmentTrim", {
              funded: shipment.fully_funded,
              trimmed: shipment.trimmed,
              dropped: shipment.dropped,
            })}
          </div>
        )}
        {selected.size === 0 && (
          <div className="mt-2 text-[11px] text-eve-dim">{t("fwNothingSelected")}</div>
        )}
      </div>

      <div className={`${PANEL} overflow-auto`}>
        {/* The action bar. Both pastes and the record button live here: the
            buyer's multibuy, the seller's price list -- which needs no session
            at all, which is the point of the split -- and the lot record. */}
        <div className="px-3 py-2 flex items-center justify-between gap-3 border-b border-eve-border/60 flex-wrap">
          <div className="flex items-center gap-2 flex-wrap">
            {SHOWS.map((v) => (
              <button
                key={v}
                onClick={() => setShow(v)}
                className={`px-2 py-0.5 rounded-sm border text-[11px] transition-colors ${
                  show === v
                    ? "border-eve-accent text-eve-accent bg-eve-accent/10"
                    : "border-eve-border text-eve-dim hover:text-eve-text"
                }`}
                title={t(v === "shipping" ? "fwShowShippingHint" : v === "actionable" ? "fwShowActionableHint" : "fwShowAllHint")}
              >
                {t(v === "shipping" ? "fwShowShipping" : v === "actionable" ? "fwShowActionable" : "fwShowAll")}
                <span className="text-eve-dim"> {showCount(v)}</span>
              </button>
            ))}
            {sortKey != null && (
              <button
                className="px-2 py-0.5 rounded-sm border border-eve-border text-eve-dim hover:text-eve-text text-[11px]"
                onClick={() => setSortKey(null)}
                title={t("fwRankedHint")}
              >
                {t("fwRanked")}
              </button>
            )}
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[10px] text-eve-dim">{t("fwSelectedCount", { count: selected.size })}</span>
            {/* Checking a row is unconditional; contributing to a paste or a
                lot is not -- a row the engine never sized has no quantity to
                give either one, and this is the one place that says so rather
                than silently dropping it. */}
            {selected.size > sizedSelected.length && (
              <span className="text-[10px] text-eve-warning" title={t("fwSomeUnsizedHint")}>
                {t("fwSomeUnsized", { unsized: selected.size - sizedSelected.length })}
              </span>
            )}
            <CopyButton text={multibuy} label={t("fwAddSelectedMultibuy")} disabled={sizedSelected.length === 0} />
            <span className="text-[10px] text-eve-dim">
              {t("fwCopyPricesLabel", { count: pricedSelected.length })}
            </span>
            <CopyButton
              text={priceList}
              label={t("fwCopySelectedPrices")}
              disabled={pricedSelected.length === 0}
            />
            <button
              className={BTN}
              disabled={busy || generating || sizedSelected.length === 0}
              onClick={() =>
                onRecord(sizedSelected.map((r) => ({ ...r, suggested_qty: effectiveQty(r) })))
              }
              title={t("fwRecordSelectedHint")}
            >
              {t("fwRecordSelected")}
            </button>
          </div>
        </div>
        <div className="px-3 py-1.5 text-[11px] text-eve-dim border-b border-eve-border/60">{ladderNote}</div>
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-eve-dark z-10">
            <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
              <th className={TH}>
                <input
                  type="checkbox"
                  checked={allVisibleSelected}
                  onChange={toggleSelectAllVisible}
                  disabled={visibleIds.length === 0}
                  title={t("fwSelectAllVisibleHint")}
                />
              </th>
              <SortTH k="item" label={t("fwColItem")} right={false} />
              <SortTH k="destroyed" label={t("fwColDestroyed")} hint={t("fwColDestroyedHint")} />
              <SortTH k="kills" label={t("fwColKills")} hint={t("fwColKillsHint")} />
              {hasLongWindow && (
                <SortTH k="destroyedLong" label={t("fwColDestroyedLong")} hint={t("fwColDestroyedLongHint")} />
              )}
              <SortTH k="stocked" label={t("fwColStocked")} hint={t("fwColStockedHint")} />
              <SortTH k="cover" label={t("fwColCover")} hint={t("fwColCoverHint")} />
              <SortTH k="jita" label={t("fwColJita")} hint={t("fwColJitaHint")} />
              <SortTH k="local" label={t("fwColLocal")} hint={t("fwColLocalHint")} />
              <SortTH k="reference" label={t("fwColReference")} hint={t("fwColReferenceHint")} />
              <SortTH k="suggested" label={t("fwColSuggested")} hint={t("fwColSuggestedHint")} />
              <SortTH k="qty" label={t("fwColQty")} hint={t("fwColQtyHint")} />
              <SortTH k="m3" label={t("fwColM3")} hint={t("fwColM3Hint")} />
              <SortTH k="isk" label={t("fwColISK")} hint={t("fwColISKHint")} />
              <SortTH k="profit" label={t("fwColProfit")} hint={t("fwColProfitHint")} />
              <SortTH k="verdict" label={t("fwColVerdict")} hint={t("fwColVerdictHint")} right={false} />
            </tr>
          </thead>
          <tbody>
            {visible.length === 0 && (
              <tr>
                <td className="px-3 py-6 text-center text-eve-dim" colSpan={hasLongWindow ? 16 : 15}>
                  {t("fwGapsEmpty")}
                </td>
              </tr>
            )}
            {visible.map((r, i) => {
              const ship = shipByType.get(r.type_id);
              return (
                <Fragment key={r.type_id}>
                  <tr
                    key={r.type_id}
                    onClick={() => setExpanded(expanded === r.type_id ? null : r.type_id)}
                    className={`border-b border-eve-border/50 hover:bg-eve-accent/5 cursor-pointer ${
                      i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"
                    } ${ship ? "border-l-2 border-l-eve-accent/70" : ""}`}
                  >
                    <td className={TD} onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        checked={selected.has(r.type_id)}
                        onChange={() => toggleSelected(r.type_id)}
                        title={
                          hasRealNumbers(r)
                            ? undefined
                            : t("fwNoQtyYetHint", { reason: r.price_reason || r.verdict_reason })
                        }
                      />
                    </td>
                    <td className={TD}>
                      <div className="flex items-center gap-1.5 flex-wrap">
                        <ItemRef typeId={r.type_id} name={r.type_name} market copyName />
                        {/* The marker is what makes one table work: every gap is
                            listed, and the ones the budget funded are the ones
                            wearing it. */}
                        {ship && (
                          <span className="px-1 py-0.5 rounded-sm border border-eve-accent/50 text-eve-accent text-[10px]">
                            {t("fwBadgeShipping")}
                          </span>
                        )}
                        {/* Included, not merely shippable -- the row is here despite
                            thin evidence or a competitor's depth because you said so,
                            and that is worth reading as a different fact from "the
                            budget funded it". */}
                        {r.included && (
                          <span className="px-1 py-0.5 rounded-sm border border-eve-warning/50 text-eve-warning text-[10px]">
                            {t("fwBadgeIncluded")}
                          </span>
                        )}
                      </div>
                      <div className="text-[10px] text-eve-dim">{r.category}</div>
                      {ship?.trim_reason && <div className="text-[10px] text-eve-warning">{ship.trim_reason}</div>}
                    </td>
                    {/* Two rates, and the accent marks the one that actually set
                        this row's cover, verdict and quantity. Usually that is
                        the campaign's setting; it differs exactly where the
                        chosen window measured nothing and the other did, and
                        that fallback is the case worth being able to see. */}
                    <td
                      className={`${TDR} ${hasLongWindow && r.sized_by !== "long" ? "text-eve-accent" : ""}`}
                      title={hasLongWindow && r.sized_by !== "long" ? t("fwSizedByThis") : undefined}
                    >
                      {formatRate(r.daily_destroyed)}
                    </td>
                    <td className={`${TDR} ${r.kills_with_item < 3 ? "text-eve-warning" : ""}`}>{r.kills_with_item}</td>
                    {hasLongWindow && (
                      <td
                        className={`${TDR} ${r.sized_by === "long" ? "text-eve-accent" : ""}`}
                        title={r.sized_by === "long" ? t("fwSizedByThis") : undefined}
                      >
                        {formatRate(r.daily_destroyed_long)}
                        {/* Three kills over a quarter is a thinner signal than
                            three over a week, so the count carries the same
                            warning colour the short column's does. */}
                        <span className={r.kills_with_item_long < 3 ? "text-eve-warning" : "text-eve-dim"}> ×{r.kills_with_item_long}</span>
                      </td>
                    )}
                    <td className={TDR}>{formatQty(r.stocked_qty)}</td>
                    <td className={TDR}>{formatCover(r.days_of_cover)}</td>
                    <td className={TDR}>{formatPrice(r.jita_best_sell)}</td>
                    <td className={TDR}>
                      {formatPrice(r.local_best_sell)}
                      {r.local_order_count > 0 && <span className="text-eve-dim"> ×{r.local_order_count}</span>}
                    </td>
                    <td className={TDR}>
                      {formatPrice(r.reference_price)}
                      <span className="text-eve-dim"> {formatMarkup(r.reference_markup)}</span>
                    </td>
                    <td className={`${TDR} text-eve-accent`}>
                      <span className="inline-flex items-center justify-end gap-1">
                        <span>
                          {formatPrice(r.suggested_price)}
                          <span className="text-eve-dim"> {formatMarkup(r.suggested_markup)}</span>
                        </span>
                        {r.suggested_price > 0 && (
                          <CopyPrice
                            value={r.suggested_price}
                            step={priceStep(r.suggested_price)}
                            label={t("fwCopyPrice")}
                            reveal="hover"
                          />
                        )}
                      </span>
                    </td>
                    {/* Three quantities can differ, and the difference is the
                        information: what cover asked for, what the minimum lot
                        raised it to, and what the budget could actually fund. */}
                    <td className={TDR}>
                      {/* Checking a row hands you its quantity -- 69 Caracals is
                          a real suggestion and still a real hassle to haul, and
                          shrinking it to 5 is as legitimate a choice as raising
                          a covered row's zero. Pre-filled with the engine's own
                          number where it has one, or with recent losses capped
                          at the minimum-lot floor where it does not, so the box
                          never opens empty when there is a sane guess to start
                          from. */}
                      {selected.has(r.type_id) ? (
                        <input
                          type="number"
                          min={0}
                          step={1}
                          className={`${INPUT} w-20 text-right`}
                          value={
                            manualQty.has(r.type_id)
                              ? manualQty.get(r.type_id)
                              : defaultQty(r) > 0
                                ? defaultQty(r)
                                : ""
                          }
                          placeholder={t("fwManualQtyPlaceholder")}
                          title={r.suggested_qty > 0 ? t("fwEditQtyHint") : t("fwManualQtyHint")}
                          onClick={(e) => e.stopPropagation()}
                          onChange={(e) => setQtyOverride(r.type_id, e.target.value)}
                        />
                      ) : ship && ship.ship_qty !== r.suggested_qty ? (
                        <>
                          <span className="text-eve-accent">{formatQty(ship.ship_qty)}</span>
                          <span className="text-eve-dim"> / {formatQty(r.suggested_qty)}</span>
                        </>
                      ) : (
                        formatQty(r.suggested_qty)
                      )}
                      {r.qty_reason && (
                        <span className="text-eve-warning" title={r.qty_reason}>
                          {" ↑"}
                        </span>
                      )}
                    </td>
                    {/* These three read the server's own total by default, and
                        only recompute from the per-unit rate for a row you
                        gave your own quantity to. */}
                    <td className={TDR}>{formatM3(rowM3(r))}</td>
                    <td className={TDR}>{formatISK(rowCostISK(r))}</td>
                    {/* Profit is what the row earns if it all sells; margin is the
                        rate, and it is the figure that survives a change of
                        quantity. A covered row shows a rate with no total, which is
                        the honest pair: worth selling, nothing to send. */}
                    <td className={`${TDR} ${r.unit_profit_isk > 0 ? "text-profit" : ""}`}>
                      {rowProfitISK(r) > 0 ? formatISK(rowProfitISK(r)) : "—"}
                      <div className="text-[10px] text-eve-dim">{formatPct(r.margin_pct)}</div>
                    </td>
                    <td className={`${TD} ${verdictTone(r.verdict)}`}>{r.verdict}</td>
                  </tr>
                  {expanded === r.type_id && (
                    <tr key={`${r.type_id}-detail`} className="bg-eve-dark/80 border-b border-eve-border/50">
                      <td colSpan={hasLongWindow ? 16 : 15} className="px-3 py-2">
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-x-6 gap-y-1 text-[11px]">
                          <div className="text-eve-dim">
                            {t("fwWhyPrice")}: <span className="text-eve-text">{r.price_reason || r.price_rule}</span>
                          </div>
                          <div className="text-eve-dim">
                            {t("fwWhyVerdict")}: <span className="text-eve-text">{r.verdict_reason}</span>
                          </div>
                          {/* A raised lot is a bet that destruction understates
                              demand. It says so here in full: both quantities and
                              the cover the larger one implies. */}
                          {r.qty_reason && (
                            <div className="text-eve-dim md:col-span-2">
                              {t("fwWhyQty")}: <span className="text-eve-warning">{r.qty_reason}</span>
                            </div>
                          )}
                          <div className="text-eve-dim">
                            {t("fwLandedCost")}:{" "}
                            <span className="font-mono text-eve-text">{formatPrice(r.landed_cost)}</span>
                            {" · "}
                            {t("fwFloorPrice")}:{" "}
                            <span className="font-mono text-eve-text">{formatPrice(r.floor_price)}</span>
                            {" · "}
                            {t("fwReferenceSource")}: <span className="text-eve-text">{r.reference_source || "—"}</span>
                          </div>
                          <div className="text-eve-dim">
                            {t("fwCompetingBelow", {
                              orders: r.competing_orders_below,
                              units: r.competing_units_below.toLocaleString(),
                            })}
                          </div>
                          {/* The margin, shown as the subtraction it is, so it can
                              be checked against the market window rather than
                              taken on trust. */}
                          <div className="text-eve-dim">
                            {t("fwNetUnit")}:{" "}
                            <span className="font-mono text-eve-text">{formatPrice(r.net_unit_isk)}</span>
                            {" − "}
                            {t("fwLandedCost")}:{" "}
                            <span className="font-mono text-eve-text">{formatPrice(r.landed_cost)}</span>
                            {" = "}
                            <span className="font-mono text-eve-text">{formatPrice(r.unit_profit_isk)}</span>{" "}
                            <span className="text-eve-dim">({formatPct(r.margin_pct)})</span>
                          </div>
                          {/* What the shipment actually funded, if anything. */}
                          {ship && (
                            <div className="text-eve-dim md:col-span-2">
                              {t("fwShipped", {
                                qty: formatQty(ship.ship_qty),
                                planned: formatQty(ship.planned_qty),
                                cost: formatISK(ship.ship_cost_isk),
                                m3: formatM3(ship.ship_cargo_m3),
                                profit: formatISK(ship.ship_profit_isk),
                              })}
                            </div>
                          )}
                          {/* Your judgment overriding the model's, for this one type.
                              Included unlocks a real price and quantity past thin
                              evidence or a competitor's depth; Excluded drops the
                              type from every future plan until removed from the
                              Settings chip list. Both regenerate immediately, rather
                              than waiting for a Regenerate click you would have to
                              remember to make. */}
                          <div className="text-eve-dim md:col-span-2 flex items-center gap-2 flex-wrap">
                            <span>{t("fwOverrideThisItem")}:</span>
                            <button
                              className={BTN}
                              disabled={busy || generating}
                              onClick={(e) => {
                                e.stopPropagation();
                                void onToggleTypeOverride(r.type_id, r.type_name, "include");
                              }}
                              title={t("fwAlwaysShipHint")}
                            >
                              {includedTypeIds.has(r.type_id) ? t("fwAlwaysShipOn") : t("fwAlwaysShip")}
                            </button>
                            <button
                              className={BTN}
                              disabled={busy || generating}
                              onClick={(e) => {
                                e.stopPropagation();
                                void onToggleTypeOverride(r.type_id, r.type_name, "exclude");
                              }}
                              title={t("fwNeverShipHint")}
                            >
                              {t("fwNeverShip")}
                            </button>
                            {generating && <span className="text-eve-dim">{t("fwRepricing")}</span>}
                          </div>
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* -------------------------------------------------------------------- lots */

function LotsPanel({
  lots,
  busy,
  onAdvance,
  onDelete,
  onBulk,
}: {
  lots: FWLot[];
  busy: boolean;
  onAdvance: (lotID: number, state: string) => void;
  onDelete: (lotID: number) => void;
  onBulk: (lotIDs: number[], action: "state" | "delete", state?: string) => void;
}) {
  const { t } = useI18n();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [confirmDelete, setConfirmDelete] = useState<number[] | null>(null);

  const sorted = useMemo(
    () =>
      [...lots].sort(
        (a, b) => LOT_STATES.indexOf(a.state) - LOT_STATES.indexOf(b.state) || a.type_name.localeCompare(b.type_name),
      ),
    [lots],
  );

  // A lot the campaign no longer has cannot stay selected. Without this, moving
  // a selection and then acting again would send ids the server would reject --
  // and because the batch is all-or-nothing, one stale id would fail the lot.
  const liveIDs = useMemo(() => new Set(lots.map((l) => l.lot_id)), [lots]);
  useEffect(() => {
    setSelected((prev) => {
      const next = new Set([...prev].filter((id) => liveIDs.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [liveIDs]);

  const selectedIDs = useMemo(() => sorted.filter((l) => selected.has(l.lot_id)).map((l) => l.lot_id), [sorted, selected]);
  const allSelected = sorted.length > 0 && selectedIDs.length === sorted.length;

  const toggle = (lotID: number) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(lotID)) next.delete(lotID);
      else next.add(lotID);
      return next;
    });

  const selectAll = () => setSelected(allSelected ? new Set() : new Set(sorted.map((l) => l.lot_id)));

  // Selecting by state is the shape the work actually arrives in: everything
  // bought becomes everything in transit on one trip, and everything listed
  // becomes sold over a week. Picking a state beats ticking fourteen boxes.
  const byState = useMemo(() => {
    const counts = new Map<string, number[]>();
    for (const lot of sorted) {
      const ids = counts.get(lot.state) ?? [];
      ids.push(lot.lot_id);
      counts.set(lot.state, ids);
    }
    return LOT_STATES.filter((s) => counts.has(s)).map((s) => ({ state: s, ids: counts.get(s) ?? [] }));
  }, [sorted]);

  // What the selection is worth, so a bulk delete is not a blind one. Sold and
  // pulled lots have already released their capital, which is why they are not
  // counted -- the same rule the per-row committed cell uses.
  const selectedCommitted = useMemo(
    () =>
      sorted
        .filter((l) => selected.has(l.lot_id) && l.state !== "sold" && l.state !== "pulled")
        .reduce((sum, l) => sum + l.qty_remaining * l.unit_cost_isk, 0),
    [sorted, selected],
  );

  if (lots.length === 0) {
    return <div className={`${PANEL} px-3 py-6 text-center text-xs text-eve-dim`}>{t("fwNoLots")}</div>;
  }

  return (
    <div className="space-y-2">
      {/* The bulk bar. It is always mounted rather than appearing on the first
          tick, because a control that materialises under the cursor is how you
          click the wrong thing -- and here the wrong thing deletes lots. */}
      <div className={`${PANEL} px-3 py-2 flex flex-wrap items-center gap-x-3 gap-y-2 text-xs`}>
        <span className="text-eve-dim">
          {t("fwSelectedCount", { count: selectedIDs.length })}
          {selectedCommitted > 0 && (
            <span className="ml-1 font-mono text-eve-text">({formatISK(selectedCommitted)})</span>
          )}
        </span>

        <span className="flex items-center gap-1">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwSelectByState")}</span>
          {byState.map((g) => (
            <button
              key={g.state}
              className={BTN}
              disabled={busy}
              onClick={() => setSelected(new Set(g.ids))}
              title={t("fwSelectStateHint", { state: g.state, count: g.ids.length })}
            >
              {g.state} <span className="text-eve-dim">{g.ids.length}</span>
            </button>
          ))}
        </span>

        <span className="flex items-center gap-1 ml-auto">
          <span className="text-[10px] uppercase tracking-wider text-eve-dim">{t("fwMoveAllTo")}</span>
          {LOT_STATES.map((s) => (
            <button
              key={s}
              className={BTN}
              disabled={busy || selectedIDs.length === 0}
              onClick={() => onBulk(selectedIDs, "state", s)}
            >
              {s}
            </button>
          ))}
          <button
            className={BTN}
            disabled={busy || selectedIDs.length === 0}
            onClick={() => setConfirmDelete(selectedIDs)}
          >
            {t("fwDelete")}
          </button>
        </span>
      </div>

      <div className={`${PANEL} overflow-auto`}>
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-eve-dark z-10">
            <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border">
              <th className="px-2 py-1.5 w-6">
                <input
                  type="checkbox"
                  checked={allSelected}
                  onChange={selectAll}
                  disabled={busy}
                  aria-label={t("fwSelectAll")}
                  title={t("fwSelectAll")}
                />
              </th>
              <th className={TH}>{t("fwColItem")}</th>
              <th className={TH}>{t("fwColState")}</th>
              <th className={THR}>{t("fwColQty")}</th>
              <th className={THR}>{t("fwColRemaining")}</th>
              <th className={THR}>{t("fwColUnitCost")}</th>
              <th className={THR}>{t("fwColCommitted")}</th>
              <th className={THR}>{t("fwColListedPrice")}</th>
              <th className={TH}>{t("fwColHolder")}</th>
              <th className={THR}></th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((lot, i) => {
              const next = NEXT_STATE[lot.state];
              const committed =
                lot.state === "sold" || lot.state === "pulled" ? 0 : lot.qty_remaining * lot.unit_cost_isk;
              const isSelected = selected.has(lot.lot_id);
              return (
                <tr
                  key={lot.lot_id}
                  className={`border-b border-eve-border/50 ${
                    isSelected ? "bg-eve-accent/10" : i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"
                  }`}
                >
                  <td className="px-2 py-1.5">
                    <input
                      type="checkbox"
                      checked={isSelected}
                      onChange={() => toggle(lot.lot_id)}
                      disabled={busy}
                      aria-label={lot.type_name}
                    />
                  </td>
                  <td className={TD}>
                    <ItemRef typeId={lot.type_id} name={lot.type_name} market copyName />
                  </td>
                  <td className={`${TD} text-eve-text`}>{lot.state}</td>
                  <td className={TDR}>{formatQty(lot.qty)}</td>
                  <td className={TDR}>{formatQty(lot.qty_remaining)}</td>
                  <td className={TDR}>{formatPrice(lot.unit_cost_isk)}</td>
                  <td className={TDR}>{committed > 0 ? formatISK(committed) : "—"}</td>
                  <td className={TDR}>{formatPrice(lot.listed_price)}</td>
                  <td className={`${TD} text-eve-dim`}>{lot.holder_name || lot.holder_owner_kind || "—"}</td>
                  <td className="px-3 py-1.5 text-right whitespace-nowrap">
                    {next && (
                      <>
                        <button className={BTN} disabled={busy} onClick={() => onAdvance(lot.lot_id, next)}>
                          {t("fwAdvanceTo", { state: next })}
                        </button>{" "}
                      </>
                    )}
                    <select
                      className={`${INPUT} h-6`}
                      value={lot.state}
                      disabled={busy}
                      onChange={(e) => onAdvance(lot.lot_id, e.target.value)}
                    >
                      {LOT_STATES.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>{" "}
                    <button className={BTN} disabled={busy} onClick={() => onDelete(lot.lot_id)}>
                      {t("fwDelete")}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {/* Deleting lots throws away the record of what the campaign spent, and in
          bulk it does so for rows that are no longer all on screen. A state move
          needs no confirmation because every state is reachable from every
          other; this is the one action that is not. */}
      {confirmDelete && (
        <ConfirmDialog
          open={true}
          title={t("fwConfirmBulkDeleteTitle")}
          message={t("fwConfirmBulkDelete", { count: confirmDelete.length })}
          confirmText={t("fwDelete")}
          variant="danger"
          onConfirm={() => {
            onBulk(confirmDelete, "delete");
            setConfirmDelete(null);
          }}
          onClose={() => setConfirmDelete(null)}
        />
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ orders */

function OrdersPanel({
  orders,
  loading,
  busy,
  onRefresh,
  onApply,
}: {
  orders: FWOrdersResponse | null;
  loading: boolean;
  busy: boolean;
  onRefresh: () => void;
  onApply: (lotIDs?: number[]) => void;
}) {
  const { t } = useI18n();
  const matches = orders?.reconciliation?.matches ?? [];
  const desk = new Map((orders?.desk_orders ?? []).map((o) => [o.order_id, o]));

  return (
    <div className="flex flex-col gap-3">
      <WarningList
        title={t("fwWarnings")}
        items={[...(orders?.warnings ?? []), ...(orders?.reconciliation?.warnings ?? [])]}
      />
      <div className={PANEL}>
        <div className="px-3 py-2 flex items-center justify-between gap-3 border-b border-eve-border/60">
          <div className="text-[11px] text-eve-dim">
            {Object.entries(orders?.counts ?? {})
              .sort(([a], [b]) => a.localeCompare(b))
              .map(([k, v]) => `${v} ${k}`)
              .join(", ") || t("fwNoMatches")}
          </div>
          <div className="flex items-center gap-2">
            <button className={BTN} onClick={onRefresh} disabled={loading}>
              {t("fwRefreshOrders")}
            </button>
            <button className={BTN_ACCENT} onClick={() => onApply()} disabled={busy || matches.length === 0}>
              {t("fwApplyAll")}
            </button>
          </div>
        </div>
        {loading && <LoadingBlock label={t("fwReadingBook")} />}
        {!loading && (
          <table className="w-full text-xs">
            <thead>
              <tr className="text-eve-dim text-[10px] uppercase tracking-wider border-b border-eve-border/60">
                <th className={TH}>{t("fwColItem")}</th>
                <th className={TH}>{t("fwColStatus")}</th>
                <th className={THR}>{t("fwColRemaining")}</th>
                <th className={THR}>{t("fwColFilled")}</th>
                <th className={TH}>{t("fwColDeskVerdict")}</th>
                <th className={THR}></th>
              </tr>
            </thead>
            <tbody>
              {matches.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-3 py-6 text-center text-eve-dim">
                    {t("fwNoMatches")}
                  </td>
                </tr>
              )}
              {matches.map((m, i) => {
                const row = m.primary_order_id ? desk.get(m.primary_order_id) : undefined;
                return (
                  <tr
                    key={m.lot_id}
                    className={`border-b border-eve-border/50 ${i % 2 === 0 ? "bg-eve-panel" : "bg-eve-dark"}`}
                  >
                    <td className={TD}>
                      <ItemRef typeId={m.type_id} name={m.type_name} market copyName />
                      {m.note && <div className="text-[10px] text-eve-dim">{m.note}</div>}
                    </td>
                    <td className={`${TD} ${m.status === "gone" ? "text-eve-warning" : "text-eve-text"}`}>
                      {m.status}
                      {m.suggested_state && <span className="text-eve-dim"> → {m.suggested_state}</span>}
                    </td>
                    <td className={TDR}>
                      {formatQty(m.qty_remaining)}
                      {m.suggested_qty_remaining !== m.qty_remaining && (
                        <span className="text-eve-accent"> → {formatQty(m.suggested_qty_remaining)}</span>
                      )}
                    </td>
                    <td className={TDR}>{m.filled_qty > 0 ? formatQty(m.filled_qty) : "—"}</td>
                    <td className={`${TD} text-[11px]`}>
                      {row ? (
                        <>
                          <span className="text-eve-text">{row.recommendation}</span>
                          <span className="text-eve-dim"> — {row.reason}</span>
                          {row.owner_kind === "corporation" && (
                            <span className="text-eve-dim">
                              {" "}
                              ({row.character_name}
                              {row.fee_character_name ? `, ${t("fwFeesOf", { name: row.fee_character_name })}` : ""})
                            </span>
                          )}
                        </>
                      ) : (
                        <span className="text-eve-dim">{t("fwNoDeskRow")}</span>
                      )}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      <button
                        className={BTN}
                        disabled={busy || m.status === "gone" || m.status === "resting"}
                        onClick={() => onApply([m.lot_id])}
                      >
                        {t("fwApply")}
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

/* --------------------------------------------------------------- occupancy */

function OccupancyPanel({ occupancy }: { occupancy: NonNullable<FWPlanResponse["plan"]>["occupancy"] }) {
  const { t } = useI18n();
  const list = occupancy ?? [];
  if (list.length === 0) return null;
  return (
    <div className={`${PANEL} p-3`}>
      <div className="text-xs text-eve-accent">{t("fwOccupancy")}</div>
      <div className="text-[10px] text-eve-dim mb-2">{t("fwOccupancyHint")}</div>
      <div className="flex flex-wrap gap-2">
        {list.map((o) => (
          <span key={o.system_id} className="px-2 py-1 rounded-sm border border-eve-border/60 text-[11px] text-eve-dim">
            <span className="text-eve-text">{o.system_name || o.system_id}</span> — {militiaName(o.occupier_faction_id)}
            {o.contested ? ` · ${o.contested}` : ""}
          </span>
        ))}
      </div>
    </div>
  );
}
