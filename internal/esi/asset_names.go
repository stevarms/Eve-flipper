package esi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// asset_names.go — the names players give their containers.
//
// A stack's location resolves to a container's *type* from the assets endpoint
// alone, so everything reads "Station Container". That is enough to know it is
// not loose in the hangar and not enough to find it: a trader with six cans in
// one station learns nothing from being told it is in a can.
//
// The names live behind a separate POST, which is why they were not free with
// the rest. Only singleton items the player has actually renamed come back, so
// an unnamed can is absent from the response rather than present with an empty
// name — the caller keeps its type name for those.

// assetNamesBatch is ESI's documented ceiling for one names request.
const assetNamesBatch = 1000

// GetCharacterAssetNames resolves player-assigned names for owned items.
//
// Requires scope: esi-assets.read_assets.v1 (the same one the asset list uses,
// so no extra consent).
func (c *Client) GetCharacterAssetNames(characterID int64, itemIDs []int64, accessToken string) (map[int64]string, error) {
	url := fmt.Sprintf("%s/characters/%d/assets/names/?datasource=tranquility", baseURL, characterID)
	return c.assetNames(url, itemIDs, accessToken)
}

// GetCorporationAssetNames resolves player-assigned names for corp items.
//
// Requires scope: esi-assets.read_corporation_assets.v1 plus a role that can
// read corp assets, i.e. exactly what listing them already needed.
func (c *Client) GetCorporationAssetNames(corporationID int32, itemIDs []int64, accessToken string) (map[int64]string, error) {
	url := fmt.Sprintf("%s/corporations/%d/assets/names/?datasource=tranquility", baseURL, corporationID)
	return c.assetNames(url, itemIDs, accessToken)
}

// assetNames posts item ids in batches and merges the replies.
//
// A failed batch is skipped rather than failing the lot: names are a convenience
// on top of a location that already works, and losing all of them because one
// request of eleven timed out is the wrong trade.
func (c *Client) assetNames(url string, itemIDs []int64, accessToken string) (map[int64]string, error) {
	out := map[int64]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}

	var firstErr error
	for start := 0; start < len(itemIDs); start += assetNamesBatch {
		end := start + assetNamesBatch
		if end > len(itemIDs) {
			end = len(itemIDs)
		}

		var names []struct {
			ItemID int64  `json:"item_id"`
			Name   string `json:"name"`
		}
		if err := c.postJSONAuth(url, itemIDs[start:end], &names, accessToken); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, n := range names {
			// ESI returns "None" for an item that exists but carries no player
			// name. Storing that would put the word None on the row, which is
			// worse than falling back to the type name.
			if n.Name == "" || n.Name == "None" {
				continue
			}
			out[n.ItemID] = n.Name
		}
	}
	// Partial success is still success: the caller falls back to type names for
	// whatever is missing.
	if len(out) > 0 {
		return out, nil
	}
	return out, firstErr
}

// postJSONAuth is PostJSON with an Authorization header.
//
// Separate rather than a parameter on PostJSON because every existing caller of
// that is unauthenticated, and widening its signature would touch all of them
// for no benefit.
func (c *Client) postJSONAuth(url string, body interface{}, dst interface{}, accessToken string) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal asset-names body: %w", err)
	}

	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "eve-flipper/1.0 (github.com)")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ESI POST %s %d: %s", url, resp.StatusCode, string(msg))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
