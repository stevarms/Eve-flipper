package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"eve-flipper/internal/config"
	"eve-flipper/internal/db"
)

// stockpileScanRow is one line in the scan response — user's threshold, what
// they actually have, and the shortfall the multibuy pill uses.
type stockpileScanRow struct {
	TypeID       int32  `json:"type_id"`
	TypeName     string `json:"type_name"`
	ThresholdQty int64  `json:"threshold_qty"`
	CurrentQty   int64  `json:"current_qty"`
	Shortfall    int64  `json:"shortfall"`
}

type stockpileScanResponse struct {
	StockpileID int64              `json:"stockpile_id"`
	StationID   int64              `json:"station_id"`
	StationName string             `json:"station_name,omitempty"`
	Items       []stockpileScanRow `json:"items"`
	Warnings    []string           `json:"warnings,omitempty"`
}

type resolveNameQty struct {
	Name string `json:"name"`
	Qty  int64  `json:"qty"`
}

type resolveResponseItem struct {
	Name       string `json:"name"`
	TypeID     int32  `json:"type_id,omitempty"`
	TypeName   string `json:"type_name,omitempty"`
	Qty        int64  `json:"qty"`
	Unresolved bool   `json:"unresolved,omitempty"`
}

type resolveResponse struct {
	Items    []resolveResponseItem `json:"items"`
	Warnings []string              `json:"warnings,omitempty"`
}

// corp roles that ESI accepts for /corporations/{id}/assets/ access.
var stockpileCorpAssetRoles = map[string]struct{}{
	"director":          {},
	"accountant":        {},
	"junior_accountant": {},
	"trader":            {},
	"auditor":           {},
}

// hasStockpileCorpAssetRole returns true if any of the character's granted
// roles matches the ESI-accepted role set.
func hasStockpileCorpAssetRole(roles []string) bool {
	for _, r := range roles {
		if _, ok := stockpileCorpAssetRoles[strings.ToLower(r)]; ok {
			return true
		}
	}
	return false
}

func parseStockpileID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.PathValue("id"))
	if raw == "" {
		return 0, errors.New("missing stockpile id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid stockpile id")
	}
	return id, nil
}

func writeStockpileError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrStockpileNotFound) {
		writeError(w, http.StatusNotFound, "stockpile not found")
		return
	}
	if errors.Is(err, db.ErrStockpileNameConflict) {
		writeError(w, http.StatusConflict, "a stockpile with that name already exists")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// resolveStationLabel resolves a station or structure name for display.
// For player structures the caller passes an access token; if none is
// available, or ESI 403s, StructureName's own fallback returns "Structure {id}".
func (s *Server) resolveStationLabel(id int64, accessToken string) string {
	if isPlayerStructure(id) {
		return s.esi.StructureName(id, accessToken)
	}
	return s.esi.StationName(id)
}

// handleListStockpiles returns all stockpiles (headers only) for the user.
// GET /api/auth/stockpiles
func (s *Server) handleListStockpiles(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	list, err := s.db.ListStockpilesForUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list stockpiles: "+err.Error())
		return
	}
	// Best-effort attach station names — quick lookup, no ESI hit for NPC stations.
	for i := range list {
		list[i].StationName = s.resolveStationLabel(list[i].StationID, "")
	}
	writeJSON(w, map[string]interface{}{"stockpiles": list})
}

// handleCreateStockpile creates an empty stockpile header.
// POST /api/auth/stockpiles
func (s *Server) handleCreateStockpile(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	var body config.Stockpile
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	created, err := s.db.CreateStockpileForUser(userID, body)
	if err != nil {
		writeStockpileError(w, err)
		return
	}
	created.StationName = s.resolveStationLabel(created.StationID, "")
	writeJSONStatus(w, http.StatusCreated, created)
}

// handleGetStockpile returns one stockpile including its items.
// GET /api/auth/stockpiles/{id}
func (s *Server) handleGetStockpile(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sp, err := s.db.GetStockpileForUser(userID, id)
	if err != nil {
		writeStockpileError(w, err)
		return
	}
	sp.StationName = s.resolveStationLabel(sp.StationID, "")
	writeJSON(w, sp)
}

// handleUpdateStockpile patches header fields.
// PATCH /api/auth/stockpiles/{id}
func (s *Server) handleUpdateStockpile(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body config.Stockpile
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	updated, err := s.db.UpdateStockpileForUser(userID, id, body)
	if err != nil {
		writeStockpileError(w, err)
		return
	}
	updated.StationName = s.resolveStationLabel(updated.StationID, "")
	writeJSON(w, updated)
}

// handleDeleteStockpile removes a stockpile and its items.
// DELETE /api/auth/stockpiles/{id}
func (s *Server) handleDeleteStockpile(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.DeleteStockpileForUser(userID, id); err != nil {
		writeStockpileError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleUpsertStockpileItems merges the given items into the stockpile's list.
// POST /api/auth/stockpiles/{id}/items
func (s *Server) handleUpsertStockpileItems(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Items []config.StockpileItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.db.UpsertStockpileItemsForUser(userID, id, body.Items); err != nil {
		writeStockpileError(w, err)
		return
	}
	sp, err := s.db.GetStockpileForUser(userID, id)
	if err != nil {
		writeStockpileError(w, err)
		return
	}
	writeJSON(w, sp)
}

// handleReplaceStockpileItems wipes and replaces the stockpile's item list.
// PUT /api/auth/stockpiles/{id}/items
func (s *Server) handleReplaceStockpileItems(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Items []config.StockpileItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.db.ReplaceStockpileItemsForUser(userID, id, body.Items); err != nil {
		writeStockpileError(w, err)
		return
	}
	sp, err := s.db.GetStockpileForUser(userID, id)
	if err != nil {
		writeStockpileError(w, err)
		return
	}
	writeJSON(w, sp)
}

// handleDeleteStockpileItem removes one row from a stockpile.
// DELETE /api/auth/stockpiles/{id}/items/{typeID}
func (s *Server) handleDeleteStockpileItem(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	typeIDStr := strings.TrimSpace(r.PathValue("typeID"))
	typeID64, parseErr := strconv.ParseInt(typeIDStr, 10, 32)
	if parseErr != nil || typeID64 <= 0 {
		writeError(w, http.StatusBadRequest, "invalid typeID")
		return
	}
	if err := s.db.DeleteStockpileItemForUser(userID, id, int32(typeID64)); err != nil {
		writeStockpileError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleResolveStockpileNames turns a paste-parsed [{name, qty}] list into
// {type_id, type_name, threshold_qty} rows for the caller to POST back into
// /items. Names that don't resolve come back with unresolved=true and are
// echoed in warnings[] so the UI can show what got dropped.
//
// POST /api/auth/stockpiles/resolve
func (s *Server) handleResolveStockpileNames(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireIndustryAuthUser(w, r); !ok {
		return
	}
	if !s.isReady() {
		writeError(w, http.StatusServiceUnavailable, "SDE still loading")
		return
	}

	var body struct {
		Items []resolveNameQty `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil {
		writeError(w, http.StatusServiceUnavailable, "SDE still loading")
		return
	}

	out := make([]resolveResponseItem, 0, len(body.Items))
	var warnings []string
	for _, in := range body.Items {
		key := strings.ToLower(strings.TrimSpace(in.Name))
		if key == "" {
			continue
		}
		item := resolveResponseItem{Name: in.Name, Qty: in.Qty}
		typeID, ok := sdeData.TypeByName[key]
		if !ok {
			item.Unresolved = true
			warnings = append(warnings, "could not resolve item name: "+in.Name)
			out = append(out, item)
			continue
		}
		itemType, ok := sdeData.Types[typeID]
		if !ok {
			item.Unresolved = true
			warnings = append(warnings, "typeID "+strconv.FormatInt(int64(typeID), 10)+" missing from SDE")
			out = append(out, item)
			continue
		}
		item.TypeID = typeID
		item.TypeName = itemType.Name
		out = append(out, item)
	}
	writeJSON(w, resolveResponse{Items: out, Warnings: warnings})
}

// handleScanStockpile runs the current stockpile against ESI assets and
// returns the threshold/current/shortfall rows.
//
// POST /api/auth/stockpiles/{id}/scan
func (s *Server) handleScanStockpile(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	id, err := parseStockpileID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sp, err := s.db.GetStockpileForUser(userID, id)
	if err != nil {
		writeStockpileError(w, err)
		return
	}

	rollup, scanToken, warnings := s.gatherStockpileRollup(userID, sp)

	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()

	items := make([]stockpileScanRow, 0, len(sp.Items))
	for _, it := range sp.Items {
		current := rollup[it.TypeID]
		name := it.TypeName
		if sdeData != nil {
			if t, ok := sdeData.Types[it.TypeID]; ok && t.Name != "" {
				name = t.Name
			}
		}
		row := stockpileScanRow{
			TypeID:       it.TypeID,
			TypeName:     name,
			ThresholdQty: it.ThresholdQty,
			CurrentQty:   current,
		}
		if it.ThresholdQty > current {
			row.Shortfall = it.ThresholdQty - current
		}
		items = append(items, row)
	}

	resp := stockpileScanResponse{
		StockpileID: sp.ID,
		StationID:   sp.StationID,
		StationName: s.resolveStationLabel(sp.StationID, scanToken),
		Items:       items,
		Warnings:    warnings,
	}
	writeJSON(w, resp)
}

// gatherStockpileRollup pulls the right asset endpoint for the stockpile,
// walks the tree at its station, and returns (totals, an access token used for
// structure name lookup, warnings for the UI). On any recoverable failure
// (missing session, missing role, ESI 403) it returns an empty map with
// warnings — never an HTTP error — so the scan endpoint always renders.
func (s *Server) gatherStockpileRollup(userID string, sp *config.Stockpile) (map[int32]int64, string, []string) {
	var warnings []string
	if s.sessions == nil || s.esi == nil || s.sso == nil {
		return map[int32]int64{}, "", []string{"SSO not configured"}
	}

	switch sp.Source {
	case config.StockpileSourceCharacter:
		if sp.SourceCharacterID <= 0 {
			return map[int32]int64{}, "", []string{"stockpile has no source_character_id"}
		}
		sess := s.sessions.GetByCharacterIDForUser(userID, sp.SourceCharacterID)
		if sess == nil {
			return map[int32]int64{}, "", []string{"source character no longer authenticated — re-add or switch source"}
		}
		token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
		if err != nil {
			return map[int32]int64{}, "", []string{"could not refresh token for " + sess.CharacterName + ": " + err.Error()}
		}
		assets, err := s.esi.GetCharacterAssets(sess.CharacterID, token)
		if err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "403") || strings.Contains(msg, "scope") {
				return map[int32]int64{}, token, []string{"missing esi-assets.read_assets.v1 scope for " + sess.CharacterName + " (re-authenticate)"}
			}
			log.Printf("[STOCKPILE] character assets fetch (%s): %v", sess.CharacterName, err)
			return map[int32]int64{}, token, []string{"could not fetch character assets: " + err.Error()}
		}
		return rollUpAtStation(stockpileAssetsFromCharacter(assets), sp.StationID), token, warnings

	case config.StockpileSourceCorporation:
		if sp.SourceCorporationID <= 0 {
			return map[int32]int64{}, "", []string{"stockpile has no source_corporation_id"}
		}
		sessions := s.sessions.ListForUser(userID)
		var (
			chosenSess    *authSessionRef
			chosenToken   string
			roleWarned    bool
			corpMemberSaw bool
		)
		for _, sess := range sessions {
			corpID, err := s.esi.GetCharacterCorporationID(sess.CharacterID)
			if err != nil || int64(corpID) != sp.SourceCorporationID {
				continue
			}
			corpMemberSaw = true
			token, tokenErr := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
			if tokenErr != nil {
				continue
			}
			roles, rolesErr := s.esi.GetCharacterRoles(sess.CharacterID, token)
			if rolesErr != nil {
				msg := strings.ToLower(rolesErr.Error())
				if strings.Contains(msg, "403") || strings.Contains(msg, "scope") {
					if !roleWarned {
						warnings = append(warnings, "missing esi-characters.read_corporation_roles.v1 scope (re-authenticate)")
						roleWarned = true
					}
					continue
				}
				log.Printf("[STOCKPILE] roles fetch (%s): %v", sess.CharacterName, rolesErr)
				continue
			}
			if roles == nil || !hasStockpileCorpAssetRole(roles.Roles) {
				continue
			}
			chosenSess = &authSessionRef{
				CharacterID:   sess.CharacterID,
				CharacterName: sess.CharacterName,
			}
			chosenToken = token
			break
		}
		if chosenSess == nil {
			if !corpMemberSaw {
				warnings = append(warnings, "no authenticated character belongs to the target corporation")
			} else {
				warnings = append(warnings, "no authenticated character in this corp holds a role that allows reading corp assets (Director, Accountant, Junior_Accountant, Trader, or Auditor)")
			}
			return map[int32]int64{}, "", warnings
		}
		corpAssets, err := s.esi.GetCorporationAssets(int32(sp.SourceCorporationID), chosenToken)
		if err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "403") || strings.Contains(msg, "scope") {
				warnings = append(warnings, "missing esi-assets.read_corporation_assets.v1 scope for "+chosenSess.CharacterName+" (re-authenticate)")
				return map[int32]int64{}, chosenToken, warnings
			}
			log.Printf("[STOCKPILE] corp assets fetch (corp %d via %s): %v", sp.SourceCorporationID, chosenSess.CharacterName, err)
			warnings = append(warnings, "could not fetch corporation assets: "+err.Error())
			return map[int32]int64{}, chosenToken, warnings
		}
		return rollUpAtStation(stockpileAssetsFromCorporation(corpAssets), sp.StationID), chosenToken, warnings
	}
	return map[int32]int64{}, "", []string{"unknown stockpile source: " + sp.Source}
}

// authSessionRef is a minimal projection of an auth.Session used inside
// gatherStockpileRollup. We avoid importing auth here to keep the file
// self-contained.
type authSessionRef struct {
	CharacterID   int64
	CharacterName string
}
