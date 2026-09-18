/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * The bug this round exists to fix: toggling "always ship" / "never ship"
 * only patched the campaign's stored lists, and the cached plan on screen
 * stayed untouched until an unprompted, separate Regenerate click -- so
 * "Record all" recorded the pre-toggle shipment and the toggle looked like it
 * did nothing. The fix is that the drawer's toggle patches AND regenerates,
 * in that order, in one click. This test is the one that would have caught
 * the original bug: it fails if the toggle stops at the patch.
 */

const calls: string[] = [];
// Bare vi.fn()s, not typed implementations -- vi.mock's factory is hoisted
// above these declarations, so the factory below can only close over them
// safely by way of the untyped (...a) => fn(...a) indirection, the same
// shape TradeJournal.test.tsx uses for the same reason.
const updateFWCampaign = vi.fn();
const generateFWPlan = vi.fn();
const getFWPlan = vi.fn();
const getFWCampaign = vi.fn();
const listFWCampaigns = vi.fn();
const getAuthStatus = vi.fn();

vi.mock("@/lib/api", () => ({
  listFWCampaigns: (...a: unknown[]) => listFWCampaigns(...a),
  getFWCampaign: (...a: unknown[]) => getFWCampaign(...a),
  getFWPlan: (...a: unknown[]) => getFWPlan(...a),
  generateFWPlan: (...a: unknown[]) => generateFWPlan(...a),
  updateFWCampaign: (...a: unknown[]) => updateFWCampaign(...a),
  createFWCampaign: vi.fn(),
  deleteFWCampaign: vi.fn(),
  deleteFWLot: vi.fn(),
  saveFWLot: vi.fn(async () => campaignFixture()),
  setFWLotState: vi.fn(),
  getFWCampaignOrders: vi.fn(async () => ({ orders: [] })),
  applyFWCampaignOrders: vi.fn(),
  bulkFWLots: vi.fn(),
  getAuthStatus: (...a: unknown[]) => getAuthStatus(...a),
}));

import { FWSupply } from "./FWSupply";
import { I18nProvider } from "@/lib/i18n";
import type { FWCampaign, FWPlanResponse, FWSupplyRow } from "@/lib/api";

function rowFixture(): FWSupplyRow {
  return {
    type_id: 12345,
    type_name: "5MN Y-T8 Compact Microwarpdrive",
    category: "module",
    volume_m3: 5,
    daily_destroyed: 0.3,
    kills_with_item: 1,
    daily_destroyed_long: 0,
    kills_with_item_long: 0,
    sized_by: "short",
    shippable: false,
    included: false,
    stocked_qty: 0,
    days_of_cover: 0,
    local_best_sell: 401_200,
    local_order_count: 155,
    jita_best_sell: 423_700,
    landed_cost: 423_700,
    floor_price: 480_828,
    reference_price: 550_000,
    reference_markup: 1.3,
    reference_source: "category ceiling",
    suggested_price: 0,
    suggested_markup: 0,
    price_rule: "none",
    price_reason: "155 units rest at 401200.00, under our landed cost of 480828.09 -- undercutting them would lose money",
    competing_units_below: 155,
    competing_orders_below: 3,
    verdict: "unpriceable",
    verdict_reason: "155 units rest at 401200.00, under our landed cost of 480828.09 -- undercutting them would lose money",
    suggested_qty: 0,
    cover_sized_qty: 0,
    qty_reason: "",
    cargo_m3: 0,
    cost_isk: 0,
    net_unit_isk: 0,
    unit_profit_isk: 0,
    margin_pct: 0,
    profit_isk: 0,
  };
}

function planFixture(): NonNullable<FWPlanResponse["plan"]> {
  return {
    campaign_id: 7,
    generated_at: "2026-09-18T00:00:00Z",
    militia_faction_id: 500001,
    militia_name: "Caldari State",
    ring: [],
    ring_radius: 2,
    frontline_systems: 3,
    demand: {
      window_seconds: 604800,
      fetched_kills: 100,
      in_warzone_kills: 40,
      truncated: false,
      destroyed_types: 5,
      sampled_at: "2026-09-18T00:00:00Z",
      long_window_seconds: 0,
      long_covered_seconds: 0,
      long_fetched_kills: 0,
      long_in_warzone_kills: 0,
      long_destroyed_types: 0,
      long_truncated: false,
      size_against: "short",
    },
    ladder: { station_id: 60015070, bands: [] },
    rows: [rowFixture()],
    shipment: {
      lines: [],
      total_cost_isk: 0,
      total_cargo_m3: 0,
      total_landed_isk: 0,
      total_profit_isk: 0,
      margin_pct: 0,
      trips: 0,
      headroom_isk: 0,
      remaining_isk: 0,
      fully_funded: 0,
      trimmed: 0,
      dropped: 0,
      notes: [],
    },
    budget: {
      budget_isk: 0,
      committed_isk: 0,
      headroom_isk: 0,
      listed_value_isk: 0,
      lots: 0,
      committed_lots: 0,
      by_state: [],
      by_owner: [],
      by_dest: [],
    },
  };
}

function campaignFixture(): FWCampaign {
  return {
    campaign_id: 7,
    name: "Caldari -- Onnamon",
    militia_faction_id: 500001,
    source_station_id: 60003760,
    dest_station_id: 60015070,
    budget_isk: 3_000_000_000,
    target_cover_days: 30,
    covered_multiple: 3,
    min_margin_pct: 1,
    freight_isk_per_m3: 0,
    step_over_days_cover: 0.5,
    category_ceilings: {},
    ship_profile: "freighter",
    max_trips: 0,
    long_demand_window_seconds: 0,
    size_against: "short",
    max_jumps_from_front: 2,
    pinned_systems: [],
    excluded_systems: [],
    included_types: [],
    excluded_types: [],
    buyer_character_id: 0,
    seller_owner_kind: "",
    seller_owner_id: 0,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-18T00:00:00Z",
    budget: {
      budget_isk: 3_000_000_000,
      committed_isk: 0,
      headroom_isk: 3_000_000_000,
      listed_value_isk: 0,
      lots: 0,
      committed_lots: 0,
      by_state: [],
      by_owner: [],
      by_dest: [],
    },
    lots: [],
  };
}

afterEach(cleanup);
beforeEach(() => {
  vi.clearAllMocks();
  calls.length = 0;
  listFWCampaigns.mockResolvedValue([campaignFixture()]);
  getFWCampaign.mockResolvedValue(campaignFixture());
  getFWPlan.mockResolvedValue({ has_plan: true, plan: planFixture() });
  getAuthStatus.mockResolvedValue({ logged_in: true, characters: [] });
  updateFWCampaign.mockImplementation(async () => {
    calls.push("updateFWCampaign");
    return campaignFixture();
  });
  generateFWPlan.mockImplementation(async () => {
    calls.push("generateFWPlan");
    return { has_plan: true, plan: planFixture() };
  });
});

describe("the drawer's always-ship toggle", () => {
  it("patches the campaign, then actually regenerates -- not a cache-only read", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider>
        <FWSupply isLoggedIn />
      </I18nProvider>,
    );

    await user.click(await screen.findByRole("button", { name: /gaps/i }));
    // The row is `unpriceable`, which the default "actionable" filter (gap /
    // thin only) hides -- exactly the state a competition-blocked row starts
    // in, so the drawer has to be reached through "All".
    const allTab = await screen.findByText((text, el) => el?.tagName === "BUTTON" && /^all/i.test(text));
    await user.click(allTab);
    await user.click(await screen.findByText("5MN Y-T8 Compact Microwarpdrive"));
    await user.click(await screen.findByRole("button", { name: "Ship anyway" }));

    await waitFor(() => expect(generateFWPlan).toHaveBeenCalled());
    expect(updateFWCampaign).toHaveBeenCalledTimes(1);
    expect(calls).toEqual(["updateFWCampaign", "generateFWPlan"]);
  });
});
