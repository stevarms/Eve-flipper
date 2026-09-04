/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { OrderRowDrawer, recommendationTone } from "./OrderRowDrawer";
import { en } from "@/lib/locale/en";
import type { TranslationKey } from "@/lib/i18n";
import type { OrderDeskOrder } from "@/lib/types";

// vitest does not run with `globals: true` here, so RTL's automatic cleanup
// is never registered. See TabWorkspace.test.tsx.
afterEach(cleanup);

beforeAll(() => {
  // Radix Dialog measures the viewport through matchMedia / ResizeObserver,
  // neither of which jsdom implements.
  if (!window.matchMedia) {
    window.matchMedia = ((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
  }
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  }
});

/** Real English strings, so a missing key shows up as a failure here too. */
const t = (key: TranslationKey, params?: Record<string, string | number>) => {
  let s: string = en[key] ?? key;
  if (params) for (const [k, v] of Object.entries(params)) s = s.replace(`{${k}}`, String(v));
  return s;
};

const ROW: OrderDeskOrder = {
  order_id: 1,
  type_id: 34,
  type_name: "Tritanium",
  location_id: 60003760,
  location_name: "Jita IV - Moon 4",
  region_id: 10000002,
  is_buy_order: false,
  price: 5.5,
  volume_remain: 400,
  volume_total: 1000,
  notional: 2200,
  net_unit_isk: 5.1,
  net_notional: 2040,
  position: 4,
  total_orders: 12,
  book_available: true,
  best_price: 5.4,
  suggested_price: 5.39,
  undercut_amount: 0.11,
  undercut_pct: 2.04,
  queue_ahead_qty: 9000,
  top_price_qty: 3000,
  avg_daily_volume: 120000,
  estimated_fill_per_day: 250,
  eta_days: 1.6,
  issued_at: "2026-08-01T12:00:00Z",
  expires_at: "2026-09-30T12:00:00Z",
  days_to_expire: 27,
  recommendation: "reprice",
  reason: "Outbid by 0.11 ISK",
  character_name: "Test Pilot",
  character_id: 90000001,
  relist_fee_isk: 22,
  net_relist_gain_isk: -3,
  warn_unprofitable_relist: true,
};

/**
 * The three-tier rule only holds if tier 2 actually carries what tier 1 gave
 * up (docs/UI_DESIGN_SYSTEM.md §4). The Orders grid dropped five columns —
 * owner, station, side, best price and expiry — on the promise that the
 * drawer keeps them. This is that promise, asserted.
 */
describe("OrderRowDrawer", () => {
  it("carries every column the six-column grid gave up", () => {
    render(<OrderRowDrawer row={ROW} onClose={() => {}} t={t} locale="en" />);

    // Owner and side/station live in the header; the rest are detail rows.
    expect(screen.getAllByText(/Test Pilot/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Jita IV - Moon 4/).length).toBeGreaterThan(0);
    expect(screen.getByText(en.ordersColBest)).toBeInTheDocument();
    expect(screen.getByText(en.ordersColExpiry)).toBeInTheDocument();
    // Sell side, rendered into the description line next to the station.
    expect(screen.getAllByText(new RegExp(en.charSell)).length).toBeGreaterThan(0);
  });

  it("surfaces the relist arithmetic the grid never had room for", () => {
    render(<OrderRowDrawer row={ROW} onClose={() => {}} t={t} locale="en" />);
    expect(screen.getByText(en.ordersDrawerRelistFee)).toBeInTheDocument();
    expect(screen.getByText(en.ordersDrawerRelistGain)).toBeInTheDocument();
    expect(screen.getByText(en.ordersDrawerQueueAhead)).toBeInTheDocument();
    // The ⚠ in the grid is a glyph; the drawer says what it means.
    expect(screen.getByText(en.ordersDrawerFeeEatsGain)).toBeInTheDocument();
  });

  it("renders nothing when no row is selected", () => {
    const { container } = render(
      <OrderRowDrawer row={null} onClose={() => {}} t={t} locale="en" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("does not crash on a malformed timestamp", () => {
    render(
      <OrderRowDrawer
        row={{ ...ROW, issued_at: "not a date", expires_at: "" }}
        onClose={() => {}}
        t={t}
        locale="en"
      />,
    );
    expect(screen.getByText(en.ordersDrawerIssued)).toBeInTheDocument();
  });
});

/**
 * The grid badge and the drawer badge must agree, which is only guaranteed
 * while they both come from here.
 */
describe("recommendationTone", () => {
  it("maps cancel to loss and reprice to warn", () => {
    expect(recommendationTone("cancel", true)).toBe("loss");
    expect(recommendationTone("reprice", true)).toBe("warn");
  });

  it("only calls a hold profitable when the book was actually read", () => {
    expect(recommendationTone("hold", true)).toBe("profit");
    // No book means we cannot claim the order is well placed.
    expect(recommendationTone("hold", false)).toBe("neutral");
  });
});
