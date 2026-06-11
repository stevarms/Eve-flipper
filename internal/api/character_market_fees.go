package api

import (
	"net/http"
)

// EVE skill IDs we care about for market-fee calculation.
const (
	skillTypeIDAccounting       = 16622 // Accounting
	skillTypeIDBrokerRelations  = 3446  // Broker Relations
)

// Project-wide convention (matches engine.PLEXDashboard at L5):
//   sales tax  = 8.0% × (1 - 0.11 × accountingLevel)   → 3.6% at L5
//   broker fee = 3.0% - 0.4% × brokerRelationsLevel    → 1.0% at L5
// (No standings adjustment in this estimate; user can tweak after.)
func suggestedSalesTax(accountingLevel int) float64 {
	if accountingLevel < 0 {
		accountingLevel = 0
	}
	if accountingLevel > 5 {
		accountingLevel = 5
	}
	return 8.0 * (1.0 - 0.11*float64(accountingLevel))
}

func suggestedBrokerFee(brokerRelationsLevel int) float64 {
	if brokerRelationsLevel < 0 {
		brokerRelationsLevel = 0
	}
	if brokerRelationsLevel > 5 {
		brokerRelationsLevel = 5
	}
	return 3.0 - 0.4*float64(brokerRelationsLevel)
}

type characterMarketFeesResponse struct {
	CharacterID            int64   `json:"character_id"`
	CharacterName          string  `json:"character_name"`
	AccountingLevel        int     `json:"accounting_level"`
	BrokerRelationsLevel   int     `json:"broker_relations_level"`
	SuggestedSalesTaxPct   float64 `json:"suggested_sales_tax_percent"`
	SuggestedBrokerFeePct  float64 `json:"suggested_broker_fee_percent"`
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

	skills, skillsErr := s.esi.GetSkills(sess.CharacterID, token)
	if skillsErr != nil {
		writeError(w, 502, "failed to fetch skills: "+skillsErr.Error())
		return
	}

	accountingLevel := 0
	brokerLevel := 0
	if skills != nil {
		for _, sk := range skills.Skills {
			switch sk.SkillID {
			case skillTypeIDAccounting:
				accountingLevel = sk.TrainedLevel
			case skillTypeIDBrokerRelations:
				brokerLevel = sk.TrainedLevel
			}
		}
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
