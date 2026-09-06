/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

// vitest does not run with `globals: true` here, so RTL's automatic cleanup
// is never registered. See TabWorkspace.test.tsx.
afterEach(cleanup);

/**
 * The Trade Journal used to refetch itself forever.
 *
 * `summary` is a freshly-parsed response object, so its identity changed on
 * every fetch. `knownCorpDivs` memoized on `summary`, `scope` memoized on
 * `knownCorpDivs`, `loadAll` was a useCallback over `scope`, and an effect
 * ran `loadAll` whenever its identity changed -- a closed cycle. The visible
 * symptom was the per-item table flickering; the invisible one was two API
 * calls per cycle, continuously.
 *
 * A second cycle sat on top of it: the silent catch-up sync keyed off
 * `summary` and ended by calling loadAll(), which replaced `summary`.
 *
 * These tests pin both shut by counting calls after the component settles.
 * They deliberately assert a small upper bound rather than an exact count --
 * the point is that it converges, not that it converges in exactly one.
 */

const getJournalSummary = vi.fn();
const getJournalByType = vi.fn();
const syncTradeJournal = vi.fn();
const getAuthStatus = vi.fn();

vi.mock("../lib/api", () => ({
  getJournalSummary: (...a: unknown[]) => getJournalSummary(...a),
  getJournalByType: (...a: unknown[]) => getJournalByType(...a),
  getJournalLots: vi.fn(async () => ({ lots: [], manufacturing_lots: [] })),
  getJournalLinkCandidates: vi.fn(async () => ({ candidates: [] })),
  linkJournalJob: vi.fn(async () => ({})),
  syncTradeJournal: (...a: unknown[]) => syncTradeJournal(...a),
  getAuthStatus: (...a: unknown[]) => getAuthStatus(...a),
}));

// PnLChart renders an SVG off measured layout; jsdom has no layout.
vi.mock("./journal/PnLPrimitives", () => ({
  PnLChart: () => null,
}));

import { TradeJournal } from "./TradeJournal";
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

/** Two wallets, one of them a corp division — the corp key is what fed
 *  `knownCorpDivs`, so it has to be present for the cycle to be reachable. */
const summaryFixture = (staleDays: number | null) => ({
  totals: {},
  daily_pnl: [],
  tracking_since: { "char:90000001": "2026-01-01", "corp:98000001:3": "2026-01-01" },
  stale_syncs:
    staleDays == null
      ? []
      : [{ wallet_key: "char:90000001", last_sync_at: "2026-01-01T00:00:00Z", days_ago: staleDays }],
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
});

const mount = () =>
  render(
    <I18nProvider>
      <TradeJournal isLoggedIn />
    </I18nProvider>,
  );

/** Let every queued promise + effect drain, then hold still and confirm the
 *  call count stopped moving. A loop keeps climbing across the second wait. */
async function settle(): Promise<{ summary: number; byType: number; sync: number }> {
  await waitFor(() => expect(getJournalByType).toHaveBeenCalled());
  await new Promise((r) => setTimeout(r, 60));
  const first = getJournalSummary.mock.calls.length;
  await new Promise((r) => setTimeout(r, 120));
  expect(getJournalSummary.mock.calls.length).toBe(first);
  return {
    summary: getJournalSummary.mock.calls.length,
    byType: getJournalByType.mock.calls.length,
    sync: syncTradeJournal.mock.calls.length,
  };
}

describe("TradeJournal fetch loop", () => {
  it("settles after mount instead of refetching forever", async () => {
    // mockImplementation, not mockResolvedValue: the loop only exists because
    // each response is a distinct object, the way a real JSON parse is. A
    // shared reference makes React bail out of the re-render and hides it.
    getJournalSummary.mockImplementation(async () => summaryFixture(null));
    mount();
    const n = await settle();
    expect(n.summary).toBeLessThanOrEqual(2);
    expect(n.byType).toBeLessThanOrEqual(2);
    expect(n.sync).toBe(0);
  });

  it("auto-syncs a stale wallet once, even when the sync cannot clear it", async () => {
    // The pathological case: the wallet stays stale after syncing (expired
    // refresh token, revoked corp role, a character that left the account).
    // The old code re-entered on every replaced summary and never stopped.
    getJournalSummary.mockImplementation(async () => summaryFixture(31));
    syncTradeJournal.mockResolvedValue({
      wallets: [],
      industry_jobs_auto_linked: 0,
      industry_jobs_still_unlinked_ambiguous: 0,
    });
    mount();
    const n = await settle();
    expect(n.sync).toBe(1);
    expect(n.summary).toBeLessThanOrEqual(3);
  });
});
