package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"eve-flipper/internal/db"
)

const cockpitPreferencesPayloadLimit = 128 * 1024

type cockpitHiddenPanelsPayload struct {
	AdvancedFilters    bool `json:"advancedFilters"`
	StationAIAssistant bool `json:"stationAiAssistant"`
	HelpButtons        bool `json:"helpButtons"`
	QuickActions       bool `json:"quickActions"`
	StatusBar          bool `json:"statusBar"`
	TabActionBars      bool `json:"tabActionBars"`
}

type cockpitTabLayoutPayload struct {
	Density      string                               `json:"density"`
	ColumnPreset string                               `json:"columnPreset"`
	FilterPreset string                               `json:"filterPreset"`
	HiddenPanels []string                             `json:"hiddenPanels"`
	ColumnState  map[string]cockpitColumnStatePayload `json:"columnState"`
}

type cockpitColumnStatePayload struct {
	Order   *int  `json:"order,omitempty"`
	Visible *bool `json:"visible,omitempty"`
	WidthPx *int  `json:"widthPx,omitempty"`
	Pinned  *bool `json:"pinned,omitempty"`
	Frozen  *bool `json:"frozen,omitempty"`
}

type cockpitPreferencesPayload struct {
	Version                      int                                  `json:"version"`
	Name                         string                               `json:"name"`
	Density                      string                               `json:"density"`
	StartupTab                   string                               `json:"startupTab"`
	LayoutLocked                 bool                                 `json:"layoutLocked"`
	AdaptiveEnabled              *bool                                `json:"adaptiveEnabled,omitempty"`
	ContextHintsEnabled          *bool                                `json:"contextHintsEnabled,omitempty"`
	TradingEdgeEnabled           *bool                                `json:"tradingEdgeEnabled,omitempty"`
	DismissedAdaptiveSuggestions []string                             `json:"dismissedAdaptiveSuggestions"`
	FavoriteTemplates            []string                             `json:"favoriteTemplates"`
	RoleBindings                 map[string]cockpitRoleBindingPayload `json:"roleBindings"`
	MainTabOrder                 []string                             `json:"mainTabOrder"`
	HiddenMainTabs               []string                             `json:"hiddenMainTabs"`
	QuickActions                 []string                             `json:"quickActions"`
	TabLayouts                   map[string]cockpitTabLayoutPayload   `json:"tabLayouts"`
	HiddenPanels                 cockpitHiddenPanelsPayload           `json:"hiddenPanels"`
}

type cockpitRoleBindingPayload struct {
	CharacterID  string                          `json:"characterId"`
	Label        string                          `json:"label"`
	PresetID     string                          `json:"presetId"`
	LoadoutID    string                          `json:"loadoutId"`
	ContextRules []cockpitRoleContextRulePayload `json:"contextRules"`
}

type cockpitRoleContextRulePayload struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Task      string `json:"task"`
	RouteMode string `json:"routeMode"`
	LoadoutID string `json:"loadoutId"`
	PresetID  string `json:"presetId"`
	Priority  int    `json:"priority"`
}

type cockpitLoadoutPayload struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Preferences cockpitPreferencesPayload `json:"preferences"`
	Active      bool                      `json:"active"`
	CreatedAt   string                    `json:"created_at,omitempty"`
	UpdatedAt   string                    `json:"updated_at,omitempty"`
}

type cockpitPreferencesResponse struct {
	Preferences     cockpitPreferencesPayload `json:"preferences"`
	Stored          bool                      `json:"stored"`
	UpdatedAt       string                    `json:"updated_at,omitempty"`
	ActiveLoadoutID string                    `json:"active_loadout_id,omitempty"`
	Loadout         *cockpitLoadoutPayload    `json:"loadout,omitempty"`
	Loadouts        []cockpitLoadoutPayload   `json:"loadouts,omitempty"`
}

type cockpitLoadoutsResponse struct {
	Loadouts        []cockpitLoadoutPayload   `json:"loadouts"`
	ActiveLoadoutID string                    `json:"active_loadout_id"`
	Preferences     cockpitPreferencesPayload `json:"preferences"`
	Stored          bool                      `json:"stored"`
}

type cockpitLoadoutRequest struct {
	Name        string                     `json:"name"`
	Preferences *cockpitPreferencesPayload `json:"preferences"`
	Activate    *bool                      `json:"activate,omitempty"`
}

var cockpitMainTabIDs = []string{
	"radius",
	"region",
	"contracts",
	"route",
	"station",
	"industry",
	"demand",
}

var cockpitQuickActionIDs = []string{
	"watchlist",
	"history",
	"itemIntel",
	"missionControl",
	"ledger",
	"journal",
	"dotlan",
	"commandPalette",
	"shortcuts",
}

var cockpitColumnPresetIDs = []string{
	"auto",
	"default",
	"compact",
	"trader",
	"hauling",
	"accounting",
}

var cockpitFilterPresetIDs = []string{
	"manual",
	"jita",
	"low_capital",
	"hauling",
	"industry",
}

var cockpitProfilePresetIDs = []string{
	"station_trader",
	"regional_hauler",
	"industry_builder",
	"ledger_accountant",
	"new_player",
	"power_user",
}

func cockpitBoolPtr(value bool) *bool {
	return &value
}

func defaultCockpitPreferencesPayload() cockpitPreferencesPayload {
	return cockpitPreferencesPayload{
		Version:                      1,
		Name:                         "Default cockpit",
		Density:                      "comfortable",
		StartupTab:                   "last",
		LayoutLocked:                 false,
		AdaptiveEnabled:              cockpitBoolPtr(true),
		ContextHintsEnabled:          cockpitBoolPtr(true),
		TradingEdgeEnabled:           cockpitBoolPtr(true),
		DismissedAdaptiveSuggestions: []string{},
		FavoriteTemplates:            []string{},
		RoleBindings:                 map[string]cockpitRoleBindingPayload{},
		MainTabOrder:                 append([]string(nil), cockpitMainTabIDs...),
		HiddenMainTabs:               []string{},
		QuickActions:                 []string{"watchlist", "history", "itemIntel"},
		TabLayouts:                   defaultCockpitTabLayoutsPayload(),
		HiddenPanels: cockpitHiddenPanelsPayload{
			AdvancedFilters:    false,
			StationAIAssistant: false,
			HelpButtons:        false,
			QuickActions:       false,
			StatusBar:          false,
			TabActionBars:      false,
		},
	}
}

func defaultCockpitTabLayoutsPayload() map[string]cockpitTabLayoutPayload {
	out := make(map[string]cockpitTabLayoutPayload, len(cockpitMainTabIDs))
	for _, tab := range cockpitMainTabIDs {
		out[tab] = cockpitTabLayoutPayload{
			Density:      "inherit",
			ColumnPreset: "auto",
			FilterPreset: "manual",
			HiddenPanels: []string{},
			ColumnState:  map[string]cockpitColumnStatePayload{},
		}
	}
	return out
}

func cockpitKnownTab(value string) bool {
	for _, tab := range cockpitMainTabIDs {
		if value == tab {
			return true
		}
	}
	return false
}

func cockpitKnownValue(value string, allowed []string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func cockpitUniqueKnownTabs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !cockpitKnownTab(value) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cockpitUniqueKnownValues(values []string, allowed []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !cockpitKnownValue(value, allowed) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sanitizeCockpitDensity(value string) string {
	value = strings.TrimSpace(value)
	if value == "compact" || value == "dense" || value == "comfortable" {
		return value
	}
	return "comfortable"
}

func sanitizeCockpitDensitySetting(value string) string {
	value = strings.TrimSpace(value)
	if value == "inherit" || value == "compact" || value == "dense" || value == "comfortable" {
		return value
	}
	return "inherit"
}

func sanitizeCockpitStartupTab(value string) string {
	value = strings.TrimSpace(value)
	if value == "last" || cockpitKnownTab(value) {
		return value
	}
	return "last"
}

func sanitizeCockpitPreset(value string, allowed []string, fallback string) string {
	value = strings.TrimSpace(value)
	if cockpitKnownValue(value, allowed) {
		return value
	}
	return fallback
}

func sanitizeCockpitHiddenPanelList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		runes := []rune(value)
		if len(runes) > 40 {
			value = string(runes[:40])
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= 40 {
			break
		}
	}
	return out
}

func sanitizeCockpitColumnState(values map[string]cockpitColumnStatePayload) map[string]cockpitColumnStatePayload {
	out := map[string]cockpitColumnStatePayload{}
	count := 0
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if len([]rune(key)) > 80 {
			key = string([]rune(key)[:80])
		}
		clean := cockpitColumnStatePayload{}
		if value.Order != nil {
			order := *value.Order
			if order < 0 {
				order = 0
			}
			if order > 1000 {
				order = 1000
			}
			clean.Order = &order
		}
		if value.Visible != nil {
			visible := *value.Visible
			clean.Visible = &visible
		}
		if value.WidthPx != nil {
			width := *value.WidthPx
			if width < 44 {
				width = 44
			}
			if width > 520 {
				width = 520
			}
			clean.WidthPx = &width
		}
		if value.Pinned != nil {
			pinned := *value.Pinned
			clean.Pinned = &pinned
		}
		if value.Frozen != nil {
			frozen := *value.Frozen
			clean.Frozen = &frozen
		}
		out[key] = clean
		count++
		if count >= 160 {
			break
		}
	}
	return out
}

func sanitizeCockpitStringList(values []string, limit int, maxRunes int) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		runes := []rune(value)
		if len(runes) > maxRunes {
			value = string(runes[:maxRunes])
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sanitizeCockpitContextTask(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "any", "station", "regional", "route", "industry", "ledger", "mission":
		return value
	default:
		return "any"
	}
}

func sanitizeCockpitRouteMode(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "any", "fastest", "safest", "balanced", "max_isk_hour":
		return value
	default:
		return "any"
	}
}

func sanitizeCockpitContextRules(values []cockpitRoleContextRulePayload) []cockpitRoleContextRulePayload {
	out := make([]cockpitRoleContextRulePayload, 0, len(values))
	for index, value := range values {
		task := sanitizeCockpitContextTask(value.Task)
		id := strings.TrimSpace(value.ID)
		if id == "" {
			id = "context-" + task + "-" + strconv.Itoa(index)
		}
		if len([]rune(id)) > 80 {
			id = string([]rune(id)[:80])
		}
		label := strings.TrimSpace(value.Label)
		if label == "" {
			label = task + " cockpit"
		}
		if len([]rune(label)) > 100 {
			label = string([]rune(label)[:100])
		}
		loadoutID := strings.TrimSpace(value.LoadoutID)
		if len([]rune(loadoutID)) > 80 {
			loadoutID = string([]rune(loadoutID)[:80])
		}
		presetID := sanitizeCockpitPreset(value.PresetID, cockpitProfilePresetIDs, "")
		if loadoutID == "" && presetID == "" {
			continue
		}
		priority := value.Priority
		if priority < 0 {
			priority = 0
		}
		if priority > 100 {
			priority = 100
		}
		out = append(out, cockpitRoleContextRulePayload{
			ID:        id,
			Label:     label,
			Task:      task,
			RouteMode: sanitizeCockpitRouteMode(value.RouteMode),
			LoadoutID: loadoutID,
			PresetID:  presetID,
			Priority:  priority,
		})
		if len(out) >= 30 {
			break
		}
	}
	return out
}

func sanitizeCockpitRoleBindings(values map[string]cockpitRoleBindingPayload) map[string]cockpitRoleBindingPayload {
	out := map[string]cockpitRoleBindingPayload{}
	count := 0
	for key, value := range values {
		characterID := strings.TrimSpace(value.CharacterID)
		if characterID == "" {
			characterID = strings.TrimSpace(key)
		}
		if characterID == "" {
			continue
		}
		if len([]rune(characterID)) > 32 {
			characterID = string([]rune(characterID)[:32])
		}
		label := strings.TrimSpace(value.Label)
		if len([]rune(label)) > 80 {
			label = string([]rune(label)[:80])
		}
		presetID := sanitizeCockpitPreset(value.PresetID, cockpitProfilePresetIDs, "")
		loadoutID := strings.TrimSpace(value.LoadoutID)
		if len([]rune(loadoutID)) > 80 {
			loadoutID = string([]rune(loadoutID)[:80])
		}
		out[characterID] = cockpitRoleBindingPayload{
			CharacterID:  characterID,
			Label:        label,
			PresetID:     presetID,
			LoadoutID:    loadoutID,
			ContextRules: sanitizeCockpitContextRules(value.ContextRules),
		}
		count++
		if count >= 40 {
			break
		}
	}
	return out
}

func sanitizeCockpitTabLayouts(in map[string]cockpitTabLayoutPayload) map[string]cockpitTabLayoutPayload {
	out := defaultCockpitTabLayoutsPayload()
	for _, tab := range cockpitMainTabIDs {
		item, ok := in[tab]
		if !ok {
			continue
		}
		out[tab] = cockpitTabLayoutPayload{
			Density:      sanitizeCockpitDensitySetting(item.Density),
			ColumnPreset: sanitizeCockpitPreset(item.ColumnPreset, cockpitColumnPresetIDs, "auto"),
			FilterPreset: sanitizeCockpitPreset(item.FilterPreset, cockpitFilterPresetIDs, "manual"),
			HiddenPanels: sanitizeCockpitHiddenPanelList(item.HiddenPanels),
			ColumnState:  sanitizeCockpitColumnState(item.ColumnState),
		}
	}
	return out
}

func sanitizeCockpitPreferencesPayload(in cockpitPreferencesPayload) cockpitPreferencesPayload {
	out := defaultCockpitPreferencesPayload()
	name := strings.TrimSpace(in.Name)
	if name != "" {
		runes := []rune(name)
		if len(runes) > 80 {
			name = string(runes[:80])
		}
		out.Name = name
	}
	out.Density = sanitizeCockpitDensity(in.Density)
	out.StartupTab = sanitizeCockpitStartupTab(in.StartupTab)
	out.LayoutLocked = in.LayoutLocked
	if in.AdaptiveEnabled != nil {
		out.AdaptiveEnabled = cockpitBoolPtr(*in.AdaptiveEnabled)
	}
	if in.ContextHintsEnabled != nil {
		out.ContextHintsEnabled = cockpitBoolPtr(*in.ContextHintsEnabled)
	}
	if in.TradingEdgeEnabled != nil {
		out.TradingEdgeEnabled = cockpitBoolPtr(*in.TradingEdgeEnabled)
	}
	out.DismissedAdaptiveSuggestions = sanitizeCockpitStringList(in.DismissedAdaptiveSuggestions, 100, 100)
	out.FavoriteTemplates = sanitizeCockpitStringList(in.FavoriteTemplates, 100, 80)
	out.RoleBindings = sanitizeCockpitRoleBindings(in.RoleBindings)

	order := cockpitUniqueKnownTabs(in.MainTabOrder)
	for _, tab := range cockpitMainTabIDs {
		found := false
		for _, existing := range order {
			if existing == tab {
				found = true
				break
			}
		}
		if !found {
			order = append(order, tab)
		}
	}
	out.MainTabOrder = order

	hidden := cockpitUniqueKnownTabs(in.HiddenMainTabs)
	if len(hidden) < len(cockpitMainTabIDs) {
		out.HiddenMainTabs = hidden
	}
	if in.QuickActions != nil {
		out.QuickActions = cockpitUniqueKnownValues(in.QuickActions, cockpitQuickActionIDs)
	}
	out.TabLayouts = sanitizeCockpitTabLayouts(in.TabLayouts)
	out.HiddenPanels = in.HiddenPanels
	return out
}

func cockpitPayloadToJSON(prefs cockpitPreferencesPayload) (string, error) {
	prefs = sanitizeCockpitPreferencesPayload(prefs)
	payload, err := json.Marshal(prefs)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func cockpitPayloadFromJSON(raw string, name string) cockpitPreferencesPayload {
	var prefs cockpitPreferencesPayload
	if err := json.Unmarshal([]byte(raw), &prefs); err != nil {
		prefs = defaultCockpitPreferencesPayload()
	}
	prefs = sanitizeCockpitPreferencesPayload(prefs)
	if strings.TrimSpace(name) != "" {
		prefs.Name = sanitizeCockpitPreferencesPayload(cockpitPreferencesPayload{Name: name}).Name
	}
	return prefs
}

func cockpitLoadoutFromDB(row db.CockpitLoadout) cockpitLoadoutPayload {
	return cockpitLoadoutPayload{
		ID:          row.LoadoutID,
		Name:        row.Name,
		Preferences: cockpitPayloadFromJSON(row.PayloadJSON, row.Name),
		Active:      row.Active,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func cockpitLoadoutsResponseFromRows(rows []db.CockpitLoadout, stored bool) cockpitLoadoutsResponse {
	loadouts := make([]cockpitLoadoutPayload, 0, len(rows))
	activeID := ""
	preferences := defaultCockpitPreferencesPayload()
	for i, row := range rows {
		item := cockpitLoadoutFromDB(row)
		loadouts = append(loadouts, item)
		if item.Active || (activeID == "" && i == 0) {
			activeID = item.ID
			preferences = item.Preferences
		}
	}
	if len(loadouts) == 0 {
		loadouts = append(loadouts, cockpitLoadoutPayload{
			ID:          "default",
			Name:        preferences.Name,
			Preferences: preferences,
			Active:      true,
		})
		activeID = "default"
	}
	return cockpitLoadoutsResponse{
		Loadouts:        loadouts,
		ActiveLoadoutID: activeID,
		Preferences:     preferences,
		Stored:          stored,
	}
}

func (s *Server) cockpitLoadoutsResponseForUser(userID string) (cockpitLoadoutsResponse, error) {
	rows, err := s.db.ListCockpitLoadoutsForUser(userID)
	if err != nil {
		return cockpitLoadoutsResponse{}, err
	}
	if len(rows) == 0 {
		payload, updatedAt, ok, err := s.db.LoadCockpitPreferencesForUser(userID)
		if err != nil {
			return cockpitLoadoutsResponse{}, err
		}
		if ok {
			rows = append(rows, db.CockpitLoadout{
				UserID:      userID,
				LoadoutID:   "default",
				Name:        "Default cockpit",
				PayloadJSON: payload,
				Active:      true,
				CreatedAt:   updatedAt,
				UpdatedAt:   updatedAt,
			})
		}
	}
	return cockpitLoadoutsResponseFromRows(rows, len(rows) > 0), nil
}

func (s *Server) handleGetCockpitPreferences(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		prefs := defaultCockpitPreferencesPayload()
		writeJSON(w, cockpitPreferencesResponse{
			Preferences: prefs,
			Stored:      false,
			Loadouts: []cockpitLoadoutPayload{{
				ID:          "default",
				Name:        prefs.Name,
				Preferences: prefs,
				Active:      true,
			}},
			ActiveLoadoutID: "default",
		})
		return
	}

	resp, err := s.cockpitLoadoutsResponseForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cockpit preferences")
		return
	}
	var active *cockpitLoadoutPayload
	for i := range resp.Loadouts {
		if resp.Loadouts[i].ID == resp.ActiveLoadoutID {
			active = &resp.Loadouts[i]
			break
		}
	}
	writeJSON(w, cockpitPreferencesResponse{
		Preferences:     resp.Preferences,
		Stored:          resp.Stored,
		UpdatedAt:       activeUpdatedAt(active),
		ActiveLoadoutID: resp.ActiveLoadoutID,
		Loadout:         active,
		Loadouts:        resp.Loadouts,
	})
}

func activeUpdatedAt(active *cockpitLoadoutPayload) string {
	if active == nil {
		return ""
	}
	return active.UpdatedAt
}

func (s *Server) handlePutCockpitPreferences(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	var prefs cockpitPreferencesPayload
	decoder := json.NewDecoder(io.LimitReader(r.Body, cockpitPreferencesPayloadLimit))
	if err := decoder.Decode(&prefs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	prefs = sanitizeCockpitPreferencesPayload(prefs)
	payload, err := cockpitPayloadToJSON(prefs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode cockpit preferences")
		return
	}
	row, err := s.db.SaveActiveCockpitLoadoutForUser(userIDFromRequest(r), prefs.Name, payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save cockpit preferences")
		return
	}
	resp, err := s.cockpitLoadoutsResponseForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cockpit loadouts")
		return
	}
	loadout := cockpitLoadoutFromDB(row)
	writeJSON(w, cockpitPreferencesResponse{
		Preferences:     loadout.Preferences,
		Stored:          true,
		UpdatedAt:       loadout.UpdatedAt,
		ActiveLoadoutID: loadout.ID,
		Loadout:         &loadout,
		Loadouts:        resp.Loadouts,
	})
}

func (s *Server) handleGetCockpitLoadouts(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, cockpitLoadoutsResponseFromRows(nil, false))
		return
	}
	resp, err := s.cockpitLoadoutsResponseForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cockpit loadouts")
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleCreateCockpitLoadout(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	req, prefs, activate, ok := decodeCockpitLoadoutRequest(w, r, true)
	if !ok {
		return
	}
	row, err := s.saveCockpitLoadoutRequest(userIDFromRequest(r), "", req.Name, prefs, activate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create cockpit loadout")
		return
	}
	resp, err := s.cockpitLoadoutsResponseForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cockpit loadouts")
		return
	}
	loadout := cockpitLoadoutFromDB(row)
	writeJSONStatus(w, http.StatusCreated, cockpitPreferencesResponse{
		Preferences:     resp.Preferences,
		Stored:          true,
		UpdatedAt:       loadout.UpdatedAt,
		ActiveLoadoutID: resp.ActiveLoadoutID,
		Loadout:         &loadout,
		Loadouts:        resp.Loadouts,
	})
}

func (s *Server) handleUpdateCockpitLoadout(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	loadoutID := r.PathValue("loadoutID")
	req, prefs, activate, ok := decodeCockpitLoadoutRequest(w, r, false)
	if !ok {
		return
	}
	row, err := s.saveCockpitLoadoutRequest(userIDFromRequest(r), loadoutID, req.Name, prefs, activate)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "cockpit loadout not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update cockpit loadout")
		return
	}
	resp, err := s.cockpitLoadoutsResponseForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cockpit loadouts")
		return
	}
	loadout := cockpitLoadoutFromDB(row)
	writeJSON(w, cockpitPreferencesResponse{
		Preferences:     resp.Preferences,
		Stored:          true,
		UpdatedAt:       loadout.UpdatedAt,
		ActiveLoadoutID: resp.ActiveLoadoutID,
		Loadout:         &loadout,
		Loadouts:        resp.Loadouts,
	})
}

func (s *Server) handleActivateCockpitLoadout(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	row, err := s.db.ActivateCockpitLoadoutForUser(userIDFromRequest(r), r.PathValue("loadoutID"))
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "cockpit loadout not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to activate cockpit loadout")
		return
	}
	resp, err := s.cockpitLoadoutsResponseForUser(userIDFromRequest(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cockpit loadouts")
		return
	}
	loadout := cockpitLoadoutFromDB(row)
	writeJSON(w, cockpitPreferencesResponse{
		Preferences:     loadout.Preferences,
		Stored:          true,
		UpdatedAt:       loadout.UpdatedAt,
		ActiveLoadoutID: loadout.ID,
		Loadout:         &loadout,
		Loadouts:        resp.Loadouts,
	})
}

func (s *Server) handleDeleteCockpitLoadout(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	rows, err := s.db.DeleteCockpitLoadoutForUser(userIDFromRequest(r), r.PathValue("loadoutID"))
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "cockpit loadout not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, cockpitLoadoutsResponseFromRows(rows, true))
}

func decodeCockpitLoadoutRequest(w http.ResponseWriter, r *http.Request, defaultActivate bool) (cockpitLoadoutRequest, cockpitPreferencesPayload, bool, bool) {
	var req cockpitLoadoutRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, cockpitPreferencesPayloadLimit))
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return req, cockpitPreferencesPayload{}, false, false
	}
	prefs := defaultCockpitPreferencesPayload()
	if req.Preferences != nil {
		prefs = sanitizeCockpitPreferencesPayload(*req.Preferences)
	}
	if strings.TrimSpace(req.Name) != "" {
		prefs.Name = sanitizeCockpitPreferencesPayload(cockpitPreferencesPayload{Name: req.Name}).Name
	}
	activate := defaultActivate
	if req.Activate != nil {
		activate = *req.Activate
	}
	return req, prefs, activate, true
}

func (s *Server) saveCockpitLoadoutRequest(userID, loadoutID, name string, prefs cockpitPreferencesPayload, activate bool) (db.CockpitLoadout, error) {
	prefs = sanitizeCockpitPreferencesPayload(prefs)
	if strings.TrimSpace(name) == "" {
		name = prefs.Name
	}
	prefs.Name = sanitizeCockpitPreferencesPayload(cockpitPreferencesPayload{Name: name}).Name
	payload, err := cockpitPayloadToJSON(prefs)
	if err != nil {
		return db.CockpitLoadout{}, err
	}
	return s.db.UpsertCockpitLoadoutForUser(userID, loadoutID, prefs.Name, payload, activate)
}
