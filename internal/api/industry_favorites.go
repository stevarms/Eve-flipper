package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"eve-flipper/internal/db"
)

// handleAuthListIndustryFavorites returns the user's starred scanner rows.
func (s *Server) handleAuthListIndustryFavorites(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	if s.db == nil {
		writeError(w, 503, "database unavailable")
		return
	}
	writeJSON(w, s.db.GetIndustryFavoritesForUser(userID))
}

// handleAuthAddIndustryFavorite stars one scanner row. Idempotent: starring
// an already-starred row refreshes its names and returns 200, so a stale
// client view can't produce a spurious error.
func (s *Server) handleAuthAddIndustryFavorite(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	if s.db == nil {
		writeError(w, 503, "database unavailable")
		return
	}

	var fav db.IndustryBlueprintFavorite
	if err := json.NewDecoder(r.Body).Decode(&fav); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if fav.BlueprintTypeID <= 0 || fav.ProductTypeID <= 0 {
		writeError(w, 400, "blueprint_type_id and product_type_id are required")
		return
	}
	if err := s.db.AddIndustryFavoriteForUser(userID, fav); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, s.db.GetIndustryFavoritesForUser(userID))
}

// handleAuthDeleteIndustryFavorite unstars one scanner row. The row key
// arrives as query params rather than a body because DELETE bodies are not
// reliably forwarded by every proxy the hosted build sits behind.
func (s *Server) handleAuthDeleteIndustryFavorite(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	if s.db == nil {
		writeError(w, 503, "database unavailable")
		return
	}

	q := r.URL.Query()
	blueprintTypeID, _ := strconv.ParseInt(q.Get("blueprint_type_id"), 10, 32)
	productTypeID, _ := strconv.ParseInt(q.Get("product_type_id"), 10, 32)
	if blueprintTypeID <= 0 || productTypeID <= 0 {
		writeError(w, 400, "blueprint_type_id and product_type_id are required")
		return
	}
	if err := s.db.DeleteIndustryFavoriteForUser(
		userID,
		int32(blueprintTypeID),
		int32(productTypeID),
		q.Get("scan_mode"),
	); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, s.db.GetIndustryFavoritesForUser(userID))
}
