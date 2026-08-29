package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"eve-flipper/internal/db"
)

// handleAuthResplitIndustryProjectJobs re-cuts a committed project's
// outstanding jobs under a new scheduler configuration.
//
// Changing the scheduler knobs used to affect only the *next* plan commit,
// which left no way to act on "these jobs are the wrong size" without wiping
// and re-committing the whole project. This endpoint applies the settings in
// place: planned and queued jobs are re-split, and anything the user has
// acted on — active, paused, completed, failed, cancelled — is preserved,
// with its runs deducted from what still needs planning.
func (s *Server) handleAuthResplitIndustryProjectJobs(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	if s.db == nil {
		writeError(w, 503, "database unavailable")
		return
	}

	projectID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("projectID")), 10, 64)
	if err != nil || projectID <= 0 {
		writeError(w, 400, "invalid project id")
		return
	}

	var req db.IndustryPlanSchedulerInput
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, 400, "invalid json")
			return
		}
	}

	summary, err := s.db.ResplitIndustryProjectJobsForUser(userID, projectID, req)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "project not found")
			return
		}
		writeError(w, 500, err.Error())
		return
	}

	writeJSON(w, map[string]interface{}{"ok": true, "summary": summary})
}
