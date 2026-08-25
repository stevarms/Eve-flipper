package engine

import "math"

// pricing.go — EVE-legal price helpers shared across the engine and the api
// layer. EVE enforces a 4-significant-digit rule on market order prices: at
// magnitude M, the smallest legal step is 10^(M-3). So a price near 12.3M
// steps in 10k increments; near 12.3B in 1M increments; near 12.3 ISK in
// 0.01 increments.
//
// These functions are the single source of truth for "what price would EVE
// actually accept". Reused by:
//   - internal/engine/undercut.go       (Order Desk suggested reprice)
//   - internal/engine/order_desk.go     (Command Center suggested reprice)
//   - internal/api/price_audit.go       (multisell "Import Prices" workflow)
//   - internal/api/hub_allocate.go      (hub allocator sell price)
//   - internal/api/pi_factory.go        (PI output undercut display)

// NextSellUndercut returns the largest EVE-legal price strictly less than
// lowestSell. Snaps to the 4-sig-fig grid at the appropriate magnitude.
// Returns 0 when the input isn't a positive finite number.
func NextSellUndercut(lowestSell float64) float64 {
	if !(lowestSell > 0) || math.IsInf(lowestSell, 0) || math.IsNaN(lowestSell) {
		return 0
	}
	place := priceStep(lowestSell)
	if place <= 0 {
		return 0
	}
	// Snap down to the nearest 4-sig-fig grid.
	floored := math.Floor(lowestSell/place) * place
	if floored < lowestSell {
		return SnapToGrid(floored, place)
	}
	// Already on a valid boundary — step down one place.
	stepped := lowestSell - place
	if stepped <= 0 {
		return 0
	}
	return SnapToGrid(stepped, place)
}

// NextBuyOverbid returns the smallest EVE-legal price strictly greater than
// topBuy. Mirror of NextSellUndercut for the bid side: snaps UP to the next
// 4-sig-fig grid boundary above topBuy; when topBuy is already on a
// boundary, adds one place value. Returns 0 for non-positive input.
//
// Note: as topBuy crosses a power-of-ten boundary (say 999,900 → 1,000,000)
// the place value jumps from 100 → 1000. Snapping up from just under the
// boundary lands on the boundary itself (a valid legal price), then the
// next call uses the larger step. This matches EVE's behaviour.
func NextBuyOverbid(topBuy float64) float64 {
	if !(topBuy > 0) || math.IsInf(topBuy, 0) || math.IsNaN(topBuy) {
		return 0
	}
	place := priceStep(topBuy)
	if place <= 0 {
		return 0
	}
	// Snap up to the nearest 4-sig-fig grid.
	ceiled := math.Ceil(topBuy/place) * place
	if ceiled > topBuy {
		return SnapToGrid(ceiled, place)
	}
	// Already on a valid boundary — step up one place.
	return SnapToGrid(topBuy+place, place)
}

// SnapToGrid rounds value to the nearest multiple of place, scrubbing
// IEEE-754 noise from subsequent add/subtract (e.g. `19.17 - 0.01`
// producing `19.169999...998`). After this the value is a clean multiple
// of place and JSON-encodes as the expected decimal string.
func SnapToGrid(value, place float64) float64 {
	if place <= 0 {
		return value
	}
	return math.Round(value/place) * place
}

// priceStep returns the 4-sig-fig place value at the magnitude of price.
// Kept unexported since the two public helpers cover every documented use
// case and a raw place-value is rarely what a caller wants.
func priceStep(price float64) float64 {
	magnitude := math.Floor(math.Log10(price))
	return math.Pow(10, magnitude-3)
}
