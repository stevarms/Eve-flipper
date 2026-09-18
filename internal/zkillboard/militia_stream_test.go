package zkillboard

import (
	"reflect"
	"testing"
	"time"
)

/* The aggregation became incremental so the long window could afford its fetch.
 * These are the guards that it did not also become a second, different
 * aggregation -- there is one path, and the seven-day result is the same result
 * it was.
 */

// TestMilitiaLossAccum_PagesEqualOneSlice: the short path hands the accumulator
// everything at once and the long path hands it two hundred at a time. Those must
// be the same measurement, or the two windows would disagree for reasons that have
// nothing to do with the windows.
func TestMilitiaLossAccum_PagesEqualOneSlice(t *testing.T) {
	const villasen, jita = 30045334, 30000142
	warzone := map[int32]bool{villasen: true}
	sdeData := militiaTestSDE()
	now := time.Now().UTC()

	var losses []ESIKillmail
	for i := 0; i < 47; i++ {
		system := int32(villasen)
		if i%3 == 0 {
			system = jita // outside the warzone, and must stay uncounted either way
		}
		qty := int32(100 + (i%5)*37)
		losses = append(losses, lossIn(system, int64(i+1), now.Add(-time.Duration(i)*time.Hour), 2679, qty))
	}

	const window = 604800

	whole := newMilitiaLossAccum(warzone, sdeData)
	whole.Add(losses)
	wholeProfile := whole.Finish(window, dailyScale(window, false, whole.oldest), false)

	paged := newMilitiaLossAccum(warzone, sdeData)
	for start := 0; start < len(losses); start += 7 {
		end := start + 7
		if end > len(losses) {
			end = len(losses)
		}
		paged.Add(losses[start:end])
	}
	pagedProfile := paged.Finish(window, dailyScale(window, false, paged.oldest), false)

	// UpdatedAt is a clock reading, not a measurement.
	wholeProfile.UpdatedAt = time.Time{}
	pagedProfile.UpdatedAt = time.Time{}

	if !reflect.DeepEqual(wholeProfile, pagedProfile) {
		t.Errorf("paging changed the answer:\n one slice: %+v\n seven at a time: %+v", wholeProfile, pagedProfile)
	}
	if wholeProfile.InWarzoneKills == 0 || len(wholeProfile.Items) == 0 {
		t.Fatal("the fixture measured nothing, so nothing is being compared")
	}
	if wholeProfile.FetchedKills != len(losses) {
		t.Errorf("FetchedKills = %d, want %d", wholeProfile.FetchedKills, len(losses))
	}
}

// TestMilitiaLossAccum_RetainsNoPage: the accumulator is handed pages the walk is
// about to drop. If it kept a reference to one, the fold would retain the whole
// walk after all and the memory argument for the long window would be wrong.
func TestMilitiaLossAccum_RetainsNoPage(t *testing.T) {
	const villasen = 30045334
	warzone := map[int32]bool{villasen: true}
	now := time.Now().UTC()

	acc := newMilitiaLossAccum(warzone, militiaTestSDE())
	page := []ESIKillmail{
		lossIn(villasen, 1, now.Add(-time.Hour), 2679, 100),
		lossIn(villasen, 2, now.Add(-2*time.Hour), 2679, 300),
	}
	acc.Add(page)

	before := acc.Finish(604800, 1, false)
	total := before.Items[2679].TotalDestroyed
	if total == 0 {
		t.Fatal("nothing was accumulated")
	}

	// Overwrite the page the way a reused buffer would. If the accumulator kept
	// it, the numbers move.
	for i := range page {
		page[i] = ESIKillmail{}
	}

	after := acc.Finish(604800, 1, false)
	if after.Items[2679] == nil || after.Items[2679].TotalDestroyed != total {
		t.Errorf("clobbering the page changed the result: %v then %v", total, after.Items[2679])
	}
}

// TestMilitiaLossAccum_FinishIsRepeatable: Finish reads the accumulator, it does
// not consume it. Nothing calls it twice today, but a profile that changed on a
// second read would make every test above conditional on call order.
func TestMilitiaLossAccum_FinishIsRepeatable(t *testing.T) {
	const villasen = 30045334
	acc := newMilitiaLossAccum(map[int32]bool{villasen: true}, militiaTestSDE())
	acc.Add([]ESIKillmail{lossIn(villasen, 1, time.Now().UTC(), 2679, 250)})

	first := acc.Finish(604800, 1, false)
	second := acc.Finish(604800, 1, false)
	first.UpdatedAt, second.UpdatedAt = time.Time{}, time.Time{}

	if !reflect.DeepEqual(first, second) {
		t.Errorf("Finish is not repeatable:\n %+v\n %+v", first, second)
	}
}
