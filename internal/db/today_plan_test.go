package db

import (
	"testing"
	"time"
)

func TestTodayPlanRoundTrip(t *testing.T) {
	d := openTestDB(t)

	if _, _, ok := d.GetTodayPlan("u1"); ok {
		t.Fatal("a plan came back before one was saved")
	}

	if err := d.SaveTodayPlan("u1", "2026-09-11T12:00:00Z", `{"actions":[]}`); err != nil {
		t.Fatalf("save: %v", err)
	}
	payload, generatedAt, ok := d.GetTodayPlan("u1")
	if !ok {
		t.Fatal("saved plan not found")
	}
	if payload != `{"actions":[]}` || generatedAt != "2026-09-11T12:00:00Z" {
		t.Fatalf("payload=%q generatedAt=%q", payload, generatedAt)
	}

	// One row per user: a refresh replaces, never accumulates.
	if err := d.SaveTodayPlan("u1", "2026-09-11T18:00:00Z", `{"actions":[1]}`); err != nil {
		t.Fatalf("replace: %v", err)
	}
	payload, generatedAt, _ = d.GetTodayPlan("u1")
	if payload != `{"actions":[1]}` || generatedAt != "2026-09-11T18:00:00Z" {
		t.Fatalf("replace did not take: payload=%q generatedAt=%q", payload, generatedAt)
	}

	// Plans are per user.
	if _, _, ok := d.GetTodayPlan("u2"); ok {
		t.Fatal("u2 can see u1's plan")
	}
}

func TestTodayActionStateRecordsWhatWasPromised(t *testing.T) {
	d := openTestDB(t)

	want := TodayActionState{
		ActionID:     "buy:34:60003760:0",
		Mode:         "done",
		Kind:         "buy",
		TypeID:       34,
		Grade:        "proven",
		ProjectedISK: 2_400_000,
		Quantity:     12000,
		Price:        1013.4,
		ActedAt:      "2026-09-11T12:30:00Z",
	}
	if err := d.SetTodayActionState("u1", want); err != nil {
		t.Fatalf("set: %v", err)
	}

	got := d.GetTodayActionStates("u1", "")
	st, ok := got[want.ActionID]
	if !ok {
		t.Fatal("state not stored")
	}
	// The projection is the whole reason this row exists: it is the only
	// record of what the plan promised at the moment of acting, and the plan
	// itself is replaced on the next refresh.
	if st.ProjectedISK != want.ProjectedISK || st.Grade != want.Grade ||
		st.Quantity != want.Quantity || st.Price != want.Price || st.Kind != want.Kind {
		t.Fatalf("projection not preserved: %+v", st)
	}
}

// Action ids are deterministic, so an order repriced yesterday produces the
// same id in today's plan. Marks therefore have to be filtered by when they
// were made, or the work would arrive already crossed out.
func TestGetTodayActionStatesFiltersByWhenTheMarkWasMade(t *testing.T) {
	d := openTestDB(t)

	yesterday := "2026-09-10T09:00:00Z"
	today := "2026-09-11T09:00:00Z"
	planBuiltAt := "2026-09-11T08:00:00Z"

	mustSet := func(id, actedAt string) {
		t.Helper()
		if err := d.SetTodayActionState("u1", TodayActionState{
			ActionID: id, Mode: "done", ActedAt: actedAt,
		}); err != nil {
			t.Fatalf("set %s: %v", id, err)
		}
	}
	mustSet("reprice:5:60003760:900", yesterday)
	mustSet("buy:34:60003760:0", today)

	current := d.GetTodayActionStates("u1", planBuiltAt)
	if _, ok := current["reprice:5:60003760:900"]; ok {
		t.Error("yesterday's mark applied to a plan built this morning")
	}
	if _, ok := current["buy:34:60003760:0"]; !ok {
		t.Error("today's mark was filtered out")
	}

	// The old row is still there, though — it is the input to grading Today
	// against its own record, and sweeping it would destroy that.
	all := d.GetTodayActionStates("u1", "")
	if len(all) != 2 {
		t.Fatalf("all states = %d, want 2; filtering must not delete", len(all))
	}
}

func TestSetTodayActionStateClearRemovesTheMark(t *testing.T) {
	d := openTestDB(t)

	id := "buy:34:60003760:0"
	if err := d.SetTodayActionState("u1", TodayActionState{
		ActionID: id, Mode: "done", ActedAt: "2026-09-11T09:00:00Z",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := d.SetTodayActionState("u1", TodayActionState{ActionID: id, Mode: "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := d.GetTodayActionStates("u1", "")[id]; ok {
		t.Fatal("clear left the mark in place, so an accidental keypress cannot be undone")
	}
}

func TestSetTodayActionStateDefaultsActedAt(t *testing.T) {
	d := openTestDB(t)

	if err := d.SetTodayActionState("u1", TodayActionState{
		ActionID: "buy:34:60003760:0", Mode: "done",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	st := d.GetTodayActionStates("u1", "")["buy:34:60003760:0"]
	if st.ActedAt == "" {
		t.Fatal("acted_at left empty; the mark would never expire out of a future plan")
	}
	if _, err := time.Parse(time.RFC3339, st.ActedAt); err != nil {
		t.Fatalf("acted_at %q is not RFC3339: %v", st.ActedAt, err)
	}
}

func TestPruneTodayActionStates(t *testing.T) {
	d := openTestDB(t)

	mustSet := func(id, actedAt string) {
		t.Helper()
		if err := d.SetTodayActionState("u1", TodayActionState{
			ActionID: id, Mode: "done", ActedAt: actedAt,
		}); err != nil {
			t.Fatalf("set %s: %v", id, err)
		}
	}
	mustSet("old", "2026-06-01T00:00:00Z")
	mustSet("recent", "2026-09-10T00:00:00Z")

	if err := d.PruneTodayActionStates("u1", "2026-08-01T00:00:00Z"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	states := d.GetTodayActionStates("u1", "")
	if _, ok := states["old"]; ok {
		t.Error("old mark survived the prune")
	}
	if _, ok := states["recent"]; !ok {
		t.Error("prune removed a mark inside the window")
	}
}
