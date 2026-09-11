package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"eve-flipper/internal/db"
)

// One place that answers "what am I actually paying to trade?".
//
// The app used to answer it three ways: the scanner prefilled inputs from the
// character's skills, the Positions tab read config, and the trade journal read
// a config sales tax with the broker fee pinned at 1%. A user with Broker
// Relations III saw three different profits for one trade. Every realized-profit
// surface now resolves its rates here and reports which source they came from.

// FeeProfile is the resolved sell-side rate pair plus its provenance.
type FeeProfile struct {
	SalesTaxPercent  float64 `json:"sales_tax_percent"`
	BrokerFeePercent float64 `json:"broker_fee_percent"`
	// Source is what the UI states next to the rates: "config" (the user set
	// them), "skills" (derived from Accounting / Broker Relations), or
	// "default" (neither was available).
	Source               string `json:"source"`
	AccountingLevel      int    `json:"accounting_level,omitempty"`
	BrokerRelationsLevel int    `json:"broker_relations_level,omitempty"`
}

// configFeeRate is one configured sell-side rate and how much authority it
// carries over the character's actual skills.
type configFeeRate struct {
	Value float64
	// Set: config held a value at all, so it beats the historical fallback.
	Set bool
	// Snapshot: the value is exactly one the skill formula can produce, which
	// makes it a past "import fees from ESI" click rather than a typed rate.
	Snapshot bool
}

// authoritative reports whether the rate represents a decision the user made
// themselves, which skills must not overrule.
func (r configFeeRate) authoritative() bool { return r.Set && !r.Snapshot }

// skillSnapshotTolerance is far tighter than the gap between adjacent skill
// levels (0.88pp for sales tax, 0.3pp for broker fee) and far looser than
// float error, so a stored 4.48 matches Accounting IV and a typed 4.5 does not.
const skillSnapshotTolerance = 1e-6

// isSkillSnapshot reports whether value is one of the six rates the skill
// formula can produce, i.e. whether the app itself most likely wrote it.
//
// This is how a stale import is told apart from a deliberate rate. Fees are
// snapshotted into config by the "import from ESI" button and then never
// revisited, so training Accounting left every profit figure in the app frozen
// at the old rate, and an import that ran while the skill sheet was unreadable
// baked in the untrained 8% / 3% permanently. A citadel's 2.5% broker fee is
// not a value the formula can produce, so a typed rate still wins; and where a
// stored value genuinely was chosen to match untrained skills, recomputing it
// from those skills returns the same number.
func isSkillSnapshot(value float64, suggest func(int) float64) bool {
	for level := 0; level <= 5; level++ {
		if math.Abs(value-suggest(level)) < skillSnapshotTolerance {
			return true
		}
	}
	return false
}

// configFeesExplicit reports the user's configured sell-side rates, sell
// specific values winning over the generic ones, along with how much authority
// each one carries.
//
// 8% / 1% is the historical fallback: 1% understates an untrained broker fee
// (3%), but it is the number every existing figure in the app was computed
// with, so it stays until skills or config replace it.
func (s *Server) configFeesExplicit(userID string) (salesTax, brokerFee configFeeRate) {
	salesTax = configFeeRate{Value: 8.0}
	brokerFee = configFeeRate{Value: 1.0}
	cfg := s.loadConfigForUser(userID)
	if cfg == nil {
		return salesTax, brokerFee
	}
	if cfg.SellSalesTaxPercent > 0 {
		salesTax = configFeeRate{Value: cfg.SellSalesTaxPercent, Set: true}
	} else if cfg.SalesTaxPercent > 0 {
		salesTax = configFeeRate{Value: cfg.SalesTaxPercent, Set: true}
	}
	if cfg.SellBrokerFeePercent > 0 {
		brokerFee = configFeeRate{Value: cfg.SellBrokerFeePercent, Set: true}
	} else if cfg.BrokerFeePercent > 0 {
		brokerFee = configFeeRate{Value: cfg.BrokerFeePercent, Set: true}
	}
	salesTax.Snapshot = salesTax.Set && isSkillSnapshot(salesTax.Value, suggestedSalesTax)
	brokerFee.Snapshot = brokerFee.Set && isSkillSnapshot(brokerFee.Value, suggestedBrokerFee)
	return salesTax, brokerFee
}

// configFees is configFeesExplicit for callers that only need the numbers.
//
// The trade journal used to read only the sales tax from config and leave the
// broker fee at 1%, which quietly made every journal figure optimistic for any
// trader without max Broker Relations.
func (s *Server) configFees(userID string) (salesTax, brokerFee float64) {
	tax, broker := s.configFeesExplicit(userID)
	return tax.Value, broker.Value
}

const feeSkillCacheTTL = 30 * time.Minute

type feeSkillEntry struct {
	accounting int
	broker     int
	cachedAt   time.Time
}

// feeSkillCache keeps skill levels off the hot path. Accounting and Broker
// Relations take weeks to train, so a half-hour-stale answer is exact for all
// practical purposes — and without it every P&L load would spend an ESI round
// trip re-reading a number that cannot have changed.
var feeSkillCache = struct {
	mu sync.Mutex
	m  map[int64]feeSkillEntry
}{m: map[int64]feeSkillEntry{}}

// marketSkillLevels returns the character's trained Accounting and Broker
// Relations levels.
//
// An empty skill sheet is an error, not a character with nothing trained. Every
// capsuleer has skills, so an empty list means ESI answered but told us nothing
// — and reporting that as level 0 is how an "import fees from ESI" click can
// bake in the untrained 8% / 3% for a trader with Accounting V.
func (s *Server) marketSkillLevels(characterID int64, token string) (accounting, broker int, err error) {
	feeSkillCache.mu.Lock()
	if e, ok := feeSkillCache.m[characterID]; ok && time.Since(e.cachedAt) < feeSkillCacheTTL {
		feeSkillCache.mu.Unlock()
		return e.accounting, e.broker, nil
	}
	feeSkillCache.mu.Unlock()

	skills, err := s.esi.GetSkills(characterID, token)
	if err != nil {
		return 0, 0, err
	}
	if skills == nil || len(skills.Skills) == 0 {
		return 0, 0, errors.New("esi returned an empty skill sheet")
	}
	for _, sk := range skills.Skills {
		switch sk.SkillID {
		case skillTypeIDAccounting:
			accounting = sk.TrainedLevel
		case skillTypeIDBrokerRelations:
			broker = sk.TrainedLevel
		}
	}
	feeSkillCache.mu.Lock()
	feeSkillCache.m[characterID] = feeSkillEntry{accounting: accounting, broker: broker, cachedAt: time.Now()}
	feeSkillCache.mu.Unlock()
	return accounting, broker, nil
}

// resolveFeeProfile answers with the rates the user deliberately chose where
// they chose them, the rates their skills imply everywhere else, and the
// historical fallback only when neither is available.
//
// A typed rate wins over skills: someone who entered 2.5% because they sell
// from a citadel with a broker-fee discount must keep seeing 2.5%. A stored
// rate that is exactly what the formula produces does not win, because it is
// almost certainly a stale "import from ESI" snapshot rather than a choice, and
// deferring to it is what left this app charging an untrained 8% to a character
// with Accounting V. See isSkillSnapshot.
func (s *Server) resolveFeeProfile(userID string, characterID int64) FeeProfile {
	salesTax, brokerFee := s.configFeesExplicit(userID)
	out := FeeProfile{SalesTaxPercent: salesTax.Value, BrokerFeePercent: brokerFee.Value, Source: "default"}
	if salesTax.authoritative() && brokerFee.authoritative() {
		out.Source = "config"
		return out
	}
	// Falling back mid-resolution still reports "config" when either side did
	// come from config, snapshot or not: the UI's override affordance has to
	// name what it is overriding, and the number on screen is the stored one.
	configOnly := func() FeeProfile {
		if salesTax.Set || brokerFee.Set {
			out.Source = "config"
		}
		return out
	}
	if characterID <= 0 || s.sessions == nil || s.sso == nil || s.esi == nil {
		return configOnly()
	}
	token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, characterID)
	if err != nil {
		return configOnly()
	}
	accounting, broker, err := s.marketSkillLevels(characterID, token)
	if err != nil {
		return configOnly()
	}
	out.AccountingLevel = accounting
	out.BrokerRelationsLevel = broker
	out.Source = "skills"
	if !salesTax.authoritative() {
		out.SalesTaxPercent = suggestedSalesTax(accounting)
	}
	if !brokerFee.authoritative() {
		out.BrokerFeePercent = suggestedBrokerFee(broker)
	}
	// Both rates came from config after all, so say so rather than crediting
	// skills for numbers they did not supply.
	if salesTax.authoritative() && brokerFee.authoritative() {
		out.Source = "config"
	}
	return out
}

// journalFeeRates is an explicit rate pair supplied by the caller, overriding
// the resolved profile for one request. The zero value means "resolve".
//
// It has to reach the FIFO engine rather than the presentation layer: fees are
// charged per matched sell during the match, so a rate applied afterwards
// would disagree with the per-lot numbers the drawer shows.
type journalFeeRates struct {
	salesTax  float64
	brokerFee float64
	set       bool
}

// journalFeeProfile picks the rates the journal compute will charge for a
// wallet scope.
//
// A scope can span several characters with different Accounting levels, and
// one flat pair cannot be right for all of them. That approximation predates
// this function — the engine takes a single rate pair — so it resolves the
// scope's first character and the UI states whose profile it is.
func (s *Server) journalFeeProfile(userID string, filter *db.WalletScopeFilter, override journalFeeRates) FeeProfile {
	if override.set {
		return FeeProfile{
			SalesTaxPercent:  override.salesTax,
			BrokerFeePercent: override.brokerFee,
			Source:           "override",
		}
	}
	var characterID int64
	if filter != nil && len(filter.IncludeCharacters) > 0 {
		characterID = filter.IncludeCharacters[0]
	} else if s.sessions != nil {
		if sess := s.sessions.GetForUser(userID); sess != nil {
			characterID = sess.CharacterID
		}
	}
	return s.resolveFeeProfile(userID, characterID)
}

// parseJournalFeeOverride reads an explicit rate pair off the query string.
//
// Both rates are required. A half-override — one typed rate and one resolved —
// would leave the UI unable to state what it is overriding, and silently mixing
// a citadel broker fee with a skill-derived sales tax produces a number nobody
// can reproduce later.
func parseJournalFeeOverride(r *http.Request) journalFeeRates {
	q := r.URL.Query()
	salesTax, taxErr := strconv.ParseFloat(q.Get("sales_tax"), 64)
	brokerFee, brokerErr := strconv.ParseFloat(q.Get("broker_fee"), 64)
	if taxErr != nil || brokerErr != nil {
		return journalFeeRates{}
	}
	// Non-finite rates slip past a range test written this way (every
	// comparison against NaN is false) and then fail the response at encode
	// time instead of here.
	if !isFiniteRate(salesTax) || !isFiniteRate(brokerFee) {
		return journalFeeRates{}
	}
	if salesTax < 0 || salesTax > 100 || brokerFee < 0 || brokerFee > 100 {
		return journalFeeRates{}
	}
	return journalFeeRates{salesTax: salesTax, brokerFee: brokerFee, set: true}
}

// isFiniteRate reports whether a client-supplied percentage is a real number.
func isFiniteRate(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
