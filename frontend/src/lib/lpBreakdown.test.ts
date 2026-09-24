import { describe, expect, it } from "vitest";
import type { LPOfferRow } from "@/lib/api";
import { en } from "@/lib/locale/en";
import type { TranslationKey } from "@/lib/i18n";
import { lpBreakdownLines, lpMethodsWithValues } from "./lpBreakdown";

// A plain interpolating t() over the English strings, and a formatter that
// shows exact numbers, so the assertions read as the sums they check.
const t = (key: TranslationKey, params?: Record<string, string | number>) =>
  (en[key] as string).replace(/\{(\w+)\}/g, (_, k) => String(params?.[k] ?? `{${k}}`));
const fmt = (n: number) => Math.round(n).toLocaleString("en-US");

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

describe("lpBreakdownLines", () => {
  it("lists a sale as gross, each fee, net, offer cost, profit and per LP", () => {
    // 1 x 10M listed; 1.5% broker = 150k, 3.5% tax = 350k; net 9.5M; cost 2M; profit 7.5M over 2,000 LP.
    const r = row({ lp_cost: 2_000, cost: 2_000_000, unit_ask: 10_000_000, listed: 3_750, broker_fee_percent: 1.5, sales_tax_percent: 3.5 });
    const lines = lpBreakdownLines(r, "list", t, fmt);
    expect(lines.join("\n")).toContain("10,000,000");
    expect(lines.some((l) => l.includes("1.5%") && l.includes("150,000"))).toBe(true);
    expect(lines.some((l) => l.includes("3.5%") && l.includes("350,000"))).toBe(true);
    expect(lines.some((l) => l.includes("9,500,000"))).toBe(true);
    expect(lines.slice(-3).join("\n")).toMatch(/2,000,000[\s\S]*7,500,000[\s\S]*2,000 LP[\s\S]*3,750/);
  });

  it("an instant sale pays sales tax only", () => {
    const r = row({ unit_bid: 1_000_000, instant: 950, broker_fee_percent: 1.5, sales_tax_percent: 5 });
    const lines = lpBreakdownLines(r, "sell", t, fmt);
    expect(lines.some((l) => l.includes("1.5%"))).toBe(false);
    expect(lines.some((l) => l.includes("5%") && l.includes("50,000"))).toBe(true);
  });

  it("walks a build: every material, materials total, job, build cost, sale, fees, net and build profit", () => {
    const r = row({
      is_blueprint: true,
      product_name: "Raven Navy Issue",
      runs: 1,
      lp_cost: 100_000,
      cost: 20_000_000,
      unit_ask: 410_000_000,
      build_units: 1,
      build_materials: [
        { type_id: 34, type_name: "Tritanium", quantity: 8_000_000, unit_price: 3.89, total_price: 31_120_000 },
        { type_id: 35, type_name: "Pyerite", quantity: 4_000_000, unit_price: 17.23, total_price: 68_920_000 },
      ],
      build_material_cost: 253_181_000,
      build_job_cost: 42_042_371,
      build_cost: 295_223_371,
      build_listed_gross: 410_000_000,
      build_listed_net: 390_000_000,
      build_listed: (390_000_000 - 295_223_371 - 20_000_000) / 100_000,
      broker_fee_percent: 1.5,
      sales_tax_percent: 3.36,
    });
    const text = lpBreakdownLines(r, "build_list", t, fmt).join("\n");
    for (const needle of ["Tritanium", "31,120,000", "Pyerite", "68,920,000", "253,181,000", "42,042,371", "295,223,371", "410,000,000", "390,000,000", "94,776,629", "20,000,000", "74,776,629"]) {
      expect(text).toContain(needle);
    }
  });

  it("a contract sale has no fees", () => {
    const r = row({ is_blueprint: true, runs: 10, bpc_per_run: 40_000_000, bpc_samples: 5, lp_cost: 200_000, cost: 200_000_000, bpc_sale: 1_000 });
    const text = lpBreakdownLines(r, "sell_bpc", t, fmt).join("\n");
    expect(text).toContain("400,000,000");
    expect(text).toContain("5 contracts");
  });

  it("is empty for an unpriced offer or a missing value", () => {
    expect(lpBreakdownLines(row({ unpriced: true, listed: 5 }), "list", t, fmt)).toEqual([]);
    expect(lpBreakdownLines(row({}), "list", t, fmt)).toEqual([]);
  });
});

describe("lpMethodsWithValues", () => {
  it("lists the methods that have a value, best first", () => {
    const r = row({ is_blueprint: true, build_listed: 700, build_instant: 500, bpc_sale: 900 });
    expect(lpMethodsWithValues(r)).toEqual(["sell_bpc", "build_list", "build_sell"]);
  });
});
