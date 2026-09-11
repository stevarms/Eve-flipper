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
	// typeID 300 (Assembly Blueprint): manufacturing 1×Assembly from 4×Widget
	// + 5×Tritanium. The Widget is the intermediate — built by blueprint 100
	// above — which is what makes this a two-level plan.
	ind.Blueprints[300] = &sde.Blueprint{
		BlueprintTypeID: 300,
		ProductTypeID:   301,
		ProductQuantity: 1,
		Activities: map[string]*sde.ActivityData{
			"manufacturing": {
				Time:      900,
				Materials: []sde.BlueprintMaterial{{TypeID: 101, Quantity: 4}, {TypeID: 34, Quantity: 5}},
				Products:  []sde.BlueprintProduct{{TypeID: 301, Quantity: 1}},
			},
		},
	}
	ind.ProductToBlueprint[301] = 300
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
			301: {ID: 301, Name: "Assembly"},
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

// twoLevelSnapshot is a project that builds 8 Widgets and assembles 2
// Assemblies out of them — so the Widget is both produced and consumed
// in-project, which is the shape the bug showed up in.
func twoLevelSnapshot(t *testing.T, widgetRuns, assemblyRuns int32) db.IndustryProjectSnapshot {
	t.Helper()
	return db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "manufacturing", ProductTypeID: 101, TargetRuns: widgetRuns, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 100})},
			{ID: 2, Activity: "manufacturing", ProductTypeID: 301, TargetRuns: assemblyRuns, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 300})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "manufacturing", Runs: widgetRuns, Status: db.IndustryJobStatusPlanned},
			{ID: 11, TaskID: 2, Activity: "manufacturing", Runs: assemblyRuns, Status: db.IndustryJobStatusPlanned},
		},
	}
}

func TestComputeRecalcRemaining_CountsInProjectProduction(t *testing.T) {
	got := computeRecalcRemainingRequirements(
		twoLevelSnapshot(t, 8, 2), buildRecalcTestSDE(),
		[]string{db.IndustryJobStatusPlanned},
	)
	// The assembly job consumes 2×4 = 8 Widgets; the widget job makes 8.
	if got.requiredByType[101] != 8 {
		t.Fatalf("Widget required = %d, want 8", got.requiredByType[101])
	}
	if got.producedByType[101] != 8 {
		t.Fatalf("Widget produced = %d, want 8", got.producedByType[101])
	}
	// Trit is consumed by both levels: 8×10 for the widgets, 2×5 for assembly.
	if got.requiredByType[34] != 90 {
		t.Fatalf("Trit required = %d, want 90", got.requiredByType[34])
	}
	// The project's own output is not a material, so nothing requires it and
	// it must not be credited into anything.
	if got.requiredByType[301] != 0 {
		t.Fatalf("Assembly required = %d, want 0", got.requiredByType[301])
	}
	if got.producedByType[301] != 2 {
		t.Fatalf("Assembly produced = %d, want 2", got.producedByType[301])
	}
}

func TestComputeRecalcRemaining_InventionOutputIsNotCredited(t *testing.T) {
	// Invention yields a blueprint, which is never a BOM line. Crediting it
	// could only ever cancel a requirement that does not exist — and the
	// probabilistic quantity would be a lie if it ever did.
	snap := db.IndustryProjectSnapshot{
		Tasks: []db.IndustryTask{
			{ID: 1, Activity: "invention", ProductTypeID: 210, TargetRuns: 3, Constraints: mustConstraints(t, map[string]interface{}{"blueprint_type_id": 200})},
		},
		Jobs: []db.IndustryJob{
			{ID: 10, TaskID: 1, Activity: "invention", Runs: 3, Status: db.IndustryJobStatusPlanned},
		},
	}
	got := computeRecalcRemainingRequirements(snap, buildRecalcTestSDE(), []string{db.IndustryJobStatusPlanned})
	if len(got.producedByType) != 0 {
		t.Fatalf("producedByType = %v, want empty for an invention-only plan", got.producedByType)
	}
}

func TestAssembleRecalcRemainingDiffs_BuildCreditComesOffBeforeStock(t *testing.T) {
	// The reported bug: the intermediate the plan manufactures was reported as
	// something to buy, because it is never in the hangar.
	diffs := assembleRecalcRemainingDiffs(
		map[int32]int64{101: 8, 34: 90},
		map[int32]int64{101: 8},
		map[int32]int64{34: 40},
		map[int32]string{101: "Widget", 34: "Tritanium"},
	)
	byType := map[int32]db.IndustryMaterialDiff{}
	for _, d := range diffs {
		byType[d.TypeID] = d
	}

	widget := byType[101]
	if widget.BuildQty != 8 || widget.BuyQty != 0 || widget.MissingQty != 0 {
		t.Fatalf("Widget = build %d / buy %d / missing %d, want 8/0/0",
			widget.BuildQty, widget.BuyQty, widget.MissingQty)
	}
	if widget.RequiredQty != 8 {
		t.Fatalf("Widget required = %d, want the full 8 kept for display", widget.RequiredQty)
	}

	// A real purchase is unaffected: 90 needed, 40 on hand, buy 50.
	trit := byType[34]
	if trit.BuildQty != 0 || trit.AvailableQty != 40 || trit.BuyQty != 50 || trit.MissingQty != 50 {
		t.Fatalf("Trit = build %d / avail %d / buy %d / missing %d, want 0/40/50/50",
			trit.BuildQty, trit.AvailableQty, trit.BuyQty, trit.MissingQty)
	}
}

func TestAssembleRecalcRemainingDiffs_PartialBuildStillBuysTheRemainder(t *testing.T) {
	// The plan builds 5 of the 8 it needs and 1 sits in the hangar, so exactly
	// 2 have to be bought. Crediting the build first, then stock, is what gets
	// that number right — either credit alone would overstate the purchase.
	diffs := assembleRecalcRemainingDiffs(
		map[int32]int64{101: 8},
		map[int32]int64{101: 5},
		map[int32]int64{101: 1},
		map[int32]string{101: "Widget"},
	)
	if len(diffs) != 1 {
		t.Fatalf("diffs = %v, want one row", diffs)
	}
	got := diffs[0]
	if got.BuildQty != 5 || got.AvailableQty != 1 || got.BuyQty != 2 || got.MissingQty != 2 {
		t.Fatalf("got build %d / avail %d / buy %d / missing %d, want 5/1/2/2",
			got.BuildQty, got.AvailableQty, got.BuyQty, got.MissingQty)
	}
}

func TestAssembleRecalcRemainingDiffs_OverproductionDoesNotGoNegative(t *testing.T) {
	// A plan can deliberately build more than it consumes (the surplus is the
	// thing being sold). The credit clamps at the requirement so the row never
	// reports a negative purchase or an inflated build obligation.
	diffs := assembleRecalcRemainingDiffs(
		map[int32]int64{101: 8},
		map[int32]int64{101: 100},
		map[int32]int64{101: 50},
		map[int32]string{101: "Widget"},
	)
	got := diffs[0]
	if got.BuildQty != 8 || got.AvailableQty != 0 || got.BuyQty != 0 || got.MissingQty != 0 {
		t.Fatalf("got build %d / avail %d / buy %d / missing %d, want 8/0/0/0",
			got.BuildQty, got.AvailableQty, got.BuyQty, got.MissingQty)
	}
}
