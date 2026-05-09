package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type AchievementState struct {
	UserID        string `json:"user_id"`
	AchievementID string `json:"achievement_id"`
	Progress      int64  `json:"progress"`
	UnlockedAt    string `json:"unlocked_at"`
	Seen          bool   `json:"seen"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type AchievementProgressPatch struct {
	AchievementID string `json:"achievement_id"`
	Progress      int64  `json:"progress"`
	UnlockedAt    string `json:"unlocked_at"`
	Seen          *bool  `json:"seen,omitempty"`
}

func cleanAchievementID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) > 128 {
		id = id[:128]
	}
	var b strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cleanAchievementTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func scanAchievementState(scanner interface {
	Scan(dest ...interface{}) error
}) (AchievementState, error) {
	var st AchievementState
	var seenInt int
	err := scanner.Scan(
		&st.UserID,
		&st.AchievementID,
		&st.Progress,
		&st.UnlockedAt,
		&seenInt,
		&st.CreatedAt,
		&st.UpdatedAt,
	)
	st.Seen = seenInt != 0
	return st, err
}

func (d *DB) ListAchievementsForUser(userID string) ([]AchievementState, error) {
	userID = normalizeUserID(userID)
	rows, err := d.sql.Query(`
		SELECT user_id, achievement_id, progress, unlocked_at, seen, created_at, updated_at
		FROM achievements
		WHERE user_id = ?
		ORDER BY achievement_id ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := []AchievementState{}
	for rows.Next() {
		st, err := scanAchievementState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func (d *DB) ApplyAchievementPatchesForUser(userID string, patches []AchievementProgressPatch) ([]AchievementState, []AchievementState, error) {
	userID = normalizeUserID(userID)
	if len(patches) > 200 {
		return nil, nil, fmt.Errorf("too many achievement patches")
	}
	d.achievementMu.Lock()
	defer d.achievementMu.Unlock()

	tx, err := d.sql.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	states := []AchievementState{}
	unlocked := []AchievementState{}
	for _, patch := range patches {
		id := cleanAchievementID(patch.AchievementID)
		if id == "" {
			return nil, nil, fmt.Errorf("achievement id is required")
		}
		if patch.Progress < 0 {
			patch.Progress = 0
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		unlockedAt := cleanAchievementTime(patch.UnlockedAt)
		seenInt := 0
		if patch.Seen != nil && *patch.Seen {
			seenInt = 1
		}

		var existing AchievementState
		var existingSeenInt int
		err := tx.QueryRow(`
			SELECT user_id, achievement_id, progress, unlocked_at, seen, created_at, updated_at
			FROM achievements
			WHERE user_id = ? AND achievement_id = ?
		`, userID, id).Scan(
			&existing.UserID,
			&existing.AchievementID,
			&existing.Progress,
			&existing.UnlockedAt,
			&existingSeenInt,
			&existing.CreatedAt,
			&existing.UpdatedAt,
		)
		existing.Seen = existingSeenInt != 0
		if err != nil && err != sql.ErrNoRows {
			return nil, nil, err
		}

		if err == sql.ErrNoRows {
			if _, err := tx.Exec(`
				INSERT INTO achievements (user_id, achievement_id, progress, unlocked_at, seen, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, userID, id, patch.Progress, unlockedAt, seenInt, now, now); err != nil {
				return nil, nil, err
			}
		} else {
			nextProgress := existing.Progress
			if patch.Progress > nextProgress {
				nextProgress = patch.Progress
			}
			nextUnlockedAt := existing.UnlockedAt
			newlyUnlocked := false
			if nextUnlockedAt == "" && unlockedAt != "" {
				nextUnlockedAt = unlockedAt
				newlyUnlocked = true
			}
			nextSeenInt := existingSeenInt
			if patch.Seen != nil {
				if *patch.Seen {
					nextSeenInt = 1
				} else {
					nextSeenInt = 0
				}
			}
			if _, err := tx.Exec(`
				UPDATE achievements
				SET progress = ?, unlocked_at = ?, seen = ?, updated_at = ?
				WHERE user_id = ? AND achievement_id = ?
			`, nextProgress, nextUnlockedAt, nextSeenInt, now, userID, id); err != nil {
				return nil, nil, err
			}
			if newlyUnlocked {
				unlocked = append(unlocked, AchievementState{
					UserID:        userID,
					AchievementID: id,
					Progress:      nextProgress,
					UnlockedAt:    nextUnlockedAt,
					Seen:          nextSeenInt != 0,
					CreatedAt:     existing.CreatedAt,
					UpdatedAt:     now,
				})
			}
		}

		st, err := scanAchievementState(tx.QueryRow(`
			SELECT user_id, achievement_id, progress, unlocked_at, seen, created_at, updated_at
			FROM achievements
			WHERE user_id = ? AND achievement_id = ?
		`, userID, id))
		if err != nil {
			return nil, nil, err
		}
		if err == nil && existing.AchievementID == "" && st.UnlockedAt != "" {
			unlocked = append(unlocked, st)
		}
		states = append(states, st)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return states, unlocked, nil
}

func (d *DB) MarkAchievementsSeenForUser(userID string, ids []string) ([]AchievementState, error) {
	userID = normalizeUserID(userID)
	if len(ids) > 200 {
		return nil, fmt.Errorf("too many achievement ids")
	}
	d.achievementMu.Lock()
	defer d.achievementMu.Unlock()

	tx, err := d.sql.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	states := []AchievementState{}
	for _, rawID := range ids {
		id := cleanAchievementID(rawID)
		if id == "" {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE achievements
			SET seen = 1, updated_at = ?
			WHERE user_id = ? AND achievement_id = ?
		`, now, userID, id); err != nil {
			return nil, err
		}
		st, err := scanAchievementState(tx.QueryRow(`
			SELECT user_id, achievement_id, progress, unlocked_at, seen, created_at, updated_at
			FROM achievements
			WHERE user_id = ? AND achievement_id = ?
		`, userID, id))
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		states = append(states, st)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return states, nil
}
