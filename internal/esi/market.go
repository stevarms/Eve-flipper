package esi

import (
	"context"
	"fmt"
	"log"
	"time"
)

// MarketOrder mirrors the ESI market order response.
type MarketOrder struct {
	OrderID      int64   `json:"order_id"`
	TypeID       int32   `json:"type_id"`
	LocationID   int64   `json:"location_id"`
	SystemID     int32   `json:"system_id"`
	Price        float64 `json:"price"`
	VolumeRemain int32   `json:"volume_remain"`
	MinVolume    int32   `json:"min_volume"`
	IsBuyOrder   bool    `json:"is_buy_order"`
	// Duration is the order's listed lifetime in days, and it is the only field
	// that separates an NPC seed from a player order: a player order caps at 90
	// days, so duration == 365 is always NPC. Measured on Black Rise's sell book,
	// 4,660 of 11,643 orders are 365-day seeds and the longest player order is
	// exactly 90 -- there is no overlap to get wrong.
	//
	// It matters because a seeded book reads as competition that will never move
	// or reprice. Hallanen lists 405 sell types, of which 55 are player orders;
	// unfiltered it looks like the deepest market in the warzone.
	Duration int32 `json:"duration"`
	// VolumeTotal is the order's original size. Every one of those 4,660 seeds has
	// VolumeRemain == VolumeTotal, so the two fields together corroborate the
	// duration test rather than resting on it alone.
	VolumeTotal int32 `json:"volume_total"`
	// Range is how far a buy order reaches: "station", "solarsystem",
	// "region", or a gate-jump count as a decimal string. Meaningless on a
	// sell order, which is always station-range in EVE.
	//
	// It matters because a region-range bid parked in another system
	// competes for every unit a seller at your station wants to move, so
	// any consumer that treats one station's book as the whole competition
	// is wrong by however much of the region is bidding over it.
	Range    string `json:"range"`
	RegionID int32  `json:"-"` // set by us
}

// IsNPCSeeded reports whether this order was seeded by an NPC corporation
// rather than placed by a player.
//
// A player order caps at 90 days, so a 365-day duration is conclusive. The rule
// lives here, on the order, because two packages need it and a second copy is a
// second thing to get wrong: half the warzone's sell book is seed, and anything
// that mistakes one for competition prices into an order that will never move.
func (o MarketOrder) IsNPCSeeded() bool {
	return o.Duration == 365
}

// MarketOrderSnapshot is a point-in-time capture of live ESI market orders.
// ESI does not expose historical order books, so callers can persist these
// snapshots as they are fetched and replay them later for orderbook backtests.
type MarketOrderSnapshot struct {
	RegionID   int32
	OrderType  string
	Source     string
	TypeID     int32
	LocationID int64
	ETag       string
	ExpiresAt  time.Time
	CapturedAt time.Time
	Orders     []MarketOrder
}

// MarketOrderRecorder persists live market order snapshots outside the ESI client.
type MarketOrderRecorder interface {
	RecordMarketOrderSnapshot(snapshot MarketOrderSnapshot) error
}

// FetchRegionOrders fetches all market orders for a region.
// Uses in-memory cache with ETag/Expires — repeated calls within the ESI refresh
// window (typically 5 min) return instantly without any network I/O.
func (c *Client) FetchRegionOrders(regionID int32, orderType string) ([]MarketOrder, error) {
	return c.FetchRegionOrdersCached(regionID, orderType)
}

// FetchRegionOrdersByType fetches all market orders for a specific type in a region.
func (c *Client) FetchRegionOrdersByType(regionID int32, typeID int32) ([]MarketOrder, error) {
	return c.FetchRegionOrdersByTypeContext(context.Background(), regionID, typeID)
}

func (c *Client) FetchRegionOrdersByTypeContext(ctx context.Context, regionID int32, typeID int32) ([]MarketOrder, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if typeID <= 0 {
		return nil, fmt.Errorf("invalid type id: %d", typeID)
	}
	cache := c.ensureOrderCache()
	if cache == nil {
		return nil, fmt.Errorf("esi client is nil")
	}
	url := fmt.Sprintf("%s/markets/%d/orders/?datasource=tranquility&order_type=all&type_id=%d",
		baseURL, regionID, typeID)

	cacheKey := orderCacheKey{RegionID: regionID, OrderType: "all", Scope: "region_type", TypeID: typeID}
	sfKey := fmt.Sprintf("region_type:%d:%d", regionID, typeID)
	result, err, _ := cache.Do(sfKey, func() (interface{}, error) {
		orders, etag, hit := cache.GetScoped(cacheKey)
		if hit {
			log.Printf("[ESI] OrderCache HIT region=%d type_id=%d (%d orders)", regionID, typeID, len(orders))
			return orders, nil
		}

		if etag != "" {
			notModified, newExpires, err := c.conditionalCheckContext(ctx, url+"&page=1", etag)
			if err == nil && notModified {
				cache.TouchScoped(cacheKey, newExpires)
				if cached, _, ok := cache.GetScoped(cacheKey); ok {
					log.Printf("[ESI] OrderCache 304 region=%d type_id=%d", regionID, typeID)
					return cached, nil
				}
			}
		}

		orders, respEtag, respExpires, err := c.getPaginatedDirectWithHeadersContext(ctx, url, regionID)
		if err != nil {
			return nil, err
		}
		cache.PutScoped(cacheKey, orders, respEtag, respExpires)
		c.recordMarketOrderSnapshot(MarketOrderSnapshot{
			RegionID:   regionID,
			OrderType:  "all",
			Source:     "region_type",
			TypeID:     typeID,
			ETag:       respEtag,
			ExpiresAt:  respExpires,
			CapturedAt: time.Now().UTC(),
			Orders:     orders,
		})
		log.Printf("[ESI] OrderCache MISS region=%d type_id=%d (%d orders, expires=%s)",
			regionID, typeID, len(orders), respExpires.Format("15:04:05"))
		return orders, nil
	})
	if err != nil {
		return nil, err
	}
	return result.([]MarketOrder), nil
}
