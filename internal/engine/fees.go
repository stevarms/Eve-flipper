package engine

// MinBrokerFeeISK is the per-order minimum broker fee CCP enforces on both
// sides of the market. Applied independent of skill, standings, or
// structure: even a 0.01% effective broker rate on a 100 ISK order still
// costs 100 ISK to list. Matters most for PI raw materials (P0/P1), ammo,
// mining crystals, and any low-value flip. Bulk trades > ~1M ISK/order
// clear the floor easily and are unaffected. Audit P2.6.
const MinBrokerFeeISK = 100.0

// BrokerFeeForOrder returns the broker fee CCP charges for a single order
// at the given notional value and effective broker rate. Applies the
// 100 ISK minimum floor. Returns 0 when orderValue is 0 or brokerPct is 0
// (a rate of exactly 0% skips the floor — matches instant-buy market
// takers who pay no broker fee at all).
func BrokerFeeForOrder(orderValue, brokerPct float64) float64 {
	if orderValue <= 0 || brokerPct <= 0 {
		return 0
	}
	raw := orderValue * brokerPct / 100.0
	if raw < MinBrokerFeeISK {
		return MinBrokerFeeISK
	}
	return raw
}

// tradeFeeInputs carries legacy + split fee fields for profitability calculations.
// Legacy mode (SplitTradeFees=false):
// - Buy side: broker only
// - Sell side: broker + sales tax
// Split mode:
// - Buy side: buy broker + buy tax
// - Sell side: sell broker + sell tax
type tradeFeeInputs struct {
	SplitTradeFees       bool
	BrokerFeePercent     float64
	SalesTaxPercent      float64
	BuyBrokerFeePercent  float64
	SellBrokerFeePercent float64
	BuySalesTaxPercent   float64
	SellSalesTaxPercent  float64
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func normalizeTradeFees(in tradeFeeInputs) tradeFeeInputs {
	in.BrokerFeePercent = clampPercent(in.BrokerFeePercent)
	in.SalesTaxPercent = clampPercent(in.SalesTaxPercent)
	in.BuyBrokerFeePercent = clampPercent(in.BuyBrokerFeePercent)
	in.SellBrokerFeePercent = clampPercent(in.SellBrokerFeePercent)
	in.BuySalesTaxPercent = clampPercent(in.BuySalesTaxPercent)
	in.SellSalesTaxPercent = clampPercent(in.SellSalesTaxPercent)

	if !in.SplitTradeFees {
		in.BuyBrokerFeePercent = in.BrokerFeePercent
		in.SellBrokerFeePercent = in.BrokerFeePercent
		// Legacy behavior: buy side has no sales tax.
		in.BuySalesTaxPercent = 0
		in.SellSalesTaxPercent = in.SalesTaxPercent
	}

	return in
}

func tradeFeePercents(in tradeFeeInputs) (buyBroker, buyTax, sellBroker, sellTax float64) {
	n := normalizeTradeFees(in)
	return n.BuyBrokerFeePercent, n.BuySalesTaxPercent, n.SellBrokerFeePercent, n.SellSalesTaxPercent
}

func tradeFeeMultipliers(in tradeFeeInputs) (buyCostMult, sellRevenueMult float64) {
	buyBroker, buyTax, sellBroker, sellTax := tradeFeePercents(in)
	buyCostMult = 1.0 + (buyBroker+buyTax)/100.0
	sellRevenueMult = 1.0 - (sellBroker+sellTax)/100.0
	if sellRevenueMult < 0 {
		sellRevenueMult = 0
	}
	return
}
