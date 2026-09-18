package api

import (
	"fmt"
	"log"
	"strings"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/corp"
	"eve-flipper/internal/esi"
	"eve-flipper/internal/sde"
)

// order_desk_corp.go — corporation market orders as a desk owner.
//
// The desk has always asked every authenticated character for its orders and
// stopped there. A corporation wallet holding sell orders was therefore
// invisible, and an invisible order does not read as "unknown" — it reads as
// "no competition" and "nothing listed", which are both wrong and both
// actionable. So the corp book is fetched here and folded in as one more owner.
//
// Everything that decides *which* corporation, *through which character*, and
// *what to say when the answer is no* lives in collectCorpDeskOrders, behind a
// narrow interface, so it can be tested without a live token.

// Owner kinds stamped onto a desk row. A row's CharacterID / CharacterName have
// always meant "who owns this"; the kind is what keeps that reading true once
// the owner can be a corporation.
const (
	orderOwnerKindCharacter   = "character"
	orderOwnerKindCorporation = "corporation"
)

// orderOwner is who a desk row belongs to, remembered by order id while the
// orders from every owner are merged into one slice.
type orderOwner struct {
	// kind is orderOwnerKindCharacter or orderOwnerKindCorporation, and says
	// which of the two id spaces id belongs to.
	kind string
	id   int64
	name string

	// feeCharacterID / feeCharacterName name the character whose standings and
	// skills set this order's real broker fee and sales tax. For a personal
	// order that is the owner; for a corporation order it is the issuer.
	feeCharacterID   int64
	feeCharacterName string
}

// corpMarketOrderRoles are the corporation roles ESI accepts on
// /corporations/{id}/orders/. Director is here because a director holds every
// corporation role implicitly, and a corp run by one person is the common case
// this feature exists for.
var corpMarketOrderRoles = map[string]bool{
	"director":   true,
	"accountant": true,
	"trader":     true,
}

// hasCorpMarketOrderRole reports whether roles include one ESI will accept.
func hasCorpMarketOrderRole(roles []string) bool {
	for _, r := range roles {
		if corpMarketOrderRoles[strings.ToLower(strings.TrimSpace(r))] {
			return true
		}
	}
	return false
}

// deskCorpSession is one authenticated character with a live token, which is
// all the corp pass needs from a session.
type deskCorpSession struct {
	characterID   int64
	characterName string
	token         string
}

// corpOrderSource is the ESI surface the corp pass uses, narrowed to an
// interface. The fan-in judgement — which corporations exist, which character
// can reach each one, and what to report when none can — is the part worth
// testing, and none of it needs a real token.
type corpOrderSource interface {
	CorporationIDFor(characterID int64) (int32, error)
	RolesFor(characterID int64, token string) ([]string, error)
	OrdersFor(corpID int32, viaCharacterID int64, token string) ([]corp.CorpMarketOrder, error)
	CorporationName(corpID int32) string
}

// collectCorpDeskOrders fetches each distinct corporation's market orders once
// and tags every row with the corporation as owner.
//
// It never returns an error. A corporation the user cannot read is a warning
// naming that corporation, because the desk is still worth showing without it —
// but only if it says so. Silence here is the failure mode the whole function
// is written against.
func collectCorpDeskOrders(sessions []deskCorpSession, src corpOrderSource) ([]esi.CharacterOrder, map[int64]orderOwner, []string) {
	owners := make(map[int64]orderOwner)
	if src == nil || len(sessions) == 0 {
		return nil, owners, nil
	}

	var out []esi.CharacterOrder
	var warnings []string
	said := make(map[string]bool)
	warn := func(format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		if said[msg] {
			return
		}
		said[msg] = true
		warnings = append(warnings, msg)
	}

	// Group first, judge second. A corporation with two authenticated
	// characters where only the second holds Trader must not warn about the
	// first: the question is whether the corporation is reachable at all, not
	// whether every character can reach it.
	byCorp := make(map[int32][]deskCorpSession)
	var corpOrder []int32
	for _, sess := range sessions {
		if sess.token == "" {
			continue
		}
		corpID, err := src.CorporationIDFor(sess.characterID)
		if err != nil || corpID <= 0 {
			continue
		}
		if _, seen := byCorp[corpID]; !seen {
			corpOrder = append(corpOrder, corpID)
		}
		byCorp[corpID] = append(byCorp[corpID], sess)
	}

	for _, corpID := range corpOrder {
		corpName := src.CorporationName(corpID)

		var holders []deskCorpSession
		roleReadFailed := false
		for _, sess := range byCorp[corpID] {
			roles, err := src.RolesFor(sess.characterID, sess.token)
			if err != nil {
				roleReadFailed = true
				continue
			}
			if hasCorpMarketOrderRole(roles) {
				holders = append(holders, sess)
			}
		}
		if len(holders) == 0 {
			// A failed role read and an honest "nobody holds the role" want
			// different fixes, and telling the user to go hand out corp roles
			// when the real problem is a missing scope wastes their evening.
			if roleReadFailed {
				warn("corp orders skipped for %s: corporation roles unreadable (re-authenticate to grant esi-characters.read_corporation_roles.v1)", corpName)
			} else {
				warn("corp orders skipped for %s: no authenticated character holds Director / Accountant / Trader", corpName)
			}
			continue
		}

		// One successful fetch per corporation. Trying the next role holder
		// after a failure covers the case where one character's token has the
		// role but not the market scope.
		fetched := false
		var lastErr error
		for _, sess := range holders {
			corpOrders, err := src.OrdersFor(corpID, sess.characterID, sess.token)
			if err != nil {
				lastErr = err
				continue
			}
			fetched = true
			converted, tags := corpOrdersToDeskOrders(corpID, corpName, corpOrders, sess)
			out = append(out, converted...)
			for id, owner := range tags {
				owners[id] = owner
			}
			break
		}
		if !fetched {
			msg := ""
			if lastErr != nil {
				msg = strings.ToLower(lastErr.Error())
			}
			if strings.Contains(msg, "403") || strings.Contains(msg, "scope") {
				warn("corp orders skipped for %s: missing esi-markets.read_corporation_orders.v1 (re-authenticate)", corpName)
			} else {
				warn("corp orders unavailable for %s: %v", corpName, lastErr)
			}
		}
	}
	return out, owners, warnings
}

// corpOrdersToDeskOrders shapes corporation orders into the CharacterOrder the
// desk engine takes, and tags each one with its owner.
//
// Appending these to the character orders cannot double-count anything: order
// ids are unique game-wide and a corporation order never appears in
// /characters/{id}/orders/. Where a character and the corporation both hold an
// order for the same type at the same station, the two rows converge on a
// single book fetch because that fan-out is keyed by (region, type) in a map.
func corpOrdersToDeskOrders(
	corpID int32,
	corpName string,
	corpOrders []corp.CorpMarketOrder,
	via deskCorpSession,
) ([]esi.CharacterOrder, map[int64]orderOwner) {
	owners := make(map[int64]orderOwner, len(corpOrders))
	out := make([]esi.CharacterOrder, 0, len(corpOrders))
	for _, o := range corpOrders {
		out = append(out, esi.CharacterOrder{
			OrderID:      o.OrderID,
			TypeID:       o.TypeID,
			LocationID:   o.LocationID,
			RegionID:     o.RegionID,
			Price:        o.Price,
			VolumeRemain: o.VolumeRemain,
			VolumeTotal:  o.VolumeTotal,
			IsBuyOrder:   o.IsBuyOrder,
			Duration:     o.Duration,
			Issued:       o.Issued,
			Range:        o.Range,
			TypeName:     o.TypeName,
			LocationName: o.LocationName,
		})

		// Whose fees these are is not a detail. A corp order's broker fee and
		// sales tax follow the *issuing* character's standings and skills, so
		// the row names that character instead of letting the desk's single
		// request-level rate pass for a profile nobody chose. Falls back to the
		// character the book was fetched through when ESI did not resolve the
		// issuer.
		feeID, feeName := o.CharacterID, strings.TrimSpace(o.CharacterName)
		if feeID <= 0 {
			feeID, feeName = via.characterID, via.characterName
		}
		if feeID > 0 && feeName == "" {
			feeName = fmt.Sprintf("Character %d", feeID)
		}
		owners[o.OrderID] = orderOwner{
			kind:             orderOwnerKindCorporation,
			id:               int64(corpID),
			name:             corpName,
			feeCharacterID:   feeID,
			feeCharacterName: feeName,
		}
	}
	return out, owners
}

// serverCorpOrderSource binds the corp pass to live ESI.
type serverCorpOrderSource struct {
	s       *Server
	sdeData *sde.Data
}

func (c serverCorpOrderSource) CorporationIDFor(characterID int64) (int32, error) {
	return c.s.esi.GetCharacterCorporationID(characterID)
}

func (c serverCorpOrderSource) RolesFor(characterID int64, token string) ([]string, error) {
	roles, err := c.s.esi.GetCharacterRoles(characterID, token)
	if err != nil {
		return nil, err
	}
	if roles == nil {
		return nil, nil
	}
	return roles.Roles, nil
}

func (c serverCorpOrderSource) OrdersFor(corpID int32, viaCharacterID int64, token string) ([]corp.CorpMarketOrder, error) {
	return corp.NewESICorpProvider(c.s.esi, c.sdeData, token, corpID, viaCharacterID).GetOrders()
}

func (c serverCorpOrderSource) CorporationName(corpID int32) string {
	return c.s.corporationName(int64(corpID))
}

// corpOrdersForDesk resolves tokens for the sessions in scope and runs the corp
// pass over them.
//
// A token that cannot be refreshed is logged and dropped rather than warned
// about: the character loop in buildOrderDesk hits the same failure first and
// already reports it, and saying it twice in the same response is noise.
func (s *Server) corpOrdersForDesk(userID string, sessions []*auth.Session) ([]esi.CharacterOrder, map[int64]orderOwner, []string) {
	if s == nil || s.esi == nil || s.sessions == nil || s.sso == nil {
		return nil, nil, nil
	}
	deskSessions := make([]deskCorpSession, 0, len(sessions))
	for _, sess := range sessions {
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			log.Printf("[AUTH] OrderDesk corp token error (%s): %v", sess.CharacterName, err)
			continue
		}
		deskSessions = append(deskSessions, deskCorpSession{
			characterID:   sess.CharacterID,
			characterName: sess.CharacterName,
			token:         token,
		})
	}
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	return collectCorpDeskOrders(deskSessions, serverCorpOrderSource{s: s, sdeData: sdeData})
}
