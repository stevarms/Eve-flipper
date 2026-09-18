package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"eve-flipper/internal/db"
	"eve-flipper/internal/sde"
	"eve-flipper/internal/zkillboard"
)

// fw_demand.go -- fetching and caching the destruction rates the gap table sizes
// against.
//
// There are two windows, and the difference between them is the whole reason this
// file exists rather than one call in fw_plan.go. Seven days is one request path,
// twenty-five pages and eleven megabytes; ninety days is a different request path,
// three hundred pages and a hundred and thirty-five megabytes, walked calendar
// month by calendar month against a per-month page limit that a busy militia
// exceeds. The first can be refetched whenever; the second cannot, so it is
// cached, and caching it is what makes the long window usable at all.
//
// Which means the cache has to carry more than the rates. A long walk can come
// back with a hole in the middle of it, and the rates are already scaled to the
// time actually covered -- so they are correct, and they look complete. The
// sentence saying they are not is stored beside them in demand_scope_coverage,
// because a warning dropped on the way to disk is worse than no cache: the second
// read is the one that looks authoritative.

const (
	// The two long windows offered. Thirty days is two partial months and almost
	// no truncation; ninety is the practical end of the road, because every month
	// past the first two needs ~119 pages against a 100-page cap, so the missing
	// fraction grows while the signal ages.
	fwLongWindow30d = 2592000
	fwLongWindow90d = 7776000

	// fwDemandFreshnessDivisor sets how long a sample stays servable, as a
	// fraction of the window it measures. A quarter's destruction rate does not
	// move hour to hour, and a seven-day rate does not move much faster; what
	// differs is the cost of being wrong about that, since refetching ninety days
	// is a two-minute walk. Window/28 puts seven days at ~6h and ninety at
	// ~3 days.
	fwDemandFreshnessDivisor = 28
)

// fwNormalizeLongWindow reduces a requested long window to one that means
// something.
//
// Zero is off. A window no longer than the short one is also off rather than an
// error: it would fetch the same seven days twice and show a reader two identical
// columns, which is a worse answer than one column. Above ninety days it clamps,
// because the walk budget is what stops a mis-set window becoming an unbounded
// fetch and silently walking past it is not a service.
func fwNormalizeLongWindow(seconds int) int {
	switch {
	case seconds <= fwDemandWindowSeconds:
		return 0
	case seconds > fwLongWindow90d:
		return fwLongWindow90d
	default:
		return seconds
	}
}

// fwDemandFreshness is how old a cached sample may be and still be served.
func fwDemandFreshness(windowSeconds int) time.Duration {
	if windowSeconds <= 0 {
		windowSeconds = fwDemandWindowSeconds
	}
	age := time.Duration(windowSeconds/fwDemandFreshnessDivisor) * time.Second
	// A floor, so a hypothetical tiny window cannot turn into a refetch per
	// request. Nothing configurable reaches it today.
	if age < time.Hour {
		age = time.Hour
	}
	return age
}

// fwMilitiaDemand returns a militia's in-warzone destruction rates over one
// window, from cache when the cache is fresh enough and from zkillboard when it
// is not.
//
// The cache is checked before the analyzer rather than inside it because the
// windows have different costs and therefore different freshness, and the
// analyzer has no idea which window it has been handed is the expensive one.
func (s *Server) fwMilitiaDemand(
	ctx context.Context,
	factionID int32,
	warzone map[int32]bool,
	windowSeconds int,
	sdeData *sde.Data,
) (*zkillboard.MilitiaDemandProfile, error) {
	if windowSeconds <= 0 {
		return nil, fmt.Errorf("demand window of %d seconds measures nothing", windowSeconds)
	}
	scope := db.MilitiaDemandScope(factionID, int64(windowSeconds))

	if s.db != nil && s.db.IsFittingProfileFresh(scope, fwDemandFreshness(windowSeconds)) {
		profile, err := s.fwCachedMilitiaDemand(scope, windowSeconds)
		switch {
		case err != nil:
			// An unreadable cache is a reason to refetch, not to fail: the rates
			// are reproducible, and pretending they are not would turn a bad row
			// into a broken plan.
			log.Printf("[FW] cached demand %s could not be read, refetching: %v", scope, err)
		case profile != nil:
			return profile, nil
		}
	}

	if s.demandAnalyzer == nil {
		return nil, fmt.Errorf("no killmail analyzer is configured, so demand could not be measured")
	}
	profile, err := s.demandAnalyzer.AnalyzeMilitiaDemand(ctx, factionID, warzone, windowSeconds, sdeData)
	if err != nil {
		return nil, err
	}
	s.fwStoreMilitiaDemand(scope, profile)
	return profile, nil
}

// fwCachedMilitiaDemand rebuilds a profile from the two cached halves, or returns
// nil when there is nothing stored.
func (s *Server) fwCachedMilitiaDemand(scope db.DemandScope, windowSeconds int) (*zkillboard.MilitiaDemandProfile, error) {
	items, err := s.db.GetFittingDemandProfile(scope)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}

	profile := &zkillboard.MilitiaDemandProfile{
		MilitiaFactionID: int32(scope.ID),
		WindowSeconds:    windowSeconds,
		Items:            make(map[int32]*zkillboard.ItemDemandProfile, len(items)),
		UpdatedAt:        items[0].UpdatedAt,
	}
	for _, item := range items {
		profile.Items[item.TypeID] = &zkillboard.ItemDemandProfile{
			TypeID:         item.TypeID,
			TypeName:       item.TypeName,
			Category:       item.Category,
			TotalDestroyed: item.TotalDestroyed,
			KillmailCount:  item.KillmailCount,
			AvgPerKillmail: item.AvgPerKillmail,
			EstDailyDemand: item.EstDailyDemand,
		}
		if item.UpdatedAt.After(profile.UpdatedAt) {
			profile.UpdatedAt = item.UpdatedAt
		}
	}

	cov, err := s.db.GetDemandScopeCoverage(scope)
	if err != nil {
		// The rates are usable without it; what is lost is the ability to say how
		// complete they are, and that gets said.
		log.Printf("[FW] coverage record for %s could not be read: %v", scope, err)
		cov = nil
	}
	if cov == nil {
		profile.Warnings = append(profile.Warnings, fmt.Sprintf(
			"these %d-day rates come from a cached sample with no coverage record beside it, so how much of the window it actually spans is unknown",
			windowSeconds/86400))
		return profile, nil
	}

	profile.FetchedKills = cov.FetchedKills
	profile.InWarzoneKills = cov.InWarzoneKills
	profile.CoveredSeconds = cov.CoveredSeconds
	profile.Truncated = cov.Truncated
	profile.Warnings = append(profile.Warnings, cov.Warnings...)
	if cov.MonthsJSON != "" {
		if err := json.Unmarshal([]byte(cov.MonthsJSON), &profile.Months); err != nil {
			profile.Warnings = append(profile.Warnings,
				"the per-month coverage of this cached demand sample could not be read back, so only its summary is shown")
		}
	}
	return profile, nil
}

// fwStoreMilitiaDemand caches a freshly measured profile.
//
// The rates go down first and the coverage record second, deliberately. Failing
// between them leaves rates whose completeness is unknown, which is a sentence the
// reader gets shown; the other order would leave a coverage record claiming to
// describe rates that were never written.
//
// A failed write is logged and not returned. The profile in hand is correct and
// the plan can be built from it; losing the cache costs the next regeneration a
// fetch, which is not worth failing a request the user asked for.
func (s *Server) fwStoreMilitiaDemand(scope db.DemandScope, profile *zkillboard.MilitiaDemandProfile) {
	if s.db == nil || profile == nil || len(profile.Items) == 0 {
		return
	}

	items := make([]db.FittingDemandItem, 0, len(profile.Items))
	for _, item := range profile.Items {
		if item == nil {
			continue
		}
		// SampledKills and TotalKills24h are left at zero on purpose. They are the
		// region analyzer's two counters, and a militia profile's equivalents live
		// in demand_scope_coverage under their own names rather than borrowing
		// columns that mean something else.
		items = append(items, db.FittingDemandItem{
			TypeID:         item.TypeID,
			TypeName:       item.TypeName,
			Category:       item.Category,
			TotalDestroyed: item.TotalDestroyed,
			KillmailCount:  item.KillmailCount,
			AvgPerKillmail: item.AvgPerKillmail,
			EstDailyDemand: item.EstDailyDemand,
		})
	}
	if err := s.db.SaveFittingDemandProfile(scope, items); err != nil {
		log.Printf("[FW] could not cache demand %s: %v", scope, err)
		return
	}

	monthsJSON := ""
	if len(profile.Months) > 0 {
		if b, err := json.Marshal(profile.Months); err == nil {
			monthsJSON = string(b)
		}
	}
	covered := profile.CoveredSeconds
	if covered <= 0 {
		// The short path does not measure coverage because it cannot have a hole:
		// one request path, one contiguous sample. Recording the window it asked
		// for is the true statement there, and it keeps a reader from having to
		// know which path produced the row.
		covered = float64(profile.WindowSeconds)
	}
	if err := s.db.SaveDemandScopeCoverage(db.DemandScopeCoverage{
		Scope:          scope,
		FetchedKills:   profile.FetchedKills,
		InWarzoneKills: profile.InWarzoneKills,
		CoveredSeconds: covered,
		Truncated:      profile.Truncated,
		Warnings:       profile.Warnings,
		MonthsJSON:     monthsJSON,
	}); err != nil {
		log.Printf("[FW] could not cache demand coverage %s: %v", scope, err)
	}
}
