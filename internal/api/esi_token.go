package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleAuthESIToken exposes the currently-selected character's ESI access
// token to the browser so it can call CCP's esi-ui.* endpoints directly.
//
// Why the browser needs the token: CCP's UI endpoints (open-market,
// set-waypoint, open-contract) only deliver the command to the running
// game client when the ESI call originates from the same public IP as
// the client. Server-side calls from a remote Docker container return
// 204 but silently drop delivery. Letting the browser make the call
// itself (from the user's IP, same as their game) is the standard fix
// used by Eve Tycoon / jita.space / etc.
//
// Refresh handling stays server-side — we return an already-refreshed
// access token, and the browser calls this endpoint again when it gets
// a 401 or when the local `expires_at` is close.
//
// Query params:
//   character_id — optional; when omitted, returns the user's active
//                  character's token (matches GetForUser semantics).
//
// Response body:
//   {
//     "access_token": "...",
//     "expires_at":   "2026-08-29T15:22:00Z",  // ISO 8601 UTC
//     "character_id": 1234567890,
//     "character_name": "Steve"
//   }
//
// Security tradeoff (documented in the SecurityVaultModal opt-out):
// exposing the access token to the browser lets an XSS-injected script
// exfiltrate it and use it for the ~20 minutes until it expires. The
// refresh token stays server-side, so the damage window is bounded.
// Users who need to close this hole can toggle "Don't expose ESI tokens
// to my browser" in the vault modal — the frontend then falls back to
// the server-side POST path which works locally but not for remote
// deployments (see the CCP IP-delivery constraint).
func (s *Server) handleAuthESIToken(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil || s.sso == nil {
		http.Error(w, `{"error":"not_logged_in"}`, http.StatusUnauthorized)
		return
	}
	userID := userIDFromRequest(r)

	// Optional ?character_id=N narrows to a specific alt. Empty = the
	// user's active session.
	var sess *authSessionLike
	if raw := strings.TrimSpace(r.URL.Query().Get("character_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, `{"error":"invalid_character_id"}`, http.StatusBadRequest)
			return
		}
		full := s.sessions.GetByCharacterIDForUser(userID, id)
		if full == nil {
			http.Error(w, `{"error":"character_not_logged_in"}`, http.StatusUnauthorized)
			return
		}
		sess = &authSessionLike{
			CharacterID:   full.CharacterID,
			CharacterName: full.CharacterName,
			ExpiresAt:     full.ExpiresAt,
		}
	} else {
		full := s.sessions.GetForUser(userID)
		if full == nil {
			http.Error(w, `{"error":"not_logged_in"}`, http.StatusUnauthorized)
			return
		}
		sess = &authSessionLike{
			CharacterID:   full.CharacterID,
			CharacterName: full.CharacterName,
			ExpiresAt:     full.ExpiresAt,
		}
	}

	// Refresh if near expiry (EnsureValidTokenForUserCharacter handles
	// the "already valid" fast path internally).
	token, err := s.sessions.EnsureValidTokenForUserCharacter(s.sso, userID, sess.CharacterID)
	if err != nil {
		http.Error(w, `{"error":"refresh_failed"}`, http.StatusUnauthorized)
		return
	}
	// After refresh, re-read the ExpiresAt so the browser knows how long
	// it can safely reuse this token.
	refreshed := s.sessions.GetByCharacterIDForUser(userID, sess.CharacterID)
	expiresAt := sess.ExpiresAt
	if refreshed != nil {
		expiresAt = refreshed.ExpiresAt
	}
	// Guard: if the token would already be expired, respond 401 so the
	// browser doesn't waste an ESI call on a dead token.
	if !expiresAt.IsZero() && time.Until(expiresAt) < 5*time.Second {
		http.Error(w, `{"error":"token_expired"}`, http.StatusUnauthorized)
		return
	}

	body := map[string]any{
		"access_token":   token,
		"expires_at":     expiresAt.UTC().Format(time.RFC3339),
		"character_id":   sess.CharacterID,
		"character_name": sess.CharacterName,
	}
	w.Header().Set("Content-Type", "application/json")
	// Never cache this response — token freshness is critical.
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}

// authSessionLike lets this file avoid a hard import of the auth package
// for the two fields it actually reads. The session store methods it
// calls (`GetForUser`, `GetByCharacterIDForUser`,
// `EnsureValidTokenForUserCharacter`) already exist on the Server struct.
type authSessionLike struct {
	CharacterID   int64
	CharacterName string
	ExpiresAt     time.Time
}
