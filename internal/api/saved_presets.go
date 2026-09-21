package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"eve-flipper/internal/db"
)

// saved_presets.go — CRUD for the scan-parameter presets a user builds up on
// top of the built-in ones (PresetPicker.tsx), server-side so they follow
// your login. Unlike cockpit's loadouts, presets need no EVE character --
// userIDFromRequest, not requireIndustryAuthUser -- matching config and
// watchlist, so PI Factory-style usage in the desktop build's single-user
// mode keeps working for someone who never logs in.

const savedPresetPayloadLimit = 64 * 1024

type savedPresetPayload struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Tab       string          `json:"tab"`
	Params    json.RawMessage `json:"params"`
	Active    bool            `json:"active"`
	CreatedAt string          `json:"created_at,omitempty"`
	UpdatedAt string          `json:"updated_at,omitempty"`
}

func savedPresetFromDB(row db.SavedPreset) savedPresetPayload {
	params := json.RawMessage(row.PayloadJSON)
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	return savedPresetPayload{
		ID:        row.PresetID,
		Name:      row.Name,
		Tab:       row.Tab,
		Params:    params,
		Active:    row.Active,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func savedPresetsFromDB(rows []db.SavedPreset) []savedPresetPayload {
	out := make([]savedPresetPayload, 0, len(rows))
	for _, row := range rows {
		out = append(out, savedPresetFromDB(row))
	}
	return out
}

func (s *Server) handleGetSavedPresets(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, map[string]any{"presets": []savedPresetPayload{}})
		return
	}
	rows, err := s.db.ListSavedPresetsForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load presets")
		return
	}
	writeJSON(w, map[string]any{"presets": savedPresetsFromDB(rows)})
}

func (s *Server) handleCreateSavedPreset(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var body struct {
		Tab      string          `json:"tab"`
		Name     string          `json:"name"`
		Params   json.RawMessage `json:"params"`
		Activate *bool           `json:"activate"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, savedPresetPayloadLimit))
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(body.Tab) == "" {
		writeError(w, http.StatusBadRequest, "tab is required")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	params := body.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	// Saving new params applies them immediately in the picker, which is why
	// this defaults to true unlike an update's pointer (nil there means
	// "leave activation alone", not "activate").
	activate := true
	if body.Activate != nil {
		activate = *body.Activate
	}
	userID := userIDFromRequest(r)
	row, err := s.db.CreateSavedPresetForUser(userID, body.Tab, body.Name, string(params), activate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create preset")
		return
	}
	rows, err := s.db.ListSavedPresetsForUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load presets")
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"preset":  savedPresetFromDB(row),
		"presets": savedPresetsFromDB(rows),
	})
}

func (s *Server) handleUpdateSavedPreset(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	presetID := r.PathValue("presetID")

	// Pointers so a rename doesn't clobber params and vice versa -- the
	// picker's rename and "update active preset" actions are separate
	// buttons that each only mean to touch the one field they show.
	var body struct {
		Name     *string          `json:"name"`
		Params   *json.RawMessage `json:"params"`
		Activate *bool            `json:"activate"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, savedPresetPayloadLimit))
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	var paramsJSON *string
	if body.Params != nil {
		v := string(*body.Params)
		paramsJSON = &v
	}
	userID := userIDFromRequest(r)
	row, err := s.db.UpdateSavedPresetForUser(userID, presetID, body.Name, paramsJSON, body.Activate)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update preset")
		return
	}
	rows, err := s.db.ListSavedPresetsForUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load presets")
		return
	}
	writeJSON(w, map[string]any{
		"preset":  savedPresetFromDB(row),
		"presets": savedPresetsFromDB(rows),
	})
}

func (s *Server) handleActivateSavedPreset(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	userID := userIDFromRequest(r)
	row, err := s.db.ActivateSavedPresetForUser(userID, r.PathValue("presetID"))
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to activate preset")
		return
	}
	rows, err := s.db.ListSavedPresetsForUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load presets")
		return
	}
	writeJSON(w, map[string]any{
		"preset":  savedPresetFromDB(row),
		"presets": savedPresetsFromDB(rows),
	})
}

func (s *Server) handleDeleteSavedPreset(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	userID := userIDFromRequest(r)
	rows, err := s.db.DeleteSavedPresetForUser(userID, r.PathValue("presetID"))
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "preset not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete preset")
		return
	}
	writeJSON(w, map[string]any{"presets": savedPresetsFromDB(rows)})
}
