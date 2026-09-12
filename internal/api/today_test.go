package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"eve-flipper/internal/config"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

func newTodayTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(config.Default(), &esi.Client{}, nil, nil, nil)
}

// This app is local-first: userIDFromRequest always resolves to a user (the
// default one when no cookie is present), so "no user" is not a state these
// endpoints can be in. Reading the plan and marking an action done therefore
// need no EVE session at all — both work against the local store. Only the
// refresh does, because only the refresh calls ESI.

// With no stored plan the endpoint still answers, and it says the plan is
// stale rather than returning an empty object the frontend has to interpret.
func TestHandleAuthTodayWithNoStoredPlanReportsStale(t *testing.T) {
	srv := newTodayTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/today", nil)
	addSignedUserCookie(req, srv, "u1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env todayPlanEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Plan != nil {
		t.Fatal("plan is non-nil with no stored plan")
	}
	if !env.Stale {
		t.Fatal("stale = false with no stored plan")
	}
	if env.StaleAfterSeconds <= 0 {
		t.Fatal("stale_after_seconds not reported, so the frontend has to hard-code its own copy")
	}
}

func TestHandleAuthTodayStateValidatesInput(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"not json", `nonsense`, http.StatusBadRequest},
		{"no action id", `{"mode":"done"}`, http.StatusBadRequest},
		{"blank action id", `{"action_id":"  ","mode":"done"}`, http.StatusBadRequest},
		{"unknown mode", `{"action_id":"buy:1:2:0","mode":"maybe"}`, http.StatusBadRequest},
		{"done", `{"action_id":"buy:1:2:0","mode":"done"}`, http.StatusOK},
		{"skip", `{"action_id":"buy:1:2:0","mode":"skip"}`, http.StatusOK},
		{"clear", `{"action_id":"buy:1:2:0","mode":"clear"}`, http.StatusOK},
		{"mode is case insensitive", `{"action_id":"buy:1:2:0","mode":"DONE"}`, http.StatusOK},
	}

	srv := newTodayTestServer(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/today/state", strings.NewReader(tc.body))
			addSignedUserCookie(req, srv, "u1")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// Refreshing needs a character, because it calls ESI. That has to come back
// as a 401 rather than a 200 with an error line inside the stream: once the
// NDJSON headers are written the status is 200 whatever happens next, and the
// client shows a login prompt for one and a retry for the other.
func TestHandleAuthTodayRefreshRequiresLogin(t *testing.T) {
	srv := newTodayTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/today/refresh", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// The bid, quantity and grade the run panel shows all have to survive the
// round trip through the stored plan, because that is what the page reads on
// every load. A field that only exists on the refresh response would appear
// once and then vanish.
func TestTodayPlanSurvivesJSONRoundTrip(t *testing.T) {
	plan := engine.TodayPlan{
		GeneratedAt: "2026-09-11T12:00:00Z",
		Actions: []engine.TodayAction{{
			ID:            "reprice:5:60003760:900",
			Kind:          engine.TodayActionReprice,
			TypeID:        5,
			TypeName:      "Zeugma Integrated Analyzer",
			PastePrice:    1237000,
			Quantity:      412,
			Grade:         engine.TodayGradeProven,
			DownsideISK7d: 70000,
			ExpectedISK7d: 95000,
			Reliability: engine.TodayReliability{
				Grade:    engine.TodayGradeProven,
				Evidence: "41 real sales of this have made you 12.0M",
			},
		}},
	}

	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back engine.TodayPlan
	if err := json.Unmarshal(payload, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(back.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(back.Actions))
	}
	a := back.Actions[0]
	if a.PastePrice != 1237000 {
		t.Errorf("paste_price = %v, want 1237000", a.PastePrice)
	}
	if a.Quantity != 412 {
		t.Errorf("quantity = %v, want 412", a.Quantity)
	}
	if a.Grade != engine.TodayGradeProven {
		t.Errorf("grade = %q, want proven", a.Grade)
	}
	if a.Reliability.Evidence == "" {
		t.Error("reliability evidence lost in the round trip")
	}
	if a.DownsideISK7d == 0 {
		t.Error("downside lost in the round trip; the queue would rank on nothing")
	}
}
