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

  it("groups trade-ins for the same product under one row", async () => {
    const user = userEvent.setup();
    const bp = { is_blueprint: true, type_id: 17637, product_type_id: 17636, type_name: "Raven Navy Issue Blueprint", product_name: "Raven Navy Issue" };
    streamed.rows = [
      row({ ...bp, offer_id: 19596, runs: 10, lp_cost: 200_000, best: 900, best_method: "build_list" }),
      row({ ...bp, offer_id: 14798, runs: 1, lp_cost: 100_000, best: 1_200, best_method: "sell_bpc" }),
    ];
    mount();
    await user.click(await screen.findByRole("button", { name: /^analyze$/i }));
    expect(await screen.findByText("2 offers")).toBeInTheDocument();
    expect(screen.queryByText("10 runs")).not.toBeInTheDocument();

    await user.click(screen.getByText("2 offers"));
    expect(screen.getByText(/10 runs/)).toBeInTheDocument();
    expect(screen.getByText(/^1 run ·|^1 run$/)).toBeInTheDocument();
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
