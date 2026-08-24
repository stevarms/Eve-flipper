package engine

import (
	"math"
	"testing"
)

func TestTradeFeeMultipliers_LegacyFallback(t *testing.T) {
	buyMult, sellMult := tradeFeeMultipliers(tradeFeeInputs{
		SplitTradeFees:   false,
		BrokerFeePercent: 3,
		SalesTaxPercent:  8,
	})

	// Legacy: buy side broker only, sell side broker + tax.
	if math.Abs(buyMult-1.03) > 1e-9 {
		t.Fatalf("buyMult = %v, want 1.03", buyMult)
	}
	if math.Abs(sellMult-0.89) > 1e-9 {
		t.Fatalf("sellMult = %v, want 0.89", sellMult)
	}
}

func TestTradeFeeMultipliers_SplitMode(t *testing.T) {
	buyMult, sellMult := tradeFeeMultipliers(tradeFeeInputs{
		SplitTradeFees:       true,
		BuyBrokerFeePercent:  0.5,
		SellBrokerFeePercent: 0.2,
		BuySalesTaxPercent:   0.1,
		SellSalesTaxPercent:  3.6,
	})

	if math.Abs(buyMult-1.006) > 1e-9 {
		t.Fatalf("buyMult = %v, want 1.006", buyMult)
	}
	if math.Abs(sellMult-0.962) > 1e-9 {
		t.Fatalf("sellMult = %v, want 0.962", sellMult)
	}
}

func TestTradeFeeMultipliers_Clamp(t *testing.T) {
	buyMult, sellMult := tradeFeeMultipliers(tradeFeeInputs{
		SplitTradeFees:       true,
		BuyBrokerFeePercent:  -5,
		SellBrokerFeePercent: 200,
		BuySalesTaxPercent:   -1,
		SellSalesTaxPercent:  50,
	})

	if math.Abs(buyMult-1.0) > 1e-9 {
		t.Fatalf("buyMult = %v, want 1.0", buyMult)
	}
	if sellMult != 0 {
		t.Fatalf("sellMult = %v, want 0", sellMult)
	}
}

func TestBrokerFeeForOrder_AppliesFloor(t *testing.T) {
	cases := []struct {
		name       string
		orderValue float64
		brokerPct  float64
		want       float64
	}{
		{"zero order", 0, 3.0, 0},
		{"zero rate skips floor", 1000, 0, 0},
		{"small order hits 100 ISK floor", 1000, 3.0, MinBrokerFeeISK},
		{"exact threshold: 10k @ 1% = 100", 10_000, 1.0, MinBrokerFeeISK},
		{"large order uses rate", 1_000_000, 3.0, 30_000},
		{"medium order clears floor by a hair", 4_000, 3.0, 120},
		{"L5 broker on ammo: 100u @ 500 ISK, 1.5%", 50_000, 1.5, 750},
		{"tiny PI raw material order", 500, 3.0, MinBrokerFeeISK},
	}
	for _, c := range cases {
		got := BrokerFeeForOrder(c.orderValue, c.brokerPct)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
