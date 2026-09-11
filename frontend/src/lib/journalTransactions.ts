// journalTransactions.ts — per-sale derivation for the Trade Journal's
// transactions view.
//
// The matcher already priced every row: gross, fees split into broker and tax,
// and net profit all arrive from the server. Two values are not stored because
// they are one arithmetic step each — cost basis and margin — and they live here
// rather than inline in the component so the judgement calls (an unmatched sell
// has no margin, a build row's cost basis is the same expression as a flip's)
// are stated once and tested.

import type { JournalLot } from "./api";

export interface TransactionRow extends JournalLot {
  /** buy_unit_price × matched_qty. 0 on an orphan sell. */
  cost: number;
  /**
   * net_profit ÷ cost, as a percentage. `null` when there is no cost basis to
   * divide by — an unmatched sell is not a 0% trade, it is a trade we cannot
   * price. Same rule the leaderboard uses for a null ROI.
   */
  marginPercent: number | null;
}

/**
 * Derives the two computed columns for one matched sell.
 *
 * `cost` is one expression for both sources: the engine stores a purchase price
 * on trade rows and a build unit cost (install + materials ÷ produced qty) on
 * manufacture rows in the same field, so nothing here needs to branch on
 * `source`. The drawer's `sell_gross - net_profit - sell_fees` is algebraically
 * the same number by a longer route.
 */
export function toTransactionRow(lot: JournalLot): TransactionRow {
  const cost = (lot.buy_unit_price ?? 0) * lot.matched_qty;
  return {
    ...lot,
    cost,
    marginPercent: cost > 0 ? (lot.net_profit / cost) * 100 : null,
  };
}

export function toTransactionRows(lots: JournalLot[]): TransactionRow[] {
  return lots.map(toTransactionRow);
}

export type TxnSortKey =
  | "sell_date"
  | "type_name"
  | "source"
  | "buy_unit_price"
  | "sell_unit_price"
  | "matched_qty"
  | "cost"
  | "sell_gross"
  | "buy_fees"
  | "sell_broker_fee"
  | "sell_tax"
  | "marginPercent"
  | "net_profit";

export type SortDir = "asc" | "desc";

/**
 * Sorts rows by one column, stably.
 *
 * Rows with no value for the sort key — a null margin, a missing buy price on
 * an orphan — always sort last regardless of direction. Ordering them as if they
 * were zero would drop unmatched sells into the middle of a profit ranking,
 * where they read as break-even trades rather than as rows with no answer.
 */
export function sortTransactions(
  rows: TransactionRow[],
  key: TxnSortKey,
  dir: SortDir,
): TransactionRow[] {
  const sign = dir === "asc" ? 1 : -1;
  const out = [...rows];
  out.sort((a, b) => {
    const av = a[key];
    const bv = b[key];
    const aMissing = av == null;
    const bMissing = bv == null;
    if (aMissing || bMissing) {
      if (aMissing && bMissing) return 0;
      return aMissing ? 1 : -1;
    }
    if (typeof av === "string" || typeof bv === "string") {
      return sign * String(av).localeCompare(String(bv));
    }
    return sign * ((av as number) - (bv as number));
  });
  return out;
}
