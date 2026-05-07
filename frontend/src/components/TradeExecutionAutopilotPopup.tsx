import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Modal } from "./Modal";
import { useGlobalToast } from "./Toast";
import { useI18n } from "@/lib/i18n";
import { formatISK, formatMargin } from "@/lib/format";
import {
  createPaperTrade,
  getCharacterInfo,
  getExecutionPlan,
  openMarketInGame,
  setWaypointInGame,
} from "@/lib/api";
import { handleEveUIError } from "@/lib/handleEveUIError";
import type {
  CharacterInfo,
  ExecutionPlanResult,
  FlipResult,
  PaperTrade,
  PaperTradeCreatePayload,
} from "@/lib/types";

type ShipKey = "sunesis" | "blockade_runner" | "dst" | "freighter" | "custom";
type RouteMode = "fastest" | "safest" | "isk_hour";
type ExecutionMode = "hauling" | "station";

type ShipProfile = {
  key: ShipKey;
  label: string;
  cargoM3: number;
  minutesPerJump: number;
  dockMinutes: number;
  safetyDelayMinutes: number;
};

type DepthFill = {
  quantity: number;
  total: number;
  avg: number;
};

type FillAssumption = {
  days: number;
  source: string;
  detail: string;
};

type QuantityGate = {
  label: string;
  quantity: number;
  detail: string;
};

type RouteVariant = {
  key: RouteMode;
  label: string;
  minutes: number;
  trips: number;
  riskPenaltyPct: number;
  iskPerHour: number;
  riskAdjustedIskPerHour: number;
};

const SHIP_PROFILES: ShipProfile[] = [
  {
    key: "sunesis",
    label: "Sunesis",
    cargoM3: 600,
    minutesPerJump: 1.25,
    dockMinutes: 5,
    safetyDelayMinutes: 2,
  },
  {
    key: "blockade_runner",
    label: "Blockade Runner",
    cargoM3: 10000,
    minutesPerJump: 1.55,
    dockMinutes: 6,
    safetyDelayMinutes: 4,
  },
  {
    key: "dst",
    label: "Deep Space Transport",
    cargoM3: 62500,
    minutesPerJump: 2.1,
    dockMinutes: 8,
    safetyDelayMinutes: 8,
  },
  {
    key: "freighter",
    label: "Freighter",
    cargoM3: 845000,
    minutesPerJump: 3.8,
    dockMinutes: 12,
    safetyDelayMinutes: 15,
  },
  {
    key: "custom",
    label: "Custom cargo",
    cargoM3: 10000,
    minutesPerJump: 1.8,
    dockMinutes: 7,
    safetyDelayMinutes: 6,
  },
];

const DEFAULT_MAX_ISK = 1_000_000_000;

export interface TradeExecutionAutopilotPopupProps {
  open: boolean;
  onClose: () => void;
  row: FlipResult | null;
  mode?: ExecutionMode;
  isLoggedIn?: boolean;
  brokerFeePercent?: number;
  salesTaxPercent?: number;
  splitTradeFees?: boolean;
  buyBrokerFeePercent?: number;
  sellBrokerFeePercent?: number;
  buySalesTaxPercent?: number;
  sellSalesTaxPercent?: number;
  onJournalCreated?: () => void;
}

function finiteNumber(value: unknown, fallback = 0): number {
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
}

function clamp(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return min;
  if (value < min) return min;
  if (value > max) return max;
  return value;
}

function intInput(value: unknown, fallback = 1): number {
  return Math.max(1, Math.floor(finiteNumber(value, fallback)));
}

function defaultQuantityForRow(row: FlipResult | null): number {
  if (!row) return 1;
  const filled = Math.floor(finiteNumber(row.FilledQty));
  if (filled > 0) return filled;
  const planned = Math.floor(finiteNumber(row.UnitsToBuy));
  if (planned > 0) return planned;
  const buyRemain = Math.floor(finiteNumber(row.BuyOrderRemain));
  const sellRemain = Math.floor(finiteNumber(row.SellOrderRemain));
  if (buyRemain > 0 && sellRemain > 0) return Math.min(buyRemain, sellRemain);
  return Math.max(1, buyRemain, sellRemain);
}

function planFilledQuantity(plan: ExecutionPlanResult | null): number {
  if (!plan) return 0;
  return plan.depth_levels?.reduce((sum, level) => sum + Math.max(0, level.volume_filled || 0), 0) ?? 0;
}

function fillFromPlan(plan: ExecutionPlanResult | null, quantity: number, fallbackPrice: number): DepthFill {
  const qty = Math.max(0, Math.floor(quantity));
  if (qty <= 0) {
    return { quantity: 0, total: 0, avg: 0 };
  }
  if (!plan || !Array.isArray(plan.depth_levels) || plan.depth_levels.length === 0) {
    const avg = Math.max(0, fallbackPrice);
    return { quantity: qty, total: avg * qty, avg };
  }

  let remaining = qty;
  let total = 0;
  let filled = 0;
  for (const level of plan.depth_levels) {
    if (remaining <= 0) break;
    const available = Math.max(0, Math.floor(level.volume_filled || level.volume || 0));
    if (available <= 0) continue;
    const take = Math.min(remaining, available);
    total += Math.max(0, level.price) * take;
    filled += take;
    remaining -= take;
  }

  if (filled < qty) {
    const price = Math.max(0, plan.expected_price || plan.best_price || fallbackPrice);
    total += price * (qty - filled);
    filled = qty;
  }
  return { quantity: filled, total, avg: filled > 0 ? total / filled : 0 };
}

function routeDangerPenalty(row: FlipResult | null): number {
  const danger = String(row?.RouteSafetyDanger ?? "").toLowerCase();
  if (danger === "red") return 0.16;
  if (danger === "yellow") return 0.08;
  const kills = finiteNumber(row?.RouteSafetyKills);
  if (kills >= 8) return 0.12;
  if (kills >= 3) return 0.06;
  return 0.02;
}

function travelJumps(row: FlipResult | null): number {
  if (!row) return 0;
  const total = Math.floor(finiteNumber(row.TotalJumps));
  if (total > 0) return total;
  return Math.max(0, Math.floor(finiteNumber(row.BuyJumps)) + Math.floor(finiteNumber(row.SellJumps)));
}

function buildRouteVariants(
  row: FlipResult | null,
  profile: ShipProfile,
  trips: number,
  expectedProfit: number,
): RouteVariant[] {
  const safeTrips = Math.max(1, trips);
  const jumps = Math.max(0, travelJumps(row));
  const dangerPenalty = routeDangerPenalty(row);
  const baseTripMinutes = Math.max(4, jumps * profile.minutesPerJump + profile.dockMinutes);
  const mk = (
    key: RouteMode,
    label: string,
    minutesFactor: number,
    extraDelay: number,
    riskFactor: number,
  ): RouteVariant => {
    const minutes = Math.max(1, safeTrips * (baseTripMinutes * minutesFactor + extraDelay));
    const riskPenaltyPct = clamp(dangerPenalty * riskFactor * 100, 0, 95);
    const iskPerHour = expectedProfit / (minutes / 60);
    return {
      key,
      label,
      minutes,
      trips: safeTrips,
      riskPenaltyPct,
      iskPerHour,
      riskAdjustedIskPerHour: iskPerHour * Math.max(0, 1 - riskPenaltyPct / 100),
    };
  };

  const fastest = mk("fastest", "Fastest", 1, 0, 1.25);
  const safest = mk("safest", "Safest", 1.28, profile.safetyDelayMinutes, 0.45);
  const iskHour = fastest.riskAdjustedIskPerHour >= safest.riskAdjustedIskPerHour
    ? { ...fastest, key: "isk_hour" as RouteMode, label: "Max ISK/hour" }
    : { ...safest, key: "isk_hour" as RouteMode, label: "Max ISK/hour" };
  return [fastest, safest, iskHour];
}

function fillAssumptionForRow(row: FlipResult | null, quantity: number): FillAssumption {
  const direct = finiteNumber(row?.FillTimeDays);
  if (direct > 0) {
    return {
      days: direct,
      source: "scan fill-time",
      detail: "Scanner liquidity model already provided a fill-time estimate.",
    };
  }
  const dailyVolume = finiteNumber(row?.DailyVolume);
  if (dailyVolume > 0) {
    return {
      days: Math.max(0.05, quantity / dailyVolume),
      source: "daily volume",
      detail: `${shortNumber(quantity)} units / ${shortNumber(dailyVolume)} daily volume.`,
    };
  }
  const velocity = finiteNumber(row?.Velocity);
  if (velocity > 0) {
    return {
      days: Math.max(0.05, quantity / velocity),
      source: "scan velocity",
      detail: `${shortNumber(quantity)} units / ${shortNumber(velocity)} scan velocity.`,
    };
  }
  return {
    days: 1,
    source: "fallback",
    detail: "No fill-time, daily volume, or velocity signal was available.",
  };
}

function fillTimeDaysForRow(row: FlipResult | null, quantity: number): number {
  return fillAssumptionForRow(row, quantity).days;
}

function buildStationVariants(row: FlipResult | null, quantity: number, expectedProfit: number): RouteVariant[] {
  const baseMinutes = Math.max(30, fillTimeDaysForRow(row, quantity) * 1440);
  const mk = (key: RouteMode, label: string, minutesFactor: number, riskPenaltyPct: number): RouteVariant => {
    const minutes = Math.max(30, baseMinutes * minutesFactor);
    const iskPerHour = expectedProfit / (minutes / 60);
    return {
      key,
      label,
      minutes,
      trips: 0,
      riskPenaltyPct,
      iskPerHour,
      riskAdjustedIskPerHour: iskPerHour * Math.max(0, 1 - riskPenaltyPct / 100),
    };
  };

  const fastest = mk("fastest", "Fast fill", 0.65, 14);
  const safest = mk("safest", "Safer spread", 1.35, 6);
  const best = fastest.riskAdjustedIskPerHour >= safest.riskAdjustedIskPerHour ? fastest : safest;
  return [fastest, safest, { ...best, key: "isk_hour", label: "Max ISK/hour" }];
}

function shortNumber(value: number): string {
  if (!Number.isFinite(value)) return "0";
  return value.toLocaleString(undefined, { maximumFractionDigits: 0 });
}

function plannedNote(lines: string[]): string {
  return lines.filter(Boolean).join("\n").slice(0, 2000);
}

function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[10px] uppercase tracking-wider text-eve-dim">{label}</span>
      {children}
    </label>
  );
}

function Metric({
  label,
  value,
  tone = "neutral",
}: {
  label: string;
  value: string;
  tone?: "neutral" | "good" | "bad" | "warn";
}) {
  const toneClass =
    tone === "good"
      ? "text-green-400"
      : tone === "bad"
        ? "text-red-300"
        : tone === "warn"
          ? "text-yellow-300"
          : "text-eve-text";
  return (
    <div className="border border-eve-border bg-eve-dark/50 px-3 py-2">
      <div className="text-[10px] uppercase tracking-wider text-eve-dim">{label}</div>
      <div className={`mt-1 font-mono text-sm ${toneClass}`}>{value}</div>
    </div>
  );
}

function InfoRow({
  label,
  value,
  tone = "neutral",
}: {
  label: string;
  value: string;
  tone?: "neutral" | "good" | "bad" | "warn";
}) {
  const toneClass =
    tone === "good"
      ? "text-green-400"
      : tone === "bad"
        ? "text-red-300"
        : tone === "warn"
          ? "text-yellow-300"
          : "text-eve-text";
  return (
    <div className="flex justify-between gap-3">
      <span className="text-eve-dim">{label}</span>
      <span className={`font-mono text-right break-words max-w-[65%] ${toneClass}`}>{value}</span>
    </div>
  );
}

function Panel({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="border border-eve-border bg-eve-dark/50">
      <div className="px-3 py-2 border-b border-eve-border text-xs uppercase tracking-wider text-eve-accent">{title}</div>
      <div className="p-3 space-y-2 text-sm">{children}</div>
    </div>
  );
}

export function TradeExecutionAutopilotPopup({
  open,
  onClose,
  row,
  mode = "hauling",
  isLoggedIn = false,
  brokerFeePercent = 0,
  salesTaxPercent = 0,
  splitTradeFees = false,
  buyBrokerFeePercent,
  sellBrokerFeePercent,
  buySalesTaxPercent,
  sellSalesTaxPercent,
  onJournalCreated,
}: TradeExecutionAutopilotPopupProps) {
  const { addToast } = useGlobalToast();
  const { t } = useI18n();
  const isStationMode = mode === "station";
  const [quantity, setQuantity] = useState(1);
  const [shipKey, setShipKey] = useState<ShipKey>("blockade_runner");
  const [customCargo, setCustomCargo] = useState(10000);
  const [routeMode, setRouteMode] = useState<RouteMode>("isk_hour");
  const [maxISK, setMaxISK] = useState(DEFAULT_MAX_ISK);
  const [reservePct, setReservePct] = useState(20);
  const [maxExposurePct, setMaxExposurePct] = useState(25);
  const [walletOverride, setWalletOverride] = useState(0);
  const [planBuy, setPlanBuy] = useState<ExecutionPlanResult | null>(null);
  const [planSell, setPlanSell] = useState<ExecutionPlanResult | null>(null);
  const [character, setCharacter] = useState<CharacterInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [characterLoading, setCharacterLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const [createdTrade, setCreatedTrade] = useState<PaperTrade | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const requestSeqRef = useRef(0);

  const selectedProfile = useMemo(() => {
    const profile = SHIP_PROFILES.find((item) => item.key === shipKey) ?? SHIP_PROFILES[1];
    if (profile.key !== "custom") return profile;
    return { ...profile, cargoM3: Math.max(0, finiteNumber(customCargo)) };
  }, [customCargo, shipKey]);

  const buyFeePct = clamp(splitTradeFees ? (buyBrokerFeePercent ?? brokerFeePercent) : brokerFeePercent, 0, 100);
  const sellFeePct = clamp(splitTradeFees ? (sellBrokerFeePercent ?? brokerFeePercent) : brokerFeePercent, 0, 100);
  const buyTaxPct = clamp(splitTradeFees ? (buySalesTaxPercent ?? 0) : 0, 0, 100);
  const sellTaxPct = clamp(splitTradeFees ? (sellSalesTaxPercent ?? salesTaxPercent) : salesTaxPercent, 0, 100);

  const planScope = useMemo(() => {
    const typeID = row?.TypeID ?? 0;
    const buyRegionID = row?.BuyRegionID || row?.SellRegionID || 0;
    const sellRegionID = row?.SellRegionID || buyRegionID;
    const buyLocationID = row?.BuyLocationID ?? 0;
    const sellLocationID = row?.SellLocationID ?? 0;
    return {
      typeID,
      buyRegionID,
      sellRegionID,
      buyLocationID,
      sellLocationID,
      key: `${mode}:${typeID}:${buyRegionID}:${buyLocationID}:${sellRegionID}:${sellLocationID}`,
    };
  }, [
    mode,
    row?.BuyLocationID,
    row?.BuyRegionID,
    row?.SellLocationID,
    row?.SellRegionID,
    row?.TypeID,
  ]);

  const fetchPlans = useCallback(
    (qty: number) => {
      if (!planScope.typeID || !planScope.buyRegionID || !planScope.sellRegionID) return;
      const buySideIsBuy = !isStationMode;
      const sellSideIsBuy = isStationMode;
      const requestID = ++requestSeqRef.current;
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      setLoading(true);
      setError("");
      Promise.all([
        getExecutionPlan({
          type_id: planScope.typeID,
          region_id: planScope.buyRegionID,
          location_id: planScope.buyLocationID,
          quantity: qty,
          is_buy: buySideIsBuy,
          signal: controller.signal,
        }),
        getExecutionPlan({
          type_id: planScope.typeID,
          region_id: planScope.sellRegionID,
          location_id: planScope.sellLocationID,
          quantity: qty,
          is_buy: sellSideIsBuy,
          signal: controller.signal,
        }),
      ])
        .then(([buy, sell]) => {
          if (controller.signal.aborted || requestID !== requestSeqRef.current) return;
          setPlanBuy(buy);
          setPlanSell(sell);
        })
        .catch((e: unknown) => {
          if (controller.signal.aborted || requestID !== requestSeqRef.current) return;
          setError(e instanceof Error ? e.message : "Execution plan failed");
          setPlanBuy(null);
          setPlanSell(null);
        })
        .finally(() => {
          if (controller.signal.aborted || requestID !== requestSeqRef.current) return;
          setLoading(false);
        });
    },
    [isStationMode, planScope],
  );

  useEffect(() => {
    if (!open || !row) {
      abortRef.current?.abort();
      return;
    }
    const q = defaultQuantityForRow(row);
    setQuantity(q);
    setPlanBuy(null);
    setPlanSell(null);
    setCreatedTrade(null);
    fetchPlans(q);
    return () => abortRef.current?.abort();
  }, [fetchPlans, open, planScope.key]);

  useEffect(() => {
    if (!open || !isLoggedIn) {
      setCharacter(null);
      return;
    }
    setCharacterLoading(true);
    getCharacterInfo()
      .then((data) => {
        setCharacter(data);
        if (data.wallet > 0) setWalletOverride(0);
      })
      .catch(() => setCharacter(null))
      .finally(() => setCharacterLoading(false));
  }, [isLoggedIn, open]);

  const characterExposure = useMemo(() => {
    if (!row) {
      return { assets: 0, buyOrders: 0, sellOrders: 0, openBuyISK: 0, openSellISK: 0 };
    }
    const out = {
      assets: Math.floor(finiteNumber(row.CharacterAssets)),
      buyOrders: Math.floor(finiteNumber(row.CharacterBuyOrders)),
      sellOrders: Math.floor(finiteNumber(row.CharacterSellOrders)),
      openBuyISK: 0,
      openSellISK: 0,
    };
    if (!character) return out;
    out.assets = 0;
    out.buyOrders = 0;
    out.sellOrders = 0;
    out.openBuyISK = 0;
    out.openSellISK = 0;
    for (const asset of character.assets ?? []) {
      if (asset.type_id === row.TypeID && asset.quantity > 0) out.assets += asset.quantity;
    }
    for (const order of character.orders ?? []) {
      if (order.type_id !== row.TypeID || order.volume_remain <= 0) continue;
      if (order.is_buy_order) {
        out.buyOrders += order.volume_remain;
        out.openBuyISK += order.volume_remain * Math.max(0, order.price);
      } else {
        out.sellOrders += order.volume_remain;
        out.openSellISK += order.volume_remain * Math.max(0, order.price);
      }
    }
    return out;
  }, [character, row]);

  const mission = useMemo(() => {
    if (!row) return null;
    const requestedQty = intInput(quantity);
    const buyDepthQty = planBuy ? planFilledQuantity(planBuy) : Math.max(0, Math.floor(finiteNumber(row.FilledQty || row.BuyOrderRemain || requestedQty)));
    const sellDepthQty = planSell ? planFilledQuantity(planSell) : Math.max(0, Math.floor(finiteNumber(row.FilledQty || row.SellOrderRemain || requestedQty)));
    const depthQty = Math.min(requestedQty, Math.max(0, buyDepthQty), Math.max(0, sellDepthQty));

    const wallet = walletOverride > 0 ? walletOverride : Math.max(0, character?.wallet ?? 0);
    const reserveCap = wallet > 0 ? wallet * Math.max(0, 1 - reservePct / 100) : Number.POSITIVE_INFINITY;
    const tradeCap = maxISK > 0 ? maxISK : Number.POSITIVE_INFINITY;
    const exposureValue =
      characterExposure.openBuyISK +
      characterExposure.openSellISK +
      characterExposure.assets * Math.max(0, finiteNumber(row.ExpectedSellPrice || row.SellPrice));
    const exposureCap =
      wallet > 0 && maxExposurePct > 0
        ? Math.max(0, wallet * (maxExposurePct / 100) - exposureValue)
        : Number.POSITIVE_INFINITY;
    const buyFallback = finiteNumber(row.ExpectedBuyPrice || row.BuyPrice);
    const roughBuyPrice = Math.max(0, planBuy?.expected_price || buyFallback);
    const estimatedCapitalPerUnit = roughBuyPrice * (1 + (buyFeePct + buyTaxPct) / 100);
    const qtyFromCapital = (cap: number, enabled: boolean): number => {
      if (!enabled) return requestedQty;
      if (!Number.isFinite(cap)) return requestedQty;
      if (estimatedCapitalPerUnit <= 0) return requestedQty;
      return Math.max(0, Math.floor(cap / estimatedCapitalPerUnit));
    };
    const reserveQty = qtyFromCapital(reserveCap, wallet > 0);
    const tradeQty = qtyFromCapital(tradeCap, maxISK > 0);
    const exposureQty = qtyFromCapital(exposureCap, wallet > 0 && maxExposurePct > 0);
    const capitalQty = Math.min(reserveQty, tradeQty, exposureQty);

    const itemVolume = Math.max(0, finiteNumber(row.Volume));
    const cargoCapacity = isStationMode ? Number.POSITIVE_INFINITY : Math.max(0, selectedProfile.cargoM3);
    const unitsPerTrip = isStationMode ? requestedQty : itemVolume > 0 ? Math.floor(cargoCapacity / itemVolume) : requestedQty;
    const cargoPossible = isStationMode || itemVolume <= 0 || unitsPerTrip > 0;
    const cargoQty = cargoPossible ? requestedQty : 0;
    const executableQty = Math.max(0, Math.min(requestedQty, depthQty, capitalQty, cargoQty));
    const trips = isStationMode ? 0 : executableQty > 0 ? Math.max(1, unitsPerTrip > 0 ? Math.ceil(executableQty / unitsPerTrip) : 1) : 0;

    const buyFill = fillFromPlan(planBuy, executableQty, buyFallback);
    const sellFill = fillFromPlan(planSell, executableQty, finiteNumber(row.ExpectedSellPrice || row.SellPrice));
    const buyFees = buyFill.total * ((buyFeePct + buyTaxPct) / 100);
    const sellFees = sellFill.total * ((sellFeePct + sellTaxPct) / 100);
    const capitalFrozen = buyFill.total + buyFees;
    const expectedProfit = sellFill.total - sellFees - capitalFrozen;
    const roi = capitalFrozen > 0 ? (expectedProfit / capitalFrozen) * 100 : 0;

    const slippageBufferPct = Math.min(8, Math.max(0.01, Math.abs(finiteNumber(planBuy?.slippage_percent)) + Math.abs(finiteNumber(planSell?.slippage_percent))));
    const undercutHaircutPct = isStationMode ? 6 : 0;
    const routeRiskHaircutPct = isStationMode ? 0 : routeDangerPenalty(row) * 100;
    const buyShockPct = Math.min(8, slippageBufferPct + (isStationMode ? 1 : routeRiskHaircutPct / 3));
    const sellHaircutPct = Math.min(18, slippageBufferPct + undercutHaircutPct + routeRiskHaircutPct);
    const worstBuyTotal = buyFill.total * (1 + buyShockPct / 100);
    const worstSellTotal = sellFill.total * (1 - sellHaircutPct / 100);
    const worstBuyFees = worstBuyTotal * ((buyFeePct + buyTaxPct) / 100);
    const worstSellFees = worstSellTotal * ((sellFeePct + sellTaxPct) / 100);
    const worstProfit = worstSellTotal - worstSellFees - worstBuyTotal - worstBuyFees;

    const fillAssumption = fillAssumptionForRow(row, Math.max(1, executableQty || requestedQty));
    const variants = isStationMode
      ? buildStationVariants(row, executableQty, expectedProfit)
      : buildRouteVariants(row, selectedProfile, trips, expectedProfit);
    const selectedVariant =
      variants.find((variant) => variant.key === routeMode) ??
      variants.find((variant) => variant.key === "isk_hour") ??
      variants[0];
    const collateral = capitalFrozen * 1.1;
    const riskPremium = capitalFrozen * (isStationMode ? 0.05 : routeDangerPenalty(row));
    const grossSpreadPerUnit = sellFill.avg - buyFill.avg;
    const grossProfit = grossSpreadPerUnit * executableQty;
    const totalFees = buyFees + sellFees;
    const netPerUnit = executableQty > 0 ? expectedProfit / executableQty : 0;
    const quantityGates: QuantityGate[] = [
      { label: "Requested", quantity: requestedQty, detail: "Quantity entered in the plan." },
      { label: isStationMode ? "Buy-order support" : "Buy depth", quantity: buyDepthQty, detail: "Executable buy side quantity from current orderbook." },
      { label: isStationMode ? "Sell-order support" : "Sell depth", quantity: sellDepthQty, detail: "Executable sell side quantity from current orderbook." },
      { label: "Max ISK/trade", quantity: tradeQty, detail: maxISK > 0 ? `${formatISK(tradeCap)} cap / ${formatISK(estimatedCapitalPerUnit)} est capital per unit.` : "Trade cap not applied." },
      { label: "Reserve wallet", quantity: reserveQty, detail: wallet > 0 ? `${reservePct}% wallet reserve leaves ${formatISK(reserveCap)} usable.` : "Wallet not loaded; reserve limit not applied." },
      { label: "Max exposure/item", quantity: exposureQty, detail: wallet > 0 ? `${maxExposurePct}% item exposure leaves ${formatISK(exposureCap)} usable after existing exposure.` : "Wallet not loaded; exposure limit not applied." },
      { label: isStationMode ? "Station scope" : "Cargo fit", quantity: cargoQty, detail: isStationMode ? "No hauling cargo limit in station mode." : cargoPossible ? `${shortNumber(unitsPerTrip)} units per trip; multiple trips allowed.` : "One unit does not fit selected cargo." },
    ];
    const activeLimiters = quantityGates.filter((gate) => gate.quantity < requestedQty && gate.quantity <= executableQty);
    const qtyReductionReason =
      executableQty >= requestedQty
        ? "No reduction; requested quantity passes all gates."
        : activeLimiters.length > 0
          ? activeLimiters.map((gate) => gate.label).join(", ")
          : "Combined constraints reduce quantity.";
    const tooSmallToTrade =
      executableQty > 0 &&
      (expectedProfit < 5_000_000 || Math.abs(selectedVariant.iskPerHour) < 1_000_000);
    const tooSmallReason =
      expectedProfit < 5_000_000
        ? "Expected PnL is below 5M ISK."
        : Math.abs(selectedVariant.iskPerHour) < 1_000_000
          ? "ISK/hour is below 1M."
          : "";

    const warnings: string[] = [];
    if (executableQty <= 0) warnings.push("No executable quantity under current depth/cargo/capital constraints.");
    if (!isStationMode && !cargoPossible) warnings.push("One unit is larger than selected ship cargo.");
    if (depthQty < requestedQty) warnings.push("Orderbook depth cannot fill the requested quantity.");
    if (capitalQty < requestedQty) warnings.push("Capital/exposure limits reduce executable quantity.");
    if (tooSmallToTrade) warnings.push(`Too small to trade: ${tooSmallReason}`);
    if (!isStationMode && (row.RouteSafetyDanger ?? "").toLowerCase() === "red") warnings.push("Route is currently marked red by hauling risk signals.");
    if (isStationMode) warnings.push("Station mode is a maker-order decision model; fill timing and undercuts are estimates, not guaranteed fills.");
    if (!character && isLoggedIn && !characterLoading) warnings.push("Character wallet/assets/orders could not be loaded.");

    return {
      requestedQty,
      buyDepthQty,
      sellDepthQty,
      depthQty,
      executableQty,
      buyFill,
      sellFill,
      buyFees,
      sellFees,
      capitalFrozen,
      expectedProfit,
      worstProfit,
      roi,
      grossSpreadPerUnit,
      grossProfit,
      totalFees,
      netPerUnit,
      wallet,
      reserveCap,
      tradeCap,
      exposureCap,
      exposureValue,
      reserveQty,
      tradeQty,
      exposureQty,
      capitalQty,
      itemVolume,
      cargoCapacity,
      unitsPerTrip,
      cargoQty,
      trips,
      variants,
      selectedVariant,
      collateral,
      riskPremium,
      quantityGates,
      qtyReductionReason,
      fillAssumption,
      tooSmallToTrade,
      tooSmallReason,
      worstModel: {
        buyShockPct,
        sellHaircutPct,
        slippageBufferPct,
        undercutHaircutPct,
        routeRiskHaircutPct,
        worstBuyFees,
        worstSellFees,
      },
      warnings,
    };
  }, [
    buyFeePct,
    buyTaxPct,
    character,
    characterExposure,
    characterLoading,
    isLoggedIn,
    isStationMode,
    maxExposurePct,
    maxISK,
    planBuy,
    planSell,
    quantity,
    reservePct,
    routeMode,
    row,
    selectedProfile,
    sellFeePct,
    sellTaxPct,
    walletOverride,
  ]);

  const handleCalculate = useCallback(() => {
    if (!row) return;
    fetchPlans(intInput(quantity));
  }, [fetchPlans, quantity, row]);

  const createJournalTrade = useCallback(async () => {
    if (!row || !mission || mission.executableQty <= 0 || creating || createdTrade) return;
    const plannedProfit = mission.expectedProfit;
    const payload: PaperTradeCreatePayload = {
      status: "planned",
      type_id: row.TypeID,
      type_name: row.TypeName,
      planned_quantity: mission.executableQty,
      planned_buy_price: mission.buyFill.avg,
      planned_sell_price: mission.sellFill.avg,
      planned_profit_isk: plannedProfit,
      planned_roi_percent: mission.roi,
      fees_isk: mission.buyFees + mission.sellFees,
      hauling_cost_isk: 0,
      buy_station: row.BuyStation,
      sell_station: row.SellStation,
      buy_system_name: row.BuySystemName,
      sell_system_name: row.SellSystemName,
      buy_system_id: row.BuySystemID,
      sell_system_id: row.SellSystemID,
      buy_region_id: row.BuyRegionID ?? 0,
      sell_region_id: row.SellRegionID ?? 0,
      buy_location_id: row.BuyLocationID ?? 0,
      sell_location_id: row.SellLocationID ?? 0,
      volume_m3: mission.itemVolume,
      source: "execution_plan",
      notes: plannedNote([
        `${isStationMode ? "Station Trader Mission Control" : "Trade Execution Autopilot"} plan. Decision support only; no orders were placed by the app.`,
        isStationMode
          ? `Station cycle: ${mission.selectedVariant.label}, approx ${mission.selectedVariant.minutes.toFixed(0)} min to fill/list/sell.`
          : `Ship: ${selectedProfile.label}, cargo ${shortNumber(mission.cargoCapacity)} m3, ${mission.trips} trip(s).`,
        isStationMode ? "" : `Route: ${mission.selectedVariant.label}, ${travelJumps(row)} jump(s), approx ${mission.selectedVariant.minutes.toFixed(0)} min.`,
        `Qty: ${mission.executableQty}/${mission.requestedQty}; depth buy/sell ${mission.buyDepthQty}/${mission.sellDepthQty}.`,
        `Quantity limiter: ${mission.qtyReductionReason}. Fill assumption: ${mission.fillAssumption.source} (${mission.fillAssumption.detail}).`,
        `Gross spread/unit: ${formatISK(mission.grossSpreadPerUnit)}; net/unit: ${formatISK(mission.netPerUnit)}; fees: ${formatISK(mission.totalFees)}.`,
        `Expected PnL: ${formatISK(mission.expectedProfit)}; worst-case PnL: ${formatISK(mission.worstProfit)}; ROI ${formatMargin(mission.roi)}.`,
        `Worst-case model: buy shock ${mission.worstModel.buyShockPct.toFixed(1)}%, sell haircut ${mission.worstModel.sellHaircutPct.toFixed(1)}%, slippage buffer ${mission.worstModel.slippageBufferPct.toFixed(1)}%, undercut ${mission.worstModel.undercutHaircutPct.toFixed(1)}%.`,
        `Capital frozen: ${formatISK(mission.capitalFrozen)}; collateral guide: ${formatISK(mission.collateral)}.`,
        `Existing exposure: assets ${mission ? characterExposure.assets : 0}, buy orders ${characterExposure.buyOrders}, sell orders ${characterExposure.sellOrders}.`,
        "Next workflow: planned -> bought -> hauled -> listed -> sold -> reconciled via ESI live sync.",
      ]),
    };

    setCreating(true);
    try {
      const res = await createPaperTrade(payload);
      setCreatedTrade(res.trade);
      addToast(`Execution plan saved: ${res.trade.type_name}`, "success", 2200);
    } catch (e) {
      addToast(e instanceof Error ? e.message : "Failed to create journal trade", "error", 3200);
    } finally {
      setCreating(false);
    }
  }, [addToast, characterExposure, createdTrade, creating, isStationMode, mission, row, selectedProfile]);

  const openJournal = useCallback(() => {
    onJournalCreated?.();
    onClose();
  }, [onClose, onJournalCreated]);

  const runEveAction = useCallback(
    async (action: "market" | "buy" | "sell") => {
      if (!row) return;
      try {
        if (action === "market") await openMarketInGame(row.TypeID);
        if (action === "buy") await setWaypointInGame(row.BuySystemID);
        if (action === "sell") await setWaypointInGame(row.SellSystemID);
        addToast("EVE UI action sent", "success", 1800);
      } catch (err: unknown) {
        const { messageKey, duration } = handleEveUIError(err);
        addToast(t(messageKey), "error", duration);
      }
    },
    [addToast, row],
  );

  if (!row) {
    return null;
  }

  return (
    <Modal open={open} onClose={onClose} title={`${isStationMode ? "Station Mission Control" : "Trade Execution Autopilot"}: ${row.TypeName}`} width="max-w-6xl">
      <div className="p-4 space-y-4">
        <div className="grid grid-cols-1 lg:grid-cols-4 gap-3">
          <Field label="Quantity">
            <input
              type="number"
              min={1}
              value={quantity}
              onChange={(e) => setQuantity(intInput(e.target.value))}
              className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text font-mono"
            />
          </Field>
          {!isStationMode ? (
            <>
              <Field label="Ship profile">
                <select
                  value={shipKey}
                  onChange={(e) => setShipKey(e.target.value as ShipKey)}
                  className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text"
                >
                  {SHIP_PROFILES.map((profile) => (
                    <option key={profile.key} value={profile.key}>
                      {profile.label} ({shortNumber(profile.cargoM3)} m3)
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Custom cargo m3">
                <input
                  type="number"
                  min={0}
                  value={customCargo}
                  disabled={shipKey !== "custom"}
                  onChange={(e) => setCustomCargo(Math.max(0, finiteNumber(e.target.value)))}
                  className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text font-mono disabled:opacity-45"
                />
              </Field>
            </>
          ) : (
            <>
              <Field label="Execution model">
                <div className="px-2 py-1.5 bg-eve-dark border border-eve-border text-eve-text">
                  Maker buy order {"->"} listed sell order
                </div>
              </Field>
              <Field label="Fill estimate">
                <div className="px-2 py-1.5 bg-eve-dark border border-eve-border text-eve-text font-mono">
                  {fillTimeDaysForRow(row, quantity).toFixed(2)}d
                </div>
              </Field>
            </>
          )}
          <Field label={isStationMode ? "Order mode" : "Route mode"}>
            <select
              value={routeMode}
              onChange={(e) => setRouteMode(e.target.value as RouteMode)}
              className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text"
            >
              <option value="fastest">{isStationMode ? "Fast fill" : "Fastest"}</option>
              <option value="safest">{isStationMode ? "Safer spread" : "Safest"}</option>
              <option value="isk_hour">Max ISK/hour</option>
            </select>
          </Field>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-4 gap-3">
          <Field label="Max ISK per trade">
            <input
              type="number"
              min={0}
              value={maxISK}
              onChange={(e) => setMaxISK(Math.max(0, finiteNumber(e.target.value)))}
              className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text font-mono"
            />
          </Field>
          <Field label="Reserve wallet %">
            <input
              type="number"
              min={0}
              max={100}
              value={reservePct}
              onChange={(e) => setReservePct(clamp(finiteNumber(e.target.value), 0, 100))}
              className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text font-mono"
            />
          </Field>
          <Field label="Max exposure / item %">
            <input
              type="number"
              min={0}
              max={100}
              value={maxExposurePct}
              onChange={(e) => setMaxExposurePct(clamp(finiteNumber(e.target.value), 0, 100))}
              className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text font-mono"
            />
          </Field>
          <Field label={character?.wallet ? "Wallet from ESI" : "Wallet override"}>
            <input
              type="number"
              min={0}
              value={walletOverride || character?.wallet || 0}
              onChange={(e) => setWalletOverride(Math.max(0, finiteNumber(e.target.value)))}
              className="px-2 py-1.5 bg-eve-input border border-eve-border text-eve-text font-mono"
            />
          </Field>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={handleCalculate}
            disabled={loading}
            className="px-3 py-1.5 bg-eve-accent text-eve-dark border border-eve-accent font-semibold uppercase tracking-wider text-xs disabled:opacity-50"
          >
            {loading ? "Calculating..." : "Recalculate"}
          </button>
          <button
            type="button"
            onClick={createJournalTrade}
            disabled={creating || !!createdTrade || !mission || mission.executableQty <= 0}
            className="px-3 py-1.5 bg-eve-panel border border-eve-accent/60 text-eve-accent font-semibold uppercase tracking-wider text-xs disabled:opacity-50"
          >
            {creating ? "Saving..." : createdTrade ? "Journal trade created" : "Create journal trade"}
          </button>
          {isLoggedIn && (
            <>
              <button type="button" onClick={() => void runEveAction("market")} className="px-3 py-1.5 bg-eve-dark border border-eve-border text-eve-dim hover:text-eve-text text-xs">
                Open market
              </button>
              <button type="button" onClick={() => void runEveAction("buy")} className="px-3 py-1.5 bg-eve-dark border border-eve-border text-eve-dim hover:text-eve-text text-xs">
                {isStationMode ? "Station waypoint" : "Buy waypoint"}
              </button>
              {!isStationMode && (
                <button type="button" onClick={() => void runEveAction("sell")} className="px-3 py-1.5 bg-eve-dark border border-eve-border text-eve-dim hover:text-eve-text text-xs">
                  Sell waypoint
                </button>
              )}
            </>
          )}
          <span className="text-xs text-eve-dim">
            {characterLoading ? "Loading character runtime..." : "No market orders are placed by this tool."}
          </span>
        </div>

        {error && <div className="text-sm text-red-300">{error}</div>}

        {mission && (
          <>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
              <Metric label="Executable qty" value={`${shortNumber(mission.executableQty)} / ${shortNumber(mission.requestedQty)}`} tone={mission.executableQty > 0 ? "good" : "bad"} />
              <Metric label="Expected PnL" value={formatISK(mission.expectedProfit)} tone={mission.expectedProfit >= 0 ? "good" : "bad"} />
              <Metric label="Worst-case PnL" value={formatISK(mission.worstProfit)} tone={mission.worstProfit >= 0 ? "good" : "bad"} />
              <Metric label="ROI" value={formatMargin(mission.roi)} tone={mission.roi >= 0 ? "good" : "bad"} />
              <Metric label="Capital frozen" value={formatISK(mission.capitalFrozen)} />
              <Metric
                label={isStationMode ? "Fill horizon" : "Cargo"}
                value={
                  isStationMode
                    ? `${(mission.selectedVariant.minutes / 1440).toFixed(2)}d`
                    : `${shortNumber(mission.unitsPerTrip)} units/trip, ${mission.trips} trip(s)`
                }
                tone={mission.unitsPerTrip > 0 ? "neutral" : "bad"}
              />
              <Metric label={isStationMode ? "Cycle time" : "Travel time"} value={`${mission.selectedVariant.minutes.toFixed(0)} min`} />
              <Metric label="ISK/hour" value={formatISK(mission.selectedVariant.iskPerHour)} tone={mission.selectedVariant.iskPerHour >= 0 ? "good" : "bad"} />
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-4 gap-3">
              <Panel title="Trade math">
                <InfoRow label="Gross spread/unit" value={formatISK(mission.grossSpreadPerUnit)} tone={mission.grossSpreadPerUnit >= 0 ? "good" : "bad"} />
                <InfoRow label="Gross profit" value={formatISK(mission.grossProfit)} tone={mission.grossProfit >= 0 ? "good" : "bad"} />
                <InfoRow label="Fees/taxes" value={formatISK(mission.totalFees)} tone={mission.totalFees > 0 ? "warn" : "neutral"} />
                <InfoRow label="Net/unit" value={formatISK(mission.netPerUnit)} tone={mission.netPerUnit >= 0 ? "good" : "bad"} />
                <InfoRow label="Trade size flag" value={mission.tooSmallToTrade ? "Too small" : "Tradable size"} tone={mission.tooSmallToTrade ? "warn" : "good"} />
              </Panel>

              <Panel title="Quantity gate">
                <InfoRow label="Why qty changed" value={mission.qtyReductionReason} tone={mission.executableQty < mission.requestedQty ? "warn" : "good"} />
                {mission.quantityGates
                  .filter((gate) => gate.label !== "Requested")
                  .map((gate) => (
                    <InfoRow
                      key={gate.label}
                      label={gate.label}
                      value={shortNumber(gate.quantity)}
                      tone={gate.quantity < mission.requestedQty ? "warn" : "neutral"}
                    />
                  ))}
                {mission.quantityGates
                  .filter((gate) => gate.quantity < mission.requestedQty)
                  .slice(0, 2)
                  .map((gate) => (
                    <div key={`${gate.label}-detail`} className="text-xs text-yellow-200/80 leading-relaxed">
                      {gate.label}: {gate.detail}
                    </div>
                  ))}
              </Panel>

              <Panel title="Fill assumption">
                <InfoRow label="Source" value={mission.fillAssumption.source} />
                <InfoRow label="Horizon" value={`${mission.fillAssumption.days.toFixed(2)}d`} />
                <div className="text-xs text-eve-dim leading-relaxed">{mission.fillAssumption.detail}</div>
              </Panel>

              <Panel title="Worst-case model">
                <InfoRow label="Buy shock" value={`${mission.worstModel.buyShockPct.toFixed(1)}%`} tone="warn" />
                <InfoRow label="Sell haircut" value={`${mission.worstModel.sellHaircutPct.toFixed(1)}%`} tone="warn" />
                <InfoRow label="Slippage buffer" value={`${mission.worstModel.slippageBufferPct.toFixed(1)}%`} />
                <InfoRow label={isStationMode ? "Undercut haircut" : "Route risk haircut"} value={`${(isStationMode ? mission.worstModel.undercutHaircutPct : mission.worstModel.routeRiskHaircutPct).toFixed(1)}%`} />
                <InfoRow label="Worst fees" value={formatISK(mission.worstModel.worstBuyFees + mission.worstModel.worstSellFees)} tone="warn" />
              </Panel>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-3">
              <Panel title={isStationMode ? "Orderbook support" : "Execution depth"}>
                <InfoRow label="Buy VWAP" value={formatISK(mission.buyFill.avg)} />
                <InfoRow label="Sell VWAP" value={formatISK(mission.sellFill.avg)} />
                <InfoRow label={isStationMode ? "Buy-order support" : "Buy depth"} value={shortNumber(mission.buyDepthQty)} />
                <InfoRow label={isStationMode ? "Sell-order support" : "Sell depth"} value={shortNumber(mission.sellDepthQty)} />
                <InfoRow label="Fees estimate" value={formatISK(mission.buyFees + mission.sellFees)} tone="warn" />
              </Panel>

              <div className="border border-eve-border bg-eve-dark/50">
                <div className="px-3 py-2 border-b border-eve-border text-xs uppercase tracking-wider text-eve-accent">{isStationMode ? "Order variants" : "Route variants"}</div>
                <div className="p-3 space-y-2">
                  {mission.variants.map((variant) => (
                    <button
                      key={variant.key}
                      type="button"
                      onClick={() => setRouteMode(variant.key)}
                      className={`w-full text-left border px-3 py-2 ${
                        routeMode === variant.key
                          ? "border-eve-accent bg-eve-accent/10"
                          : "border-eve-border bg-eve-dark/40 hover:border-eve-accent/40"
                      }`}
                    >
                      <div className="flex justify-between text-sm">
                        <span className="text-eve-text">{variant.label}</span>
                        <span className="font-mono text-eve-accent">{formatISK(variant.riskAdjustedIskPerHour)}/h</span>
                      </div>
                      <div className="mt-1 text-[11px] text-eve-dim">
                        {variant.minutes.toFixed(0)} min, {isStationMode ? "undercut/price risk" : "risk penalty"} {variant.riskPenaltyPct.toFixed(1)}%
                      </div>
                    </button>
                  ))}
                </div>
              </div>

              <div className="border border-eve-border bg-eve-dark/50">
                <div className="px-3 py-2 border-b border-eve-border text-xs uppercase tracking-wider text-eve-accent">Character context</div>
                <div className="p-3 space-y-2 text-sm">
                  <div className="flex justify-between"><span className="text-eve-dim">Wallet used</span><span className="font-mono text-eve-text">{mission.wallet > 0 ? formatISK(mission.wallet) : "-"}</span></div>
                  <div className="flex justify-between"><span className="text-eve-dim">Assets</span><span className="font-mono text-eve-text">{shortNumber(characterExposure.assets)}</span></div>
                  <div className="flex justify-between"><span className="text-eve-dim">Buy orders</span><span className="font-mono text-eve-text">{shortNumber(characterExposure.buyOrders)}</span></div>
                  <div className="flex justify-between"><span className="text-eve-dim">Sell orders</span><span className="font-mono text-eve-text">{shortNumber(characterExposure.sellOrders)}</span></div>
                  <div className="flex justify-between"><span className="text-eve-dim">{isStationMode ? "Open order capital" : "Collateral guide"}</span><span className="font-mono text-eve-text">{formatISK(isStationMode ? mission.capitalFrozen : mission.collateral)}</span></div>
                  <div className="flex justify-between"><span className="text-eve-dim">{isStationMode ? "Price-risk reserve" : "Risk premium guide"}</span><span className="font-mono text-eve-text">{formatISK(mission.riskPremium)}</span></div>
                </div>
              </div>
            </div>

            <Panel title="Journal expected vs actual">
              <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                <div className="space-y-2">
                  <div className="text-[10px] uppercase tracking-wider text-eve-dim">Plan</div>
                  <InfoRow label="Qty" value={shortNumber(mission.executableQty)} />
                  <InfoRow label="Buy" value={formatISK(mission.buyFill.avg)} />
                  <InfoRow label="Sell" value={formatISK(mission.sellFill.avg)} />
                  <InfoRow label="Expected PnL" value={formatISK(mission.expectedProfit)} tone={mission.expectedProfit >= 0 ? "good" : "bad"} />
                </div>
                <div className="space-y-2">
                  <div className="text-[10px] uppercase tracking-wider text-eve-dim">Actual after ESI sync</div>
                  <InfoRow label="Buy fill" value="pending" />
                  <InfoRow label="Sell fill" value="pending" />
                  <InfoRow label="Real PnL" value="pending" />
                  <InfoRow label="Loss bucket" value="slippage / fees / partial fill / price moved" />
                </div>
                <div className="space-y-2">
                  <div className="text-[10px] uppercase tracking-wider text-eve-dim">Journal state</div>
                  <InfoRow label="Entry" value={createdTrade ? `#${createdTrade.id}` : "not created"} tone={createdTrade ? "good" : "warn"} />
                  <InfoRow label="Status" value={createdTrade?.status ?? "planned draft"} />
                  <InfoRow label="Workflow" value="planned -> bought -> listed -> sold -> reconciled" />
                  {createdTrade && (
                    <button
                      type="button"
                      onClick={openJournal}
                      className="mt-1 px-3 py-1.5 bg-eve-panel border border-eve-accent/60 text-eve-accent font-semibold uppercase tracking-wider text-xs"
                    >
                      Open journal
                    </button>
                  )}
                </div>
              </div>
            </Panel>

            {mission.warnings.length > 0 && (
              <div className="border border-yellow-500/40 bg-yellow-950/20 px-3 py-2 text-sm text-yellow-200">
                {mission.warnings.map((warning) => (
                  <div key={warning}>{warning}</div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </Modal>
  );
}
