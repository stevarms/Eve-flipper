import { describe, expect, it } from "vitest";
import type { LPOfferRow } from "@/lib/api";
import { computeLPBasket } from "./lpBasket";

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

const pkg = { type_id: 93611, type_name: "Federal Strategic Materiel Supply Package", unit_price: 1_000_000, priced: true };

describe("computeLPBasket", () => {
  it("is empty with nothing selected", () => {
    const b = computeLPBasket([row({})], new Map(), { includeBuild: false, balance: 5000 });
    expect(b.lp).toBe(0);
    expect(b.isk).toBe(0);
    expect(b.multibuy).toBe("");
    expect(b.overBalance).toBe(false);
  });

  it("totals LP, ISK and the expected return at each row's best, times the count", () => {
    const a = row({ offer_id: 1, lp_cost: 200_000, cost: 208_000_000, best: 1_500, best_method: "build_list" });
    const b = row({ offer_id: 2, lp_cost: 1_000, cost: 1_000_000, best: 2_000, best_method: "sell" });
    const basket = computeLPBasket([a, b], new Map([[1, 2], [2, 3]]), { includeBuild: false, balance: null });

    expect(basket.lp).toBe(2 * 200_000 + 3 * 1_000);
    expect(basket.isk).toBe(2 * 208_000_000 + 3 * 1_000_000);
    // profit = best x lp_cost per redemption
    expect(basket.profit).toBe(2 * 1_500 * 200_000 + 3 * 2_000 * 1_000);
    expect(basket.blendedISKPerLP).toBeCloseTo(basket.profit / basket.lp, 6);
    expect(basket.unvalued).toBe(0);
  });

  it("counts selected rows with no value separately rather than as zero", () => {
    const basket = computeLPBasket(
      [row({ offer_id: 1, best: null, unpriced: true }), row({ offer_id: 2, best: 10 })],
      new Map([[1, 1], [2, 1]]),
      { includeBuild: false, balance: null },
    );
    expect(basket.unvalued).toBe(1);
    expect(basket.blendedISKPerLP).toBeCloseTo(10 * 1000 / 1000, 6);
  });

  it("flags going over the LP balance without blocking anything", () => {
    const basket = computeLPBasket([row({ lp_cost: 200_000 })], new Map([[1, 16]]), { includeBuild: false, balance: 3_000_000 });
    expect(basket.lp).toBe(3_200_000);
    expect(basket.overBalance).toBe(true);
  });

  it("merges required items by type across offers, times the count", () => {
    const a = row({ offer_id: 1, required_items: [{ ...pkg, quantity: 8 }] });
    const b = row({ offer_id: 2, required_items: [{ ...pkg, quantity: 4 }, { type_id: 5, type_name: "Estamel Tharchon's Tag", quantity: 1, unit_price: 9, priced: true }] });
    const basket = computeLPBasket([a, b], new Map([[1, 1], [2, 1]]), { includeBuild: false, balance: null });
    expect(basket.multibuy.split("\n").sort()).toEqual([
      "Estamel Tharchon's Tag 1",
      "Federal Strategic Materiel Supply Package 12",
    ]);
  });

  it("merges required items and build materials by type only when asked", () => {
    const bp = row({
      offer_id: 1,
      is_blueprint: true,
      runs: 10,
      required_items: [{ type_id: 34, type_name: "Tritanium", quantity: 5, unit_price: 4, priced: true }],
      build_materials: [
        { type_id: 34, type_name: "Tritanium", quantity: 80_000_000 },
        { type_id: 35, type_name: "Pyerite", quantity: 40_000_000 },
      ],
    });
    const without = computeLPBasket([bp], new Map([[1, 2]]), { includeBuild: false, balance: null });
    expect(without.multibuy).toBe("Tritanium 10");

    const withBuild = computeLPBasket([bp], new Map([[1, 2]]), { includeBuild: true, balance: null });
    expect(withBuild.multibuy.split("\n").sort()).toEqual(["Pyerite 80000000", "Tritanium 160000010"]);
  });

  it("warns when a selection is more than a week of the market's volume", () => {
    const thin = row({ offer_id: 1, type_name: "Raven Navy Issue Blueprint", units_per_redemption: 10, avg_daily_volume: 2 });
    const deep = row({ offer_id: 2, units_per_redemption: 10, avg_daily_volume: 1000 });
    const none = row({ offer_id: 3, type_name: "Obscure", avg_daily_volume: 0 });
    const basket = computeLPBasket([thin, deep, none], new Map([[1, 2], [2, 2], [3, 1]]), { includeBuild: false, balance: null });

    expect(basket.warnings).toEqual([
      { offer_id: 1, name: "Raven Navy Issue Blueprint", days: 10 },
      { offer_id: 3, name: "Obscure", days: null },
    ]);
  });

  it("ignores a selection for an offer no longer in the table", () => {
    const basket = computeLPBasket([row({ offer_id: 1 })], new Map([[99, 5]]), { includeBuild: false, balance: null });
    expect(basket.lp).toBe(0);
  });
});
