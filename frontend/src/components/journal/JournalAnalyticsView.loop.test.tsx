/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

afterEach(cleanup);

/**
 * The Analytics half of the Trade Journal, driven through its real parent.
 *
 * TradeJournal.test.tsx pins the Summary half's refetch cycle shut, but it
 * mocks lib/api without getJournalAnalytics, so the Analytics view has never
 * been mounted under test. This exercises it with the props the parent
 * actually passes.
 */

const getJournalSummary = vi.fn();
const getJournalByType = vi.fn();
const getJournalAnalytics = vi.fn();
const syncTradeJournal = vi.fn();
const getAuthStatus = vi.fn();

vi.mock("../../lib/api", () => ({
  getJournalSummary: (...a: unknown[]) => getJournalSummary(...a),
  getJournalByType: (...a: unknown[]) => getJournalByType(...a),
  getJournalAnalytics: (...a: unknown[]) => getJournalAnalytics(...a),
  getJournalLots: vi.fn(async () => ({ lots: [], manufacturing_lots: [] })),
  getJournalLinkCandidates: vi.fn(async () => ({ candidates: [] })),
  linkJournalJob: vi.fn(async () => ({})),
  syncTradeJournal: (...a: unknown[]) => syncTradeJournal(...a),
  getAuthStatus: (...a: unknown[]) => getAuthStatus(...a),
}));

vi.mock("./PnLPrimitives", () => ({
  PnLChart: () => null,
  PnLLedgerTable: () => null,
  PnLStationsTable: () => null,
  SlotEfficiencyTable: () => null,
}));

import { TradeJournal } from "../TradeJournal";
import { I18nProvider } from "@/lib/i18n";

beforeAll(() => {
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }
});

const summaryFixture = () => ({
  totals: {},
  daily_pnl: [],
  tracking_since: { "char:90000001": "2026-01-01", "corp:98000001:3": "2026-01-01" },
  stale_syncs: [],
  fifo_mode: "strict_date",
  since: "2026-01-01T00:00:00Z",
});

/** A response with real rows: the empty-state early return would otherwise
 *  hide whether the view settled or is spinning. */
const analyticsFixture = () => ({
  analytics: {
    daily_pnl: [
      {
        date: "2026-01-02",
        buy_total: 100,
        sell_total: 200,
        net_pnl: 100,
        cumulative_pnl: 100,
        drawdown_pct: 0,
        transactions: 2,
      },
    ],
    top_items: [],
    top_stations: [],
    ledger: [{ type_id: 34, type_name: "Tritanium", realized_pnl: 100 }],
    open_positions: [],
    slot_efficiency: [],
    summary: {
      total_pnl: 100,
      roi_percent: 5,
      win_rate: 100,
      profitable_days: 1,
      total_days: 1,
      avg_daily_pnl: 100,
      best_day_pnl: 100,
      best_day_date: "2026-01-02",
      worst_day_pnl: 100,
      worst_day_date: "2026-01-02",
      total_bought: 2_000_000,
      total_sold: 2_000_100,
      sharpe_ratio: 1,
      max_drawdown_isk: 0,
      max_drawdown_pct: 0,
      max_drawdown_days: 0,
      profit_factor: 2,
      expectancy_per_trade: 50,
      open_cost_basis: 0,
      open_positions: 0,
    },
    settings: {},
    coverage: {},
  },
  leaderboard: [
    {
      type_id: 34,
      type_name: "Tritanium",
      net_pnl: 100,
      cost_basis: 2_000_000,
      revenue: 2_000_100,
      qty_sold: 10,
      transactions: 2,
      trade_pnl: 100,
      trade_cost: 2_000_000,
      manufacture_pnl: 0,
      manufacture_cost: 0,
      roi_percent: 0.005,
    },
  ],
  source: "",
  fifo_mode: "strict_date",
  since: "2026-01-01T00:00:00Z",
});

beforeEach(() => {
  vi.clearAllMocks();
  getAuthStatus.mockResolvedValue({
    logged_in: true,
    characters: [{ character_id: 90000001, character_name: "Pilot" }],
  });
  getJournalByType.mockImplementation(async () => ({ rows: [] }));
  getJournalSummary.mockImplementation(async () => summaryFixture());
  getJournalAnalytics.mockImplementation(async () => analyticsFixture());
});

describe("Analytics view fetch loop", () => {
  it("settles after switching to Analytics instead of refetching forever", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <TradeJournal isLoggedIn />
      </I18nProvider>,
    );
    await waitFor(() => expect(getJournalByType).toHaveBeenCalled());

    await user.click(screen.getByRole("button", { name: "Analytics" }));
    await waitFor(() => expect(getJournalAnalytics).toHaveBeenCalled());

    // Hold still and confirm the count stopped moving. A loop keeps climbing.
    await new Promise((r) => setTimeout(r, 80));
    const first = getJournalAnalytics.mock.calls.length;
    await new Promise((r) => setTimeout(r, 160));
    expect(getJournalAnalytics.mock.calls.length).toBe(first);
    expect(first).toBeLessThanOrEqual(2);
  });
});
