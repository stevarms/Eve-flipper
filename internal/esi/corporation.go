package esi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// CorporationBlueprint mirrors the character blueprint shape — ESI returns the
// same fields for both /characters/{id}/blueprints/ and /corporations/{id}/blueprints/.
type CorporationBlueprint struct {
	ItemID             int64  `json:"item_id"`
	TypeID             int32  `json:"type_id"`
	LocationID         int64  `json:"location_id"`
	LocationFlag       string `json:"location_flag"`
	Quantity           int64  `json:"quantity"`
	TimeEfficiency     int32  `json:"time_efficiency"`
	MaterialEfficiency int32  `json:"material_efficiency"`
	Runs               int64  `json:"runs"`
}

// GetCorporationBlueprints fetches all pages of corporation-owned blueprints.
// The token's character must hold the Director role; otherwise ESI returns 403.
//
// Requires scope: esi-corporations.read_blueprints.v1
func (c *Client) GetCorporationBlueprints(corporationID int32, accessToken string) ([]CorporationBlueprint, error) {
	blueprintsURL := fmt.Sprintf("%s/corporations/%d/blueprints/?datasource=tranquility", baseURL, corporationID)

	c.sem <- struct{}{}

	req, err := http.NewRequest("GET", blueprintsURL+"&page=1", nil)
	if err != nil {
		<-c.sem
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "eve-flipper/1.0 (github.com)")

	resp, err := c.http.Do(req)
	if err != nil {
		<-c.sem
		return nil, fmt.Errorf("corp blueprints page 1: %w", err)
	}

	totalPages := 1
	if p := resp.Header.Get("X-Pages"); p != "" {
		if tp, parseErr := strconv.Atoi(p); parseErr == nil && tp > 1 {
			totalPages = tp
		}
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		<-c.sem
		return nil, fmt.Errorf("corp blueprints: ESI %d: %s", resp.StatusCode, string(body))
	}

	var page1 []CorporationBlueprint
	decErr := json.NewDecoder(resp.Body).Decode(&page1)
	resp.Body.Close()
	<-c.sem
	if decErr != nil {
		return nil, fmt.Errorf("corp blueprints decode: %w", decErr)
	}

	if totalPages <= 1 {
		return page1, nil
	}

	type pageResult struct {
		data []CorporationBlueprint
		err  error
	}
	results := make(chan pageResult, totalPages-1)

	for p := 2; p <= totalPages; p++ {
		go func(pageNum int) {
			pageURL := fmt.Sprintf("%s&page=%d", blueprintsURL, pageNum)
			var data []CorporationBlueprint
			if fetchErr := c.AuthGetJSON(pageURL, accessToken, &data); fetchErr != nil {
				results <- pageResult{err: fetchErr}
				return
			}
			results <- pageResult{data: data}
		}(p)
	}

	all := make([]CorporationBlueprint, 0, len(page1)*totalPages)
	all = append(all, page1...)
	for i := 0; i < totalPages-1; i++ {
		r := <-results
		if r.err != nil {
			continue
		}
		all = append(all, r.data...)
	}
	return all, nil
}
