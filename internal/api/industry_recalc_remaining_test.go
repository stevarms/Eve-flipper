package api

import (
	"encoding/json"
	"testing"

	"eve-flipper/internal/db"
	"eve-flipper/internal/sde"
)

// buildRecalcTestSDE returns an SDE with two blueprints:
//   - typeID 100 (Widget Blueprint): manufacturing 1×Widget from 10×Tritanium
//   - typeID 200 (Gadget T1 Blueprint): invention product typeID 210 (Gadget T2
//     BPC), invention materials 2×Datacore
//
// Types map covers all mentioned typeIDs so name lookup succeeds.
func buildRecalcTestSDE() *sde.Data {
	ind := sde.NewIndustryData()
	ind.Blueprints[100] = &sde.Blueprint{
		BlueprintTypeID: 100,
		ProductTypeID:   101,
		ProductQuantity: 1,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {
				Time:      600,
				Materials: []sde.BlueprintMaterial{{TypeID: 34, Quantity: 10}},
				Products:  []sde.BlueprintProduct{{TypeID: 101, Quantity: 1}},
			},
		},
	}
	ind.ProductToBlueprint[101] = 100
	ind.Blueprints[200] = &sde.Blueprint{
		BlueprintTypeID: 200,
		Activities: map[string]*sde.ActivityData{
			"invention": {
				Time:      1000,
				Materials: []sde.BlueprintMaterial{{TypeID: 999, Quantity: 2}},
				Products:  []sde.BlueprintProduct{{TypeID: 210, Quantity: 10, Probability: 0.4}},
			},
		},
	}
	return &sde.Data{
		Types: map[int32]*sde.ItemType{
			34:  {ID: 34, Name: "Tritanium"},
			101: {ID: 101, Name: "Widget"},
			210: {ID: 210, Name: "Gadget T2 Blueprint"},
			999: {ID: 999, Name: "Datacore"},
		},
		Industry: ind,
	}
}

// mustConstraints marshals a JSON constraints blob for a task, panicking on
// malformed inputs — test-only helper.
func mustConstraints(t *testing.T, kv map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(kv)
	if err != nil {
		t.Fatalf("marshal constraints: %v", err)
	}
	return json.RawMessage(b)
}

func TestComputeRecalcRemaining_FiltersByJobStatus(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: 5, Constraints: mustConstraints(t, map[string]interface{}{"me": 0, "blueprint_type_id": 100})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: 5, Status: db.IndustryJobStatusCompleted},
			{ID: 11, TaskID: 1, Activity: "manufacturing", Runs: 5, Status: db.IndustryJobStatusCancelled},
			{ID: 12, TaskID: 1, Activity: "manufacturing", Runs: 5, Status: db.IndustryJobStatusFailed},
		},
	}

	// Default: planned+queued only. All three jobs are terminal → nothing.
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{
		db.IndustryJobStatusPlanned, db.IndustryJobStatusQueued,
	})
	if got.unfinishedJobs != 0 {
		t.Fatalf("unfinishedJobs = %d, want 0 (all jobs terminal)", got.unfinishedJobs)
	}
	if len(got.requiredByType) != 0 {
		t.Fatalf("requiredByType = %v, want empty", got.requiredByType)
	}

	// Add a planned job — it should now contribute.
	snap.Jobs = append(snap.Jobs, db.IndustryJob{ID: 13, TaskID: 1, Activity: "manufacturing", Runs: 5, Status: db.IndustryJobStatusPlanned})
	got = computeRecalcRemainingRequirements(snap, sdeData, []string{
		db.IndustryJobStatusPlanned, db.IndustryJobStatusQueued,
	})
	if got.unfinishedJobs != 1 {
		t.Fatalf("unfinishedJobs = %d, want 1", got.unfinishedJobs)
	}
	if got.requiredByType[34] != 50 { // 5 runs × 10 Trit per run
		t.Fatalf("Trit required = %d, want 50", got.requiredByType[34])
	}
}

func TestComputeRecalcRemaining_IncludeActiveJobsToggle(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: 2, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 100})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: 2, Status: db.IndustryJobStatusActive},
			{ID: 11, TaskID: 1, Activity: "manufacturing", Runs: 2, Status: db.IndustryJobStatusPaused},
		},
	}

	// Default excludes active + paused.
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{
		db.IndustryJobStatusPlanned, db.IndustryJobStatusQueued,
	})
	if got.unfinishedJobs != 0 {
		t.Fatalf("without toggle: unfinishedJobs = %d, want 0", got.unfinishedJobs)
	}

	// Opt in.
	got = computeRecalcRemainingRequirements(snap, sdeData, []string{
		db.IndustryJobStatusPlanned, db.IndustryJobStatusQueued,
		db.IndustryJobStatusActive, db.IndustryJobStatusPaused,
	})
	if got.unfinishedJobs != 2 {
		t.Fatalf("with toggle: unfinishedJobs = %d, want 2", got.unfinishedJobs)
	}
	if got.requiredByType[34] != 40 { // 2 jobs × 2 runs × 10 Trit
		t.Fatalf("Trit required = %d, want 40 (2 jobs × 2 runs × 10)", got.requiredByType[34])
	}
}

func TestComputeRecalcRemaining_MEReductionApplied(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	// ME=10 → 0.90 multiplier → 100 runs × 10 Trit × 0.90 = 900 (exact).
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: 100, Constraints: mustConstraints(t, map[string]interface{}{"me": 10, "blueprint_type_id": 100})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: 100, Status: db.IndustryJobStatusPlanned},
		},
	}
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{db.IndustryJobStatusPlanned})
	if got.requiredByType[34] != 900 {
		t.Fatalf("Trit with ME=10 = %d, want 900", got.requiredByType[34])
	}
}

func TestComputeRecalcRemaining_InventionUsesRawQuantity(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	// Invention: 5 attempts × 2 Datacore/attempt = 10. Constraints point at
	// the T1 source BP (typeID 200); the task's product is the T2 BPC (210).
	// ME on the constraints should NOT reduce datacore counts (invention
	// falls through the default branch in calculateActivityMaterials).
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "invention", ProductTypeID: 210, TargetRuns: 5, Constraints: mustConstraints(t, map[string]interface{}{"me": 5, "blueprint_type_id": 200})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "invention", Runs: 5, Status: db.IndustryJobStatusPlanned},
		},
	}
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{db.IndustryJobStatusPlanned})
	if got.requiredByType[999] != 10 {
		t.Fatalf("Datacore for 5 attempts = %d, want 10 (ME must not apply)", got.requiredByType[999])
	}
	// Sanity: no manufacturing materials should leak in.
	if _, ok := got.requiredByType[34]; ok {
		t.Fatalf("Trit unexpectedly present for invention-only recalc: %v", got.requiredByType)
	}
}

func TestComputeRecalcRemaining_AggregatesAcrossJobs(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	// Two mfg jobs on the same task → aggregate required_qty.
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: 4, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 100})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: 4, Status: db.IndustryJobStatusPlanned},
			{ID: 11, TaskID: 1, Activity: "manufacturing", Runs: 4, Status: db.IndustryJobStatusQueued},
		},
	}
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{
		db.IndustryJobStatusPlanned, db.IndustryJobStatusQueued,
	})
	if got.unfinishedJobs != 2 {
		t.Fatalf("unfinishedJobs = %d, want 2", got.unfinishedJobs)
	}
	if got.requiredByType[34] != 80 { // 2 jobs × 4 runs × 10 Trit
		t.Fatalf("Trit required = %d, want 80", got.requiredByType[34])
	}
	if got.typeNames[34] != "Tritanium" {
		t.Fatalf("Trit name = %q, want Tritanium", got.typeNames[34])
	}
}

func TestComputeRecalcRemaining_UnresolvableTaskOrBlueprintSkipped(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			// task ID 1 → resolves fine
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: 1, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 100})},
			// task ID 2 → product_type_id has no blueprint in the fake SDE
			{ID: 2, Activity: "manufacturing", ProductTypeID: 9999, TargetRuns: 1},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: 1, Status: db.IndustryJobStatusPlanned},
			{ID: 11, TaskID: 999 /* missing task */, Activity: "manufacturing", Runs: 1, Status: db.IndustryJobStatusPlanned},
			{ID: 12, TaskID: 2, Activity: "manufacturing", Runs: 1, Status: db.IndustryJobStatusPlanned},
		},
	}
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{db.IndustryJobStatusPlanned})
	if got.unfinishedJobs != 1 {
		t.Fatalf("unfinishedJobs = %d, want 1 (only task 1's job resolvable)", got.unfinishedJobs)
	}
	if got.skippedJobs != 2 {
		t.Fatalf("skippedJobs = %d, want 2 (missing task + unresolvable BP)", got.skippedJobs)
	}
	if got.requiredByType[34] != 10 { // just the one job × 1 run × 10 Trit
		t.Fatalf("Trit required = %d, want 10", got.requiredByType[34])
	}
}

func TestComputeRecalcRemaining_FallbackToTaskTargetRunsWhenJobRunsZero(t *testing.T) {
	sdeData := buildRecalcTestSDE()
	// job.Runs=0 (mis-recorded) but task.TargetRuns=7 → recalc uses 7.
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: 7, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 100})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: 0, Status: db.IndustryJobStatusPlanned},
		},
	}
	got := computeRecalcRemainingRequirements(snap, sdeData, []string{db.IndustryJobStatusPlanned})
	if got.requiredByType[34] != 70 { // 7 runs × 10 Trit
		t.Fatalf("Trit required = %d, want 70 (fell back to task.TargetRuns)", got.requiredByType[34])
	}
}
