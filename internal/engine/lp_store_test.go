package engine

import (
	"encoding/json"
	"math"
	"testing"
)

func lpClose(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: got nil, want %.4f", name, want)
	}
	if math.Abs(*got-want) > 0.01 {
		t.Fatalf("%s: got %.4f, want %.4f", name, *got, want)
	}
}

func lpNil(t *testing.T, name string, got *float64) {
	t.Helper()
	if got != nil {
		t.Fatalf("%s: got %.4f, want nil", name, *got)
	}
}

// ravenOffer is store 1000180's offer 19596: one 10-run Raven Navy Issue
// Blueprint copy for 200k LP + 200M ISK + 8 Federal Strategic Materiel Supply
// Packages. packagePrice 0 leaves the packages out entirely.
func ravenOffer(packagePrice float64) LPOffer {
	o := LPOffer{OfferID: 19596, TypeID: 17637, Quantity: 10, LPCost: 200_000, ISKCost: 200_000_000}
	if packagePrice > 0 {
		o.RequiredItems = []LPRequiredItem{{TypeID: 93611, Quantity: 8, UnitPrice: packagePrice, Priced: true}}
	}
	return o
}

var ravenMeta = LPOfferMeta{TypeName: "Raven Navy Issue Blueprint", IsBlueprint: true, ProductTypeID: 17636, ProductName: "Raven Navy Issue", ProductPerRun: 1}

// Fuzzwork's figure for this offer, with no fees and the packages free:
// (10 x 410,850,276.24 - 3,925,945,320 - 200,000,000) / 200,000.
const fuzzworkRavenRevenue = 10 * 410_850_276.24
const fuzzworkRavenMaterials = 3_925_945_320.0

func TestLPReferenceCaseMatchesFuzzworkWithoutPackages(t *testing.T) {
	row := NewLPOfferRow(ravenOffer(0), ravenMeta, nil, LPFees{})
	row.ApplyBuild(LPBuildResult{ListedProfit: fuzzworkRavenRevenue - fuzzworkRavenMaterials})
	lpClose(t, "build_listed", row.BuildListed, -87.21)
	if row.Runs != 10 {
		t.Fatalf("runs = %d, want 10 (quantity is runs on one copy)", row.Runs)
	}
}

func TestLPReferenceCasePackagesCostWhatTheyCost(t *testing.T) {
	const pkg = 1_500_000.0
	row := NewLPOfferRow(ravenOffer(pkg), ravenMeta, nil, LPFees{})
	row.ApplyBuild(LPBuildResult{ListedProfit: fuzzworkRavenRevenue - fuzzworkRavenMaterials})
	lpClose(t, "build_listed", row.BuildListed, -87.21-8*pkg/200_000)
	if row.Cost != 200_000_000+8*pkg {
		t.Fatalf("cost = %.2f", row.Cost)
	}
}

func TestLPMarketValuesApplyTheRightFees(t *testing.T) {
	o := LPOffer{OfferID: 1, TypeID: 100, Quantity: 5, LPCost: 1_000, ISKCost: 1_000_000}
	q := &LPMarketQuote{BestBid: 900_000, BestAsk: 1_000_000, AvgDailyVolume: 20}
	fees := LPFees{SalesTaxPercent: 4, BrokerFeePercent: 1.5}
	row := NewLPOfferRow(o, LPOfferMeta{TypeName: "Implant"}, q, fees)

	// instant: 5 x 900k x 0.96 = 4,320,000; minus 1M cost = 3,320,000 / 1000
	lpClose(t, "instant", row.Instant, 3_320)
	// listed: 5 x 1M x (1 - 0.055) = 4,725,000; minus 1M = 3,725,000 / 1000
	lpClose(t, "listed", row.Listed, 3_725)
	lpClose(t, "best", row.Best, 3_725)
	if row.BestMethod != "list" {
		t.Fatalf("best method = %q, want list", row.BestMethod)
	}
	if row.UnitBid != 900_000 || row.UnitAsk != 1_000_000 {
		t.Fatalf("the quote must be kept for display: bid %v ask %v", row.UnitBid, row.UnitAsk)
	}
	if row.UnitsPerRedemption != 5 || row.AvgDailyVolume != 20 {
		t.Fatalf("liquidity = %d units, %.1f/day", row.UnitsPerRedemption, row.AvgDailyVolume)
	}
}

func TestLPMissingSideIsNilNotZero(t *testing.T) {
	o := LPOffer{OfferID: 1, TypeID: 100, Quantity: 1, LPCost: 1_000}
	row := NewLPOfferRow(o, LPOfferMeta{}, &LPMarketQuote{BestAsk: 5_000}, LPFees{})
	lpNil(t, "instant", row.Instant)
	lpClose(t, "listed", row.Listed, 5)
}

func TestLPUnpricedRequiredItem(t *testing.T) {
	o := LPOffer{OfferID: 1, TypeID: 100, Quantity: 1, LPCost: 1_000, ISKCost: 10,
		RequiredItems: []LPRequiredItem{{TypeID: 5, Quantity: 1, Priced: false}}}
	row := NewLPOfferRow(o, LPOfferMeta{}, &LPMarketQuote{BestBid: 1e9, BestAsk: 1e9}, LPFees{})
	if !row.Unpriced {
		t.Fatal("an offer with an unpriceable required item must be unpriced")
	}
	lpNil(t, "instant", row.Instant)
	lpNil(t, "listed", row.Listed)
	lpNil(t, "best", row.Best)

	row.ApplyBuild(LPBuildResult{ListedProfit: 1e9, InstantProfit: 1e9, InstantAvailable: true})
	row.ApplyBPCPrice(&LPBPCPrice{PerRun: 1e9, Samples: 3})
	lpNil(t, "build_listed", row.BuildListed)
	lpNil(t, "bpc_sale", row.BPCSale)
	lpNil(t, "best", row.Best)
}

func TestLPZeroLPCost(t *testing.T) {
	o := LPOffer{OfferID: 1, TypeID: 100, Quantity: 1, LPCost: 0, ISKCost: 10}
	row := NewLPOfferRow(o, LPOfferMeta{}, &LPMarketQuote{BestBid: 100, BestAsk: 200}, LPFees{})
	lpNil(t, "instant", row.Instant)
	lpNil(t, "listed", row.Listed)
	if _, err := json.Marshal(row); err != nil {
		t.Fatalf("row must marshal: %v", err)
	}
}

func TestLPBlueprintHasNoMarketValues(t *testing.T) {
	q := &LPMarketQuote{BestBid: 400e6, BestAsk: 410e6, AvgDailyVolume: 12}
	row := NewLPOfferRow(ravenOffer(0), ravenMeta, q, LPFees{})
	lpNil(t, "instant", row.Instant)
	lpNil(t, "listed", row.Listed)
	if row.UnitsPerRedemption != 10 || row.AvgDailyVolume != 12 {
		t.Fatalf("a blueprint's liquidity is its product's: %d units, %.1f/day", row.UnitsPerRedemption, row.AvgDailyVolume)
	}
}

func TestLPBPCSaleIsPerRunWithoutFees(t *testing.T) {
	row := NewLPOfferRow(ravenOffer(0), ravenMeta, nil, LPFees{SalesTaxPercent: 4, BrokerFeePercent: 3})
	row.ApplyBPCPrice(&LPBPCPrice{PerRun: 40_000_000, Samples: 5})
	// 10 runs x 40M = 400M, minus 200M = 200M / 200k LP
	lpClose(t, "bpc_sale", row.BPCSale, 1_000)
	if row.BPCSamples != 5 || row.BPCOverride {
		t.Fatalf("samples=%d override=%v", row.BPCSamples, row.BPCOverride)
	}
	lpClose(t, "best", row.Best, 1_000)
	if row.BestMethod != "sell_bpc" {
		t.Fatalf("best method = %q", row.BestMethod)
	}

	row.ApplyBPCPrice(nil)
	lpNil(t, "bpc_sale", row.BPCSale)
	lpNil(t, "best", row.Best)
}

func TestLPBuildPicksBestOfBuildAndBPC(t *testing.T) {
	row := NewLPOfferRow(ravenOffer(0), ravenMeta, nil, LPFees{})
	row.ApplyBPCPrice(&LPBPCPrice{PerRun: 30_000_000, Samples: 2}) // (300M-200M)/200k = 500
	row.ApplyBuild(LPBuildResult{ListedProfit: 500_000_000, InstantProfit: 300_000_000, InstantAvailable: true, BuildCost: 392e6, JobCost: 2e6})
	lpClose(t, "build_listed", row.BuildListed, 1_500)
	if row.BuildCost != 392e6 || row.BuildJobCost != 2e6 {
		t.Fatalf("build cost %v job %v", row.BuildCost, row.BuildJobCost)
	}
	lpClose(t, "build_instant", row.BuildInstant, 500)
	lpClose(t, "best", row.Best, 1_500)
	if row.BestMethod != "build_list" {
		t.Fatalf("best method = %q", row.BestMethod)
	}
}

func TestLPBuildInstantUnavailable(t *testing.T) {
	row := NewLPOfferRow(ravenOffer(0), ravenMeta, nil, LPFees{})
	row.ApplyBuild(LPBuildResult{ListedProfit: 500_000_000, InstantProfit: 999e9, InstantAvailable: false})
	lpNil(t, "build_instant", row.BuildInstant)
}

func TestLPBuildErrorIsKeptAndValuesNil(t *testing.T) {
	row := NewLPOfferRow(ravenOffer(0), ravenMeta, nil, LPFees{})
	row.ApplyBuild(LPBuildResult{Error: "no blueprint data"})
	lpNil(t, "build_listed", row.BuildListed)
	if row.BuildError != "no blueprint data" {
		t.Fatalf("build error = %q", row.BuildError)
	}
}

func TestLPMedianPerRun(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", nil, 0},
		{"one", []float64{5}, 5},
		{"odd", []float64{9, 1, 5}, 5},
		{"even", []float64{1, 3, 5, 100}, 4},
		{"outlier does not drag it", []float64{10, 11, 12, 1_000_000}, 11.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LPMedian(tc.in); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
