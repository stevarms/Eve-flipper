import { Sheet, SheetContent } from "@/components/ui/sheet";
import { Badge } from "@/components/ui/badge";
import { DetailGroup as Group, DetailRow as Row } from "@/components/ui/DetailList";
import { formatISK, formatMargin, formatNumber } from "@/lib/format";
import { TypeIcon } from "@/components/ui/TypeIcon";
import type { StationTrade } from "@/lib/types";

/**
 * Tier 2 of the three-tier disclosure rule (docs/UI_DESIGN_SYSTEM.md).
 *
 * The grid shows six columns to DECIDE with. This drawer is where the other
 * fourteen live, plus everything the grid never had room for at all — so the
 * six-column default loses nothing. Grouped and labelled in full words,
 * because "SDS" in a column header helps nobody: three of the acronyms the
 * grid used to lead with (CTS, D.O.S., SDS) have no hint text anywhere in
 * the locale.
 */

const isk = (n: number | undefined) =>
  n != null && Number.isFinite(n) ? formatISK(n) : "—";
const pct = (n: number | undefined) =>
  n != null && Number.isFinite(n) ? formatMargin(n) : "—";
const num = (n: number | undefined, digits = 2) =>
  n != null && Number.isFinite(n) ? n.toFixed(digits) : "—";
const int = (n: number | undefined) =>
  n != null && Number.isFinite(n) ? formatNumber(n) : "—";

export interface StationRowDrawerProps {
  row: StationTrade | null;
  onClose: () => void;
  /** Cascade used by the grid's Daily Profit column, passed in so the two agree. */
  dailyProfit: (row: StationTrade) => number;
}

export function StationRowDrawer({ row, onClose, dailyProfit }: StationRowDrawerProps) {
  if (!row) return null;

  const profit = dailyProfit(row);

  return (
    <Sheet open={!!row} onOpenChange={(open) => !open && onClose()}>
      <SheetContent
        width="w-[560px]"
        title={
          <span className="flex items-center gap-2">
            <TypeIcon typeId={row.TypeID} categoryId={row.CategoryID} size={24} />
            <span className="truncate">{row.TypeName}</span>
          </span>
        }
        description={`#${row.TypeID}${row.CategoryName ? ` · ${row.CategoryName}` : ""} · ${row.StationName}`}
      >
        {(row.IsHighRiskFlag || row.IsExtremePriceFlag || row.IsContraband) && (
          <div className="mb-3 flex flex-wrap gap-1.5">
            {row.IsHighRiskFlag && <Badge tone="warn">High risk</Badge>}
            {row.IsExtremePriceFlag && <Badge tone="warn">Extreme price</Badge>}
            {row.IsContraband && <Badge tone="loss">Contraband</Badge>}
            {row.HistoryAvailable === false && <Badge tone="neutral">No history</Badge>}
          </div>
        )}

        <Group title="Decision">
          <Row
            label="Daily profit"
            value={isk(profit)}
            tone={profit > 0 ? "profit" : "loss"}
            hint="Realistic ISK per day after fees."
          />
          <Row label="Capital required" value={isk(row.CapitalRequired)} />
          <Row
            label="ROI %"
            value={pct(row.MarginPercent)}
            tone={(row.MarginPercent ?? 0) > 0 ? "profit" : "loss"}
          />
          <Row label="Profit / unit" value={isk(row.ProfitPerUnit)} />
          <Row label="Expected PnL (per cycle)" value={isk(row.ExpectedPnL)} />
          <Row label="Now ROI %" value={pct(row.NowROI)} />
          <Row label="Period ROI %" value={pct(row.PeriodROI)} />
          <Row label="Real margin %" value={pct(row.RealMarginPercent)} />
        </Group>

        <Group title="Prices">
          <Row label="Buy" value={isk(row.BuyPrice)} />
          <Row label="Sell" value={isk(row.SellPrice)} />
          <Row label="Spread" value={isk(row.Spread)} />
          <Row
            label="Region average"
            value={
              <>
                {isk(row.RegionAvg)}
                {row.RegionAvgSource === "sde" && (
                  <span className="ml-1 text-t-caption text-fg-tertiary">SDE</span>
                )}
              </>
            }
            hint="ESI market average, or SDE base price for thin markets."
          />
          <Row label="Suggested buy" value={isk(row.SuggestedBid)} />
          <Row label="VWAP" value={isk(row.VWAP)} />
          <Row label="Average price" value={isk(row.AvgPrice)} />
          <Row label="High / low" value={`${isk(row.PriceHigh)} / ${isk(row.PriceLow)}`} />
          <Row label="Expected buy fill" value={isk(row.ExpectedBuyPrice)} />
          <Row label="Expected sell fill" value={isk(row.ExpectedSellPrice)} />
        </Group>

        <Group title="Liquidity">
          <Row label="Daily volume" value={int(row.DailyVolume)} />
          <Row
            label="Days of supply"
            value={num(row.DOS)}
            hint="How long the current sell-side stock would last at recent demand."
          />
          <Row label="Competing buy orders" value={int(row.BuyOrderCount)} />
          <Row label="Competing sell orders" value={int(row.SellOrderCount)} />
          <Row label="Buy volume" value={int(row.BuyVolume)} />
          <Row label="Sell volume" value={int(row.SellVolume)} />
          <Row
            label="Sell-to-buy / day"
            value={num(row.S2BPerDay)}
            hint="Estimated daily sells into buy orders."
          />
          <Row
            label="Buy-from-sell / day"
            value={num(row.BfSPerDay)}
            hint="Estimated daily buys from sell orders."
          />
          <Row label="S2B / BfS ratio" value={num(row.S2BBfSRatio)} hint="Flow balance." />
          <Row label="Cargo volume" value={`${int(row.Volume)} m³`} />
        </Group>

        <Group title="Scores">
          <Row
            label="CTS"
            value={num(row.CTS, 1)}
            hint="Composite trade score used to rank station-trading candidates."
          />
          <Row
            label="DS — discount score"
            value={num(row.DS, 1)}
            hint="0-100 rating for the patient-buy-order workflow. High = deep discount vs region average, few competing buys, real volume."
          />
          <Row label="SDS" value={num(row.SDS, 1)} />
          <Row label="OBDS" value={num(row.OBDS)} />
          <Row label="PVI" value={pct(row.PVI)} />
          <Row label="CI" value={num(row.CI)} />
          {row.ConfidenceScore != null && (
            <Row
              label="Confidence"
              value={`${num(row.ConfidenceScore, 0)}${row.ConfidenceLabel ? ` · ${row.ConfidenceLabel}` : ""}`}
            />
          )}
        </Group>

        {(row.CharacterAssets || row.CharacterBuyOrders || row.CharacterSellOrders) && (
          <Group title="Your position">
            <Row label="Units owned" value={int(row.CharacterAssets)} />
            <Row label="Open buy orders" value={int(row.CharacterBuyOrders)} />
            <Row label="Open sell orders" value={int(row.CharacterSellOrders)} />
          </Group>
        )}
      </SheetContent>
    </Sheet>
  );
}
