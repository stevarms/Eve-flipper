package db

import (
	"testing"
	"time"
)

// resplitFixture commits one 1000-run manufacturing task cut into five
// 200-run jobs, which is what the balanced scheduler profile produces.
func resplitFixture(t *testing.T) (*DB, int64) {
	t.Helper()
	d := openTestDB(t)

	project, err := d.CreateIndustryProjectForUser("user-a", IndustryProjectCreateInput{
		Name:     "Hammerhead II Run",
		Strategy: "balanced",
	})
	if err != nil {
		t.Fatalf("CreateIndustryProjectForUser: %v", err)
	}

	jobs := make([]IndustryJobPlanInput, 0, 5)
	for i := 0; i < 5; i++ {
		jobs = append(jobs, IndustryJobPlanInput{
			TaskID:          -1,
			CharacterID:     900001,
			FacilityID:      60003760,
			Activity:        "manufacturing",
			Runs:            200,
			DurationSeconds: 200 * 60,
			CostISK:         200 * 10,
			Status:          IndustryJobStatusQueued,
		})
	}
	if _, err := d.ApplyIndustryPlanForUser("user-a", project.ID, IndustryPlanPatch{
		Replace: true,
		Tasks: []IndustryTaskPlanInput{{
			Name:          "Manufacture Hammerhead II",
			Activity:      "manufacturing",
			ProductTypeID: 2185,
			TargetRuns:    1000,
		}},
		Jobs: jobs,
	}); err != nil {
		t.Fatalf("ApplyIndustryPlanForUser: %v", err)
	}
	return d, project.ID
}

func resplitJobs(t *testing.T, d *DB, projectID int64) []IndustryJob {
	t.Helper()
	snapshot, err := d.GetIndustryProjectSnapshotForUser("user-a", projectID)
	if err != nil {
		t.Fatalf("GetIndustryProjectSnapshotForUser: %v", err)
	}
	return snapshot.Jobs
}

func countJobRuns(jobs []IndustryJob, statuses ...string) (count int, runs int32) {
	want := map[string]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	for _, j := range jobs {
		if len(want) > 0 && !want[j.Status] {
			continue
		}
		count++
		runs += j.Runs
	}
	return count, runs
}

// A re-split with tighter limits must re-cut the untouched jobs and leave the
// project's totals intact.
func TestResplitIndustryProjectJobsRecutsUntouchedJobs(t *testing.T) {
	d, projectID := resplitFixture(t)
	defer d.Close()

	summary, err := d.ResplitIndustryProjectJobsForUser("user-a", projectID, IndustryPlanSchedulerInput{
		Enabled:               true,
		SlotCount:             2,
		MaxJobRuns:            250,
		MaxJobDurationSeconds: 24 * 3600,
		QueueStatus:           IndustryJobStatusQueued,
	})
	if err != nil {
		t.Fatalf("ResplitIndustryProjectJobsForUser: %v", err)
	}
	if summary.JobsRemoved != 5 {
		t.Errorf("JobsRemoved = %d, want 5", summary.JobsRemoved)
	}
	if summary.JobsPreserved != 0 {
		t.Errorf("JobsPreserved = %d, want 0", summary.JobsPreserved)
	}
	if summary.TasksResplit != 1 {
		t.Errorf("TasksResplit = %d, want 1", summary.TasksResplit)
	}

	jobs := resplitJobs(t, d, projectID)
	count, runs := countJobRuns(jobs)
	// 1000 runs at 250/job.
	if count != 4 {
		t.Errorf("job count = %d, want 4 (%+v)", count, jobs)
	}
	if runs != 1000 {
		t.Errorf("total runs = %d, want 1000 — a re-split must not change what gets built", runs)
	}
	for _, j := range jobs {
		if j.Runs > 250 {
			t.Errorf("job %d has %d runs, above the 250 limit", j.ID, j.Runs)
		}
	}
}

// The user's rule: jobs they have already acted on are facts, not plans.
// Re-splitting must leave them alone and plan only the shortfall.
func TestResplitIndustryProjectJobsSkipsActiveAndCompleted(t *testing.T) {
	d, projectID := resplitFixture(t)
	defer d.Close()

	before := resplitJobs(t, d, projectID)
	if len(before) != 5 {
		t.Fatalf("fixture job count = %d, want 5", len(before))
	}
	if _, err := d.UpdateIndustryJobStatusForUser("user-a", before[0].ID, IndustryJobStatusCompleted, "", "", ""); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	finish := time.Now().UTC().Add(6 * time.Hour).Format(time.RFC3339)
	if _, err := d.UpdateIndustryJobStatusForUser("user-a", before[1].ID, IndustryJobStatusActive, "", finish, ""); err != nil {
		t.Fatalf("mark active: %v", err)
	}
	keptIDs := map[int64]bool{before[0].ID: true, before[1].ID: true}

	summary, err := d.ResplitIndustryProjectJobsForUser("user-a", projectID, IndustryPlanSchedulerInput{
		Enabled:               true,
		SlotCount:             2,
		MaxJobRuns:            250,
		MaxJobDurationSeconds: 24 * 3600,
		QueueStatus:           IndustryJobStatusQueued,
	})
	if err != nil {
		t.Fatalf("ResplitIndustryProjectJobsForUser: %v", err)
	}
	if summary.JobsPreserved != 2 {
		t.Errorf("JobsPreserved = %d, want 2", summary.JobsPreserved)
	}
	if summary.JobsRemoved != 3 {
		t.Errorf("JobsRemoved = %d, want 3", summary.JobsRemoved)
	}

	after := resplitJobs(t, d, projectID)
	survivors := 0
	for _, j := range after {
		if keptIDs[j.ID] {
			survivors++
			if j.Runs != 200 {
				t.Errorf("preserved job %d runs = %d, want 200 untouched", j.ID, j.Runs)
			}
		}
	}
	if survivors != 2 {
		t.Errorf("preserved jobs still present = %d, want 2 — active/completed rows must keep their ids", survivors)
	}

	// 400 of the 1000 runs are already covered, so only 600 get re-planned:
	// three 200-run chunks at the 250 limit.
	_, newRuns := countJobRuns(after, IndustryJobStatusQueued)
	if newRuns != 600 {
		t.Errorf("re-planned runs = %d, want 600 (1000 target less 400 covered)", newRuns)
	}
	_, totalRuns := countJobRuns(after)
	if totalRuns != 1000 {
		t.Errorf("total runs = %d, want 1000", totalRuns)
	}
}

// A job in flight is holding an install slot. New chunks must be dated after
// it delivers, not stacked on top of it at "now".
func TestResplitIndustryProjectJobsSchedulesAroundJobsInFlight(t *testing.T) {
	d, projectID := resplitFixture(t)
	defer d.Close()

	before := resplitJobs(t, d, projectID)
	// Truncated: the stored timestamp is RFC3339, which drops the fraction.
	finish := time.Now().UTC().Add(30 * time.Hour).Truncate(time.Second)
	if _, err := d.UpdateIndustryJobStatusForUser(
		"user-a", before[0].ID, IndustryJobStatusActive, "", finish.Format(time.RFC3339), "",
	); err != nil {
		t.Fatalf("mark active: %v", err)
	}

	if _, err := d.ResplitIndustryProjectJobsForUser("user-a", projectID, IndustryPlanSchedulerInput{
		Enabled:               true,
		SlotCount:             1,
		MaxJobRuns:            250,
		MaxJobDurationSeconds: 24 * 3600,
		QueueStatus:           IndustryJobStatusQueued,
	}); err != nil {
		t.Fatalf("ResplitIndustryProjectJobsForUser: %v", err)
	}

	for _, j := range resplitJobs(t, d, projectID) {
		if j.Status != IndustryJobStatusQueued {
			continue
		}
		start, err := time.Parse(time.RFC3339, j.StartedAt)
		if err != nil {
			t.Fatalf("job %d has unparseable start %q: %v", j.ID, j.StartedAt, err)
		}
		if start.Before(finish) {
			t.Errorf("job %d starts %s, before the in-flight job frees its only slot at %s",
				j.ID, start, finish)
		}
	}
}

// Nothing left to plan is a valid outcome, not an error, and it must not
// leave stray zero-run jobs behind.
func TestResplitIndustryProjectJobsNoOpWhenFullyCovered(t *testing.T) {
	d, projectID := resplitFixture(t)
	defer d.Close()

	for _, j := range resplitJobs(t, d, projectID) {
		if _, err := d.UpdateIndustryJobStatusForUser("user-a", j.ID, IndustryJobStatusCompleted, "", "", ""); err != nil {
			t.Fatalf("mark completed: %v", err)
		}
	}

	summary, err := d.ResplitIndustryProjectJobsForUser("user-a", projectID, IndustryPlanSchedulerInput{
		Enabled:    true,
		MaxJobRuns: 50,
	})
	if err != nil {
		t.Fatalf("ResplitIndustryProjectJobsForUser: %v", err)
	}
	if summary.JobsCreated != 0 || summary.JobsRemoved != 0 {
		t.Errorf("created = %d / removed = %d, want 0 / 0", summary.JobsCreated, summary.JobsRemoved)
	}
	if summary.JobsPreserved != 5 || summary.TasksSkipped != 1 {
		t.Errorf("preserved = %d / skipped = %d, want 5 / 1", summary.JobsPreserved, summary.TasksSkipped)
	}
	if len(resplitJobs(t, d, projectID)) != 5 {
		t.Errorf("job rows changed on a fully covered project")
	}
}

// Tasks and materials are not the scheduler's business — a re-split must
// leave them byte-identical.
func TestResplitIndustryProjectJobsLeavesTasksAndMaterialsAlone(t *testing.T) {
	d, projectID := resplitFixture(t)
	defer d.Close()

	if _, err := d.ApplyIndustryPlanForUser("user-a", projectID, IndustryPlanPatch{
		Materials: []IndustryMaterialPlanInput{{
			TypeID: 34, TypeName: "Tritanium", RequiredQty: 500000, AvailableQty: 120000, BuyQty: 380000, Source: "market",
		}},
	}); err != nil {
		t.Fatalf("seed materials: %v", err)
	}

	beforeSnapshot, err := d.GetIndustryProjectSnapshotForUser("user-a", projectID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(beforeSnapshot.Tasks) != 1 || len(beforeSnapshot.Materials) != 1 {
		t.Fatalf("fixture tasks = %d / materials = %d, want 1 / 1", len(beforeSnapshot.Tasks), len(beforeSnapshot.Materials))
	}

	if _, err := d.ResplitIndustryProjectJobsForUser("user-a", projectID, IndustryPlanSchedulerInput{
		Enabled:    true,
		MaxJobRuns: 100,
	}); err != nil {
		t.Fatalf("ResplitIndustryProjectJobsForUser: %v", err)
	}

	afterSnapshot, err := d.GetIndustryProjectSnapshotForUser("user-a", projectID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(afterSnapshot.Tasks) != 1 || afterSnapshot.Tasks[0].ID != beforeSnapshot.Tasks[0].ID {
		t.Errorf("task rows changed: %+v", afterSnapshot.Tasks)
	}
	if len(afterSnapshot.Materials) != 1 || afterSnapshot.Materials[0].RequiredQty != 500000 {
		t.Errorf("material rows changed: %+v", afterSnapshot.Materials)
	}
}
