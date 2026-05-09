package db

import (
	"fmt"
	"sync"
	"testing"
)

func TestAchievementPatchesConcurrentWrites(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := d.ApplyAchievementPatchesForUser("user-achievements", []AchievementProgressPatch{
				{AchievementID: "mission-controller", Progress: int64(i + 1)},
				{AchievementID: "fee-awareness", Progress: 1},
			})
			if err != nil {
				errs <- fmt.Errorf("worker %d: %w", i, err)
			}
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	states, err := d.ListAchievementsForUser("user-achievements")
	if err != nil {
		t.Fatalf("list achievements: %v", err)
	}
	byID := make(map[string]AchievementState, len(states))
	for _, st := range states {
		byID[st.AchievementID] = st
	}
	if got := byID["mission-controller"].Progress; got != workers {
		t.Fatalf("mission-controller progress = %d, want %d", got, workers)
	}
	if got := byID["fee-awareness"].Progress; got != 1 {
		t.Fatalf("fee-awareness progress = %d, want 1", got)
	}
}
