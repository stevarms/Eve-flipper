/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { GapsPanel } from "./FWSupply";
import type { FWCampaign, FWSupplyRow, FWPlanResponse } from "@/lib/api";

// vitest does not run with `globals: true` here, so RTL's automatic cleanup
// is never registered -- see TabWorkspace.test.tsx / TradeJournal.test.tsx.
afterEach(cleanup);

/**
 * fw_supply, round four: the gap table's checkboxes replace the automatic
 * budget-trim as what drives the action bar. These tests pin the two
 * properties that make that trustworthy:
 *
 * - A row with nothing computed to select (no price, no quantity, or thin
 *   evidence marking it unshippable) cannot be checked, so "checking a box"
 *   never silently produces a blank multibuy line or a zero-quantity lot.
 * - The action bar reflects exactly what is checked, not the plan's own
 *   `shipment.lines` -- the bug this round exists to fix was "Record all"
 *   reading a stale, pre-toggle list.
 */

function row(over: Partial<FWSupplyRow>): FWSupplyRow {
  return {
    type_id: 1,
    type_name: "Thing",
    category: "ammo",
    volume_m3: 1,
    daily_destroyed: 10,
    kills_with_item: 10,
    daily_destroyed_long: 0,
    kills_with_item_long: 0,
    sized_by: "short",
    shippable: true,
    included: false,
    stocked_qty: 0,
    days_of_cover: 0,
    local_best_sell: 0,
    local_order_count: 0,
    jita_best_sell: 100,
    landed_cost: 100,
    floor_price: 110,
    reference_price: 150,
    reference_markup: 1.5,
    reference_source: "ladder",
    suggested_price: 150,
    suggested_markup: 1.5,
    price_rule: "reference",
    price_reason: "",
    competing_units_below: 0,
    competing_orders_below: 0,
    verdict: "gap",
    verdict_reason: "",
    suggested_qty: 10,
    cover_sized_qty: 10,
    qty_reason: "",
    cargo_m3: 10,
    cost_isk: 1000,
    net_unit_isk: 140,
    unit_profit_isk: 40,
    margin_pct: 40,
    profit_isk: 400,
    ...over,
  };
}

function makePlan(rows: FWSupplyRow[]): NonNullable<FWPlanResponse["plan"]> {
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
    rows,
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

function makeCampaign(over: Partial<FWCampaign> = {}): FWCampaign {
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
    ...over,
  };
}

const noop = () => {};

function mount(rows: FWSupplyRow[], campaignOver: Partial<FWCampaign> = {}, onRecord = vi.fn()) {
  const onToggleTypeOverride = vi.fn();
  render(
    <I18nProvider>
      <GapsPanel
        plan={makePlan(rows)}
        campaign={makeCampaign(campaignOver)}
        onToggleTypeOverride={onToggleTypeOverride}
        rows={rows}
        busy={false}
        generating={false}
        show="all"
        setShow={noop}
        expanded={null}
        setExpanded={noop}
        onRecord={onRecord}
        ladderNote=""
      />
    </I18nProvider>,
  );
  return { onRecord, onToggleTypeOverride };
}

describe("GapsPanel checkboxes are never disabled", () => {
  it("a row with a real price and quantity is checkable, no caveat title", () => {
    mount([row({ type_id: 1, type_name: "Inferno Light Missile" })]);
    const boxes = screen.getAllByRole("checkbox");
    // [0] is the header's select-all box; [1] is the row's own.
    expect(boxes[1]).not.toBeDisabled();
    expect(boxes[1]).not.toHaveAttribute("title");
  });

  it("a covered row with nothing even to guess from is still checkable, with a title explaining why", () => {
    mount([
      row({
        type_id: 2,
        type_name: "Civilian Miner",
        verdict: "covered",
        suggested_qty: 0,
        suggested_price: 0,
        cost_isk: 0,
        daily_destroyed: 0,
        kills_with_item: 0,
        verdict_reason: "nothing measurable is being destroyed",
      }),
    ]);
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes[1]).not.toBeDisabled();
    expect(boxes[1]).toHaveAttribute("title", expect.stringContaining("nothing measurable"));
  });

  it("a covered row with a real destruction rate carries no caveat -- the prefill already gives it a quantity", () => {
    mount([
      row({
        type_id: 2,
        type_name: "Spike S",
        verdict: "covered",
        suggested_qty: 0,
        suggested_price: 40.47,
        cost_isk: 0,
        // Two killmails, 2,278 units a day -- ammo comes in stacks, so kills
        // alone would say "bring 2" when the rate says otherwise.
        kills_with_item: 2,
        daily_destroyed: 2278,
        verdict_reason: "219 days of cover against 2278 destroyed a day; already stocked",
      }),
    ]);
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes[1]).not.toBeDisabled();
    expect(boxes[1]).not.toHaveAttribute("title");
  });

  it("a thin-evidence row that still has a real quantity carries no caveat -- shippable is not what gates a checkbox", () => {
    mount([
      row({
        type_id: 3,
        type_name: "5MN Y-T8 Compact Microwarpdrive",
        shippable: false,
        kills_with_item: 1,
      }),
    ]);
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes[1]).not.toBeDisabled();
    expect(boxes[1]).not.toHaveAttribute("title");
  });
});

describe("GapsPanel selection drives the action bar, not the shipment", () => {
  it("records exactly the checked rows at their own suggested_qty, not a trimmed shipment", async () => {
    const user = userEvent.setup();
    const gap = row({ type_id: 1, type_name: "Inferno Light Missile", suggested_qty: 6000 });
    const other = row({ type_id: 2, type_name: "Warrior II", suggested_qty: 40 });
    const { onRecord } = mount([gap, other]);

    const boxes = screen.getAllByRole("checkbox");
    await user.click(boxes[1]); // the first row, not the header or the second row

    await user.click(screen.getByRole("button", { name: /record selected as lots/i }));

    expect(onRecord).toHaveBeenCalledTimes(1);
    const recorded = onRecord.mock.calls[0][0] as FWSupplyRow[];
    expect(recorded).toHaveLength(1);
    expect(recorded[0].type_id).toBe(1);
    expect(recorded[0].suggested_qty).toBe(6000);
  });

  it("copies exactly the checked rows to the clipboard, unioned from neither the shipment nor every row", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    const gap = row({ type_id: 1, type_name: "Inferno Light Missile", suggested_qty: 6000 });
    const other = row({ type_id: 2, type_name: "Warrior II", suggested_qty: 40 });
    mount([gap, other]);

    const boxes = screen.getAllByRole("checkbox");
    await user.click(boxes[1]);

    await user.click(screen.getByRole("button", { name: /add selected to multibuy/i }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("Inferno Light Missile 6000"));
  });

  it("leaves the record button disabled with nothing checked", () => {
    mount([row({ type_id: 1 })]);
    expect(screen.getByRole("button", { name: /record selected as lots/i })).toBeDisabled();
  });

  it("warns, but does not disable recording, when the selection exceeds remaining budget", async () => {
    const user = userEvent.setup();
    const expensive = row({ type_id: 1, type_name: "Freighter Hull", cost_isk: 5_000_000_000, suggested_qty: 1 });
    mount([expensive], { budget: {
      budget_isk: 1_000_000_000,
      committed_isk: 0,
      headroom_isk: 1_000_000_000,
      listed_value_isk: 0,
      lots: 0,
      committed_lots: 0,
      by_state: [],
      by_owner: [],
      by_dest: [],
    } });

    const boxes = screen.getAllByRole("checkbox");
    await user.click(boxes[1]);

    expect(await screen.findByText(/over your remaining budget/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /record selected as lots/i })).not.toBeDisabled();
  });

  it("checking a covered row with nothing at all to guess from names it and leaves it out of multibuy and recording", async () => {
    const user = userEvent.setup();
    const civilianMiner = row({
      type_id: 1,
      type_name: "Civilian Miner",
      verdict: "covered",
      suggested_qty: 0,
      suggested_price: 0,
      cost_isk: 0,
      daily_destroyed: 0,
      kills_with_item: 0,
    });
    const gap = row({ type_id: 2, type_name: "Inferno Light Missile", suggested_qty: 6000 });
    const { onRecord } = mount([civilianMiner, gap]);

    const boxes = screen.getAllByRole("checkbox");
    await user.click(boxes[1]); // Civilian Miner, checkable despite being covered
    await user.click(boxes[2]); // Inferno Light Missile, has a real quantity

    expect(await screen.findByText(/1 of these have no computed quantity yet/i)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /record selected as lots/i }));
    const recorded = onRecord.mock.calls[0][0] as FWSupplyRow[];
    expect(recorded.map((r) => r.type_id)).toEqual([2]);
  });

  it("pre-fills a covered row's quantity from rate x cover target, not from kills -- ammo comes in stacks", async () => {
    const user = userEvent.setup();
    // Under 100k, the floor is 50. 2,278 a day for 30 days is nowhere near
    // it, and only 2 killmails carried it -- kills would have suggested 2.
    const highRate = row({
      type_id: 1,
      type_name: "Spike S",
      verdict: "covered",
      suggested_qty: 0,
      jita_best_sell: 32,
      kills_with_item: 2,
      daily_destroyed: 2278,
    });
    // The same band, but a rate low enough that rate x cover target lands
    // under the floor -- the guess should not round up past that.
    const lowRate = row({
      type_id: 2,
      type_name: "Quake S",
      verdict: "covered",
      suggested_qty: 0,
      jita_best_sell: 40,
      kills_with_item: 40,
      daily_destroyed: 0.3,
    });
    mount([highRate, lowRate]);

    const boxes = screen.getAllByRole("checkbox");
    await user.click(boxes[1]);
    await user.click(boxes[2]);

    // campaign.target_cover_days is 30 in makeCampaign(): 2278*30 caps at the
    // 50 floor; 0.3*30 = 9, under the floor, so it stands as computed.
    const qtyInputs = screen.getAllByPlaceholderText("qty") as HTMLInputElement[];
    expect(qtyInputs.map((i) => i.value)).toEqual(["50", "9"]);
  });

  it("a typed quantity for a checked, zero-qty row flows into multibuy and the recorded lot -- the arbitrage case", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    // 500k stocked against a slow rate reads as "covered," but the reference
    // price sits real ISK above what it costs to land -- a trade the cover
    // model has no way to recommend and no reason to be allowed to block.
    // daily_destroyed is 0 so no prefill masks the typed override.
    const spike = row({
      type_id: 1,
      type_name: "Spike S",
      verdict: "covered",
      suggested_qty: 0,
      suggested_price: 40.47,
      jita_best_sell: 32,
      cost_isk: 0,
      daily_destroyed: 0,
      kills_with_item: 0,
    });
    const { onRecord } = mount([spike]);

    await user.click(screen.getAllByRole("checkbox")[1]);
    expect(screen.getByRole("button", { name: /record selected as lots/i })).toBeDisabled();

    await user.type(screen.getByPlaceholderText("qty"), "5000");

    // The typed quantity unlocks the row for every action -- the note is
    // gone, multibuy carries it, and the lot records at the typed amount.
    expect(screen.queryByText(/have no computed quantity yet/i)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /add selected to multibuy/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("Spike S 5000"));

    await user.click(screen.getByRole("button", { name: /record selected as lots/i }));
    const recorded = onRecord.mock.calls[0][0] as FWSupplyRow[];
    expect(recorded).toHaveLength(1);
    expect(recorded[0].suggested_qty).toBe(5000);
    // The price is the engine's own, never invented by the override.
    expect(recorded[0].suggested_price).toBe(40.47);
  });

  it("lets a real suggested quantity be reduced -- 69 Caracals is a real number and a real hassle to haul", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    const caracal = row({
      type_id: 1,
      type_name: "Caracal",
      suggested_qty: 69,
      jita_best_sell: 9_500_000,
      landed_cost: 9_500_000,
      volume_m3: 118_000,
      unit_profit_isk: 1_000_000,
      cost_isk: 69 * 9_500_000,
      cargo_m3: 69 * 118_000,
      profit_isk: 69 * 1_000_000,
    });
    const { onRecord } = mount([caracal]);

    await user.click(screen.getAllByRole("checkbox")[1]);
    const qtyInput = screen.getByPlaceholderText("qty") as HTMLInputElement;
    // Pre-filled with the engine's own suggestion, not empty.
    expect(qtyInput.value).toBe("69");

    // A real edit: select the "69" away and type "5" over it. Clearing must
    // not snap back to 69 before the new digit lands, or "5" would append
    // onto the old value instead of replacing it.
    await user.clear(qtyInput);
    await user.type(qtyInput, "5");

    await user.click(screen.getByRole("button", { name: /add selected to multibuy/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("Caracal 5"));

    await user.click(screen.getByRole("button", { name: /record selected as lots/i }));
    const recorded = onRecord.mock.calls[0][0] as FWSupplyRow[];
    expect(recorded[0].suggested_qty).toBe(5);
  });
});
