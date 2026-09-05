import { DetailGroup, DetailRow } from "@/components/ui/DetailList";
import { Badge } from "@/components/ui/badge";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { TypeIcon } from "@/components/ui/TypeIcon";
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
}

export function OrderRowDrawer({
  row,
  onClose,
  t,
  locale,
  bookLevels,
  bookLevelsLoading = false,
}: OrderRowDrawerProps) {
  if (!row) return null;

  const side = row.is_buy_order ? t("charBuy") : t("charSell");
  const filled = row.volume_total > 0 ? row.volume_total - row.volume_remain : 0;

  return (
    <Sheet open={!!row} onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        width="w-[520px]"
        title={
          <span className="flex items-center gap-2">
            <TypeIcon typeId={row.type_id} size={24} />
            <span className="truncate">{row.type_name || `#${row.type_id}`}</span>
          </span>
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
        </div>

        {row.reason && (
          <p className="mb-4 font-ui text-t-cell leading-relaxed text-fg-secondary">
            {row.reason}
          </p>
        )}

        <DetailGroup title={t("ordersDrawerGroupPricing")}>
          <DetailRow label={t("ordersColCurrent")} value={isk(row.price)} />
          <DetailRow
            label={t("ordersColBest")}
            value={row.book_available ? isk(row.best_price) : "—"}
            hint={t("ordersDrawerBestHint")}
          />
          <DetailRow
            label={t("operatorSuggestedPriceCol")}
            value={row.book_available && row.suggested_price > 0 ? isk(row.suggested_price) : "—"}
            tone={row.position === 1 ? "muted" : "info"}
            hint={t("ordersDrawerSuggestedHint")}
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
