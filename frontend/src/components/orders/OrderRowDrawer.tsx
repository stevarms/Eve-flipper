import { DetailGroup, DetailRow } from "@/components/ui/DetailList";
import { OrderRuleEditor } from "@/components/orders/OrderRuleEditor";
import { Badge } from "@/components/ui/badge";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { ItemRef } from "@/components/ui/ItemRef";
import { priceStep } from "@/lib/pricing";
import { formatISK, formatNumber } from "@/lib/format";
import type { TranslationKey } from "@/lib/i18n";
import type { BookLevel, OrderDeskOrder } from "@/lib/types";

/**
 * Tier 2 for the order desk (docs/UI_DESIGN_SYSTEM.md §4).
 *
 * The grid answers "which orders do I touch, and what do I type into EVE".
 * Everything that explains *why* — the fee arithmetic behind an unprofitable
 * relist, the queue ahead of you, the fill rate the ETA is derived from —
 * lives here. Eleven grid columns became six; none of the eleven was lost.
 *
 * The price ladder at the bottom is the one thing the old character-modal
 * order tab had that the desk payload does not carry: it comes from
 * getUndercuts(), fetched lazily by the tab the first time a row is inspected.
 */

const isk = (n: number | undefined) =>
  n != null && Number.isFinite(n) ? formatISK(n) : "—";
const int = (n: number | undefined) =>
  n != null && Number.isFinite(n) ? formatNumber(n) : "—";
const days = (n: number | undefined) =>
  n != null && n >= 0 ? `${n.toFixed(1)}d` : "—";

/** ISO timestamp → the user's locale, or an em dash. Never throws on junk. */
function stamp(iso: string | undefined, locale: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString(locale);
}

/** What the margin on this row is measured against. A number with no stated
 *  basis is worse than no number: book margin and cost-basis margin answer
 *  different questions and only one of them is about a position. */
const MARGIN_BASIS_LABEL: Record<string, TranslationKey> = {
  book: "ordersMarginBasisBook",
  cost_basis: "ordersMarginBasisCostBasis",
  target: "ordersMarginBasisTarget",
  none: "ordersMarginBasisNone",
};

/** ESI's `range` as something readable. A numeric range is a jump count. */
function rangeLabel(
  range: string | undefined,
  t: (key: TranslationKey, params?: Record<string, string | number>) => string,
): string {
  if (!range) return "—";
  if (range === "region") return t("ordersRangeRegion");
  if (range === "station") return t("ordersRangeStation");
  if (range === "solarsystem") return t("ordersRangeSystem");
  const n = Number(range);
  return Number.isFinite(n) ? t("ordersRangeJumps", { n }) : range;
}

/** Whether the desk is actually asking you to move this order's price.
 *
 *  A suggested price is computed for every row from the book, whether or not
 *  the desk wants it taken, so its presence proves nothing on its own. A
 *  parked bid carries one forty percent above itself and a hold verdict saying
 *  to leave it there; a sell under its target carries one it is expressly
 *  forbidden to take. Only `reprice` and `review` are verdicts about a price,
 *  and a parked bid's `review` is about its expiry rather than its price. */
export function proposesReprice(row: OrderDeskOrder): boolean {
  if (!row.book_available || row.suggested_price <= 0) return false;
  if (row.suggested_price === row.price) return false;
  if (row.is_lowball || row.patient_bid) return false;
  return row.recommendation === "reprice" || row.recommendation === "review";
}

/** Whether the row's after-reprice margin is worth showing: a move the desk is
 *  advising, and a basis to measure it on.
 *
 *  The "before → after" pair is an argument about a move. On a row told to
 *  hold there is no move, so the pair read as a change of price that nobody
 *  proposed — which is what this predicate exists to prevent. Such a row shows
 *  one number: what it returns if it fills at the price it is standing at.
 *
 *  Shared with the grid's Margin column so the two cannot disagree about
 *  which rows have a second number, the same way recommendationTone is. */
export function showsAfterMargin(row: OrderDeskOrder): boolean {
  return row.margin_basis !== "none" && proposesReprice(row);
}

export type OrderTone = "profit" | "loss" | "warn" | "neutral";

/** Shared by the grid badge and the drawer badge so they cannot disagree. */
export function recommendationTone(recommendation: string, bookAvailable: boolean): OrderTone {
  if (recommendation === "cancel") return "loss";
  if (recommendation === "reprice") return "warn";
  return bookAvailable ? "profit" : "neutral";
}

export interface OrderRowDrawerProps {
  row: OrderDeskOrder | null;
  onClose: () => void;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
  locale: string;
  /** Depth around this order's price, from getUndercuts(). */
  bookLevels?: BookLevel[];
  bookLevelsLoading?: boolean;
  /** Refetch the desk after a price rule is stored or cleared. Omitted by
   *  callers that cannot refetch — the editor is then hidden rather than
   *  offered and silently ignored. */
  onRuleSaved?: () => void;
}

export function OrderRowDrawer({
  row,
  onClose,
  t,
  locale,
  bookLevels,
  bookLevelsLoading = false,
  onRuleSaved,
}: OrderRowDrawerProps) {
  if (!row) return null;

  const side = row.is_buy_order ? t("charBuy") : t("charSell");
  const filled = row.volume_total > 0 ? row.volume_total - row.volume_remain : 0;

  return (
    <Sheet open={!!row} onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        width="w-[520px]"
        title={
          <ItemRef
            typeId={row.type_id}
            name={row.type_name}
            iconSize={24}
            tone="title"
            market
            copyName
            marketLabel={t("openMarketHint")}
          />
        }
        description={`${side} · ${row.location_name || `#${row.location_id}`}`}
      >
        <div className="mb-3 flex flex-wrap items-center gap-1.5">
          <Badge tone={recommendationTone(row.recommendation, row.book_available)}>
            {row.recommendation}
          </Badge>
          {row.character_name && <Badge tone="neutral">{row.character_name}</Badge>}
          {row.warn_unprofitable_relist && (
            <Badge tone="warn">{t("ordersDrawerFeeEatsGain")}</Badge>
          )}
          {(row.is_lowball || row.patient_bid) && (
            <Badge tone="neutral">{t("ordersBadgeParked")}</Badge>
          )}
        </div>

        {row.reason && (
          <p className="mb-4 font-ui text-t-cell leading-relaxed text-fg-secondary">
            {row.reason}
          </p>
        )}

        <DetailGroup title={t("ordersDrawerGroupPricing")}>
          <DetailRow label={t("ordersColCurrent")} value={isk(row.price)} copyValue={row.price} copyLabel={t("copyPrice")} />
          <DetailRow
            label={t("ordersColBest")}
            value={row.book_available ? isk(row.best_price) : "—"}
            hint={t("ordersDrawerBestHint")}
            copyValue={row.book_available ? row.best_price : undefined} copyLabel={t("copyPrice")}
          />
          <DetailRow
            label={t("operatorSuggestedPriceCol")}
            value={row.book_available && row.suggested_price > 0 ? isk(row.suggested_price) : "—"}
            tone={row.position === 1 ? "muted" : "info"}
            hint={t("ordersDrawerSuggestedHint")}
            /* The one number in this drawer that gets typed back into EVE, so
               it carries the grid step that keeps a legal undercut legal. */
            copyValue={row.book_available ? row.suggested_price : undefined} copyLabel={t("copyPrice")}
            copyStep={priceStep(row.suggested_price)}
          />
          <DetailRow
            label={t("ordersDrawerUndercut")}
            value={
              row.book_available
                ? `${isk(row.undercut_amount)} (${row.undercut_pct.toFixed(2)}%)`
                : "—"
            }
            hint={t("ordersDrawerUndercutHint")}
          />
          <DetailRow label={t("ordersDrawerNetUnit")} value={isk(row.net_unit_isk)} />
        </DetailGroup>

        <MarginRiskGroup row={row} t={t} />

        {onRuleSaved && <OrderRuleEditor row={row} onSaved={onRuleSaved} />}

        <DetailGroup title={t("ordersDrawerGroupRelist")}>
          <DetailRow label={t("ordersDrawerRelistFee")} value={isk(row.relist_fee_isk)} />
          <DetailRow
            label={t("ordersDrawerRelistGain")}
            value={isk(row.net_relist_gain_isk)}
            tone={
              row.net_relist_gain_isk == null
                ? undefined
                : row.net_relist_gain_isk > 0
                  ? "profit"
                  : "loss"
            }
            hint={t("ordersDrawerRelistGainHint")}
          />
        </DetailGroup>

        <DetailGroup title={t("ordersDrawerGroupQueue")}>
          <DetailRow
            label={t("ordersColPosition")}
            value={row.book_available ? `${row.position} / ${row.total_orders}` : "—"}
          />
          <DetailRow
            label={t("ordersDrawerQueueAhead")}
            value={int(row.queue_ahead_qty)}
            hint={t("ordersDrawerQueueAheadHint")}
          />
          <DetailRow label={t("ordersDrawerTopQty")} value={int(row.top_price_qty)} />
          <DetailRow label={t("ordersDrawerDailyVolume")} value={int(row.avg_daily_volume)} />
          <DetailRow
            label={t("ordersDrawerFillPerDay")}
            value={
              row.estimated_fill_per_day > 0 ? row.estimated_fill_per_day.toFixed(1) : "—"
            }
            hint={t("ordersDrawerFillPerDayHint")}
          />
          <DetailRow label={t("ordersColEta")} value={days(row.eta_days)} />
        </DetailGroup>

        <DetailGroup title={t("ordersDrawerGroupOrder")}>
          <DetailRow
            label={t("ordersDrawerRemaining")}
            value={`${int(row.volume_remain)} / ${int(row.volume_total)}`}
            hint={filled > 0 ? t("ordersDrawerFilled", { qty: formatNumber(filled) }) : undefined}
          />
          <DetailRow label={t("ordersKpiNotional")} value={isk(row.notional)} />
          <DetailRow label={t("ordersDrawerNetNotional")} value={isk(row.net_notional)} />
          <DetailRow label={t("ordersDrawerIssued")} value={stamp(row.issued_at, locale)} />
          <DetailRow
            label={t("ordersColExpiry")}
            value={stamp(row.expires_at, locale)}
            tone={row.days_to_expire >= 0 && row.days_to_expire <= 3 ? "warn" : undefined}
          />
          <DetailRow label={t("ordersColStation")} value={row.location_name || `#${row.location_id}`} />
          <DetailRow label={t("ordersColOwner")} value={row.character_name || "—"} />
        </DetailGroup>

        <BookLadder
          levels={bookLevels}
          loading={bookLevelsLoading}
          isBuy={row.is_buy_order}
          t={t}
        />
      </SheetContent>
    </Sheet>
  );
}

/**
 * What the order is worth, and what taking the desk's advice would cost.
 *
 * The grid can only afford one margin figure per row. Everything that decides
 * whether that figure is worth acting on — what it is measured against, what
 * it becomes after the move, how much more ISK the move commits, and where the
 * price sits in the item's own year — is here. The capital and percentile rows
 * exist because a bid chasing a book that has run away passes every per-unit
 * test there is; these are the numbers that do not.
 */
function MarginRiskGroup({
  row,
  t,
}: {
  row: OrderDeskOrder;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  const after = showsAfterMargin(row);
  const measurable = row.margin_basis !== "none";

  /** A percentile only means something with a year behind it. Without one the
   *  row says so rather than printing a zero that reads as "cheapest ever". */
  const pct = (v: number | undefined) =>
    row.percentile_basis === "history" && v != null && Number.isFinite(v)
      ? t("ordersDrawerPercentileValue", { pct: Math.round(v) })
      : t("ordersDrawerPercentileNone");

  const marginTone = (v: number | undefined): "profit" | "loss" | undefined =>
    v == null ? undefined : v > 0 ? "profit" : "loss";

  return (
    <DetailGroup title={t("ordersDrawerGroupRisk")}>
      <DetailRow
        label={t("ordersDrawerMarginBasis")}
        value={t(MARGIN_BASIS_LABEL[row.margin_basis] ?? "ordersMarginBasisNone")}
        tone={measurable ? undefined : "muted"}
      />
      {measurable && (
        <DetailRow
          label={t("ordersDrawerMarginNow")}
          value={`${isk(row.margin_unit_isk)} (${row.margin_percent.toFixed(1)}%)`}
          tone={marginTone(row.margin_unit_isk)}
        />
      )}
      {after && (
        <DetailRow
          label={t("ordersDrawerMarginAfter")}
          value={`${isk(row.suggested_margin_unit_isk)} (${row.suggested_margin_percent.toFixed(1)}%)`}
          tone={marginTone(row.suggested_margin_unit_isk)}
          hint={t("ordersDrawerMarginAfterHint")}
        />
      )}
      {row.margin_basis === "cost_basis" && (
        <DetailRow label={t("ordersDrawerCostBasisUnit")} value={isk(row.cost_basis_isk)} />
      )}
      {row.margin_basis === "book" && (
        // A resale price read somewhere the stock is not has to say where.
        // Bids parked beside the hub to dodge its broker fee are the norm and
        // their own station usually has no sell side at all, so this is the
        // common case rather than the exotic one.
        <DetailRow
          label={t("ordersDrawerExitPrice")}
          value={
            row.exit_location_name
              ? t("ordersDrawerExitAt", {
                  price: isk(row.exit_price),
                  station: row.exit_location_name,
                })
              : isk(row.exit_price)
          }
          hint={row.exit_location_name ? t("ordersDrawerExitRemoteHint") : undefined}
        />
      )}
      {row.margin_basis === "target" && (
        <DetailRow label={t("holdingRuleTarget")} value={isk(row.target_price)} />
      )}

      {after && row.suggested_notional != null && (
        <DetailRow
          label={t("ordersDrawerSuggestedNotional")}
          value={isk(row.suggested_notional)}
        />
      )}
      {/* Buy rows only: a sell order commits stock you already own, so there
          is no extra ISK to put at risk by moving its price. */}
      {row.is_buy_order && after && row.added_capital_isk != null && (
        <DetailRow
          label={t("ordersDrawerAddedCapital")}
          value={isk(row.added_capital_isk)}
          tone={row.added_capital_isk > 0 ? "warn" : undefined}
          hint={t("ordersDrawerAddedCapitalHint")}
        />
      )}

      <DetailRow
        label={t("ordersDrawerPercentileNow")}
        value={pct(row.price_percentile)}
        hint={t("ordersDrawerPercentileHint")}
      />
      {after && (
        <DetailRow
          label={t("ordersDrawerPercentileAfter")}
          value={pct(row.suggested_price_percentile)}
        />
      )}

      {row.is_buy_order && (
        <>
          <DetailRow label={t("ordersDrawerOrderRange")} value={rangeLabel(row.order_range, t)} />
          {row.competing_remote_bids != null && row.competing_remote_bids > 0 && (
            <DetailRow
              label={t("ordersDrawerRemoteBids")}
              value={int(row.competing_remote_bids)}
              hint={t("ordersDrawerRemoteBidsHint")}
            />
          )}
          {row.lowball_price != null && row.lowball_price > 0 && (
            <DetailRow
              label={t("ordersDrawerLowballPrice")}
              value={isk(row.lowball_price)}
              hint={
                row.lowball_fill_days_pct != null
                  ? `${t("ordersDrawerLowballHint")} ${t("ordersDrawerLowballFillDays", {
                      pct: Math.round(row.lowball_fill_days_pct),
                    })}`
                  : t("ordersDrawerLowballHint")
              }
              copyValue={row.lowball_price}
              copyLabel={t("copyPrice")}
              copyStep={priceStep(row.lowball_price)}
            />
          )}
        </>
      )}
    </DetailGroup>
  );
}

/**
 * The queue you are standing in, drawn to scale. Bar length is volume relative
 * to the deepest level shown; your own order is picked out in accent so you can
 * see at a glance how far you are from the front.
 */
function BookLadder({
  levels,
  loading,
  isBuy,
  t,
}: {
  levels: BookLevel[] | undefined;
  loading: boolean;
  isBuy: boolean;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
}) {
  if (loading && !levels) {
    return (
      <DetailGroup title={t("undercutOrderBook")}>
        <p className="px-1 py-1 font-ui text-t-cell text-fg-tertiary">{t("undercutLoading")}</p>
      </DetailGroup>
    );
  }
  if (!levels || levels.length === 0) return null;

  const maxVolume = Math.max(...levels.map((l) => l.volume), 1);

  return (
    <DetailGroup title={t("undercutOrderBook")}>
      <div className="space-y-0.5 py-1">
        {levels.map((level, i) => (
          <div key={`${level.price}-${i}`} className="flex h-5 items-center gap-2">
            <div
              className={
                level.is_player
                  ? "w-24 text-right font-num tnum text-t-cell text-eve-accent"
                  : "w-24 text-right font-num tnum text-t-cell text-fg"
              }
            >
              {isk(level.price)}
            </div>
            <div className="relative h-full flex-1 overflow-hidden rounded-sm bg-surface-2">
              <div
                className={
                  level.is_player
                    ? "absolute inset-y-0 left-0 rounded-sm bg-eve-accent/30"
                    : isBuy
                      ? "absolute inset-y-0 left-0 rounded-sm bg-profit/15"
                      : "absolute inset-y-0 left-0 rounded-sm bg-loss/15"
                }
                style={{ width: `${(level.volume / maxVolume) * 100}%` }}
              />
              <div className="relative flex h-full items-center px-1.5">
                <span className="font-num tnum text-t-caption text-fg-tertiary">
                  {int(level.volume)}
                </span>
              </div>
            </div>
            {level.is_player && (
              <span className="font-ui text-t-caption font-bold tracking-wider text-eve-accent">
                {t("undercutYou")}
              </span>
            )}
          </div>
        ))}
      </div>
    </DetailGroup>
  );
}
