package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"eve-flipper/internal/db"
	"eve-flipper/internal/engine"
	"eve-flipper/internal/esi"
)

// fw_campaign.go -- the HTTP surface for FW Supply campaigns and their lots.
//
// The split from db/fw_campaign.go is the usual one: the store enforces what is
// true of the data (a lot's state is one of seven, remaining never exceeds
// bought, every read is user-scoped) and this file enforces what is true of a
// request (who is asking, what a partial body means, which fields a client may
// not set).
//
// Two decisions here are load-bearing rather than plumbing.
//
// First, a campaign update MERGES. A budget is a number the user typed once and
// a settings panel that PATCHes a subset must not zero it, so the body is
// decoded over the stored campaign rather than into an empty one. Getting this
// wrong is silent: the response would look fine and the headroom would be wrong
// on the next read.
//
// Second, a lot's state transition has its own endpoint and cannot carry
// quantities. Moving bought -> in_transit -> at_dest -> listed must not change
// headroom by one ISK, and the way to guarantee that is for the transition not
// to be able to write a quantity -- the same reasoning the store's
// SetFWLotState states, enforced again at the edge so a client cannot route
// around it by PATCHing a lot instead.

// fwCampaignPayload is one campaign with everything the tab needs to render it
// without a second round-trip: the budget measured from its lots, the lots
// themselves, and resolved station names.
type fwCampaignPayload struct {
	db.FWCampaign

	SourceStationName string `json:"source_station_name,omitempty"`
	DestStationName   string `json:"dest_station_name,omitempty"`

	Budget engine.FWBudget `json:"budget"`
	Lots   []engine.FWLot  `json:"lots"`

	// PlanGeneratedAt is when the cached gap table was last built, so the UI can
	// say how stale it is without fetching the payload. Empty means never
	// planned, which is the signal to generate one.
	PlanGeneratedAt string `json:"plan_generated_at,omitempty"`
}

// parseFWCampaignID reads the campaign id from the path.
func parseFWCampaignID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.PathValue("id"))
	if raw == "" {
		return 0, errors.New("missing campaign id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid campaign id")
	}
	return id, nil
}

// parseFWLotID reads the lot id from the path.
func parseFWLotID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.PathValue("lotID"))
	if raw == "" {
		return 0, errors.New("missing lot id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid lot id")
	}
	return id, nil
}

// requireFWCampaign loads the campaign named in the path, or writes the error.
//
// A campaign that belongs to somebody else and one that does not exist are the
// same 404 here, because the store deliberately cannot tell them apart.
func (s *Server) requireFWCampaign(w http.ResponseWriter, r *http.Request, userID string) (*db.FWCampaign, bool) {
	id, err := parseFWCampaignID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	campaign, err := s.db.GetFWCampaign(userID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return nil, false
	}
	if campaign == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("fw campaign %d not found", id))
		return nil, false
	}
	return campaign, true
}

// fwCampaignResponse assembles the full payload for one campaign.
func (s *Server) fwCampaignResponse(userID string, campaign *db.FWCampaign) (fwCampaignPayload, error) {
	lots, err := s.db.GetFWLots(userID, campaign.CampaignID)
	if err != nil {
		return fwCampaignPayload{}, err
	}
	if lots == nil {
		lots = []engine.FWLot{}
	}
	out := fwCampaignPayload{
		FWCampaign: *campaign,
		Budget:     engine.MeasureFWBudget(campaign.BudgetISK, lots),
		Lots:       lots,
	}
	out.SourceStationName = s.resolveStationLabel(campaign.SourceStationID, "")
	if campaign.DestStationID > 0 {
		out.DestStationName = s.resolveStationLabel(campaign.DestStationID, "")
	}
	if _, generatedAt, ok := s.db.GetFWPlan(userID, campaign.CampaignID); ok {
		out.PlanGeneratedAt = generatedAt
	}
	return out, nil
}

// handleFWCampaigns lists the user's campaigns with their budgets.
// GET /api/auth/fw/campaigns
//
// The budget is measured per campaign rather than left to the client, because
// "capital at cost" is a rule about which lot states count and there should be
// exactly one implementation of it.
func (s *Server) handleFWCampaigns(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	list, err := s.db.ListFWCampaigns(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list fw campaigns: "+err.Error())
		return
	}
	out := make([]fwCampaignPayload, 0, len(list))
	for i := range list {
		payload, err := s.fwCampaignResponse(userID, &list[i])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
			return
		}
		out = append(out, payload)
	}
	writeJSON(w, map[string]interface{}{"campaigns": out})
}

// handleFWCampaignCreate creates a campaign.
// POST /api/auth/fw/campaigns
//
// A new campaign opens with this militia's bulwark already pinned, and with the
// engine's own defaults written out rather than left as zeros. Zeros would plan
// identically -- FWSupplyConfig.withDefaults fills them -- but the settings
// panel would show a cover target of 0 days, and a number the user cannot see
// is a number they cannot disagree with.
func (s *Server) handleFWCampaignCreate(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	var body db.FWCampaign
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if _, known := esi.BulwarkSystems[body.MilitiaFactionID]; !known {
		writeError(w, http.StatusBadRequest,
			"militia_faction_id must be one of 500001 (Caldari), 500002 (Minmatar), 500003 (Amarr), 500004 (Gallente)")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		body.Name = fwDefaultCampaignName(body.MilitiaFactionID)
	}

	// The client sets neither of these; taking them from the body would let one
	// user write into another's campaign by guessing an id.
	body.UserID = userID
	body.CampaignID = 0
	body.CreatedAt = ""
	body.UpdatedAt = ""

	applyFWCampaignDefaults(&body)

	id, err := s.db.SaveFWCampaign(&body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save fw campaign: "+err.Error())
		return
	}
	stored, err := s.db.GetFWCampaign(userID, id)
	if err != nil || stored == nil {
		writeError(w, http.StatusInternalServerError, "fw campaign saved but could not be read back")
		return
	}
	payload, err := s.fwCampaignResponse(userID, stored)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSONStatus(w, http.StatusCreated, payload)
}

// applyFWCampaignDefaults fills the parameters a client did not state, and seeds
// the militia's bulwark into the pin list.
//
// The seed only pins. It never excludes and never short-circuits scoring, so a
// bulwark that has gone quiet still has to earn its rank against the derived
// ring -- which is also why the pin is stored on the campaign rather than read
// from the constant at plan time: excluding it has to be able to stick.
func applyFWCampaignDefaults(c *db.FWCampaign) {
	if c.TargetCoverDays <= 0 {
		c.TargetCoverDays = engine.DefaultTargetCoverDays
	}
	if c.CoveredMultiple <= 1 {
		c.CoveredMultiple = engine.DefaultCoveredMultiple
	}
	if c.MinMarginPct <= 0 {
		c.MinMarginPct = engine.DefaultMinMarginPct
	}
	if c.StepOverDaysCover <= 0 {
		c.StepOverDaysCover = engine.DefaultStepOverDaysCover
	}
	if len(c.CategoryCeilings) == 0 {
		c.CategoryCeilings = engine.DefaultCategoryCeilings()
	}
	if c.MaxJumpsFromFront <= 0 {
		c.MaxJumpsFromFront = engine.DefaultMaxJumpsFromFront
	}
	if bulwark, ok := esi.BulwarkSystems[c.MilitiaFactionID]; ok {
		if !containsInt32(c.PinnedSystems, bulwark) && !containsInt32(c.ExcludedSystems, bulwark) {
			c.PinnedSystems = append(c.PinnedSystems, bulwark)
		}
	}

	// The second demand window, reduced to something that means a fetch. The same
	// rule the plan applies, applied at the door, so what is stored is what will
	// happen rather than a request the plan silently declines.
	c.LongDemandWindowSeconds = fwNormalizeLongWindow(c.LongDemandWindowSeconds)
	// Anything that is not exactly "long" is short. Sizing is a two-valued choice
	// and an unrecognized third value has to resolve to the window that always
	// exists -- the other way round, a typo would size every row against a rate
	// that may not have been measured, and an unmeasured rate is zero, which is
	// unbounded cover, which reads as already stocked.
	if c.SizeAgainst != engine.FWSizedByLong || c.LongDemandWindowSeconds <= 0 {
		c.SizeAgainst = engine.FWSizedByShort
	}
}

func containsInt32(haystack []int32, needle int32) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// fwDefaultCampaignName names an unnamed campaign after its militia, so the list
// never shows a blank row.
func fwDefaultCampaignName(militiaFactionID int32) string {
	switch militiaFactionID {
	case esi.MilitiaCaldari:
		return "Caldari militia supply"
	case esi.MilitiaMinmatar:
		return "Minmatar militia supply"
	case esi.MilitiaAmarr:
		return "Amarr militia supply"
	case esi.MilitiaGallente:
		return "Gallente militia supply"
	}
	return "FW supply"
}

// handleFWCampaignGet returns one campaign with its budget and lots.
// GET /api/auth/fw/campaigns/{id}
func (s *Server) handleFWCampaignGet(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	payload, err := s.fwCampaignResponse(userID, campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSON(w, payload)
}

// handleFWCampaignUpdate merges the body into the stored campaign.
// PATCH /api/auth/fw/campaigns/{id}
//
// Merge, not replace. The settings panel sends the field that changed, and a
// body carrying only {"budget_isk": 2000000000} must not silently reset the
// cover target, the pins and the ownership pair to zero.
//
// Decoding over a populated struct gets that for free for scalars, but not for
// category_ceilings: encoding/json merges into an existing map, so a ceiling
// could be raised and never removed. So the raw keys are inspected first and a
// stated ceilings object replaces the map wholesale.
func (s *Server) handleFWCampaignUpdate(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := mergeFWCampaignPatch(campaign, raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// The path decides which campaign and the session decides whose. Neither is
	// negotiable by the body.
	id, _ := parseFWCampaignID(r)
	campaign.CampaignID = id
	campaign.UserID = userID

	if _, known := esi.BulwarkSystems[campaign.MilitiaFactionID]; !known {
		writeError(w, http.StatusBadRequest, "militia_faction_id must be a militia faction")
		return
	}
	if campaign.BudgetISK < 0 {
		writeError(w, http.StatusBadRequest, "budget_isk cannot be negative")
		return
	}
	applyFWCampaignDefaults(campaign)

	if _, err := s.db.SaveFWCampaign(campaign); err != nil {
		writeError(w, http.StatusInternalServerError, "save fw campaign: "+err.Error())
		return
	}

	// Destination and militia are the plan's two premises. Changing either makes
	// the cached gap table describe a place or a warzone the campaign is no
	// longer about, so it is dropped rather than left to look current. The lots
	// are untouched -- that is the whole point of the cache/record split.
	if fwPlanPremiseChanged(raw) {
		if err := s.db.DeleteFWPlan(userID, id); err != nil {
			writeError(w, http.StatusInternalServerError, "drop stale fw plan: "+err.Error())
			return
		}
	}

	payload, err := s.fwCampaignResponse(userID, campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSON(w, payload)
}

// mergeFWCampaignPatch decodes a partial body over a stored campaign.
//
// Scalars merge for free: encoding/json leaves a field absent from the body
// alone, which is exactly what a PATCH should do. Two things do not come free.
//
// category_ceilings is a map, and json.Unmarshal merges into an existing map
// rather than replacing it -- so a ceiling could be raised forever and never
// removed. A stated ceilings object therefore clears the map first and replaces
// it wholesale, which is the only reading under which "set my ceilings to this"
// means what it says.
//
// The pin/exclude system lists and the included/excluded type lists are all
// slices, which json does replace, and that is right: sending a list means
// "these are the pins now".
func mergeFWCampaignPatch(campaign *db.FWCampaign, raw map[string]json.RawMessage) error {
	if campaign == nil {
		return errors.New("no campaign to patch")
	}
	if _, stated := raw["category_ceilings"]; stated {
		campaign.CategoryCeilings = nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, campaign)
}

// fwPlanPremiseChanged reports whether an update touched a field the cached plan
// was computed against.
//
// Deliberately narrow. A budget change re-trims the shipping list from the same
// gap table, so it does not invalidate anything; a destination change means the
// stocked quantities, the markup ladder and every price in the table were
// measured at the wrong station.
func fwPlanPremiseChanged(raw map[string]json.RawMessage) bool {
	for _, key := range []string{
		"dest_station_id", "source_station_id", "militia_faction_id",
		"target_cover_days", "covered_multiple", "min_margin_pct",
		"freight_isk_per_m3", "step_over_days_cover", "category_ceilings",
	} {
		if _, stated := raw[key]; stated {
			return true
		}
	}
	return false
}

// handleFWCampaignDelete removes a campaign, its lots and its cached plan.
// DELETE /api/auth/fw/campaigns/{id}
//
// This destroys the spend record, which is the one thing here that cannot be
// rebuilt. It requires ?confirm=1 so a mis-routed request cannot do it, and the
// error says what would be lost.
func (s *Server) handleFWCampaignDelete(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	if r.URL.Query().Get("confirm") != "1" {
		lots, err := s.db.GetFWLots(userID, campaign.CampaignID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "fw lots: "+err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"deleting %q also deletes its %d lots, which is the only record of what was bought and what it cost; repeat with ?confirm=1",
			campaign.Name, len(lots)))
		return
	}
	if err := s.db.DeleteFWCampaign(userID, campaign.CampaignID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete fw campaign: "+err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleFWLotSave creates or updates one lot.
// POST /api/auth/fw/campaigns/{id}/lots
//
// A lot with no destination inherits the campaign's, and a lot with no remaining
// quantity stated is assumed whole -- both defaults the store expects the caller
// to have applied. A lot arriving with an explicit qty_remaining of 0 in a
// budget-consuming state is a contradiction the store rejects; that error is
// passed through rather than smoothed over.
func (s *Server) handleFWLotSave(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	var lot engine.FWLot
	if err := json.NewDecoder(r.Body).Decode(&lot); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if lot.TypeID <= 0 {
		writeError(w, http.StatusBadRequest, "type_id is required")
		return
	}
	if lot.Qty <= 0 {
		writeError(w, http.StatusBadRequest, "qty must be positive")
		return
	}
	if lot.QtyRemaining == 0 && lot.State != engine.FWLotSold {
		lot.QtyRemaining = lot.Qty
	}
	if lot.DestStationID <= 0 {
		lot.DestStationID = campaign.DestStationID
	}
	if lot.HolderOwnerKind == "" {
		lot.HolderOwnerKind = campaign.SellerOwnerKind
	}
	if lot.HolderOwnerID == 0 {
		lot.HolderOwnerID = campaign.SellerOwnerID
	}
	if lot.AcquiredByCharacterID == 0 {
		lot.AcquiredByCharacterID = campaign.BuyerCharacterID
	}
	if strings.TrimSpace(lot.TypeName) == "" {
		lot.TypeName = s.typeNameForID(lot.TypeID)
	}
	if strings.TrimSpace(lot.HolderName) == "" && lot.HolderOwnerID > 0 {
		lot.HolderName = s.fwOwnerName(userID, lot.HolderOwnerKind, lot.HolderOwnerID)
	}

	if _, err := s.db.SaveFWLot(userID, campaign.CampaignID, &lot); err != nil {
		writeError(w, http.StatusBadRequest, "save fw lot: "+err.Error())
		return
	}
	payload, err := s.fwCampaignResponse(userID, campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSON(w, payload)
}

// handleFWLotState advances one lot through the lifecycle.
// POST /api/auth/fw/campaigns/{id}/lots/{lotID}/state
//
// Body is {"state": "in_transit"}. Quantities are deliberately not accepted
// here: bought -> in_transit -> at_dest -> listed must not move headroom, and an
// endpoint that cannot write a quantity cannot break that.
func (s *Server) handleFWLotState(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	lotID, err := parseFWLotID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	state := strings.TrimSpace(strings.ToLower(body.State))
	if !engine.IsFWLotState(state) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"state must be one of %s", strings.Join(engine.FWLotStates(), ", ")))
		return
	}
	if err := s.db.SetFWLotState(userID, campaign.CampaignID, lotID, state); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	payload, err := s.fwCampaignResponse(userID, campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSON(w, payload)
}

// handleFWLotDelete removes a lot that should never have existed.
// DELETE /api/auth/fw/campaigns/{id}/lots/{lotID}
//
// For a mistyped planned row. Stock that was hauled back or written off is
// `pulled`, which keeps the record of what the campaign spent; deleting the row
// throws that away, so the two are different operations.
func (s *Server) handleFWLotDelete(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	lotID, err := parseFWLotID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.db.DeleteFWLot(userID, campaign.CampaignID, lotID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete fw lot: "+err.Error())
		return
	}
	payload, err := s.fwCampaignResponse(userID, campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSON(w, payload)
}

// fwBulkLotLimit bounds one bulk request.
//
// A campaign's lots are a shipment's worth of rows, so this sits far above any
// real selection. It is here so that a malformed client cannot ask the database
// for an unbounded transaction, not to tell the trader how much they may move.
const fwBulkLotLimit = 500

// handleFWLotsBulk applies one action to many lots at once.
// POST /api/auth/fw/campaigns/{id}/lots/bulk
//
// Body is {"lot_ids":[1,2,3],"action":"state","state":"in_transit"}, or the same
// with "action":"delete" and no state.
//
// This exists instead of letting the client loop the single-lot endpoints
// because the budget is computed from the lots. Ten separate requests can fail
// at the fourth, leaving headroom describing a position that never existed and
// the screen showing a mixture of before and after. One transaction, one
// recomputed campaign back, so what is on screen is always a position the
// campaign was actually in.
func (s *Server) handleFWLotsBulk(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireIndustryAuthUser(w, r)
	if !ok {
		return
	}
	campaign, ok := s.requireFWCampaign(w, r, userID)
	if !ok {
		return
	}
	var body struct {
		LotIDs []int64 `json:"lot_ids"`
		Action string  `json:"action"`
		State  string  `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.LotIDs) == 0 {
		writeError(w, http.StatusBadRequest, "lot_ids is required: name the lots to act on")
		return
	}
	if len(body.LotIDs) > fwBulkLotLimit {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"too many lots in one request: %d, limit %d", len(body.LotIDs), fwBulkLotLimit))
		return
	}

	switch strings.TrimSpace(strings.ToLower(body.Action)) {
	case "state":
		state := strings.TrimSpace(strings.ToLower(body.State))
		if !engine.IsFWLotState(state) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"state must be one of %s", strings.Join(engine.FWLotStates(), ", ")))
			return
		}
		if err := s.db.SetFWLotStates(userID, campaign.CampaignID, body.LotIDs, state); err != nil {
			// Nothing was written -- the transaction rolled back -- so the client's
			// view is still correct and this is a request problem, not a server one.
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
	case "delete":
		if err := s.db.DeleteFWLots(userID, campaign.CampaignID, body.LotIDs); err != nil {
			writeError(w, http.StatusInternalServerError, "delete fw lots: "+err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, `action must be "state" or "delete"`)
		return
	}

	payload, err := s.fwCampaignResponse(userID, campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fw campaign: "+err.Error())
		return
	}
	writeJSON(w, payload)
}

// fwOwnerName resolves a holder's display name from its owner triple.
//
// A corporation id looked up as a character returns nothing, so the kind has to
// pick the lookup -- the same reason the desk's owner cell branches on it.
//
// A character is resolved from the user's own sessions rather than from ESI:
// the holder of a lot is one of their characters, and a holder who is not
// currently logged in should leave the name blank rather than send this request
// off to a public endpoint. The lot stores the name it was created with, so this
// only fills a first write.
func (s *Server) fwOwnerName(userID, kind string, ownerID int64) string {
	if ownerID <= 0 {
		return ""
	}
	if kind == orderOwnerKindCorporation {
		return s.corporationName(ownerID)
	}
	if s.sessions == nil {
		return ""
	}
	for _, sess := range s.sessions.ListForUser(userID) {
		if sess.CharacterID == ownerID {
			return sess.CharacterName
		}
	}
	return ""
}

// typeNameForID resolves a type name from the loaded SDE, or returns "" while it
// is still loading. A blank name is better than a wrong one: the lot's type id
// is what everything downstream matches on.
func (s *Server) typeNameForID(typeID int32) string {
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil {
		return ""
	}
	if t, ok := sdeData.Types[typeID]; ok && t != nil {
		return t.Name
	}
	return ""
}
