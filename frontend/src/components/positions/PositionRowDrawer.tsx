import { DetailGroup, DetailRow } from "@/components/ui/DetailList";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent } from "@/components/ui/sheet";
import { ItemRef } from "@/components/ui/ItemRef";
import { formatISK, formatIsk, formatNumber } from "@/lib/format";
import type { TranslationKey } from "@/lib/i18n";
import type { PositionRow } from "@/lib/types";
import { HoldingRuleEditor } from "./HoldingRuleEditor";

/**
 * Tier 2 for Positions (docs/UI_DESIGN_SYSTEM.md §4).
 *
 * The grid answers "should I sell this today". This answers "why that
 * number" — where the cost basis came from, what the hub is paying, and how
 * much of the gross the broker and CCP take before any of it is yours.
 */

const isk = (n: number | undefined) => (n != null && Number.isFinite(n) ? formatISK(n) : "—");

/** Per-unit prices, to two decimals — this is the tier the user opened to see
 *  exactly why the grid said what it said, and a 3.91 ISK mineral rounded to
 *  "4" is the number they came here to check. See PositionsTab. */
const unit = (n: number | undefined) =>
  n != null && Number.isFinite(n)
    ? formatIsk(n, undefined, { maxTier: "T", space: false, decimals: { t: 2, b: 2, m: 2, k: 2, unit: 2 } })
    : "—";

/** ISO or plain date → the user's locale, or an em dash. Never throws on junk. */
function stamp(iso: string | undefined, locale: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString(locale);
}

export const SOURCE_LABEL_KEYS: Record<string, TranslationKey> = {
  trade: "positionsSourceTrade",
  manufacture: "positionsSourceManufacture",
  orphan: "positionsSourceOrphan",
  manual: "positionsSourceManual",
};

export function PositionRowDrawer({
  row,
  salesTaxPercent,
  brokerFeePercent,
  onClose,
  onDelete,
  onRuleSaved,
  t,
  locale,
}: {
  row: PositionRow | null;
  salesTaxPercent: number;
  brokerFeePercent: number;
  onClose: () => void;
  onDelete: (manualID: number) => void;
  /** A holding rule was stored or cleared; the tab refetches. */
  onRuleSaved: (typeId: number) => void;
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
  locale: string;
}) {
  if (!row) return null;

  const gross = row.market_value;
  const brokerISK = (gross * brokerFeePercent) / 100;
  const taxISK = (gross * salesTaxPercent) / 100;
  const sourceKey = SOURCE_LABEL_KEYS[row.source] ?? "positionsSourceTrade";

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
            marketLabel={t("positionsListHint")}
          />
        }
        description={`${formatNumber(row.qty)} × ${t(sourceKey)}`}
        footer={
          row.manual_id ? (
            <Button variant="danger" size="sm" onClick={() => onDelete(row.manual_id!)}>
              {t("positionsDrawerDelete")}
            </Button>
          ) : undefined
        }
      >
        <div className="mb-3 flex flex-wrap items-center gap-1.5">
          <Badge tone={row.source === "manual" ? "info" : "neutral"}>{t(sourceKey)}</Badge>
          {row.listed_qty > 0 && (
            <Badge tone="accent">{t("positionsListed", { n: formatNumber(row.listed_qty) })}</Badge>
          )}
          {row.market_price <= 0 && <Badge tone="warn">{t("positionsNoPrice")}</Badge>}
        </div>

        <DetailGroup title={t("positionsDrawerHolding")}>
          <DetailRow label={t("positionsColQty")} value={formatNumber(row.qty)} />
          {row.reserved_qty ? (
            <DetailRow
              label={t("holdingRuleReserved")}
              value={t("holdingRuleTradeable", {
                n: formatNumber(row.tradeable_qty),
                total: formatNumber(row.qty),
              })}
              tone="warn"
            />
          ) : null}
          <DetailRow label={t("positionsDrawerSource")} value={t(sourceKey)} />
          <DetailRow label={t("positionsDrawerOldest")} value={stamp(row.oldest_date, locale)} />
          <DetailRow label={t("positionsColAge")} value={t("positionsAgeDays", { n: row.days_held })} />
        </DetailGroup>

        <DetailGroup title={t("positionsDrawerCost")}>
          <DetailRow label={t("positionsColAvgCost")} value={unit(row.avg_unit_cost)} />
          <DetailRow label={t("positionsDrawerCostBasis")} value={isk(row.cost_basis)} />
          {row.target_price ? (
            <DetailRow
              label={t("positionsDrawerTarget")}
              value={
                row.target_met
                  ? `${unit(row.target_price)} · ${t("holdingRuleTargetMet")}`
                  : `${unit(row.target_price)} · ${t("holdingRuleProgress", {
                      pct: String(Math.round(row.target_progress_pct ?? 0)),
                    })}`
              }
              tone={row.target_met ? "profit" : "info"}
              copyValue={row.target_price}
              copyLabel={t("copyPrice")}
            />
          ) : null}
        </DetailGroup>

        <DetailGroup title={t("positionsDrawerMarket")}>
          <DetailRow label={t("positionsColNow")} value={unit(row.market_price)} copyValue={row.market_price} copyLabel={t("copyPrice")} />
          <DetailRow label={t("positionsDrawerGross")} value={isk(gross)} />
          <DetailRow
            label={t("positionsDrawerBrokerFee", { pct: brokerFeePercent })}
            value={`−${isk(brokerISK)}`}
            tone="muted"
          />
          <DetailRow
            label={t("positionsDrawerSalesTax", { pct: salesTaxPercent })}
            value={`−${isk(taxISK)}`}
            tone="muted"
          />
          <DetailRow label={t("positionsDrawerNetProceeds")} value={isk(row.net_proceeds)} />
          <DetailRow
            label={t("positionsColUnrealized")}
            value={`${row.unrealized_isk >= 0 ? "+" : ""}${isk(row.unrealized_isk)}`}
            tone={row.unrealized_isk >= 0 ? "profit" : "loss"}
          />
        </DetailGroup>

        {row.listed_qty > 0 && (
          <DetailGroup title={t("positionsDrawerListed")}>
            <DetailRow label={t("positionsColQty")} value={formatNumber(row.listed_qty)} />
            <DetailRow label={t("positionsDrawerListedPrice")} value={unit(row.listed_price)} copyValue={row.listed_price} copyLabel={t("copyPrice")} />
          </DetailGroup>
        )}

        <HoldingRuleEditor row={row} onSaved={onRuleSaved} />

        {row.note && (
          <DetailGroup title={t("positionsDrawerNote")}>
            <p className="font-ui text-t-cell leading-relaxed text-fg-secondary">{row.note}</p>
          </DetailGroup>
        )}
      </SheetContent>
    </Sheet>
  );
}
