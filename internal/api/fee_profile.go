package api

import (
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

// configFeesExplicit reports the user's configured sell-side rates and whether
// each was actually set, sell-specific values winning over the generic ones.
//
// The "was it set" half is what lets skills fill a gap without overwriting a
// deliberate choice. 8% / 1% is the historical fallback — 1% understates an
// untrained broker fee (3%), but it is the number every existing figure in the
// app was computed with, so it stays until skills or config replace it.
func (s *Server) configFeesExplicit(userID string) (salesTax float64, salesSet bool, brokerFee float64, brokerSet bool) {
	salesTax, brokerFee = 8.0, 1.0
	cfg := s.loadConfigForUser(userID)
	if cfg == nil {
		return salesTax, false, brokerFee, false
	}
	if cfg.SellSalesTaxPercent > 0 {
		salesTax, salesSet = cfg.SellSalesTaxPercent, true
	} else if cfg.SalesTaxPercent > 0 {
		salesTax, salesSet = cfg.SalesTaxPercent, true
	}
	if cfg.SellBrokerFeePercent > 0 {
		brokerFee, brokerSet = cfg.SellBrokerFeePercent, true
	} else if cfg.BrokerFeePercent > 0 {
		brokerFee, brokerSet = cfg.BrokerFeePercent, true
	}
	return salesTax, salesSet, brokerFee, brokerSet
}

// configFees is configFeesExplicit for callers that only need the numbers.
//
// The trade journal used to read only the sales tax from config and leave the
// broker fee at 1%, which quietly made every journal figure optimistic for any
// trader without max Broker Relations.
func (s *Server) configFees(userID string) (salesTax, brokerFee float64) {
	salesTax, _, brokerFee, _ = s.configFeesExplicit(userID)
	return salesTax, brokerFee
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
	if skills != nil {
		for _, sk := range skills.Skills {
			switch sk.SkillID {
			case skillTypeIDAccounting:
				accounting = sk.TrainedLevel
			case skillTypeIDBrokerRelations:
				broker = sk.TrainedLevel
			}
		}
	}
	feeSkillCache.mu.Lock()
	feeSkillCache.m[characterID] = feeSkillEntry{accounting: accounting, broker: broker, cachedAt: time.Now()}
	feeSkillCache.mu.Unlock()
	return accounting, broker, nil
}

// resolveFeeProfile answers with the user's configured rates where they set
// them, the rates their skills imply where they did not, and the historical
// fallback only when neither is available.
//
// Skills win over the fallback but never over an explicit setting: a user who
// typed 2.5% because they sell from a citadel with a broker-fee discount must
// keep seeing 2.5%.
func (s *Server) resolveFeeProfile(userID string, characterID int64) FeeProfile {
	salesTax, salesSet, brokerFee, brokerSet := s.configFeesExplicit(userID)
	out := FeeProfile{SalesTaxPercent: salesTax, BrokerFeePercent: brokerFee, Source: "default"}
	if salesSet && brokerSet {
		out.Source = "config"
		return out
	}
	// Falling back mid-resolution still reports "config" when one side did
	// come from config — the UI's override affordance has to name what it is
	// overriding, and "default" would be a lie for that half.
	configOnly := func() FeeProfile {
		if salesSet || brokerSet {
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
	if !salesSet {
		out.SalesTaxPercent = suggestedSalesTax(accounting)
	}
	if !brokerSet {
		out.BrokerFeePercent = suggestedBrokerFee(broker)
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
	if salesTax < 0 || salesTax > 100 || brokerFee < 0 || brokerFee > 100 {
		return journalFeeRates{}
	}
	return journalFeeRates{salesTax: salesTax, brokerFee: brokerFee, set: true}
}
