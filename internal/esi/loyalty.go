package esi

import (
	"fmt"
	"sync"
	"time"
)

// loyalty.go -- LP store offers, a character's LP balances, and the names of
// the corporations those belong to.

// LoyaltyOffer is one offer in an NPC corporation's LP store.
//
// For a blueprint, Quantity is the number of runs on the single copy the store
// hands over (the quantity-10 Raven Navy Issue offer is one 10-run copy).
// AKCost is Analysis Kredits, a separate currency the LP tool ignores.
type LoyaltyOffer struct {
	OfferID       int32                 `json:"offer_id"`
	TypeID        int32                 `json:"type_id"`
	Quantity      int64                 `json:"quantity"`
	LPCost        int64                 `json:"lp_cost"`
	ISKCost       float64               `json:"isk_cost"`
	AKCost        int64                 `json:"ak_cost"`
	RequiredItems []LoyaltyRequiredItem `json:"required_items"`
}

// LoyaltyRequiredItem is an item the store takes alongside LP and ISK.
type LoyaltyRequiredItem struct {
	TypeID   int32 `json:"type_id"`
	Quantity int64 `json:"quantity"`
}

// CharacterLoyaltyPoints is one corporation's LP balance for a character.
type CharacterLoyaltyPoints struct {
	CorporationID int32 `json:"corporation_id"`
	LoyaltyPoints int64 `json:"loyalty_points"`
}

// loyaltyOffersTTL matches ESI's own cache on the offers route: stores change
// with patches, not by the minute, so an hour of reuse costs nothing real and
// spares a 387-offer fetch on every re-analysis.
const loyaltyOffersTTL = time.Hour

type loyaltyOffersEntry struct {
	offers  []LoyaltyOffer
	fetched time.Time
}

var (
	loyaltyOffersMu    sync.Mutex
	loyaltyOffersCache = map[string]loyaltyOffersEntry{}
	entityNamesCache   sync.Map // int64 -> string
)

// FetchLoyaltyOffers returns a corporation's LP store offers, from a one-hour
// cache when it can.
func (c *Client) FetchLoyaltyOffers(corpID int32) ([]LoyaltyOffer, error) {
	url := fmt.Sprintf("%s/loyalty/stores/%d/offers/?datasource=tranquility", baseURL, corpID)
	return c.fetchLoyaltyOffersFrom(url, time.Now())
}

func (c *Client) fetchLoyaltyOffersFrom(url string, now time.Time) ([]LoyaltyOffer, error) {
	loyaltyOffersMu.Lock()
	entry, ok := loyaltyOffersCache[url]
	loyaltyOffersMu.Unlock()
	if ok && now.Sub(entry.fetched) < loyaltyOffersTTL {
		return entry.offers, nil
	}

	var offers []LoyaltyOffer
	if err := c.GetJSON(url, &offers); err != nil {
		return nil, fmt.Errorf("loyalty store offers: %w", err)
	}
	if offers == nil {
		offers = []LoyaltyOffer{}
	}
	loyaltyOffersMu.Lock()
	loyaltyOffersCache[url] = loyaltyOffersEntry{offers: offers, fetched: now}
	loyaltyOffersMu.Unlock()
	return offers, nil
}

// GetCharacterLoyaltyPoints needs esi-characters.read_loyalty.v1. A token
// without it gets a 403 from ESI, which comes back as an error here; the
// caller decides that means "not available" rather than "broken".
func (c *Client) GetCharacterLoyaltyPoints(characterID int64, accessToken string) ([]CharacterLoyaltyPoints, error) {
	url := fmt.Sprintf("%s/characters/%d/loyalty/points/?datasource=tranquility", baseURL, characterID)
	var points []CharacterLoyaltyPoints
	if err := c.AuthGetJSON(url, accessToken, &points); err != nil {
		return nil, fmt.Errorf("loyalty points: %w", err)
	}
	return points, nil
}

// ResolveNames names corporations (or any other entity ESI can name) through
// POST /universe/names/. Names never change for NPC corporations, so every
// answer is kept for the life of the process.
func (c *Client) ResolveNames(ids []int64) (map[int64]string, error) {
	return c.resolveNamesAt(baseURL+"/universe/names/?datasource=tranquility", ids)
}

func (c *Client) resolveNamesAt(url string, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	var missing []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if name, ok := entityNamesCache.Load(id); ok {
			out[id] = name.(string)
			continue
		}
		missing = append(missing, id)
	}
	// ESI takes at most 1000 IDs a call.
	for start := 0; start < len(missing); start += 1000 {
		end := start + 1000
		if end > len(missing) {
			end = len(missing)
		}
		var resp []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := c.PostJSON(url, missing[start:end], &resp); err != nil {
			return out, fmt.Errorf("universe names: %w", err)
		}
		for _, r := range resp {
			entityNamesCache.Store(r.ID, r.Name)
			out[r.ID] = r.Name
		}
	}
	return out, nil
}
