package engine

import (
	"math"
	"sort"
	"time"

	"eve-flipper/internal/sde"
)

// JournalTxn is a wallet-source-agnostic transaction the trade-journal
// engine consumes. The api layer converts JournalTxn → JournalTxn.
// (engine can't import db without creating an import cycle.)
type JournalTxn struct {
	WalletKey     string
	TransactionID int64
	Date          string
	TypeID        int32
	TypeName      string
	UnitPrice     float64
	Quantity      int32
	IsBuy         bool
	// Where the trade happened. The journal itself aggregates across
	// stations, but the P&L projection needs these for its per-station
	// breakdown — dropping them here is what used to make that table
	// impossible to derive from this engine.
	LocationID   int64
	LocationName string
}

// JournalIndustryJob is the engine's view of a completed industry job.
type JournalIndustryJob struct {
	JobID           int64
	CharacterID     int64
	ActivityID      int32
	BlueprintTypeID int32
	ProductTypeID   int32
	ProductTypeName string
	Runs            int32
	InstallCost     float64
	Status          string
	StartDate       string
	CompletedDate   string
	SuccessfulRuns  int32
}

// portfolio_manufacturing.go extends the Portfolio P&L pipeline to cover
// manufacturing. Where portfolio.go's ComputePortfolioPnLWithOptions runs
// FIFO over character wallet transactions alone, ComputeTradeJournal below
// merges character + corp wallet transactions with completed industry jobs
// into a single chronological event loop, maintains one lot pool per
// typeID with source tagging, and produces separate Trading / Manufacturing
// / Combined totals for the Trade Journal tab.

// FIFOMode controls how the engine picks which lot to consume when a sell
// could match against both a trading buy lot and a manufacturing lot for
// the same typeID. `strict_date` (default) picks the oldest across both.
// The two side-preference modes exist to let the user express intent
// (trade_first mirrors Eve Tycoon; manufacture_first favours producers).
type FIFOMode string

const (
	FIFOModeStrictDate       FIFOMode = "strict_date"
	FIFOModeTradeFirst       FIFOMode = "trade_first"
	FIFOModeManufactureFirst FIFOMode = "manufacture_first"
)

// LotSource marks the origin of an inventory lot on a realized-trade row.
type LotSource string

const (
	LotSourceTrade       LotSource = "trade"
	LotSourceManufacture LotSource = "manufacture"
	LotSourceOrphan      LotSource = "orphan"
)

// MEResolution wraps the ME lookup chain's result.
type MEResolution struct {
	ME     int32
	Source string // "planner" | "bpo" | "bpc" | "t1_default" | "t2_default" | "fallback"
}

// MaterialCostSource marks how a job's material line was priced.
type MaterialCostSource string

const (
	MatCostFIFO     MaterialCostSource = "fifo" // real cost from trading pool
	MatCostFallback MaterialCostSource = "avg"  // ESI region avg fallback
)

// ManufacturingLotMaterial is a per-material row for the drawer breakdown.
type ManufacturingLotMaterial struct {
	TypeID    int32              `json:"type_id"`
	TypeName  string             `json:"type_name,omitempty"`
	Qty       int64              `json:"qty"`
	UnitCost  float64            `json:"unit_cost"`
	TotalCost float64            `json:"total_cost"`
	Source    MaterialCostSource `json:"source"`
}

// ManufacturingLot is one produced-item lot generated at a job's completion.
// Sells later match against these in the mfg pool.
type ManufacturingLot struct {
	JobID              int64                      `json:"job_id"`
	CharacterID        int64                      `json:"character_id"`
	ProductTypeID      int32                      `json:"product_type_id"`
	ProductName        string                     `json:"product_name,omitempty"`
	CompletedDate      string                     `json:"completed_date"`
	ProducedQty        int64                      `json:"produced_qty"`
	InstallCost        float64                    `json:"install_cost"`
	MaterialCost       float64                    `json:"material_cost"`
	UnitCost           float64                    `json:"unit_cost"`
	ME                 int32                      `json:"me"`
	METag              string                     `json:"me_tag"`
	MaterialsEstimated bool                       `json:"materials_estimated"`
	Materials          []ManufacturingLotMaterial `json:"materials,omitempty"`
}

// TradeJournalLot is one realized-trade row emitted by the compute. The
// same shape covers both trading and manufacturing matches — the Source
// field distinguishes them; manufacturing rows populate the Manufacture*
// fields, trade rows populate the Buy* fields, orphan sells populate
// neither (BuyDate = "" and Source = orphan).
type TradeJournalLot struct {
	Source     LotSource `json:"source"`
	SellDate   string    `json:"sell_date"`
	SellTxnID  int64     `json:"sell_txn_id"`
	SellWallet string    `json:"sell_wallet_key"`
	TypeID     int32     `json:"type_id"`
	TypeName   string    `json:"type_name,omitempty"`
	MatchedQty int64     `json:"matched_qty"`

	SellUnitPrice float64 `json:"sell_unit_price"`
	SellGross     float64 `json:"sell_gross"`
	SellFees      float64 `json:"sell_fees"`
	NetProfit     float64 `json:"net_profit"`

	// SellFees split into its two components. The journal itself only ever
	// needs the sum, but the P&L projection reports broker fees and sales
	// tax on separate lines, and re-deriving the split from a rate would
	// drift the moment the rate changed mid-period.
	SellBrokerFee float64 `json:"sell_broker_fee"`
	SellTax       float64 `json:"sell_tax"`

	// SellTaxActual is true when SellTax is the amount CCP actually charged,
	// recovered from the wallet journal, rather than the rate this app models
	// from the character's skills. False on rows older than the journal
	// archive (ESI only serves ~30 days, so they can never be reconciled) and
	// on any sale the pairing could not resolve. The UI marks the modelled
	// ones, since inside the archive window they are the exception.
	SellTaxActual bool `json:"sell_tax_actual,omitempty"`

	// Where the sell happened, and where the matched buy happened. Empty on
	// the manufacture side: a built item has no purchase station (the job's
	// output location is not tracked by ESI's industry endpoint).
	SellLocationID   int64  `json:"sell_location_id,omitempty"`
	SellLocationName string `json:"sell_location_name,omitempty"`
	BuyLocationID    int64  `json:"buy_location_id,omitempty"`
	BuyLocationName  string `json:"buy_location_name,omitempty"`

	// Cost side. BuyUnitPrice carries the purchase price on trade rows and
	// the build unit cost (install + materials ÷ produced qty) on
	// manufacture rows, so cost basis is one expression for both sources.
	BuyDate      string  `json:"buy_date,omitempty"`
	BuyTxnID     int64   `json:"buy_txn_id,omitempty"`
	BuyWallet    string  `json:"buy_wallet_key,omitempty"`
	BuyUnitPrice float64 `json:"buy_unit_price,omitempty"`
	BuyFees      float64 `json:"buy_fees,omitempty"`

	// Manufacture-side (Source == "manufacture")
	ManufactureJobID   int64  `json:"manufacture_job_id,omitempty"`
	ManufactureME      int32  `json:"manufacture_me,omitempty"`
	ManufactureMETag   string `json:"manufacture_me_tag,omitempty"`
	MaterialsEstimated bool   `json:"materials_estimated,omitempty"`
}

// JournalOpenPosition mirrors PortfolioPnL's OpenPosition and adds Source.
// Rows aggregate across wallets but split by station, so the P&L projection
// can reproduce the per-station cost basis; a surface with no station column
// (the Positions tab) collapses them back by type first.
type JournalOpenPosition struct {
	TypeID   int32     `json:"type_id"`
	TypeName string    `json:"type_name,omitempty"`
	Source   LotSource `json:"source"`
	// Where the inventory sits. Trade lots are grouped by type + station,
	// because the same item held in Jita and in Amarr is not one position.
	// Zero on manufacture rows: ESI's industry endpoint does not report a
	// job's output location.
	LocationID   int64   `json:"location_id,omitempty"`
	LocationName string  `json:"location_name,omitempty"`
	Qty          int64   `json:"qty"`
	AvgUnitCost  float64 `json:"avg_unit_cost"`
	OldestDate   string  `json:"oldest_date"`
	CostBasis    float64 `json:"cost_basis"`
}

// TotalsBreakdown is the KPI-tile input.
type TotalsBreakdown struct {
	TradingPnL         float64 `json:"trading_pnl"`
	ManufacturingPnL   float64 `json:"manufacturing_pnl"`
	CombinedPnL        float64 `json:"combined_pnl"`
	BuyISK             float64 `json:"buy_isk"`
	SellISK            float64 `json:"sell_isk"`
	FeesISK            float64 `json:"fees_isk"`
	UnattributedISK    float64 `json:"unattributed_isk"`
	EstMaterialCostISK float64 `json:"est_material_cost_isk"`

	// Fees as the wallet journal recorded them, for the part of the period the
	// journal archive covers. These are period figures, not row figures, and
	// the last two cannot be anything else: broker fee and the structure
	// owner's provider tax are charged when an order is placed, against the
	// whole order, so one charge can cover many fills — and an order that
	// never filled still cost ISK. Attributing them to a sale would invent a
	// number; leaving them out (as the app did until now) hides real ISK.
	//
	// The caller fills these in from the journal; the matcher does not compute
	// them. Zero means "the journal had nothing to say about this window".
	ActualSalesTaxISK    float64 `json:"actual_sales_tax_isk,omitempty"`
	ActualBrokerFeeISK   float64 `json:"actual_broker_fee_isk,omitempty"`
	ActualProviderTaxISK float64 `json:"actual_provider_tax_isk,omitempty"`
}

// DailyBreakdown feeds the three-series cumulative chart.
type DailyBreakdown struct {
	Date             string  `json:"date"`
	TradingPnL       float64 `json:"trading_pnl"`
	ManufacturingPnL float64 `json:"manufacturing_pnl"`
	CombinedPnL      float64 `json:"combined_pnl"`
	BuyISK           float64 `json:"buy_isk"`
	SellISK          float64 `json:"sell_isk"`
	FeesISK          float64 `json:"fees_isk"`
	Transactions     int     `json:"transactions"`
}

// TradeJournalOptions parameterises ComputeTradeJournal.
type TradeJournalOptions struct {
	SinceDate        time.Time
	FIFOMode         FIFOMode
	SalesTaxPercent  float64
	BrokerFeePercent float64
	// ActualSellTaxRateByTxnID is the sales tax rate, in percent, that was
	// actually charged on a sell transaction, keyed by its ESI transaction id.
	// A sale found here is charged this rate instead of SalesTaxPercent.
	//
	// A rate rather than an ISK amount on purpose: one sale can be matched
	// against several buy lots and emitted as several rows, and a rate
	// apportions across that split by construction. Nil or a miss means fall
	// back to the modelled rate.
	ActualSellTaxRateByTxnID map[int64]float64
	Materials                map[int32][]sde.BlueprintMaterial
	Products                 map[int32]sde.BlueprintProduct
	// MEByJob is the resolved ME lookup chain — planner link → owned BP →
	// tech-level default → 0 fallback. See internal/api/trade_journal.go's
	// resolveME for the concrete chain.
	MEByJob         func(job JournalIndustryJob) MEResolution
	RegionAvgByType map[int32]float64
	// TypeNameFor resolves typeID → name for lot / material display.
	TypeNameFor func(typeID int32) string
}

// TradeJournalResult is the compute output the API projects into the three
// GET endpoints (summary, by-type, lots).
type TradeJournalResult struct {
	Totals            TotalsBreakdown       `json:"totals"`
	DailyPnL          []DailyBreakdown      `json:"daily_pnl"`
	Lots              []TradeJournalLot     `json:"lots"`
	ManufacturingLots []ManufacturingLot    `json:"manufacturing_lots"`
	OpenPositions     []JournalOpenPosition `json:"open_positions"`
}

// --- internal state ---

// tradeLot is an open buy-side lot in the trading pool.
type tradeLot struct {
	date         time.Time
	txnID        int64
	walletKey    string
	typeName     string
	locationID   int64
	locationName string
	unitPrice    float64
	remaining    int64
}

// mfgLot is an open manufacturing lot in the manufacturing pool.
type mfgLot struct {
	jobID              int64
	characterID        int64
	productName        string
	date               time.Time
	unitCost           float64
	remaining          int64
	me                 int32
	meTag              string
	materialsEstimated bool
}

// eventKind orders same-timestamp events deterministically.
type eventKind int

const (
	evtBuy         eventKind = 1
	evtJobStart    eventKind = 2
	evtJobComplete eventKind = 3
	evtSell        eventKind = 4 // sells last so job completions on same day feed them
)

type journalEvent struct {
	when time.Time
	kind eventKind
	id   int64 // stable tiebreaker
	txn  *JournalTxn
	job  *JournalIndustryJob
}

func normalizeFIFOMode(m FIFOMode) FIFOMode {
	switch m {
	case FIFOModeTradeFirst, FIFOModeManufactureFirst:
		return m
	default:
		return FIFOModeStrictDate
	}
}

// ComputeTradeJournal runs the interleaved FIFO event loop.
func ComputeTradeJournal(
	txns []JournalTxn,
	jobs []JournalIndustryJob,
	opts TradeJournalOptions,
) *TradeJournalResult {
	opts.FIFOMode = normalizeFIFOMode(opts.FIFOMode)
	result := &TradeJournalResult{
		DailyPnL:          []DailyBreakdown{},
		Lots:              []TradeJournalLot{},
		ManufacturingLots: []ManufacturingLot{},
		OpenPositions:     []JournalOpenPosition{},
	}
	if opts.TypeNameFor == nil {
		opts.TypeNameFor = func(int32) string { return "" }
	}

	// Build the chronological event stream.
	events := make([]journalEvent, 0, len(txns)+2*len(jobs))
	for i := range txns {
		t, err := parseISO(txns[i].Date)
		if err != nil {
			continue
		}
		kind := evtBuy
		if !txns[i].IsBuy {
			kind = evtSell
		}
		events = append(events, journalEvent{when: t, kind: kind, id: txns[i].TransactionID, txn: &txns[i]})
	}
	// Include activity_id=1 (Manufacturing) and 11 (Reactions). Both
	// produce sellable items with the same cost-basis semantics: install
	// cost + material cost divided by produced qty. Reactions read their
	// materials from bp.Activities["reaction"] on the SDE side; the api
	// layer is responsible for populating opts.Materials with the right
	// activity's rows per job.
	// Only successful, delivered jobs count. Failed jobs consumed
	// materials but produced nothing — v1.5 will surface that loss as a
	// manufacturing expense.
	for i := range jobs {
		j := &jobs[i]
		if j.ActivityID != 1 && j.ActivityID != 11 {
			continue
		}
		if j.Status != "delivered" || j.SuccessfulRuns <= 0 {
			continue
		}
		if start, err := parseISO(j.StartDate); err == nil {
			events = append(events, journalEvent{when: start, kind: evtJobStart, id: j.JobID, job: j})
		}
		if done, err := parseISO(j.CompletedDate); err == nil {
			events = append(events, journalEvent{when: done, kind: evtJobComplete, id: j.JobID, job: j})
		}
	}
	sort.SliceStable(events, func(a, b int) bool {
		if !events[a].when.Equal(events[b].when) {
			return events[a].when.Before(events[b].when)
		}
		if events[a].kind != events[b].kind {
			return events[a].kind < events[b].kind
		}
		return events[a].id < events[b].id
	})

	// Cutoff for what enters realized-P&L totals. Anything before this is
	// still processed (so open-lot state is correct) but doesn't count
	// toward totals or the daily series — matches Portfolio's behavior.
	cutoff := opts.SinceDate

	tradePool := make(map[int32][]tradeLot)
	mfgPool := make(map[int32][]mfgLot)
	// pendingJobs[jobID] holds the accumulated material cost + estimation
	// flag between job_start and job_complete for one job.
	type pendingJob struct {
		materialCost       float64
		materialsEstimated bool
		materials          []ManufacturingLotMaterial
	}
	pending := make(map[int64]*pendingJob)

	dailyIndex := make(map[string]*DailyBreakdown)
	getDay := func(t time.Time) *DailyBreakdown {
		key := t.Format("2006-01-02")
		if d, ok := dailyIndex[key]; ok {
			return d
		}
		d := &DailyBreakdown{Date: key}
		dailyIndex[key] = d
		result.DailyPnL = append(result.DailyPnL, *d) // placeholder; replaced below
		dailyIndex[key] = &result.DailyPnL[len(result.DailyPnL)-1]
		return dailyIndex[key]
	}

	// Kept as percentages and divided at the point of use — gross*pct/100.0,
	// not gross*(pct/100.0). The two are the same in exact arithmetic and
	// differ in the last bits in float64, and the legacy engine uses the
	// former. Matching it is what lets ToPortfolioPnL reproduce
	// ComputePortfolioPnLWithOptions exactly (portfolio_projection_test.go).
	brokerPct := opts.BrokerFeePercent
	if brokerPct < 0 {
		brokerPct = 0
	}
	salesTaxPct := opts.SalesTaxPercent
	if salesTaxPct < 0 {
		salesTaxPct = 0
	}

	for _, ev := range events {
		switch ev.kind {
		case evtBuy:
			tx := ev.txn
			gross := tx.UnitPrice * float64(tx.Quantity)
			buyFee := gross * brokerPct / 100.0
			tradePool[tx.TypeID] = append(tradePool[tx.TypeID], tradeLot{
				date:         ev.when,
				txnID:        tx.TransactionID,
				walletKey:    tx.WalletKey,
				typeName:     tx.TypeName,
				locationID:   tx.LocationID,
				locationName: tx.LocationName,
				unitPrice:    tx.UnitPrice,
				remaining:    int64(tx.Quantity),
			})
			if ev.when.After(cutoff) || ev.when.Equal(cutoff) || cutoff.IsZero() {
				d := getDay(ev.when)
				d.BuyISK += gross + buyFee
				d.FeesISK += buyFee
				d.Transactions++
				result.Totals.BuyISK += gross + buyFee
				result.Totals.FeesISK += buyFee
			}

		case evtJobStart:
			job := ev.job
			// Look up base materials for the BP.
			mats := opts.Materials[job.BlueprintTypeID]
			meRes := MEResolution{ME: 0, Source: "fallback"}
			if opts.MEByJob != nil {
				meRes = opts.MEByJob(*job)
			}
			// EVE ME formula: adjusted qty = ceil(base × runs × (1 - ME/100)).
			// Rounding rules also apply per-material floors but ceil is the
			// canonical CCP approximation used by third-party calculators.
			meFactor := 1.0 - float64(meRes.ME)/100.0
			if meFactor < 0.01 {
				meFactor = 0.01
			}
			var matCostTotal float64
			var estimated bool
			breakdown := make([]ManufacturingLotMaterial, 0, len(mats))
			for _, m := range mats {
				need := int64(math.Ceil(float64(m.Quantity) * float64(job.Runs) * meFactor))
				if need <= 0 {
					continue
				}
				gotFIFO, fifoCost := consumeTradePoolFIFO(tradePool, m.TypeID, need)
				remainder := need - gotFIFO
				var fallbackCost float64
				if remainder > 0 {
					avg := opts.RegionAvgByType[m.TypeID]
					fallbackCost = float64(remainder) * avg
					estimated = estimated || avg > 0 || remainder > 0
				}
				total := fifoCost + fallbackCost
				matCostTotal += total
				var unitCost float64
				if need > 0 {
					unitCost = total / float64(need)
				}
				src := MatCostFIFO
				if remainder > 0 {
					src = MatCostFallback
				}
				breakdown = append(breakdown, ManufacturingLotMaterial{
					TypeID:    m.TypeID,
					TypeName:  opts.TypeNameFor(m.TypeID),
					Qty:       need,
					UnitCost:  unitCost,
					TotalCost: total,
					Source:    src,
				})
				if remainder > 0 && !cutoff.IsZero() && (ev.when.After(cutoff) || ev.when.Equal(cutoff)) {
					result.Totals.EstMaterialCostISK += fallbackCost
				}
			}
			pending[job.JobID] = &pendingJob{
				materialCost:       matCostTotal,
				materialsEstimated: estimated,
				materials:          breakdown,
			}
			// Stash the ME resolution for use at job_complete.
			pending[job.JobID].materials = breakdown
			// Store the meRes tag in a side-channel via the pendingJob
			// materials only (we look it up again at job_complete since
			// MEByJob is stateless).
			_ = meRes

		case evtJobComplete:
			job := ev.job
			p, ok := pending[job.JobID]
			if !ok {
				continue
			}
			delete(pending, job.JobID)
			// Resolve ME again for the tag (cheap; MEByJob is a map lookup).
			meRes := MEResolution{ME: 0, Source: "fallback"}
			if opts.MEByJob != nil {
				meRes = opts.MEByJob(*job)
			}
			// Runs × products_per_run. Default to 1 product per run if the
			// BP isn't in the Products map (defensive).
			prod, hasProd := opts.Products[job.BlueprintTypeID]
			productsPerRun := int32(1)
			if hasProd {
				productsPerRun = prod.Quantity
			}
			producedQty := int64(job.SuccessfulRuns) * int64(productsPerRun)
			if producedQty <= 0 {
				continue
			}
			totalCost := job.InstallCost + p.materialCost
			unitCost := totalCost / float64(producedQty)

			// Store the completed manufacturing lot for the drawer.
			completedLot := ManufacturingLot{
				JobID:              job.JobID,
				CharacterID:        job.CharacterID,
				ProductTypeID:      job.ProductTypeID,
				ProductName:        job.ProductTypeName,
				CompletedDate:      job.CompletedDate,
				ProducedQty:        producedQty,
				InstallCost:        job.InstallCost,
				MaterialCost:       p.materialCost,
				UnitCost:           unitCost,
				ME:                 meRes.ME,
				METag:              meRes.Source,
				MaterialsEstimated: p.materialsEstimated,
				Materials:          p.materials,
			}
			result.ManufacturingLots = append(result.ManufacturingLots, completedLot)

			mfgPool[job.ProductTypeID] = append(mfgPool[job.ProductTypeID], mfgLot{
				jobID:              job.JobID,
				characterID:        job.CharacterID,
				productName:        job.ProductTypeName,
				date:               ev.when,
				unitCost:           unitCost,
				remaining:          producedQty,
				me:                 meRes.ME,
				meTag:              meRes.Source,
				materialsEstimated: p.materialsEstimated,
			})

		case evtSell:
			tx := ev.txn
			sellQty := tx.Quantity
			sellGrossPerUnit := tx.UnitPrice
			// Fees are taken on the matched gross, not per unit and then
			// multiplied. Both are the same number in exact arithmetic, but
			// only this order reproduces ComputePortfolioPnLWithOptions bit
			// for bit — and a projection that differs in the last float digit
			// still shows the user two different profits.
			//
			// The tax rate is per-sale, not per-period: where the wallet
			// journal recorded what CCP actually took on this transaction, a
			// historic row reports that fact instead of what today's skills
			// would imply. A rate (rather than the charge itself) is what
			// makes this work when one sale is split across several buy lots —
			// each row taxes its own slice of the gross and the slices sum to
			// the transaction's charge.
			taxPct := salesTaxPct
			taxActual := false
			if rate, ok := opts.ActualSellTaxRateByTxnID[tx.TransactionID]; ok {
				taxPct = rate
				taxActual = true
			}
			feesOn := func(gross float64) (broker, tax float64) {
				return gross * brokerPct / 100.0, gross * taxPct / 100.0
			}
			inWindow := cutoff.IsZero() || !ev.when.Before(cutoff)

			if inWindow {
				grossTotal := sellGrossPerUnit * float64(sellQty)
				d := getDay(ev.when)
				d.SellISK += grossTotal
				d.Transactions++
				result.Totals.SellISK += grossTotal
			}

			for sellQty > 0 {
				pick := pickNextLot(opts.FIFOMode, tradePool, mfgPool, tx.TypeID)
				if pick.kind == lotKindNone {
					// Orphan — no cost basis available. Record the sell
					// revenue as unattributed and stop matching.
					if inWindow {
						orphanRev := sellGrossPerUnit * float64(sellQty)
						orphanBroker, orphanTax := feesOn(orphanRev)
						result.Totals.UnattributedISK += orphanRev
						result.Lots = append(result.Lots, TradeJournalLot{
							Source:           LotSourceOrphan,
							SellDate:         tx.Date,
							SellTxnID:        tx.TransactionID,
							SellWallet:       tx.WalletKey,
							SellLocationID:   tx.LocationID,
							SellLocationName: tx.LocationName,
							TypeID:           tx.TypeID,
							TypeName:         firstNonEmpty(tx.TypeName, opts.TypeNameFor(tx.TypeID)),
							MatchedQty:       int64(sellQty),
							SellUnitPrice:    sellGrossPerUnit,
							SellGross:        orphanRev,
							SellFees:         orphanBroker + orphanTax,
							SellBrokerFee:    orphanBroker,
							SellTax:          orphanTax,
							SellTaxActual:    taxActual,
							NetProfit:        0,
						})
					}
					sellQty = 0
					continue
				}
				matched := pick.remaining
				if matched > int64(sellQty) {
					matched = int64(sellQty)
				}
				consumeFront(pick.kind, tradePool, mfgPool, tx.TypeID, matched)
				sellQty -= int32(matched)

				if !inWindow {
					continue
				}

				sellGross := sellGrossPerUnit * float64(matched)
				sellBroker, sellTax := feesOn(sellGross)
				sellFees := sellBroker + sellTax

				switch pick.kind {
				case lotKindTrade:
					buyGross := pick.unitPrice * float64(matched)
					buyFees := buyGross * brokerPct / 100.0
					net := sellGross - sellFees - buyGross - buyFees
					result.Lots = append(result.Lots, TradeJournalLot{
						Source:           LotSourceTrade,
						SellDate:         tx.Date,
						SellTxnID:        tx.TransactionID,
						SellWallet:       tx.WalletKey,
						SellLocationID:   tx.LocationID,
						SellLocationName: tx.LocationName,
						TypeID:           tx.TypeID,
						TypeName:         firstNonEmpty(tx.TypeName, opts.TypeNameFor(tx.TypeID)),
						MatchedQty:       matched,
						SellUnitPrice:    sellGrossPerUnit,
						SellGross:        sellGross,
						SellFees:         sellFees,
						SellBrokerFee:    sellBroker,
						SellTax:          sellTax,
						SellTaxActual:    taxActual,
						NetProfit:        net,
						BuyDate:          pick.date.Format(time.RFC3339),
						BuyTxnID:         pick.txnID,
						BuyWallet:        pick.walletKey,
						BuyLocationID:    pick.locationID,
						BuyLocationName:  pick.locationName,
						BuyUnitPrice:     pick.unitPrice,
						BuyFees:          buyFees,
					})
					result.Totals.TradingPnL += net
					result.Totals.FeesISK += sellFees
					d := getDay(ev.when)
					d.TradingPnL += net
					d.FeesISK += sellFees
				case lotKindMfg:
					buildCost := pick.unitPrice * float64(matched)
					net := sellGross - sellFees - buildCost
					result.Lots = append(result.Lots, TradeJournalLot{
						Source:           LotSourceManufacture,
						SellDate:         tx.Date,
						SellTxnID:        tx.TransactionID,
						SellWallet:       tx.WalletKey,
						SellLocationID:   tx.LocationID,
						SellLocationName: tx.LocationName,
						TypeID:           tx.TypeID,
						TypeName:         firstNonEmpty(tx.TypeName, opts.TypeNameFor(tx.TypeID)),
						MatchedQty:       matched,
						SellUnitPrice:    sellGrossPerUnit,
						SellGross:        sellGross,
						SellFees:         sellFees,
						SellBrokerFee:    sellBroker,
						SellTax:          sellTax,
						SellTaxActual:    taxActual,
						NetProfit:        net,
						// The build unit cost is the manufacture row's cost
						// basis. It was previously spent computing `net` and
						// then discarded, which is why P&L could not see what
						// a built item cost.
						BuyUnitPrice:       pick.unitPrice,
						ManufactureJobID:   pick.jobID,
						ManufactureME:      pick.me,
						ManufactureMETag:   pick.meTag,
						MaterialsEstimated: pick.materialsEstimated,
					})
					result.Totals.ManufacturingPnL += net
					result.Totals.FeesISK += sellFees
					d := getDay(ev.when)
					d.ManufacturingPnL += net
					d.FeesISK += sellFees
				}
			}
		}
	}

	// Combined = trading + manufacturing.
	result.Totals.CombinedPnL = result.Totals.TradingPnL + result.Totals.ManufacturingPnL
	// Fill combined_pnl on each daily bucket.
	for i := range result.DailyPnL {
		result.DailyPnL[i].CombinedPnL = result.DailyPnL[i].TradingPnL + result.DailyPnL[i].ManufacturingPnL
	}
	// Daily buckets aren't guaranteed to be in order (map iteration); sort.
	sort.Slice(result.DailyPnL, func(a, b int) bool {
		return result.DailyPnL[a].Date < result.DailyPnL[b].Date
	})

	// Emit open positions from the pool state at end of stream.
	//
	// Trade lots split by station and carry the buy broker fee in their cost
	// basis, matching ComputePortfolioPnLWithOptions exactly — the P&L
	// projection compares against it, and a position that cost 5% more than
	// it says is a position that reports a profit it never made.
	type openTradeKey struct {
		typeID     int32
		locationID int64
	}
	type openTradeAgg struct {
		typeName     string
		locationName string
		qty          int64
		costSum      float64
		oldest       time.Time
	}
	openTrades := make(map[openTradeKey]*openTradeAgg)
	for typeID, lots := range tradePool {
		for _, l := range lots {
			if l.remaining <= 0 {
				continue
			}
			key := openTradeKey{typeID: typeID, locationID: l.locationID}
			a := openTrades[key]
			if a == nil {
				a = &openTradeAgg{typeName: l.typeName, locationName: l.locationName, oldest: l.date}
				openTrades[key] = a
			} else {
				if a.typeName == "" && l.typeName != "" {
					a.typeName = l.typeName
				}
				if a.locationName == "" && l.locationName != "" {
					a.locationName = l.locationName
				}
			}
			a.qty += l.remaining
			lotGross := l.unitPrice * float64(l.remaining)
			a.costSum += lotGross + lotGross*brokerPct/100.0
			if l.date.Before(a.oldest) {
				a.oldest = l.date
			}
		}
	}
	for key, a := range openTrades {
		if a.qty <= 0 {
			continue
		}
		result.OpenPositions = append(result.OpenPositions, JournalOpenPosition{
			TypeID:       key.typeID,
			TypeName:     firstNonEmpty(a.typeName, opts.TypeNameFor(key.typeID)),
			Source:       LotSourceTrade,
			LocationID:   key.locationID,
			LocationName: a.locationName,
			Qty:          a.qty,
			AvgUnitCost:  a.costSum / float64(a.qty),
			CostBasis:    a.costSum,
			OldestDate:   a.oldest.Format(time.RFC3339),
		})
	}
	for typeID, lots := range mfgPool {
		var qty int64
		var costSum float64
		var oldest time.Time
		var productName string
		for _, l := range lots {
			if l.remaining <= 0 {
				continue
			}
			qty += l.remaining
			costSum += l.unitCost * float64(l.remaining)
			if oldest.IsZero() || l.date.Before(oldest) {
				oldest = l.date
			}
			if productName == "" {
				productName = l.productName
			}
		}
		if qty <= 0 {
			continue
		}
		avg := 0.0
		if qty > 0 {
			avg = costSum / float64(qty)
		}
		result.OpenPositions = append(result.OpenPositions, JournalOpenPosition{
			TypeID:      typeID,
			TypeName:    firstNonEmpty(productName, opts.TypeNameFor(typeID)),
			Source:      LotSourceManufacture,
			Qty:         qty,
			AvgUnitCost: avg,
			CostBasis:   costSum,
			OldestDate:  oldest.Format(time.RFC3339),
		})
	}
	sort.Slice(result.OpenPositions, func(a, b int) bool {
		if result.OpenPositions[a].TypeID != result.OpenPositions[b].TypeID {
			return result.OpenPositions[a].TypeID < result.OpenPositions[b].TypeID
		}
		if result.OpenPositions[a].Source != result.OpenPositions[b].Source {
			return result.OpenPositions[a].Source < result.OpenPositions[b].Source
		}
		return result.OpenPositions[a].LocationID < result.OpenPositions[b].LocationID
	})

	return result
}

// --- pool + pop helpers ---
//
// Go maps aren't addressable, so we can't pass a pointer to a slice entry.
// Instead consumeFront() operates on the map + typeID directly and writes
// the truncated slice back to the map when the front lot is depleted.

type lotKind int

const (
	lotKindNone lotKind = iota
	lotKindTrade
	lotKindMfg
)

// pickedLot is a read-only snapshot of the current front lot, populated
// by pickNextLot. consumeFront then reduces the actual lot in the pool.
type pickedLot struct {
	kind lotKind

	// Trade-side snapshot
	unitPrice    float64
	date         time.Time
	txnID        int64
	walletKey    string
	locationID   int64
	locationName string

	// Mfg-side snapshot
	jobID              int64
	me                 int32
	meTag              string
	materialsEstimated bool

	remaining int64
}

// pickNextLot returns a snapshot of the front lot for typeID under the
// given FIFO mode. Returns kind=lotKindNone when both pools are empty.
func pickNextLot(mode FIFOMode, tradePool map[int32][]tradeLot, mfgPool map[int32][]mfgLot, typeID int32) pickedLot {
	tradeSlice := tradePool[typeID]
	mfgSlice := mfgPool[typeID]
	haveT := len(tradeSlice) > 0 && tradeSlice[0].remaining > 0
	haveM := len(mfgSlice) > 0 && mfgSlice[0].remaining > 0
	if !haveT && !haveM {
		return pickedLot{kind: lotKindNone}
	}
	pickT := func() pickedLot {
		l := tradeSlice[0]
		return pickedLot{
			kind:         lotKindTrade,
			unitPrice:    l.unitPrice,
			date:         l.date,
			txnID:        l.txnID,
			walletKey:    l.walletKey,
			locationID:   l.locationID,
			locationName: l.locationName,
			remaining:    l.remaining,
		}
	}
	pickM := func() pickedLot {
		l := mfgSlice[0]
		return pickedLot{
			kind:               lotKindMfg,
			unitPrice:          l.unitCost,
			date:               l.date,
			jobID:              l.jobID,
			me:                 l.me,
			meTag:              l.meTag,
			materialsEstimated: l.materialsEstimated,
			remaining:          l.remaining,
		}
	}
	switch mode {
	case FIFOModeTradeFirst:
		if haveT {
			return pickT()
		}
		return pickM()
	case FIFOModeManufactureFirst:
		if haveM {
			return pickM()
		}
		return pickT()
	default: // strict_date
		if !haveT {
			return pickM()
		}
		if !haveM {
			return pickT()
		}
		if tradeSlice[0].date.Before(mfgSlice[0].date) {
			return pickT()
		}
		return pickM()
	}
}

// consumeFront reduces the front lot of the chosen pool by n units,
// dropping it from the deque when depleted. Writes the modified slice
// back to the map so the truncation actually sticks.
func consumeFront(kind lotKind, tradePool map[int32][]tradeLot, mfgPool map[int32][]mfgLot, typeID int32, n int64) {
	switch kind {
	case lotKindTrade:
		slice := tradePool[typeID]
		if len(slice) == 0 {
			return
		}
		slice[0].remaining -= n
		if slice[0].remaining <= 0 {
			slice = slice[1:]
		}
		tradePool[typeID] = slice
	case lotKindMfg:
		slice := mfgPool[typeID]
		if len(slice) == 0 {
			return
		}
		slice[0].remaining -= n
		if slice[0].remaining <= 0 {
			slice = slice[1:]
		}
		mfgPool[typeID] = slice
	}
}

// consumeTradePoolFIFO pops `need` units of the given typeID from the
// trading pool, returning the actual qty consumed and its total cost
// (unit prices × consumed, exclusive of broker fees — job material cost
// is CCP's install-fee basis + raw material cost).
func consumeTradePoolFIFO(pool map[int32][]tradeLot, typeID int32, need int64) (consumed int64, cost float64) {
	slice := pool[typeID]
	for need > 0 && len(slice) > 0 && slice[0].remaining > 0 {
		take := slice[0].remaining
		if take > need {
			take = need
		}
		slice[0].remaining -= take
		cost += slice[0].unitPrice * float64(take)
		consumed += take
		need -= take
		if slice[0].remaining <= 0 {
			slice = slice[1:]
		}
	}
	pool[typeID] = slice
	return
}

// --- utilities ---

func parseISO(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errEmptyTime
	}
	return time.Parse(time.RFC3339, s)
}

var errEmptyTime = &emptyTimeError{}

type emptyTimeError struct{}

func (e *emptyTimeError) Error() string { return "empty time string" }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
