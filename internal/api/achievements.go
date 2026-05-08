package api

import (
	"encoding/json"
	"net/http"

	"eve-flipper/internal/db"
)

type achievementPatchRequest struct {
	Patches []db.AchievementProgressPatch `json:"patches"`
}

type achievementSeenRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleAuthListAchievements(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if s.db == nil {
		writeJSON(w, map[string]interface{}{
			"states": []db.AchievementState{},
			"count":  0,
		})
		return
	}

	states, err := s.db.ListAchievementsForUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list achievements")
		return
	}
	if states == nil {
		states = []db.AchievementState{}
	}
	writeJSON(w, map[string]interface{}{
		"states": states,
		"count":  len(states),
	})
}

func (s *Server) handleAuthPatchAchievements(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var req achievementPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	states, unlocked, err := s.db.ApplyAchievementPatchesForUser(userID, req.Patches)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if states == nil {
		states = []db.AchievementState{}
	}
	if unlocked == nil {
		unlocked = []db.AchievementState{}
	}
	writeJSON(w, map[string]interface{}{
		"ok":       true,
		"states":   states,
		"unlocked": unlocked,
	})
}

func (s *Server) handleAuthMarkAchievementsSeen(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var req achievementSeenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	states, err := s.db.MarkAchievementsSeenForUser(userID, req.IDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if states == nil {
		states = []db.AchievementState{}
	}
	writeJSON(w, map[string]interface{}{
		"ok":     true,
		"states": states,
	})
}
