import { describe, expect, it } from "vitest";
import type { JournalLot } from "./api";
import { sortTransactions, toTransactionRow, toTransactionRows } from "./journalTransactions";

// A matched sell, minus the fields each test overrides.
function lot(over: Partial<JournalLot>): JournalLot {
  return {
    source: "trade",
    sell_date: "2026-09-01T12:00:00Z",
    sell_txn_id: 1,
    sell_wallet_key: "char:1",
    type_id: 34,
    type_name: "Tritanium",
    matched_qty: 10,
    sell_unit_price: 100,
    sell_gross: 1000,
    sell_fees: 51,
    sell_broker_fee: 15,
    sell_tax: 36,
    net_profit: 449,
    buy_unit_price: 50,
    buy_fees: 5,
    ...over,
  };
}

describe("toTransactionRow", () => {
  it("costs a trade row from its purchase price", () => {
    const row = toTransactionRow(lot({}));
    expect(row.cost).toBe(500);
    // 449 / 500
    expect(row.marginPercent).toBeCloseTo(89.8, 6);
  });

  it("costs a build row from the same field, with no branch on source", () => {
    // The engine stores install + materials ÷ produced qty in buy_unit_price on
    // manufacture rows, so this must not need its own formula.
    const row = toTransactionRow(
      lot({ source: "manufacture", buy_unit_price: 20, net_profit: 749, manufacture_job_id: 77 }),
    );
    expect(row.cost).toBe(200);
    expect(row.marginPercent).toBeCloseTo(374.5, 6);
  });

  it("gives an unmatched sell no margin rather than 0%", () => {
    const row = toTransactionRow(
      lot({ source: "orphan", buy_unit_price: undefined, buy_fees: undefined, net_profit: 0 }),
    );
    expect(row.cost).toBe(0);
    // Not 0: a sale we cannot price is not a break-even sale.
    expect(row.marginPercent).toBeNull();
  });

  it("reports a loss as a negative margin", () => {
    const row = toTransactionRow(lot({ buy_unit_price: 120, net_profit: -251 }));
    expect(row.marginPercent).toBeCloseTo((-251 / 1200) * 100, 6);
  });
});

describe("sortTransactions", () => {
  const rows = toTransactionRows([
    lot({ sell_txn_id: 1, sell_date: "2026-09-01T00:00:00Z", type_name: "Bravo", net_profit: 300 }),
    lot({ sell_txn_id: 2, sell_date: "2026-09-03T00:00:00Z", type_name: "Alpha", net_profit: -100 }),
    lot({ sell_txn_id: 3, sell_date: "2026-09-02T00:00:00Z", type_name: "Charlie", net_profit: 900 }),
  ]);

  it("orders by date in both directions", () => {
    expect(sortTransactions(rows, "sell_date", "desc").map((r) => r.sell_txn_id)).toEqual([2, 3, 1]);
    expect(sortTransactions(rows, "sell_date", "asc").map((r) => r.sell_txn_id)).toEqual([1, 3, 2]);
  });

  it("orders by profit numerically, not as text", () => {
    expect(sortTransactions(rows, "net_profit", "desc").map((r) => r.net_profit)).toEqual([
      900, 300, -100,
    ]);
  });

  it("orders names alphabetically", () => {
    expect(sortTransactions(rows, "type_name", "asc").map((r) => r.type_name)).toEqual([
      "Alpha",
      "Bravo",
      "Charlie",
    ]);
  });

  it("sinks rows with no value for the key, whichever direction", () => {
    const withOrphan = toTransactionRows([
      lot({ sell_txn_id: 9, source: "orphan", buy_unit_price: undefined, net_profit: 0 }),
      lot({ sell_txn_id: 1, net_profit: -500 }),
      lot({ sell_txn_id: 2, net_profit: 500 }),
    ]);
    // A null margin must not land mid-ranking, where it would read as a
    // break-even trade.
    expect(sortTransactions(withOrphan, "marginPercent", "desc").map((r) => r.sell_txn_id)).toEqual([
      2, 1, 9,
    ]);
    expect(sortTransactions(withOrphan, "marginPercent", "asc").map((r) => r.sell_txn_id)).toEqual([
      1, 2, 9,
    ]);
  });

  it("leaves the input array untouched", () => {
    const before = rows.map((r) => r.sell_txn_id);
    sortTransactions(rows, "net_profit", "asc");
    expect(rows.map((r) => r.sell_txn_id)).toEqual(before);
  });
});
