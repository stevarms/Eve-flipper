package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"eve-flipper/internal/auth"
	"eve-flipper/internal/corp"
	"eve-flipper/internal/db"
	"eve-flipper/internal/esi"
)

// ownerScope is who a request wants data for: any mix of characters and
// corporations. It is deliberately separate from parseAuthScope's
// (characterID, all) pair, which cannot express a corporation at all and is
// read by dozens of endpoints that would not know what to do with one.
type ownerScope struct {
	Characters      []int64
	AllCharacters   bool
	Corporations    []int64
	AllCorporations bool
}

func (o ownerScope) hasCorp() bool  { return o.AllCorporations || len(o.Corporations) > 0 }
func (o ownerScope) hasChars() bool { return o.AllCharacters || len(o.Characters) > 0 }

// displayName labels a combined result, standing in for a character name.
func (o ownerScope) displayName() string {
	switch {
	case o.AllCharacters && o.AllCorporations:
		return "All Owners"
	case o.hasCorp() && !o.hasChars():
		if len(o.Corporations) == 1 {
			return fmt.Sprintf("Corporation %d", o.Corporations[0])
		}
		return "All Corporations"
	default:
		return "All Characters"
	}
}

// parseOwnerScope reads the `owner` query parameter:
//
//	char:<id>   one character
//	characters  every logged-in character
//	corp:<id>   one corporation (all of its wallet divisions)
//	corps       every corporation with archived wallet data
//	all         everything combined
//
// ok is false when the parameter is absent, which means the caller should fall
// back to parseAuthScope — that is what keeps every existing client working.
func parseOwnerScope(r *http.Request) (scope ownerScope, ok bool, err error) {
	raw := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("owner")))
	if raw == "" {
		return ownerScope{}, false, nil
	}
	switch {
	case raw == "all":
		return ownerScope{AllCharacters: true, AllCorporations: true}, true, nil
	case raw == "characters":
		return ownerScope{AllCharacters: true}, true, nil
	case raw == "corps":
		return ownerScope{AllCorporations: true}, true, nil
	case strings.HasPrefix(raw, "char:"):
		id, parseErr := strconv.ParseInt(raw[len("char:"):], 10, 64)
		if parseErr != nil || id <= 0 {
			return ownerScope{}, false, fmt.Errorf("invalid owner character id")
		}
		return ownerScope{Characters: []int64{id}}, true, nil
	case strings.HasPrefix(raw, "corp:"):
		id, parseErr := strconv.ParseInt(raw[len("corp:"):], 10, 64)
		if parseErr != nil || id <= 0 {
			return ownerScope{}, false, fmt.Errorf("invalid owner corporation id")
		}
		return ownerScope{Corporations: []int64{id}}, true, nil
	}
	return ownerScope{}, false, fmt.Errorf("invalid owner scope")
}

// corpDivisionsForUser lists the (corporation, division) wallets that have
// actually been archived for this user. These are, by construction, the corp
// wallets some character of theirs has the roles to read — so it doubles as
// the corporation discovery list and needs no roles fan-out.
func (s *Server) corpDivisionsForUser(userID string) []db.CorpDivisionKey {
	if s.db == nil {
		return nil
	}
	metas, err := s.db.ListWalletArchiveMetaForUser(userID, db.WalletScopeFilter{IncludeAll: true})
	if err != nil {
		return nil
	}
	out := []db.CorpDivisionKey{}
	for _, m := range metas {
		if !strings.HasPrefix(m.WalletKey, "corp:") {
			continue
		}
		parts := strings.Split(m.WalletKey, ":")
		if len(parts) != 3 {
			continue
		}
		corpID, err1 := strconv.ParseInt(parts[1], 10, 64)
		division, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || corpID <= 0 || division < 1 {
			continue
		}
		out = append(out, db.CorpDivisionKey{CorporationID: corpID, Division: division})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CorporationID != out[j].CorporationID {
			return out[i].CorporationID < out[j].CorporationID
		}
		return out[i].Division < out[j].Division
	})
	return out
}

// walletScopeFilterForOwner turns an ownerScope into the archive-layer filter.
// Corporations expand to every archived division of that corporation, because
// divisions partition a corp wallet rather than identifying an owner.
func (s *Server) walletScopeFilterForOwner(userID string, scope ownerScope, sessions []*auth.Session) db.WalletScopeFilter {
	if scope.AllCharacters && scope.AllCorporations {
		return db.WalletScopeFilter{IncludeAll: true}
	}
	filter := db.WalletScopeFilter{}
	if scope.AllCharacters {
		filter.IncludeCharacters = characterIDsForSessions(sessions)
	} else if len(scope.Characters) > 0 {
		filter.IncludeCharacters = append(filter.IncludeCharacters, scope.Characters...)
	}
	if scope.hasCorp() {
		wanted := map[int64]bool{}
		for _, id := range scope.Corporations {
			wanted[id] = true
		}
		for _, cd := range s.corpDivisionsForUser(userID) {
			if scope.AllCorporations || wanted[cd.CorporationID] {
				filter.IncludeCorpDivisions = append(filter.IncludeCorpDivisions, cd)
			}
		}
	}
	return filter
}

// corpNameCache memoises the public corporation-name lookup. Names are stable
// enough that a process-lifetime cache is the right trade: the owner picker
// would otherwise hit ESI once per corporation every time it is opened.
var corpNameCache = struct {
	mu    sync.Mutex
	names map[int64]string
}{names: map[int64]string{}}

func (s *Server) corporationName(corporationID int64) string {
	corpNameCache.mu.Lock()
	if name, ok := corpNameCache.names[corporationID]; ok {
		corpNameCache.mu.Unlock()
		return name
	}
	corpNameCache.mu.Unlock()

	name := ""
	if s.esi != nil {
		// Corporation info is a public endpoint, so no token is needed.
		info := corp.NewESICorpProvider(s.esi, nil, "", int32(corporationID), 0).GetInfo()
		name = strings.TrimSpace(info.Name)
	}
	if name == "" {
		name = fmt.Sprintf("Corporation %d", corporationID)
	}
	corpNameCache.mu.Lock()
	corpNameCache.names[corporationID] = name
	corpNameCache.mu.Unlock()
	return name
}

// ownerNamesForUser maps WalletKey → display name for both kinds of owner.
func (s *Server) ownerNamesForUser(userID string) map[string]string {
	out := map[string]string{}
	if s.sessions != nil {
		for _, sess := range s.sessions.ListForUser(userID) {
			out[fmt.Sprintf("char:%d", sess.CharacterID)] = sess.CharacterName
		}
	}
	for _, cd := range s.corpDivisionsForUser(userID) {
		out[fmt.Sprintf("corp:%d:%d", cd.CorporationID, cd.Division)] =
			fmt.Sprintf("%s (%d)", s.corporationName(cd.CorporationID), cd.Division)
	}
	return out
}

// stampOwnerNames fills OwnerName in place for rows that carry a WalletKey.
func stampOwnerNames(txns []esi.WalletTransaction, names map[string]string) {
	for i := range txns {
		if txns[i].WalletKey == "" || txns[i].OwnerName != "" {
			continue
		}
		if name, ok := names[txns[i].WalletKey]; ok {
			txns[i].OwnerName = name
		}
	}
}

// handleAuthOwners lists the owners the scope picker may offer. Corporations
// come from the wallet archive rather than from a roles query: a corp wallet
// that has been archived is one the user can read, and one with no rows would
// have nothing to show anyway.
func (s *Server) handleAuthOwners(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if s.sessions == nil || s.sessions.GetForUser(userID) == nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	type ownerCharacter struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	type ownerCorporation struct {
		CorporationID   int64  `json:"corporation_id"`
		CorporationName string `json:"corporation_name"`
		Divisions       []int  `json:"divisions"`
	}

	characters := []ownerCharacter{}
	for _, sess := range s.sessions.ListForUser(userID) {
		characters = append(characters, ownerCharacter{ID: sess.CharacterID, Name: sess.CharacterName})
	}

	byCorp := map[int64][]int{}
	order := []int64{}
	for _, cd := range s.corpDivisionsForUser(userID) {
		if _, seen := byCorp[cd.CorporationID]; !seen {
			order = append(order, cd.CorporationID)
		}
		byCorp[cd.CorporationID] = append(byCorp[cd.CorporationID], cd.Division)
	}
	corporations := []ownerCorporation{}
	for _, corpID := range order {
		corporations = append(corporations, ownerCorporation{
			CorporationID:   corpID,
			CorporationName: s.corporationName(corpID),
			Divisions:       byCorp[corpID],
		})
	}

	writeJSON(w, map[string]any{
		"characters":   characters,
		"corporations": corporations,
	})
}
