package engine

import (
	"math"
	"sort"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/graph"
	"eve-flipper/internal/sde"
)

// DefaultMaxJumpsFromFront is how far outside the front a staging system may sit.
//
// Two, and it is a measurement rather than a preference. At radius 1 the ring
// misses Intaki, the Gallente bulwark, which sits 2 jumps from the nearest
// occupied system; at radius 2 all four bulwarks are inside it, at 41-49 station
// systems per militia, which is small enough to rank and eyeball. Radius 3 buys
// nothing but noise.
const DefaultMaxJumpsFromFront = 2

// FWStagingCandidate is one station that could serve as a campaign destination.
//
// The geometry fields are derived from the gate graph alone. The market fields
// are filled in afterwards from an order book, because a candidate list has to
// be cheap: forty stations is forty market fetches, and the geometry narrows
// that set before anything is fetched.
type FWStagingCandidate struct {
	StationID   int64   `json:"station_id"`
	StationName string  `json:"station_name"`
	SystemID    int32   `json:"system_id"`
	SystemName  string  `json:"system_name"`
	RegionID    int32   `json:"region_id"`
	Security    float64 `json:"security"`

	// IsBulwark marks a system from the maintained bulwark constant, and Pinned a
	// system the user pinned. A bulwark is seeded as a pin, so it is normally
	// both -- but they are separate flags because the badge and the reason it is
	// in the list are different facts, and a pin can be removed.
	IsBulwark bool `json:"is_bulwark"`
	Pinned    bool `json:"pinned"`
	// InRing is false for a candidate that only appears because it was pinned.
	// Dropping MaxJumpsFromFront to 1 must remove Intaki from the ring while
	// leaving it in the list as a pin, and this is the field that says which.
	InRing bool `json:"in_ring"`

	// JumpsToFront is the distance to the nearest frontline system: how far the
	// militia has to travel to spend what they buy here. Zero means the station
	// is in contested space itself.
	JumpsToFront int `json:"jumps_to_front"`
	// JumpsFromSource and LowsecJumpsFromSource describe the supply route from
	// the trade hub. The lowsec count is the cheap half of haul risk -- the
	// expensive half is gankcheck, which runs later and only for a chosen route.
	JumpsFromSource       int  `json:"jumps_from_source"`
	LowsecJumpsFromSource int  `json:"lowsec_jumps_from_source"`
	HighsecRoute          bool `json:"highsec_route"`

	// Market depth, filled by ScoreStagingCandidates. PlayerOrders and
	// PlayerTypes exclude NPC seeds; FWItemOverlap counts stocked types that the
	// warzone is actually destroying.
	PlayerOrders  int `json:"player_orders"`
	PlayerTypes   int `json:"player_types"`
	FWItemOverlap int `json:"fw_item_overlap"`

	Score float64 `json:"score"`
}

// highsecMinimum is the security at which a system stops being highsec. Below
// this a freighter can be stopped without a CONCORD response.
const highsecMinimum = 0.45

// RingOptions configures the derivation. SourceSystemID is the trade hub the
// stock is bought at; a zero value skips the supply-route fields rather than
// guessing at Jita.
type RingOptions struct {
	MaxJumpsFromFront int
	SourceSystemID    int32
	// PinnedSystems always appear, whether or not the ring reaches them.
	// ExcludedSystems never appear, even if pinned -- exclusion is the stronger
	// statement, so a bulwark written off stays written off.
	PinnedSystems   map[int32]bool
	ExcludedSystems map[int32]bool
	// BulwarkSystemID is this militia's bulwark, badged and pinned. Zero for a
	// militia with none.
	BulwarkSystemID int32
}

// DeriveStagingRing returns every station system within MaxJumpsFromFront of the
// frontline, plus any pinned system, minus any excluded one.
//
// The ring is what makes the tool work on geometry rather than on a list of hub
// names. It deliberately includes non-FW highsec, because that is where the
// bulwarks are and where the militia actually buys: Onnamon holds 3,144 player
// sell orders against Villasen's 569, and pays a higher markup despite being
// safe. Restricting candidates to /fw/systems/ hides the best destination
// entirely, which is the mistake this function exists to avoid.
//
// The bulwark constant only pins. It never excludes, and it never short-circuits
// the ranking, so a bulwark that has gone quiet still has to earn its place --
// and the ring stays the discovery path for the good staging systems nobody has
// named yet.
func DeriveStagingRing(universe *graph.Universe, sdeData *sde.Data, frontline map[int32]bool, opts RingOptions) []FWStagingCandidate {
	if universe == nil || sdeData == nil {
		return nil
	}

	radius := opts.MaxJumpsFromFront
	if radius <= 0 {
		radius = DefaultMaxJumpsFromFront
	}

	ring := jumpsToNearest(universe, frontline, radius)

	// Pinned systems join the candidate set even when the ring does not reach
	// them, with their true distance to the front measured separately so the
	// column stays honest.
	candidateSystems := make(map[int32]int, len(ring)+len(opts.PinnedSystems)+1)
	for systemID, jumps := range ring {
		candidateSystems[systemID] = jumps
	}
	pinned := make(map[int32]bool, len(opts.PinnedSystems)+1)
	for systemID := range opts.PinnedSystems {
		pinned[systemID] = true
	}
	if opts.BulwarkSystemID > 0 {
		pinned[opts.BulwarkSystemID] = true
	}
	for systemID := range pinned {
		if _, ok := candidateSystems[systemID]; !ok {
			candidateSystems[systemID] = distanceToSet(universe, systemID, frontline)
		}
	}

	stationsBySystem := stationsBySystem(sdeData)

	var out []FWStagingCandidate
	for systemID, jumpsToFront := range candidateSystems {
		if opts.ExcludedSystems[systemID] {
			continue
		}
		stations := stationsBySystem[systemID]
		if len(stations) == 0 {
			continue // nothing to list from
		}

		system := sdeData.Systems[systemID]
		security, regionID, systemName := 0.0, int32(0), ""
		if system != nil {
			security, regionID, systemName = system.Security, system.RegionID, system.Name
		}

		jumpsFromSource, lowsecJumps, highsecRoute := supplyRoute(universe, opts.SourceSystemID, systemID)
		_, inRing := ring[systemID]

		for _, station := range stations {
			out = append(out, FWStagingCandidate{
				StationID:             station.ID,
				StationName:           station.Name,
				SystemID:              systemID,
				SystemName:            systemName,
				RegionID:              regionID,
				Security:              security,
				IsBulwark:             systemID == opts.BulwarkSystemID,
				Pinned:                pinned[systemID],
				InRing:                inRing,
				JumpsToFront:          jumpsToFront,
				JumpsFromSource:       jumpsFromSource,
				LowsecJumpsFromSource: lowsecJumps,
				HighsecRoute:          highsecRoute,
			})
		}
	}

	// Stable ordering before any market data exists, so the list is reproducible.
	sort.Slice(out, func(i, j int) bool {
		if out[i].JumpsToFront != out[j].JumpsToFront {
			return out[i].JumpsToFront < out[j].JumpsToFront
		}
		return out[i].StationID < out[j].StationID
	})
	return out
}

// StagingDepth is one station's player-order depth: how much of a real market
// sits there, and how much of it is stock the warzone is actually consuming.
//
// Every count here excludes NPC seeds. Onnamon III lists 562 sell types of which
// 152 are player orders, and Hallanen 405 of which 55 -- so an unfiltered book
// overstates the market by up to sevenfold, and would rank a station nobody
// trades at above one where the militia actually shops.
type StagingDepth struct {
	Orders  int `json:"orders"`
	Types   int `json:"types"`
	Overlap int `json:"overlap"`
}

// MeasureStagingDepth reduces a sell book to one StagingDepth per station,
// counting player orders only and crossing stocked types against destroyed ones.
//
// It takes orders rather than a client because the ring is derived from geometry
// first: one region fetch feeds every candidate in it, instead of one fetch per
// candidate.
func MeasureStagingDepth(orders []esi.MarketOrder, destroyedTypes map[int32]bool) map[int64]StagingDepth {
	seen := make(map[int64]map[int32]bool)
	counts := make(map[int64]StagingDepth)
	for _, order := range orders {
		if order.IsNPCSeeded() {
			continue
		}
		depth := counts[order.LocationID]
		depth.Orders++
		types := seen[order.LocationID]
		if types == nil {
			types = make(map[int32]bool)
			seen[order.LocationID] = types
		}
		if !types[order.TypeID] {
			types[order.TypeID] = true
			depth.Types++
			if destroyedTypes[order.TypeID] {
				depth.Overlap++
			}
		}
		counts[order.LocationID] = depth
	}
	return counts
}

// Score weights. Demand evidence carries 0.70 of the score between overlap and
// breadth, because a station where nothing sells cannot be redeemed by being
// safe, while a dangerous station where the militia actually shops still works.
//
// The remaining 0.30 is cost, not opportunity: distance to the front is the
// buyer's inconvenience, the route terms are mine, and none of them is worth
// more than the demand gap it would otherwise paper over.
const (
	weightOverlap      = 0.50
	weightBreadth      = 0.20
	weightJumpsToFront = 0.10
	weightRoute        = 0.12
	weightSecurity     = 0.08
)

// Reference points for the two log-scaled demand terms, both measured rather
// than chosen. Onnamon IV -- the best Caldari destination found -- stocks 636 of
// the types being destroyed out of 1,999 player types, so a station reaching
// those numbers is a fully served market and scores 1.0.
//
// Log scaling because the range is 4 to 1,999 and the interesting differences are
// at the bottom: 34 overlap versus 157 is the difference between a dead station
// and a working one, while 600 versus 636 is noise.
const (
	overlapReference = 500.0
	breadthReference = 2000.0
)

// ScoreStagingCandidates attaches market depth to each candidate, scores it out
// of 100, and sorts best-first.
//
// The score is a default sort order, not a verdict. Every term it weighs stays
// on the row -- orders, types, overlap, jumps to front, lowsec jumps, security --
// because "safe and deep, one jump out" against "captive and thin, on the front"
// is a judgement the trader makes, and a single number that hid the tradeoff
// would be answering a different question than the one being asked.
//
// maxJumpsFromFront scales the distance term only; pass the radius the ring was
// derived with so a pinned system beyond it scores zero there rather than
// negative.
func ScoreStagingCandidates(candidates []FWStagingCandidate, depth map[int64]StagingDepth, maxJumpsFromFront int) []FWStagingCandidate {
	radius := maxJumpsFromFront
	if radius <= 0 {
		radius = DefaultMaxJumpsFromFront
	}

	for i := range candidates {
		c := &candidates[i]
		d := depth[c.StationID]
		c.PlayerOrders = d.Orders
		c.PlayerTypes = d.Types
		c.FWItemOverlap = d.Overlap

		score := weightOverlap * logScore(float64(d.Overlap), overlapReference)
		score += weightBreadth * logScore(float64(d.Types), breadthReference)
		score += weightJumpsToFront * frontScore(c.JumpsToFront, radius)
		score += weightRoute * routeScore(c.JumpsFromSource, c.LowsecJumpsFromSource, c.HighsecRoute)
		score += weightSecurity * securityScore(c.Security)
		c.Score = score * 100
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].StationID < candidates[j].StationID
	})
	return candidates
}

// logScore maps a count onto 0..1 against a reference count, compressing the top
// of the range. A station at or above the reference scores 1.0; zero scores zero.
func logScore(value, reference float64) float64 {
	if value <= 0 || reference <= 0 {
		return 0
	}
	return math.Min(1, math.Log1p(value)/math.Log1p(reference))
}

// frontScore is 1.0 on the front and falls linearly to zero one jump past the
// ring's edge. A negative distance means no frontline system is reachable at all,
// which scores zero rather than looking close.
func frontScore(jumpsToFront, radius int) float64 {
	if jumpsToFront < 0 {
		return 0
	}
	return math.Max(0, 1-float64(jumpsToFront)/float64(radius+1))
}

// routeScore rewards a haul that stays in highsec and penalises one that does
// not by how much lowsec it crosses.
//
// With no source hub configured every candidate scores 1.0 here, so the term
// drops out of the ranking rather than quietly favouring anything -- an
// unmeasured route is not a safe one.
func routeScore(jumpsFromSource, lowsecJumps int, highsecRoute bool) float64 {
	if jumpsFromSource < 0 {
		return 0 // no route at all
	}
	if highsecRoute {
		return 1
	}
	return 1 / float64(1+lowsecJumps)
}

// securityScore is 1.0 for highsec and falls to zero at nullsec. It is about
// holding stock at the destination, not about the trip there.
func securityScore(security float64) float64 {
	if security >= highsecMinimum {
		return 1
	}
	if security <= 0 {
		return 0
	}
	return security / highsecMinimum
}

// jumpsToNearest is a multi-source breadth-first walk outward from every
// frontline system at once, returning each reachable system's distance to the
// closest one.
//
// Multi-source rather than one search per origin: the frontline is 25-55 systems,
// and their radius-2 closures overlap heavily, so walking them together is one
// pass instead of fifty-five.
func jumpsToNearest(universe *graph.Universe, frontline map[int32]bool, radius int) map[int32]int {
	dist := make(map[int32]int, len(frontline)*8)
	queue := make([]int32, 0, len(frontline)*8)
	for systemID := range frontline {
		dist[systemID] = 0
		queue = append(queue, systemID)
	}
	// The frontline set comes out of a map, so fix an order or the walk is
	// nondeterministic in which equal-distance path it finds first.
	sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })

	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if dist[current] >= radius {
			continue
		}
		neighbors := append([]int32(nil), universe.Adj[current]...)
		sort.Slice(neighbors, func(i, j int) bool { return neighbors[i] < neighbors[j] })
		for _, neighbor := range neighbors {
			if _, seen := dist[neighbor]; seen {
				continue
			}
			dist[neighbor] = dist[current] + 1
			queue = append(queue, neighbor)
		}
	}
	return dist
}

// distanceToSet returns the shortest jump count from one system to any member of
// a set, or -1 when none is reachable. Used only for pinned systems outside the
// ring, which are few.
func distanceToSet(universe *graph.Universe, from int32, set map[int32]bool) int {
	if set[from] {
		return 0
	}
	best := -1
	for target := range set {
		d := universe.ShortestPath(from, target)
		if d < 0 {
			continue
		}
		if best < 0 || d < best {
			best = d
		}
	}
	return best
}

// supplyRoute measures the haul from the source hub: total jumps, how many of
// them leave highsec, and whether an all-highsec route exists at all.
//
// The lowsec count is taken from the shortest route, not the safest, because that
// is the route being measured -- HighsecRoute answers separately whether a safe
// one exists. A destination with an all-highsec route is the difference between
// a supply run and a gamble, and it is the reason the ring reaches past the
// front in the first place.
func supplyRoute(universe *graph.Universe, sourceSystemID, destSystemID int32) (jumps, lowsecJumps int, highsecRoute bool) {
	if sourceSystemID <= 0 || sourceSystemID == destSystemID {
		return 0, 0, sourceSystemID == destSystemID
	}

	jumps = universe.ShortestPath(sourceSystemID, destSystemID)
	if jumps < 0 {
		return -1, 0, false
	}

	if path := universe.GetPath(sourceSystemID, destSystemID, 0); path != nil {
		for _, systemID := range path {
			if systemID == sourceSystemID {
				continue
			}
			if sec, ok := universe.SystemSecurity[systemID]; ok && sec < highsecMinimum {
				lowsecJumps++
			}
		}
	}

	highsecRoute = universe.ShortestPathMinSecurity(sourceSystemID, destSystemID, highsecMinimum) >= 0
	return jumps, lowsecJumps, highsecRoute
}

// stationsBySystem indexes NPC stations by their system.
func stationsBySystem(sdeData *sde.Data) map[int32][]*sde.Station {
	index := make(map[int32][]*sde.Station, len(sdeData.Stations)/2+1)
	for _, station := range sdeData.Stations {
		index[station.SystemID] = append(index[station.SystemID], station)
	}
	for systemID := range index {
		stations := index[systemID]
		sort.Slice(stations, func(i, j int) bool { return stations[i].ID < stations[j].ID })
	}
	return index
}
