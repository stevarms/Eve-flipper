/* @vitest-environment jsdom */

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import type { LPOfferRow, LPStreamMessage } from "@/lib/api";

afterEach(cleanup);

const streamed: { rows: LPOfferRow[] } = { rows: [] };

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    getLPCorporations: vi.fn().mockResolvedValue([
      { corporation_id: 1000180, name: "State Protectorate", militia: true },
    ]),
    getLPBalances: vi.fn().mockResolvedValue({
      available: true,
      character_name: "Pilot",
      balances: [{ corporation_id: 1000180, name: "State Protectorate", loyalty_points: 300_000 }],
    }),
    getLPBPCPrices: vi.fn().mockResolvedValue({}),
    setLPBPCPrice: vi.fn().mockResolvedValue({}),
    analyzeLPStore: vi.fn(async (_req: unknown, onMessage: (m: LPStreamMessage) => void) => {
      onMessage({ type: "offers", corporation_id: 1000180, region_id: 10000002, rows: streamed.rows });
      onMessage({ type: "done" });
    }),
  };
});

const { LPStoreTab } = await import("./LPStoreTab");

function row(over: Partial<LPOfferRow>): LPOfferRow {
  return {
    offer_id: 1,
    type_id: 100,
    type_name: "Thing",
    product_type_id: 0,
    product_name: "",
    is_blueprint: false,
    category: "",
    group: "",
    market_path: [],
    runs: 0,
    quantity: 1,
    lp_cost: 1000,
    isk_cost: 0,
    required_items: [],
    cost: 0,
    unpriced: false,
    instant: null,
    listed: null,
    bpc_sale: null,
    build_instant: null,
    build_listed: null,
    best: null,
    best_method: "",
    units_per_redemption: 1,
    avg_daily_volume: 100,
    unit_bid: 0,
    unit_ask: 0,
    build_cost: 0,
    build_job_cost: 0,
    build_material_cost: 0,
    build_units: 0,
    build_listed_gross: 0,
    build_listed_net: 0,
    build_instant_gross: 0,
    build_instant_net: 0,
    broker_fee_percent: 0,
    sales_tax_percent: 0,
    bpc_per_run: 0,
    bpc_samples: 0,
    bpc_override: false,
    ...over,
  };
}

const pkg = { type_id: 93611, type_name: "Federal Strategic Materiel Supply Package", quantity: 8, unit_price: 1_000_000, priced: true };

function mount() {
  render(
    <I18nProvider>
      <LPStoreTab isLoggedIn={true} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  sessionStorage.clear();
  localStorage.clear();
  streamed.rows = [];
});

describe("LPStoreTab", () => {
  it("reads the LP balance from the character and ranks offers after analysis", async () => {
    const user = userEvent.setup();
    streamed.rows = [
      row({ offer_id: 1, type_id: 101, type_name: "Low Implant", best: 500, listed: 500, best_method: "list" }),
      row({ offer_id: 2, type_id: 102, type_name: "High Implant", best: 2_000, listed: 2_000, best_method: "list" }),
    ];
    mount();
    expect(await screen.findByText("300,000")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /^analyze$/i }));
    await screen.findByText("High Implant");
    const names = screen.getAllByText(/Implant$/).map((el) => el.textContent);
    expect(names).toEqual(["High Implant", "Low Implant"]);
  });

  it("shows ? for an offer whose required item cannot be priced", async () => {
    const user = userEvent.setup();
    streamed.rows = [row({ offer_id: 1, type_name: "Tagged Thing", unpriced: true, required_items: [{ ...pkg, priced: false, unit_price: 0 }] })];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    const cells = within((await screen.findByText("Tagged Thing")).closest("tr")!).getAllByText("?");
    expect(cells.length).toBeGreaterThan(0);
  });

  it("lists every trade-in as its own row, with no collapsing", async () => {
    const user = userEvent.setup();
    const bp = { is_blueprint: true, type_id: 17637, product_type_id: 17636, type_name: "Raven Navy Issue Blueprint", product_name: "Raven Navy Issue" };
    streamed.rows = [
      row({ ...bp, offer_id: 19596, runs: 10, lp_cost: 200_000, best: 900, best_method: "build_list" }),
      row({ ...bp, offer_id: 14798, runs: 1, lp_cost: 100_000, best: 1_200, best_method: "sell_bpc" }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    expect(await screen.findByText(/^10 runs/)).toBeInTheDocument();
    expect(screen.getByText(/^1 run/)).toBeInTheDocument();
    expect(screen.getAllByRole("checkbox", { name: /select raven navy issue blueprint/i })).toHaveLength(2);
  });

  it("sorts by category so implants sit together, and back the other way on a second click", async () => {
    const user = userEvent.setup();
    streamed.rows = [
      row({ offer_id: 1, type_id: 1, type_name: "Hail S", category: "Charge", group: "Projectile Ammo", best: 3_000 }),
      row({ offer_id: 2, type_id: 2, type_name: "Talon Alpha", category: "Implant", group: "Cyberimplant", best: 1_000 }),
      row({ offer_id: 3, type_id: 3, type_name: "Raven Navy Issue Blueprint", category: "Ship", group: "Battleship", best: 2_000 }),
      row({ offer_id: 4, type_id: 4, type_name: "Talon Beta", category: "Implant", group: "Cyberimplant", best: 500 }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    await screen.findByText("Hail S");
    const order = () => screen.getAllByRole("checkbox", { name: /^select /i }).map((el) => el.getAttribute("aria-label"));

    expect(order()).toEqual(["Select Hail S", "Select Raven Navy Issue Blueprint", "Select Talon Alpha", "Select Talon Beta"]);

    await user.click(screen.getByRole("button", { name: /^category$/i }));
    expect(order()).toEqual(["Select Hail S", "Select Talon Alpha", "Select Talon Beta", "Select Raven Navy Issue Blueprint"]);

    await user.click(screen.getByRole("button", { name: /^category/i }));
    expect(order()).toEqual(["Select Raven Navy Issue Blueprint", "Select Talon Alpha", "Select Talon Beta", "Select Hail S"]);
  });

  it("finds ammo by searching its market group, not just its name", async () => {
    const user = userEvent.setup();
    streamed.rows = [
      row({ offer_id: 1, type_id: 1, type_name: "Caldari Navy Scourge Light Missile", category: "Charge", group: "Light Missile", market_path: ["Ammunition & Charges", "Missiles", "Light Missiles"], best: 800 }),
      row({ offer_id: 2, type_id: 2, type_name: "Low-grade Talon Alpha", category: "Implant", group: "Cyberimplant", market_path: ["Implants & Boosters", "Implants"], best: 2_000 }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    await screen.findByText("Low-grade Talon Alpha");

    await user.type(screen.getByPlaceholderText(/search/i), "ammunition");
    expect(screen.getByText("Caldari Navy Scourge Light Missile")).toBeInTheDocument();
    expect(screen.queryByText("Low-grade Talon Alpha")).not.toBeInTheDocument();

    await user.clear(screen.getByPlaceholderText(/search/i));
    await user.type(screen.getByPlaceholderText(/search/i), "implant");
    expect(screen.getByText("Low-grade Talon Alpha")).toBeInTheDocument();
    expect(screen.queryByText("Caldari Navy Scourge Light Missile")).not.toBeInTheDocument();
  });

  it("shows profit per redemption under Best and explains each value's arithmetic on hover", async () => {
    const user = userEvent.setup();
    // Low-grade Talon Omega, no fees: (8.41M - 2.146M) / 2,000 LP = 3,132 ISK/LP, 6.264M a redemption.
    streamed.rows = [
      row({
        offer_id: 1,
        type_name: "Low-grade Talon Omega",
        lp_cost: 2_000,
        isk_cost: 1_000_000,
        cost: 2_146_000,
        unit_bid: 1_483_000,
        unit_ask: 8_410_000,
        instant: -331.5,
        listed: 3_132,
        best: 3_132,
        best_method: "list",
      }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    const tr = (await screen.findByText("Low-grade Talon Omega")).closest("tr")!;

    expect(within(tr).getByText(/6\.26\s?M \/ redemption/)).toBeInTheDocument();

    const listed = within(tr).getAllByText("3,132").find((el) => el.getAttribute("title")?.startsWith("List"))!;
    const tip = listed.getAttribute("title")!;
    expect(tip).toMatch(/List 1 at 8\.41\s?M each/);
    expect(tip).toMatch(/Offer cost \(ISK \+ items\): -2\.15\s?M/);
    expect(tip).toMatch(/Profit per redemption: 6\.26\s?M/);
    expect(tip).toMatch(/\/ 2,000 LP = 3,132 ISK\/LP/);

    expect(screen.getAllByText("ISK/LP").length).toBeGreaterThanOrEqual(6);
  });

  it("hides offers that trade less than the minimum volume, including ones with none", async () => {
    const user = userEvent.setup();
    streamed.rows = [
      row({ offer_id: 1, type_id: 301, type_name: "Busy Implant", avg_daily_volume: 12, best: 900, best_method: "list" }),
      row({ offer_id: 2, type_id: 302, type_name: "Quiet Implant", avg_daily_volume: 0.4, best: 5_000, best_method: "list" }),
      row({ offer_id: 3, type_id: 303, type_name: "Dead Implant", avg_daily_volume: 0, best: 9_000, best_method: "list" }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    await screen.findByText("Quiet Implant");

    await user.type(screen.getByRole("spinbutton", { name: /min vol\/day/i }), "1");
    expect(screen.getByText("Busy Implant")).toBeInTheDocument();
    expect(screen.queryByText("Quiet Implant")).not.toBeInTheDocument();
    expect(screen.queryByText("Dead Implant")).not.toBeInTheDocument();
  });

  it("tallies the selection against the LP balance and copies one merged multibuy", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    streamed.rows = [
      row({ offer_id: 1, type_id: 201, type_name: "Implant A", lp_cost: 200_000, cost: 8_000_000, best: 1_000, best_method: "list", required_items: [pkg] }),
      row({ offer_id: 2, type_id: 202, type_name: "Implant B", lp_cost: 50_000, cost: 4_000_000, best: 1_000, best_method: "list", required_items: [{ ...pkg, quantity: 4 }] }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    await user.click(await screen.findByRole("checkbox", { name: /select implant a/i }));
    await user.click(screen.getByRole("checkbox", { name: /select implant b/i }));
    const countBox = screen.getByRole("spinbutton", { name: /times to redeem implant b/i });
    await user.clear(countBox);
    await user.type(countBox, "3");

    // 200k + 3 x 50k = 350k LP against 300k: over balance
    const lp = screen.getByText((_, el) => el?.tagName === "SPAN" && /^350,000/.test(el.textContent ?? "") && /font-mono/.test(el.className));
    expect(lp.className).toMatch(/text-eve-error/);
    expect(lp.textContent).toBe("350,000 / 300,000");

    await user.click(screen.getByRole("button", { name: /copy multibuy/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("Federal Strategic Materiel Supply Package 20"));
  });
});
