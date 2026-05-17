import type { ScanParams } from "./types";

export interface TaxProfile {
  split_trade_fees: boolean;
  broker_fee_percent: number;
  sales_tax_percent: number;
  buy_broker_fee_percent: number;
  sell_broker_fee_percent: number;
  buy_sales_tax_percent: number;
  sell_sales_tax_percent: number;
}

export function normalizeTaxProfile(params: Partial<ScanParams>): TaxProfile {
  const broker = params.broker_fee_percent ?? 0;
  const tax = params.sales_tax_percent ?? 8;
  return {
    split_trade_fees: Boolean(params.split_trade_fees),
    broker_fee_percent: broker,
    sales_tax_percent: tax,
    buy_broker_fee_percent: params.buy_broker_fee_percent ?? broker,
    sell_broker_fee_percent: params.sell_broker_fee_percent ?? broker,
    buy_sales_tax_percent: params.buy_sales_tax_percent ?? 0,
    sell_sales_tax_percent: params.sell_sales_tax_percent ?? tax,
  };
}

export function sameTaxProfile(a: Partial<ScanParams>, b: TaxProfile): boolean {
  const normalized = normalizeTaxProfile(a);
  return (
    normalized.split_trade_fees === b.split_trade_fees &&
    normalized.broker_fee_percent === b.broker_fee_percent &&
    normalized.sales_tax_percent === b.sales_tax_percent &&
    normalized.buy_broker_fee_percent === b.buy_broker_fee_percent &&
    normalized.sell_broker_fee_percent === b.sell_broker_fee_percent &&
    normalized.buy_sales_tax_percent === b.buy_sales_tax_percent &&
    normalized.sell_sales_tax_percent === b.sell_sales_tax_percent
  );
}

export function taxProfileKey(params: Partial<ScanParams>): string {
  const tax = normalizeTaxProfile(params);
  return [
    tax.split_trade_fees ? "1" : "0",
    tax.broker_fee_percent,
    tax.sales_tax_percent,
    tax.buy_broker_fee_percent,
    tax.sell_broker_fee_percent,
    tax.buy_sales_tax_percent,
    tax.sell_sales_tax_percent,
  ].join("|");
}
