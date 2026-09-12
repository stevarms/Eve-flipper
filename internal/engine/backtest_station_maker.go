package engine

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// backtest_station_maker.go — replay a station-trading strategy honestly.
//
// Station trading and hauling are opposite execution models, and running one
// through the other's simulation does not produce a pessimistic answer, it
// produces a meaningless one.
//
// BuildOrderBookReplayBacktest is a *taker* on both legs: it buys from the
// lowest asks at the source and sells into the highest bids at the destination.
// That is exactly right when the two are different markets — it is what hauling
// is. Applied to a single station it buys at the ask and sells at the bid, and
// since the ask is above the bid by definition, every simulated trade loses
// money no matter how good the item is. Same failure as pairing daily averages
// across one region, arrived at from the other direction.
//
// What station trading actually is: you place a buy order at (or just above)
// the best bid and wait for someone to market-sell into it, then place a sell
// order at (or just below) the best ask and wait for someone to market-buy from
// it. You are the one being crossed, so the spread is your margin instead of
// your cost. Hence a separate replay.
//
// Two consequences beyond flipping which side each leg reads:
//
//   - Markup and haircut change meaning, though not direction. As a taker they
//     model paying up to get filled. As a maker they model the tick you must
//     give away to sit at the front of the queue: bid above the best bid, ask
//     below the best ask. Still "buying costs more, selling earns less", still
//     the same knobs, so a user who understands them on the hauling screen is
//     not being retrained.
//
//   - Quantity comes from flow, not depth. A taker can size against the book
//     because the book is what they consume. A maker consumes nothing: they
//     wait, and what arrives is a day's trading. Sizing a maker strategy
//     against visible depth is how a backtest concludes you could have bought
//     out Jita every morning. So the cap here is the item's own flippable flow
//     (the lesser of what you can buy and what you can sell per day), scaled by
//     the volume share the user is willing to claim.
//
// Resting depth is still read, as queue position: the volume already sitting at
// or better than your price is ahead of you in line, and it is reported per
// trade rather than folded silently into the number, because "your order was
// twelfth in a queue of 400,000 units" is the honest reason a spread you can
// see is not a spread you can capture.

// stationMakerQueueLevels is how far into the book counts as "ahead of you".
//
// One level, because you are modelled as joining the front: the orders already
// at the best price are your competition and anything worse than that is behind
// you once you post. Reading deeper would describe a passive order left to rot,
// which is a different strategy.
const stationMakerQueueLevels = 1

type stationMakerDiag struct {
	BidBooks     int
	AskBooks     int
	Pairs        int
	NoPair       int
	NoQuantity   int
	NoFlow       int
	Inverted     int
	BelowROI     int
	Errors       int
	CrossStation int
	BestROI      float64
	HasBestROI   bool
}

func (d *stationMakerDiag) add(other stationMakerDiag) {
	d.BidBooks += other.BidBooks
	d.AskBooks += other.AskBooks
	d.Pairs += other.Pairs
	d.NoPair += other.NoPair
	d.NoQuantity += other.NoQuantity
	d.NoFlow += other.NoFlow
	d.Inverted += other.Inverted
	d.BelowROI += other.BelowROI
	d.Errors += other.Errors
	d.CrossStation += other.CrossStation
	if other.HasBestROI && (!d.HasBestROI || other.BestROI > d.BestROI) {
		d.BestROI = other.BestROI
		d.HasBestROI = true
	}
}

func (d *stationMakerDiag) observeROI(roi float64) {
	if !d.HasBestROI || roi > d.BestROI {
		d.BestROI = roi
		d.HasBestROI = true
	}
}

func (d stationMakerDiag) warning() string {
	best := "n/a"
	if d.HasBestROI {
		best = fmt.Sprintf("%.1f%%", sanitizeFloat(d.BestROI))
	}
	return fmt.Sprintf(
		"station maker replay found no trades: bid_books=%d ask_books=%d paired=%d no_pair=%d no_flow=%d no_quantity=%d spread_inverted=%d below_min_roi=%d cross_station_rows=%d errors=%d best_roi=%s",
		d.BidBooks,
		d.AskBooks,
		d.Pairs,
		d.NoPair,
		d.NoFlow,
		d.NoQuantity,
		d.Inverted,
		d.BelowROI,
		d.CrossStation,
		d.Errors,
		best,
	)
}

// BuildStationMakerReplayBacktest replays a maker strategy at one station
// against stored order books.
//
// Rows are expected to describe a single venue — buy and sell location equal.
// A row whose legs differ is counted and skipped rather than quietly simulated,
// because at that point the caller means hauling and wants the other function.
func BuildStationMakerReplayBacktest(
	rows []FlipResult,
	params FlipBacktestParams,
	getBooks OrderBookReplayGetter,
) FlipBacktestResult {
	params.StrategyMode = "instant_flip"
	params.InstantPriceMode = "recorded_orderbook"
	params = normalizeFlipBacktestParams(params)

	result := FlipBacktestResult{}
	if len(rows) == 0 || getBooks == nil {
		result.Summary = summarizeFlipBacktest(rows, nil, params)
		result.Assumptions = buildFlipBacktestAssumptions(params)
		result.Diagnostics = buildFlipBacktestDiagnostics(rows, nil, params)
		result.Warnings = append(result.Warnings, "station maker replay needs stored orderbook snapshots")
		return result
	}
	if len(rows) > params.MaxRows {
		rows = rows[:params.MaxRows]
		result.Warnings = append(result.Warnings, "rows truncated to max_rows")
	}

	buyCostMult, sellRevenueMult := tradeFeeMultipliers(tradeFeeInputs{
		SplitTradeFees:       params.SplitTradeFees,
		BrokerFeePercent:     params.BrokerFeePercent,
		SalesTaxPercent:      params.SalesTaxPercent,
		BuyBrokerFeePercent:  params.BuyBrokerFeePercent,
		SellBrokerFeePercent: params.SellBrokerFeePercent,
		BuySalesTaxPercent:   params.BuySalesTaxPercent,
		SellSalesTaxPercent:  params.SellSalesTaxPercent,
	})

	var diag stationMakerDiag
	for _, row := range rows {
		trades, rowDiag := backtestStationMakerRow(row, params, buyCostMult, sellRevenueMult, getBooks)
		diag.add(rowDiag)
		if len(trades) == 0 {
			continue
		}
		result.Ledger = append(result.Ledger, trades...)
	}

	sort.Slice(result.Ledger, func(i, j int) bool {
		if result.Ledger[i].ExitDate == result.Ledger[j].ExitDate {
			if result.Ledger[i].EntryDate == result.Ledger[j].EntryDate {
				return result.Ledger[i].TypeID < result.Ledger[j].TypeID
			}
			return result.Ledger[i].EntryDate < result.Ledger[j].EntryDate
		}
		return result.Ledger[i].ExitDate < result.Ledger[j].ExitDate
	})

	result.Summary = summarizeFlipBacktest(rows, result.Ledger, params)
	result.Items = summarizeFlipBacktestItems(result.Ledger)
	result.Equity = buildFlipBacktestEquityCurve(result.Ledger)
	result.Assumptions = buildFlipBacktestAssumptions(params)
	result.Diagnostics = buildFlipBacktestDiagnostics(rows, result.Ledger, params)
	result.Diagnostics.CandidateEntries = diag.Pairs
	result.Diagnostics.SkippedNoQuantity = diag.NoQuantity + diag.NoFlow
	result.Diagnostics.SkippedBelowROI = diag.BelowROI
	result.Diagnostics.SkippedUnfillable = diag.Inverted
	if diag.HasBestROI {
		result.Diagnostics.BestROI = sanitizeFloat(diag.BestROI)
	}
	if len(result.Ledger) == 0 {
		result.Warnings = append(result.Warnings, diag.warning())
	}
	if diag.CrossStation > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d row(s) skipped: buy and sell venues differ, which is hauling rather than station trading",
			diag.CrossStation))
	}
	return result
}

// stationMakerFlowPerDay is the units per day a maker can realistically turn
// over in an item: the lesser of what they can acquire and what they can shed,
// because a station trade needs both halves and the slower one governs.
//
// Falls back to a fraction of raw daily volume when the flow split is absent,
// and to nothing at all when even that is missing — sizing an unknown as
// unlimited is the specific mistake that makes a backtest look brilliant.
func stationMakerFlowPerDay(row FlipResult) float64 {
	buyFlow := row.BfSPerDay
	sellFlow := row.S2BPerDay
	if buyFlow > 0 && sellFlow > 0 {
		return math.Min(buyFlow, sellFlow)
	}
	if buyFlow > 0 {
		return buyFlow
	}
	if sellFlow > 0 {
		return sellFlow
	}
	if row.DailyVolume > 0 {
		// Both sides of every trade are counted in reported volume, and a maker
		// is only ever one side, so half is the ceiling before any share is
		// applied.
		return float64(row.DailyVolume) / 2
	}
	return 0
}

// stationMakerQuantity caps a cycle by flow first, then by whatever the user's
// quantity mode asks for. Flow is the binding constraint that a depth-based
// sizing would miss.
func stationMakerQuantity(row FlipResult, params FlipBacktestParams, bidPrice, buyCostMult float64) (int32, bool) {
	flow := stationMakerFlowPerDay(row)
	if flow <= 0 {
		return 0, false
	}
	share := params.VolumeFillFraction
	if share <= 0 {
		share = 100
	}
	if share > 100 {
		share = 100
	}
	capacity := math.Floor(flow * share / 100)
	if capacity < 1 {
		return 0, false
	}

	want := capacity
	switch params.QuantityMode {
	case "fixed":
		if params.FixedQuantity > 0 {
			want = math.Min(want, float64(params.FixedQuantity))
		}
	case "budget":
		if params.BudgetISK <= 0 || bidPrice <= 0 || buyCostMult <= 0 {
			return 0, true
		}
		want = math.Min(want, math.Floor(params.BudgetISK/(bidPrice*buyCostMult)))
	default:
		// Scan quantity, when the row carries one it believes in.
		if row.FilledQty > 0 {
			want = math.Min(want, float64(row.FilledQty))
		} else if row.UnitsToBuy > 0 {
			want = math.Min(want, float64(row.UnitsToBuy))
		}
	}
	if want < 1 {
		return 0, true
	}
	const maxInt32 = float64(1<<31 - 1)
	if want > maxInt32 {
		want = maxInt32
	}
	return int32(want), true
}

// stationMakerQueueAhead totals the volume resting at the front of the book,
// which is what stands between posting an order and being filled.
func stationMakerQueueAhead(levels []OrderBookReplayLevel) int64 {
	var ahead int64
	for i, level := range levels {
		if i >= stationMakerQueueLevels {
			break
		}
		if level.VolumeRemain > 0 {
			ahead += level.VolumeRemain
		}
	}
	return ahead
}

func backtestStationMakerRow(
	row FlipResult,
	params FlipBacktestParams,
	buyCostMult float64,
	sellRevenueMult float64,
	getBooks OrderBookReplayGetter,
) ([]FlipBacktestTrade, stationMakerDiag) {
	var diag stationMakerDiag
	if row.TypeID <= 0 {
		return nil, diag
	}
	if row.BuyRegionID <= 0 && row.BuyLocationID <= 0 {
		diag.Errors++
		return nil, diag
	}
	// One venue, both legs. A row that says otherwise is a hauling row and the
	// caller wants BuildOrderBookReplayBacktest.
	if row.SellLocationID != 0 && row.BuyLocationID != 0 && row.SellLocationID != row.BuyLocationID {
		diag.CrossStation++
		return nil, diag
	}

	now := time.Now().UTC()
	from := now.Add(-time.Duration(params.WindowDays) * 24 * time.Hour)

	// The bid book is where the acquisition happens: you join it, so its top is
	// the price you pay. The ask book is where you exit for the same reason.
	// This is the inversion that makes the whole file necessary.
	bidBooks, err := getBooks(OrderBookReplayFilter{
		RegionID:   row.BuyRegionID,
		TypeID:     row.TypeID,
		LocationID: row.BuyLocationID,
		Side:       "buy",
		From:       from,
		To:         now,
		Limit:      2000,
	})
	if err != nil {
		diag.Errors++
		return nil, diag
	}
	askBooks, err := getBooks(OrderBookReplayFilter{
		RegionID:   row.BuyRegionID,
		TypeID:     row.TypeID,
		LocationID: row.BuyLocationID,
		Side:       "sell",
		From:       from,
		To:         now,
		Limit:      2000,
	})
	if err != nil {
		diag.Errors++
		return nil, diag
	}

	bidBooks = normalizeReplayBooks(bidBooks, "buy")
	askBooks = normalizeReplayBooks(askBooks, "sell")
	diag.BidBooks = len(bidBooks)
	diag.AskBooks = len(askBooks)
	if len(bidBooks) == 0 || len(askBooks) == 0 {
		return nil, diag
	}

	// Both sides of a station trade come out of one archived snapshot, so they
	// pair at zero age. The tolerance still applies for books recorded live,
	// where the two sides were written by separate passes.
	maxAge := time.Duration(params.OrderBookMaxAgeMin) * time.Minute
	cooldown := time.Duration(params.OrderBookCooldownMin) * time.Minute

	var out []FlipBacktestTrade
	var nextAllowed time.Time
	for _, bidBook := range bidBooks {
		if !nextAllowed.IsZero() && bidBook.CapturedAt.Before(nextAllowed) {
			continue
		}
		askBook, ok := nearestReplayBook(bidBook.CapturedAt, askBooks, maxAge)
		if !ok {
			diag.NoPair++
			continue
		}
		diag.Pairs++

		bestBid := bidBook.Levels[0].Price
		bestAsk := askBook.Levels[0].Price
		if bestBid <= 0 || bestAsk <= 0 {
			diag.NoQuantity++
			continue
		}

		// Give away a tick on each side to reach the front of the queue. Same
		// knobs and same direction as the taker replay; different mechanism.
		buyPrice := bestBid * (1 + params.BuyPriceMarkupPct/100)
		sellPrice := bestAsk * (1 - params.SellPriceHaircutPct/100)
		if sellPrice <= buyPrice {
			// After paying for queue priority there is no spread left. The
			// common and honest outcome on a tight book, and the reason a
			// station strategy can be real and still not be yours to capture.
			diag.Inverted++
			continue
		}

		qty, hadFlow := stationMakerQuantity(row, params, buyPrice, buyCostMult)
		if !hadFlow {
			diag.NoFlow++
			continue
		}
		if qty <= 0 {
			diag.NoQuantity++
			continue
		}

		buyCost := buyPrice * float64(qty) * buyCostMult
		sellRevenue := sellPrice * float64(qty) * sellRevenueMult
		pnl := sellRevenue - buyCost
		roi := 0.0
		if buyCost > 0 {
			roi = pnl / buyCost * 100
		}
		diag.observeROI(roi)
		if roi < params.MinROIPercent {
			diag.BelowROI++
			continue
		}

		bidQueue := stationMakerQueueAhead(bidBook.Levels)
		askQueue := stationMakerQueueAhead(askBook.Levels)
		snapshotAge := askBook.CapturedAt.Sub(bidBook.CapturedAt)
		if snapshotAge < 0 {
			snapshotAge = -snapshotAge
		}

		// A maker cycle is not instant: the buy must fill before the sell can be
		// posted. Both legs are dated to the snapshot because that is the only
		// moment actually observed — the cooldown is what stops the simulation
		// claiming a fresh cycle every half hour.
		stamp := bidBook.CapturedAt.UTC().Format("2006-01-02")
		out = append(out, FlipBacktestTrade{
			TypeID:            row.TypeID,
			TypeName:          row.TypeName,
			EntryDate:         stamp,
			ExitDate:          stamp,
			Status:            "closed",
			Quantity:          qty,
			RequestedQuantity: qty,
			BuyPrice:          sanitizeFloat(buyPrice),
			SellPrice:         sanitizeFloat(sellPrice),
			BuyCost:           sanitizeFloat(buyCost),
			SellRevenue:       sanitizeFloat(sellRevenue),
			PnL:               sanitizeFloat(pnl),
			ROIPercent:        sanitizeFloat(roi),
			Fillable:          true,
			FillPercent:       100,
			FillSource:        "recorded_orderbook_maker",
			// Queue depth rather than consumed depth: nothing was taken, these
			// are the units that were in front of the order.
			FillReason:         "maker_queue",
			SourceVolume:       bidQueue,
			TargetVolume:       askQueue,
			BuySnapshotID:      bidBook.SnapshotID,
			SellSnapshotID:     askBook.SnapshotID,
			SnapshotAgeSeconds: int64(snapshotAge.Seconds()),
		})
		nextAllowed = bidBook.CapturedAt.Add(cooldown)
	}
	return out, diag
}
