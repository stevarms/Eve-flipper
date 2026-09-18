package zkillboard

import (
	"context"
	"fmt"
	"time"

	"eve-flipper/internal/logger"
	"eve-flipper/internal/sde"
)

// MilitiaDemandProfile is what a militia is losing inside its own warzone over
// a window, which is the demand an FW hub actually has to refill.
//
// It is deliberately not a RegionDemandProfile: the scope is a faction and a set
// of systems rather than one region, and the window is days rather than the
// fixed 24 hours that profile assumes.
type MilitiaDemandProfile struct {
	MilitiaFactionID int32                        `json:"militia_faction_id"`
	WindowSeconds    int                          `json:"window_seconds"`
	FetchedKills     int                          `json:"fetched_kills"`
	InWarzoneKills   int                          `json:"in_warzone_kills"`
	Truncated        bool                         `json:"truncated"`
	Items            map[int32]*ItemDemandProfile `json:"items"`
	UpdatedAt        time.Time                    `json:"updated_at"`

	// CoveredSeconds is what the sample actually spans, which past seven days is
	// not the same as WindowSeconds: each calendar month is paged separately and
	// each has its own 100-page limit, so a long window can have holes in the
	// middle. The rates are scaled by this, and it is reported so the difference
	// is visible rather than inferred.
	CoveredSeconds float64 `json:"covered_seconds"`

	// Months is per-month coverage on the long path, empty on the short one.
	Months []MonthCoverage `json:"months,omitempty"`

	// Warnings say where the sample fell short, in the words a plan can show.
	// A fetch that came back thin is a warning, never an absence -- an item whose
	// evidence was cut off must not read as an item nothing is destroying.
	Warnings []string `json:"warnings,omitempty"`
}

// militiaMaxPages bounds the paging walk. A busy militia loses roughly 700 ships
// a day, so a 7-day window is about 25 pages of 200; 50 only binds when
// something is abnormal, and Truncated says so when it does.
const militiaMaxPages = 50

// minKillsWithItem is the plan's shippability threshold, exported through the
// engine layer rather than applied here.
//
// The analyzer measures; it does not decide what is worth shipping. An item on
// one killmail is a real observation and a bad rate estimate, and those are
// different problems: dropping it here would hide the thinness that the gap
// table is supposed to show. ItemDemandProfile.KillmailCount already carries
// exactly the "how many losses actually had this" count the gate needs, so no
// second field is added for it.
const minKillsWithItem = 3

// combatAmmoMultiplier scales loaded ammunition up to ammunition consumed.
//
// A killmail shows what was still in the magazine when the ship died, not what
// was fired getting there. Three is the existing WarTracker assumption, kept
// so both demand paths tell the same story; it is a fudge factor, and the one
// number in this file with no measurement behind it.
const combatAmmoMultiplier = 3.0

// AnalyzeMilitiaDemand measures what a militia loses inside its own warzone.
//
// Faction membership is not location: only 29% of sampled Caldari militia losses
// happened in a warzone system, the rest being Jita, nullsec and wherever else
// those pilots fly. Demand for an FW hub is the intersection, so warzoneSystems
// is required rather than optional -- an empty set is an error, because silently
// analyzing every loss a militia takes anywhere would look like a working answer
// and be a wrong one. Callers wanting a whole region already have
// AnalyzeRegionFittings.
//
// Per-type rates are winsorized at p90 across the killmails carrying that type.
// The per-kill distribution is heavy-tailed -- in the calibration sample a single
// loss was 32-42% of a type's entire destroyed count -- and one ammo barge going
// down must not manufacture a week of demand.
func (d *DemandAnalyzer) AnalyzeMilitiaDemand(ctx context.Context, militiaFactionID int32, warzoneSystems map[int32]bool, pastSeconds int, sdeData *sde.Data) (*MilitiaDemandProfile, error) {
	if militiaFactionID <= 0 {
		return nil, fmt.Errorf("militia faction id required")
	}
	if len(warzoneSystems) == 0 {
		return nil, fmt.Errorf("warzone system set is empty: militia losses outside the warzone are not FW hub demand")
	}

	// Past seven days the pastSeconds modifier is not available at all, so the
	// walk has to go month by month. Both paths fold through the same
	// accumulator: one aggregation, not a cheap one and a careful one.
	if pastSeconds > zkillMaxPastSeconds {
		return d.analyzeMilitiaDemandLong(ctx, militiaFactionID, warzoneSystems, pastSeconds, sdeData)
	}

	window := normalizePastSeconds(pastSeconds)
	losses, err := d.client.GetFactionLosses(ctx, militiaFactionID, window, militiaMaxPages)
	if err != nil {
		return nil, fmt.Errorf("get faction losses: %w", err)
	}

	profile := aggregateMilitiaLosses(losses, warzoneSystems, sdeData, window)
	profile.MilitiaFactionID = militiaFactionID

	logger.Success("Demand", fmt.Sprintf("Militia %d: %d losses fetched, %d in warzone, %d shippable item types over %dh",
		militiaFactionID, profile.FetchedKills, profile.InWarzoneKills, len(profile.Items), window/3600))

	return profile, nil
}

// analyzeMilitiaDemandLong measures the same thing over a window longer than
// zkillboard's seven-day pastSeconds ceiling, walking calendar months.
//
// It differs from the short path in two ways and only two. The fetch is a fold
// rather than a slice, because a quarter of a militia's losses is ~104 MB of
// killmails if held at once and ~0.3 MB if folded a page at a time. And the daily
// rate is scaled by summed per-month coverage rather than by the requested
// window, because a month that hit its page limit is a hole inside the span --
// dividing by the whole window there would count the missing days as days when
// nothing died, and a rate that is quietly low reads on screen as a covered item.
func (d *DemandAnalyzer) analyzeMilitiaDemandLong(ctx context.Context, militiaFactionID int32,
	warzoneSystems map[int32]bool, windowSeconds int, sdeData *sde.Data) (*MilitiaDemandProfile, error) {
	now := time.Now().UTC()
	since := now.Add(-time.Duration(windowSeconds) * time.Second)

	acc := newMilitiaLossAccum(warzoneSystems, sdeData)
	walk, err := d.client.WalkFactionLossesSince(ctx, militiaFactionID, since, func(page []ESIKillmail) error {
		acc.Add(page)
		return nil
	})
	if err != nil {
		// A cancelled or refused walk with nothing in it is a failure; one that
		// got some way in is partial data, and the coverage record says how far.
		if walk == nil || acc.fetched == 0 {
			return nil, fmt.Errorf("walk faction losses: %w", err)
		}
		logger.Warn("Demand", fmt.Sprintf("militia %d long walk stopped early: %v", militiaFactionID, err))
	}

	covered := coveredSeconds(walk.Months, since, now)
	if covered <= 0 {
		// Nothing was covered, so there is no denominator and no rate. Say so
		// rather than dividing by the window and reporting zeros as measurements.
		return nil, fmt.Errorf("militia %d: the %d-day walk covered no time at all",
			militiaFactionID, windowSeconds/86400)
	}

	profile := acc.Finish(windowSeconds, 86400/covered, walk.BudgetReached)
	profile.MilitiaFactionID = militiaFactionID
	profile.CoveredSeconds = covered
	profile.Months = walk.Months
	profile.Warnings = coverageWarnings(walk, since, now, windowSeconds)

	logger.Success("Demand", fmt.Sprintf("Militia %d: %d losses over %d pages, %d in warzone, %d item types, %.1f of %.0f days covered",
		militiaFactionID, profile.FetchedKills, walk.Pages, profile.InWarzoneKills, len(profile.Items),
		covered/86400, float64(windowSeconds)/86400))

	return profile, nil
}

// coveredSeconds sums what the walk actually sampled, month by month.
//
// This is the whole reason MonthCoverage exists. dailyScale's rule -- the span
// back to the oldest killmail seen -- is right when the sample is one contiguous
// run cut off at its far end, which is what the pastSeconds path returns. It is
// wrong once months are paged separately: a truncated month in the middle leaves
// a gap with covered time on both sides of it, and treating the span as covered
// counts that gap as quiet days. Per month the arithmetic is unambiguous, because
// a month's sample is contiguous from its newest killmail back to either the
// month's start or the point truncation stopped it.
func coveredSeconds(months []MonthCoverage, since, now time.Time) float64 {
	var total float64
	for _, m := range months {
		start := time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)

		// The window's own edges bound the first and last months.
		if start.Before(since) {
			start = since
		}
		if end.After(now) {
			end = now
		}

		// Truncation moves the near edge forward to where the sample stops. With
		// no killmail at all there is nothing covered to claim.
		if m.Truncated {
			if m.OldestSeen.IsZero() {
				continue
			}
			if m.OldestSeen.After(start) {
				start = m.OldestSeen
			}
		}

		if d := end.Sub(start).Seconds(); d > 0 {
			total += d
		}
	}
	return total
}

// coverageWarnings turns what the walk missed into sentences a plan can show.
//
// Each truncated month is named individually, with how much of it is missing and
// the fact that the rate was scaled to what was covered -- a reader who is about
// to spend ISK on these numbers needs to know the sample has a hole in it and
// that the arithmetic already accounts for it. Both halves matter: without the
// first the numbers look complete, and without the second they look wrong.
func coverageWarnings(walk *LossWalk, since, now time.Time, windowSeconds int) []string {
	if walk == nil {
		return nil
	}

	var warnings []string
	for _, m := range walk.Months {
		if !m.Truncated {
			continue
		}
		label := time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, time.UTC).Format("January 2006")

		start := time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, time.UTC)
		if start.Before(since) {
			start = since
		}
		missing := ""
		if !m.OldestSeen.IsZero() && m.OldestSeen.After(start) {
			missing = fmt.Sprintf(", so its oldest ~%.0f days are not in the sample",
				m.OldestSeen.Sub(start).Hours()/24)
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s hit %s%s; the destruction rate is scaled to what was actually covered, not to the full window",
			label, m.Reason, missing))
	}

	if walk.BudgetReached {
		warnings = append(warnings, fmt.Sprintf(
			"the %d-page walk budget ran out before reaching %d days back, so the oldest part of the window was never fetched",
			fwMaxLossPages, windowSeconds/86400))
	}

	covered := coveredSeconds(walk.Months, since, now)
	if requested := float64(windowSeconds); covered > 0 && covered < requested*0.9 {
		warnings = append(warnings, fmt.Sprintf(
			"the sample covers %.0f of the %.0f days asked for; rates are per-day over what was covered, but a %.0f-day sample is what is behind them",
			covered/86400, requested/86400, covered/86400))
	}

	return warnings
}

// militiaLossAccum folds pages of a militia's losses into a demand profile,
// counting only those inside the warzone.
//
// It is incremental rather than a function over a full slice because the long
// window cannot hold its fetch: ~69,000 killmails at ~1.5 KB each is ~104 MB, and
// the scope filter below discards roughly two thirds of them -- so accumulating
// the walk would mean holding all of it in order to throw most of it away. Pages
// go in, one page at a time, and nothing but the per-type histograms is retained.
//
// It is also still the whole of AnalyzeMilitiaDemand except the fetch, so the
// scope rule and the rate arithmetic stay testable without zkillboard. Tests feed
// it pages.
type militiaLossAccum struct {
	warzoneSystems map[int32]bool
	sdeData        *sde.Data

	types     map[int32]*killmailAccum
	fetched   int
	inWarzone int
	oldest    time.Time
}

func newMilitiaLossAccum(warzoneSystems map[int32]bool, sdeData *sde.Data) *militiaLossAccum {
	return &militiaLossAccum{
		warzoneSystems: warzoneSystems,
		sdeData:        sdeData,
		types:          make(map[int32]*killmailAccum),
	}
}

// Add folds one page of losses. The page is not retained.
func (a *militiaLossAccum) Add(page []ESIKillmail) {
	for i := range page {
		km := &page[i]
		a.fetched++
		if !a.warzoneSystems[km.SolarSystemID] {
			continue
		}
		a.inWarzone++
		accumulateKillmail(a.types, km, a.sdeData)

		if t, err := time.Parse(time.RFC3339, km.KillmailTime); err == nil {
			if a.oldest.IsZero() || t.Before(a.oldest) {
				a.oldest = t
			}
		}
	}
}

// Finish collapses what was folded into a profile. scale converts a window total
// into a per-day rate, and is the caller's because only the caller knows whether
// the window was actually covered.
func (a *militiaLossAccum) Finish(window int, scale float64, truncated bool) *MilitiaDemandProfile {
	profile := &MilitiaDemandProfile{
		WindowSeconds:  window,
		FetchedKills:   a.fetched,
		InWarzoneKills: a.inWarzone,
		Truncated:      truncated,
		Items:          make(map[int32]*ItemDemandProfile, len(a.types)),
		UpdatedAt:      time.Now(),
	}

	for typeID, t := range a.types {
		if !restockable(a.sdeData, typeID) {
			continue
		}
		profile.Items[typeID] = t.profile(typeID, true)
	}

	finalizeProfiles(profile.Items, scale)
	return profile
}

// aggregateMilitiaLosses is the seven-day path's aggregation: one page in, one
// profile out, scaled by the requested window with dailyScale's truncation rule.
func aggregateMilitiaLosses(losses []ESIKillmail, warzoneSystems map[int32]bool, sdeData *sde.Data, window int) *MilitiaDemandProfile {
	acc := newMilitiaLossAccum(warzoneSystems, sdeData)
	acc.Add(losses)

	truncated := acc.fetched >= militiaMaxPages*zkillPageSize
	return acc.Finish(window, dailyScale(window, truncated, acc.oldest), truncated)
}

// dailyScale converts a window total into a daily rate.
//
// The requested window is the right denominator even when the militia was quiet:
// fewer losses over seven days is less demand, not a shorter sample. Truncation
// is the exception -- zkillboard returns newest first, so hitting the page cap
// means the window was never reached, and dividing by it would under-report by
// however much was cut off. There the oldest killmail seen is the honest edge of
// what was actually covered.
func dailyScale(window int, truncated bool, oldest time.Time) float64 {
	seconds := float64(window)
	if truncated && !oldest.IsZero() {
		if covered := time.Since(oldest).Seconds(); covered > 0 && covered < seconds {
			seconds = covered
		}
	}
	if seconds <= 0 {
		return 0
	}
	return 86400 / seconds
}

// restockable rejects types this tool could never ship: officer, deadspace and
// abyssal drops, which are loot rather than stock, and anything with no market
// group, which is not traded on the market at all.
//
// With no SDE loaded nothing can be judged, so everything passes -- the same
// tolerance the classifier has.
func restockable(sdeData *sde.Data, typeID int32) bool {
	if sdeData == nil {
		return true
	}
	t, ok := sdeData.Types[typeID]
	if !ok {
		return false
	}
	switch t.MetaGroupID {
	case 5, 6, 15: // officer, deadspace, abyssal
		return false
	}
	return t.MarketGroupID != 0
}

// finalizeProfiles fills the derived rates: the per-killmail average, and the
// daily destruction rate, where dailyScale multiplies a window total into a
// per-day one.
func finalizeProfiles(items map[int32]*ItemDemandProfile, scale float64) {
	for _, profile := range items {
		if profile.KillmailCount > 0 {
			profile.AvgPerKillmail = float64(profile.TotalDestroyed) / float64(profile.KillmailCount)
		}
		profile.EstDailyDemand = float64(profile.TotalDestroyed) * scale
		if profile.Category == "ammo" {
			profile.EstDailyDemand *= combatAmmoMultiplier
		}
	}
}
