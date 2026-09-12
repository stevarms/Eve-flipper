package api

import (
	"net/http"
)

// EVE skill IDs we care about for market-fee calculation.
const (
	skillTypeIDAccounting      = 16622 // Accounting
	skillTypeIDBrokerRelations = 3446  // Broker Relations
)

// baseSalesTaxPercent is the untrained sales tax rate, i.e. the rate at
// Accounting 0.
//
// Measured, not quoted. 3157 sales in the wallet journal archive with an
// unambiguous transaction_tax pairing all imply exactly 3.3750%, with no
// spread, for a character ESI reports as Accounting V. That fixes the base at
// 3.375 / (1 - 0.11×5) = 7.5. The figure that was here before, 8.0, produced
// 3.60% and overstated the tax on every sale in the app by 6.25% relative.
//
// If you are about to "correct" this back to 8.0 because a wiki says so,
// re-run the measurement against a live wallet first: pair each
// transaction_tax journal entry with the market_transaction entry sharing its
// timestamp and divide.
const baseSalesTaxPercent = 7.5

// EVE canonical:
//
//	sales tax  = 7.5% × (1 - 0.11 × accountingLevel)   → 3.375% at L5
//	broker fee = 3.0% - 0.3% × brokerRelationsLevel    → 1.5% at L5
//
// (No standings adjustment in this estimate; user can tweak after.)
func suggestedSalesTax(accountingLevel int) float64 {
	if accountingLevel < 0 {
		accountingLevel = 0
	}
	if accountingLevel > 5 {
		accountingLevel = 5
	}
	return salesTaxAtBase(baseSalesTaxPercent, accountingLevel)
}

// salesTaxAtBase applies the Accounting reduction to an arbitrary base rate.
// Snapshot detection needs it for bases this app no longer uses; see
// salesTaxBases in fee_profile.go.
func salesTaxAtBase(base float64, accountingLevel int) float64 {
	return base * (1.0 - 0.11*float64(accountingLevel))
}

func suggestedBrokerFee(brokerRelationsLevel int) float64 {
	if brokerRelationsLevel < 0 {
		brokerRelationsLevel = 0
	}
	if brokerRelationsLevel > 5 {
		brokerRelationsLevel = 5
	}
	return 3.0 - 0.3*float64(brokerRelationsLevel)
}

type characterMarketFeesResponse struct {
	CharacterID           int64   `json:"character_id"`
	CharacterName         string  `json:"character_name"`
	AccountingLevel       int     `json:"accounting_level"`
	BrokerRelationsLevel  int     `json:"broker_relations_level"`
	SuggestedSalesTaxPct  float64 `json:"suggested_sales_tax_percent"`
	SuggestedBrokerFeePct float64 `json:"suggested_broker_fee_percent"`
}

// handleAuthCharacterMarketFees returns suggested market fee percentages
// derived from the active character's Accounting and Broker Relations skill
// levels. The scanner UI uses this to populate fee inputs without forcing the
// user to remember their skill levels.
func (s *Server) handleAuthCharacterMarketFees(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	if s.sessions == nil {
		writeError(w, 401, "not logged in")
		return
	}
	sess := s.sessions.GetForUser(userID)
	if sess == nil {
		writeError(w, 401, "not logged in")
		return
	}

	token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
	if tokenErr != nil {
		writeError(w, 401, tokenErr.Error())
		return
	}

	accountingLevel, brokerLevel, skillsErr := s.marketSkillLevels(sess.CharacterID, token)
	if skillsErr != nil {
		writeError(w, 502, "failed to fetch skills: "+skillsErr.Error())
		return
	}

	resp := characterMarketFeesResponse{
		CharacterID:           sess.CharacterID,
		CharacterName:         sess.CharacterName,
		AccountingLevel:       accountingLevel,
		BrokerRelationsLevel:  brokerLevel,
		SuggestedSalesTaxPct:  suggestedSalesTax(accountingLevel),
		SuggestedBrokerFeePct: suggestedBrokerFee(brokerLevel),
	}
	writeJSON(w, resp)
}
