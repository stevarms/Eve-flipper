package esi

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Militia faction ids. These are the four faction warfare militias a character
// can enlist in; they are also what zkillboard's factionID modifier takes.
const (
	MilitiaCaldari  int32 = 500001
	MilitiaMinmatar int32 = 500002
	MilitiaAmarr    int32 = 500003
	MilitiaGallente int32 = 500004
)

// FWSystem is one contested system from /fw/systems/.
//
// OwnerFactionID is who holds sovereignty; OccupierFactionID is who holds the
// system now. They differ while a system is flipped, and it is the occupier that
// decides which militia stages and buys there.
type FWSystem struct {
	SolarSystemID          int32  `json:"solar_system_id"`
	OwnerFactionID         int32  `json:"owner_faction_id"`
	OccupierFactionID      int32  `json:"occupier_faction_id"`
	Contested              string `json:"contested"`
	VictoryPoints          int32  `json:"victory_points"`
	VictoryPointsThreshold int32  `json:"victory_points_threshold"`
}

// BulwarkSystems maps a militia to its bulwark staging system.
//
// Not derivable from ESI. None of the eight FW endpoints carries the
// designation, and the words bulwark, frontline and rearguard appear nowhere in
// the OpenAPI spec -- so this is a hand-maintained constant. All four are
// highsec, all four are absent from /fw/systems/ because they are not contested,
// and all four sit 1-2 jumps from occupied FW space, which is exactly why they
// are where militia actually buy.
//
// Verified 2026-09-16 against ESI and the local SDE. Correct here if CCP moves
// one; nothing else needs to change, because the staging ring finds these
// systems geometrically as well and this map only pins them.
var BulwarkSystems = map[int32]int32{
	MilitiaCaldari:  30045324, // Onnamon  (0.56, Black Rise)  -- 1 jump from Kinakka
	MilitiaMinmatar: 30002055, // Amo      (0.47, Metropolis)  -- 1 jump from Resbroko
	MilitiaAmarr:    30002974, // Mehatoor (0.66, Devoid)      -- 1 jump from Raa
	MilitiaGallente: 30003788, // Intaki   (0.60, Placid)      -- 2 jumps from Loes
}

// fwSystemsCacheTTL matches the endpoint's own cache header. The warzone does
// not move faster than this, and refetching 160 systems more often buys nothing.
const fwSystemsCacheTTL = 30 * time.Minute

// FWSystemsCache holds one fetch of /fw/systems/, which is a single
// unpaginated list covering the whole warzone.
type FWSystemsCache struct {
	mu        sync.RWMutex
	systems   []FWSystem
	fetchedAt time.Time
}

// NewFWSystemsCache returns an empty cache.
func NewFWSystemsCache() *FWSystemsCache {
	return &FWSystemsCache{}
}

// FetchFWSystems returns every contested faction warfare system.
//
// Unauthenticated and unpaginated: around 160 systems in one response.
func (c *Client) FetchFWSystems(ctx context.Context) ([]FWSystem, error) {
	url := fmt.Sprintf("%s/fw/systems/?datasource=tranquility", baseURL)
	var result []FWSystem
	if err := c.GetJSONContext(ctx, url, &result); err != nil {
		return nil, fmt.Errorf("fetch fw systems: %w", err)
	}
	return result, nil
}

// FetchFWSystemsCached is FetchFWSystems behind a 30 minute cache.
//
// On a failed refetch the stale copy is returned rather than an error: a
// warzone map half an hour old is a far better answer than none, and the only
// thing that changes in that time is which militia occupies a handful of
// systems.
func (c *Client) FetchFWSystemsCached(ctx context.Context, cache *FWSystemsCache) ([]FWSystem, error) {
	if cache == nil {
		return c.FetchFWSystems(ctx)
	}

	cache.mu.RLock()
	if fresh := time.Since(cache.fetchedAt) < fwSystemsCacheTTL && len(cache.systems) > 0; fresh {
		systems := cache.systems
		cache.mu.RUnlock()
		return systems, nil
	}
	stale := cache.systems
	cache.mu.RUnlock()

	systems, err := c.FetchFWSystems(ctx)
	if err != nil {
		if len(stale) > 0 {
			return stale, nil
		}
		return nil, err
	}

	cache.mu.Lock()
	cache.systems = systems
	cache.fetchedAt = time.Now()
	cache.mu.Unlock()

	return systems, nil
}

// OccupiedBy returns the systems a militia currently holds, as a set.
//
// Occupation, not ownership: a Caldari-owned system the Gallente have taken is
// stocked by Gallente pilots until it flips back, and it is who is there now
// that the demand comes from.
func OccupiedBy(systems []FWSystem, militiaFactionID int32) map[int32]bool {
	occupied := make(map[int32]bool)
	for _, s := range systems {
		if s.OccupierFactionID == militiaFactionID {
			occupied[s.SolarSystemID] = true
		}
	}
	return occupied
}

// WarzoneSystems returns every system in a militia's theatre, as a set: both
// the systems its own side holds and the systems it is attacking.
//
// This is the demand scope, and including the enemy half is the whole point. A
// Caldari pilot who dies raiding Gallente space still has to replace that ship,
// and they replace it at their own hub -- so their loss is demand there. Measured
// over 600 Caldari militia losses in one week:
//
//	32.3%  in Caldari-occupied systems
//	72.3%  anywhere in the Caldari/Gallente theatre
//	 0.0%  at Onnamon, the bulwark they stage from
//
// Restricting to systems their own militia occupies would discard more than half
// the signal, all of it from the aggressive half of the warzone. What the scope
// does exclude is dominated by Jita -- 103 of those 600 losses -- which is
// finding 5 in one number: militia membership is not a location.
//
// Theatre membership comes from sovereignty rather than occupation, because
// sovereignty is what does not move: a Caldari-owned system the Gallente have
// taken is still in the Caldari/Gallente theatre. Occupation never crosses a
// theatre, so the two readings agree today (90 systems either way) -- owner is
// used because it is the definition that cannot drift.
func WarzoneSystems(systems []FWSystem, militiaFactionID int32) map[int32]bool {
	opponent, ok := opposingMilitia[militiaFactionID]
	if !ok {
		return OccupiedBy(systems, militiaFactionID)
	}

	warzone := make(map[int32]bool)
	for _, s := range systems {
		switch s.OwnerFactionID {
		case militiaFactionID, opponent:
			warzone[s.SolarSystemID] = true
		}
	}
	return warzone
}

// opposingMilitia pairs the two faction warfare theatres: Caldari against
// Gallente, Amarr against Minmatar.
var opposingMilitia = map[int32]int32{
	MilitiaCaldari:  MilitiaGallente,
	MilitiaGallente: MilitiaCaldari,
	MilitiaAmarr:    MilitiaMinmatar,
	MilitiaMinmatar: MilitiaAmarr,
}
