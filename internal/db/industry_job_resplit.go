package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// IndustryJobResplitSummary reports what a re-split changed, so the UI can
// say "12 replaced, 3 left alone" instead of silently rewriting the ledger.
type IndustryJobResplitSummary struct {
	ProjectID     int64    `json:"project_id"`
	JobsPreserved int      `json:"jobs_preserved"`
	JobsRemoved   int      `json:"jobs_removed"`
	JobsCreated   int      `json:"jobs_created"`
	TasksResplit  int      `json:"tasks_resplit"`
	TasksSkipped  int      `json:"tasks_skipped"`
	Warnings      []string `json:"warnings,omitempty"`
	UpdatedAt     string   `json:"updated_at"`
}

// industryResplitReplaceable reports whether a job may be thrown away and
// re-cut by a re-split.
//
// Only jobs that exist purely on paper qualify. Anything the user has acted
// on — installed in EVE (active), deliberately held (paused), delivered
// (completed), or recorded as failed/cancelled — is a fact about what
// happened, not a plan, and a settings change must not rewrite it.
func industryResplitReplaceable(status string) bool {
	switch normalizeIndustryJobStatus(status) {
	case IndustryJobStatusPlanned, IndustryJobStatusQueued:
		return true
	default:
		return false
	}
}

// industryResplitCoversRuns reports whether a preserved job's runs count
// against its task's target, and so shrink what still needs planning.
//
// Failed and cancelled jobs are preserved as history but produce nothing, so
// their runs go back into the pool to be re-planned.
func industryResplitCoversRuns(status string) bool {
	switch normalizeIndustryJobStatus(status) {
	case IndustryJobStatusActive, IndustryJobStatusPaused, IndustryJobStatusCompleted:
		return true
	default:
		return false
	}
}

// industryResplitJobRow is the slice of a job row a re-split needs. Notes are
// deliberately not read: the regenerated jobs get freshly synthesized
// "scheduler chunk N/M" notes, so there is no reason to round-trip the
// privacy codec on rows that are about to be deleted.
type industryResplitJobRow struct {
	ID              int64
	TaskID          int64
	CharacterID     int64
	FacilityID      int64
	Activity        string
	Runs            int32
	DurationSeconds int64
	CostISK         float64
	Status          string
	StartedAt       string
	FinishedAt      string
}

func loadIndustryResplitJobsTx(tx *sql.Tx, userID string, projectID int64) ([]industryResplitJobRow, error) {
	rows, err := tx.Query(`
		SELECT id, COALESCE(task_id, 0), character_id, facility_id, activity, runs,
		       duration_seconds, cost_isk, status, started_at, finished_at
		  FROM industry_jobs
		 WHERE user_id = ? AND project_id = ?
		 ORDER BY id
	`, userID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]industryResplitJobRow, 0, 64)
	for rows.Next() {
		var r industryResplitJobRow
		if err := rows.Scan(
			&r.ID, &r.TaskID, &r.CharacterID, &r.FacilityID, &r.Activity, &r.Runs,
			&r.DurationSeconds, &r.CostISK, &r.Status, &r.StartedAt, &r.FinishedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// industryResplitFreesAt returns when a job in flight releases its install
// slot, and whether it is holding one at all.
func industryResplitFreesAt(job industryResplitJobRow, now time.Time) (time.Time, bool) {
	if finish, ok := parseRFC3339UTC(job.FinishedAt); ok {
		return finish, finish.After(now)
	}
	if start, ok := parseRFC3339UTC(job.StartedAt); ok && job.DurationSeconds > 0 {
		finish := start.Add(time.Duration(job.DurationSeconds) * time.Second)
		return finish, finish.After(now)
	}
	return time.Time{}, false
}

// resplitSeed accumulates, per task, everything needed to re-cut its
// outstanding runs into fresh installs.
type resplitSeed struct {
	task industryPlanTaskRecord

	coveredRuns int32

	// Rates observed on the jobs being replaced. Preferred over the task's
	// constraints because they reflect the ME/TE and facility the plan was
	// actually committed at.
	replaceableRuns    int32
	replaceableSeconds int64
	replaceableCost    float64

	anyRuns    int32
	anySeconds int64
	anyCost    float64

	characterID     int64
	facilityID      int64
	haveCharacterID bool

	replaceableActivity string
}

// perRunRates picks the most trustworthy duration and cost per run available
// for a task: what the replaced jobs were costed at, else any other job on
// the task, else the plan-time constraints.
func (s *resplitSeed) perRunRates() (seconds float64, cost float64) {
	if s.replaceableRuns > 0 {
		return float64(s.replaceableSeconds) / float64(s.replaceableRuns),
			s.replaceableCost / float64(s.replaceableRuns)
	}
	if s.anyRuns > 0 {
		return float64(s.anySeconds) / float64(s.anyRuns),
			s.anyCost / float64(s.anyRuns)
	}
	constraints := map[string]interface{}{}
	if strings.TrimSpace(s.task.ConstraintsJSON) != "" {
		_ = json.Unmarshal([]byte(s.task.ConstraintsJSON), &constraints)
	}
	if perRun, ok := extractConstraintFloat(constraints, "duration_seconds_per_run"); ok && perRun > 0 {
		seconds = perRun
	} else if total, ok := extractConstraintFloat(constraints, "duration_seconds"); ok && total > 0 && s.task.TargetRuns > 0 {
		seconds = total / float64(s.task.TargetRuns)
	}
	if perRun, ok := extractConstraintFloat(constraints, "cost_isk_per_run"); ok && perRun > 0 {
		cost = perRun
	} else if total, ok := extractConstraintFloat(constraints, "cost_isk"); ok && total > 0 && s.task.TargetRuns > 0 {
		cost = total / float64(s.task.TargetRuns)
	}
	return seconds, cost
}

// ResplitIndustryProjectJobsForUser re-cuts a committed project's outstanding
// jobs under a new scheduler configuration, without disturbing tasks,
// materials, the blueprint pool, or any job the user has already acted on.
//
// This is the "I changed the scheduler settings, now apply them" path. The
// plan apply route cannot serve it: that one is Replace-mode, so it would
// wipe and reinsert every task and job, losing rowids, install records and
// job history along the way. Here only planned/queued job rows are deleted;
// everything else in the project is left exactly as it is.
//
// Runs already covered by jobs in flight or delivered are subtracted from
// each task's target, so re-splitting mid-operation plans the shortfall
// rather than duplicating work. Slots occupied by jobs in flight are handed
// to the scheduler as busy, so new installs are dated after the running ones
// deliver instead of all piling onto "now".
func (d *DB) ResplitIndustryProjectJobsForUser(
	userID string,
	projectID int64,
	in IndustryPlanSchedulerInput,
) (IndustryJobResplitSummary, error) {
	userID = normalizeUserID(userID)
	if projectID <= 0 {
		return IndustryJobResplitSummary{}, fmt.Errorf("project_id must be positive")
	}

	project, err := d.GetIndustryProjectForUser(userID, projectID)
	if err != nil {
		return IndustryJobResplitSummary{}, err
	}
	// Regenerated jobs carry synthesized scheduler notes, which are written
	// through the privacy codec like any other job note.
	if err := d.warmPrivateString(userID, "industry_jobs.notes"); err != nil {
		return IndustryJobResplitSummary{}, err
	}

	cfg := normalizeIndustrySchedulerInput(in, project.Strategy)
	// Enabled gates whether a *plan apply* runs the scheduler at all. Asking
	// for a re-split is itself the opt-in, so the flag is not consulted here.
	cfg.Enabled = true

	tx, err := d.sql.Begin()
	if err != nil {
		return IndustryJobResplitSummary{}, err
	}
	defer tx.Rollback()

	tasks, err := listIndustryTaskRecordsForProjectTx(tx, userID, projectID)
	if err != nil {
		return IndustryJobResplitSummary{}, err
	}
	jobs, err := loadIndustryResplitJobsTx(tx, userID, projectID)
	if err != nil {
		return IndustryJobResplitSummary{}, err
	}

	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339)

	seeds := make(map[int64]*resplitSeed, len(tasks))
	order := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		seeds[task.ID] = &resplitSeed{task: task}
		order = append(order, task.ID)
	}

	removeIDs := make([]int64, 0, len(jobs))
	busyUntil := make([]time.Time, 0, 8)
	preserved := 0

	for _, job := range jobs {
		replaceable := industryResplitReplaceable(job.Status)
		if replaceable {
			removeIDs = append(removeIDs, job.ID)
		} else {
			preserved++
			if freesAt, busy := industryResplitFreesAt(job, nowTime); busy {
				busyUntil = append(busyUntil, freesAt)
			}
		}
		seed := seeds[job.TaskID]
		if seed == nil {
			// An orphan job whose task is gone. Preserved rows stay put;
			// replaceable ones are dropped with the rest, since there is no
			// task target left to re-plan them against.
			continue
		}
		runs := job.Runs
		if runs < 0 {
			runs = 0
		}
		seed.anyRuns += runs
		seed.anySeconds += job.DurationSeconds
		seed.anyCost += job.CostISK
		if industryResplitCoversRuns(job.Status) {
			seed.coveredRuns += runs
		}
		if replaceable {
			seed.replaceableRuns += runs
			seed.replaceableSeconds += job.DurationSeconds
			seed.replaceableCost += job.CostISK
			if seed.replaceableActivity == "" {
				seed.replaceableActivity = normalizeIndustryActivity(job.Activity)
			}
		}
		if !seed.haveCharacterID && job.CharacterID > 0 {
			seed.characterID = job.CharacterID
			seed.facilityID = job.FacilityID
			seed.haveCharacterID = true
		}
	}

	warnings := make([]string, 0, 4)
	summary := IndustryJobResplitSummary{
		ProjectID:     projectID,
		JobsPreserved: preserved,
		JobsRemoved:   len(removeIDs),
		UpdatedAt:     now,
	}

	// One seed job per task carrying its outstanding runs; the scheduler
	// below is what cuts each into install-sized chunks.
	seedJobs := make([]IndustryJobPlanInput, 0, len(order))
	for _, taskID := range order {
		seed := seeds[taskID]
		remaining := seed.task.TargetRuns - seed.coveredRuns
		if remaining <= 0 {
			summary.TasksSkipped++
			continue
		}
		perRunSeconds, perRunCost := seed.perRunRates()
		activity := seed.replaceableActivity
		if activity == "" {
			activity = normalizeIndustryActivity(seed.task.Activity)
		}
		seedJobs = append(seedJobs, IndustryJobPlanInput{
			TaskID:          taskID,
			CharacterID:     seed.characterID,
			FacilityID:      seed.facilityID,
			Activity:        activity,
			Runs:            remaining,
			DurationSeconds: int64(math.Round(perRunSeconds * float64(remaining))),
			CostISK:         perRunCost * float64(remaining),
			Status:          cfg.QueueStatus,
			// External job IDs belong to a specific ESI install; a freshly
			// cut chunk is not that job.
			ExternalJobID: 0,
		})
		summary.TasksResplit++
		if perRunSeconds <= 0 {
			warnings = append(warnings, fmt.Sprintf("task %q has no known duration; scheduled with zero length", seed.task.Name))
		}
	}

	if len(seedJobs) > 0 {
		taskParents := map[int64]int64{}
		taskPlannedEnd := map[int64]time.Time{}
		if err := loadIndustryTaskSchedulingMapsTx(tx, userID, projectID, taskParents, taskPlannedEnd); err != nil {
			return IndustryJobResplitSummary{}, err
		}
		sort.Slice(busyUntil, func(i, j int) bool { return busyUntil[i].Before(busyUntil[j]) })
		scheduled := splitAndScheduleIndustryJobs(seedJobs, cfg, nowTime, busyUntil, taskParents, taskPlannedEnd)

		blueprintPool, err := listIndustryBlueprintPoolForProjectTx(tx, userID, projectID)
		if err != nil {
			return IndustryJobResplitSummary{}, err
		}
		if len(tasks) > 0 {
			// Non-strict: a re-split must never fail the whole operation over
			// a blueprint gate the original commit already passed.
			var capWarnings []string
			scheduled, capWarnings = applyBlueprintRunCapsToJobs(scheduled, tasks, blueprintPool, false)
			warnings = append(warnings, capWarnings...)
		}
		seedJobs = scheduled
	}

	for _, id := range removeIDs {
		if _, err := tx.Exec(`DELETE FROM industry_jobs WHERE user_id = ? AND project_id = ? AND id = ?`, userID, projectID, id); err != nil {
			return IndustryJobResplitSummary{}, err
		}
	}

	if len(seedJobs) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO industry_jobs (
				user_id, project_id, task_id, character_id, facility_id, activity, runs,
				duration_seconds, cost_isk, status, started_at, finished_at, external_job_id, notes, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return IndustryJobResplitSummary{}, err
		}
		defer stmt.Close()
		for _, j := range seedJobs {
			storedNotes, err := d.protectPrivateString(userID, "industry_jobs.notes", strings.TrimSpace(j.Notes))
			if err != nil {
				return IndustryJobResplitSummary{}, err
			}
			if _, err := stmt.Exec(
				userID, projectID, nullablePositiveInt64(j.TaskID), j.CharacterID, j.FacilityID,
				normalizeIndustryActivity(j.Activity), j.Runs, j.DurationSeconds, j.CostISK,
				defaultIndustryJobStatus(j.Status), strings.TrimSpace(j.StartedAt), strings.TrimSpace(j.FinishedAt),
				j.ExternalJobID, storedNotes, now, now,
			); err != nil {
				return IndustryJobResplitSummary{}, err
			}
			summary.JobsCreated++
		}
	}

	if _, err := tx.Exec(`UPDATE industry_projects SET updated_at = ? WHERE user_id = ? AND id = ?`, now, userID, projectID); err != nil {
		return IndustryJobResplitSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return IndustryJobResplitSummary{}, err
	}

	if len(warnings) > 0 {
		summary.Warnings = warnings
	}
	return summary, nil
}
