package api

import (
	"encoding/json"
	"net/http"
	"sort"
)

// structureRigDTO is the wire shape for a single Standup rig. Mirrors
// sde.StructureRig but keeps the JSON tags stable independent of internal
// field renames.
type structureRigDTO struct {
	TypeID              int32   `json:"type_id"`
	Name                string  `json:"name"`
	GroupID             int32   `json:"group_id"`
	GroupName           string  `json:"group_name"`
	MetaGroupID         int32   `json:"meta_group_id"`
	RigSize             int32   `json:"rig_size"`
	Family              string  `json:"family"`
	Activity            string  `json:"activity"`
	ProductCategoryIDs  []int32 `json:"product_category_ids"`
	ProductGroupIDs     []int32 `json:"product_group_ids"`
	ProductMetaGroupIDs []int32 `json:"product_meta_group_ids"`
	AffinityDescription string  `json:"affinity_description"`
	MEBonus             float64 `json:"me_bonus"`
	TEBonus             float64 `json:"te_bonus"`
	CostBonus           float64 `json:"cost_bonus"`
	HiSecMult           float64 `json:"hi_sec_mult"`
	LowSecMult          float64 `json:"low_sec_mult"`
	NullSecMult         float64 `json:"null_sec_mult"`
	FitsStructureGroups []int32 `json:"fits_structure_groups"`
}

type structureRigsResponse struct {
	Rigs []structureRigDTO `json:"rigs"`
}

// handleIndustryStructureRigs returns the catalog of Standup rigs known to
// the SDE, with their bonuses, sec-status multipliers, and affinity info.
// The frontend rig picker uses this to render options filtered by structure
// hull. Static after SDE load.
func (s *Server) handleIndustryStructureRigs(w http.ResponseWriter, r *http.Request) {
	if !s.isReady() {
		writeError(w, 503, "SDE not loaded yet")
		return
	}
	s.mu.RLock()
	sdeData := s.sdeData
	s.mu.RUnlock()
	if sdeData == nil || len(sdeData.Rigs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(structureRigsResponse{Rigs: []structureRigDTO{}})
		return
	}

	dtos := make([]structureRigDTO, 0, len(sdeData.Rigs))
	for _, rig := range sdeData.Rigs {
		if rig == nil {
			continue
		}
		aff := sdeData.RigAffinities[rig.GroupID]
		groupName := ""
		if g, ok := sdeData.Groups[rig.GroupID]; ok && g != nil {
			groupName = g.Name
		}
		dtos = append(dtos, structureRigDTO{
			TypeID:              rig.TypeID,
			Name:                rig.Name,
			GroupID:             rig.GroupID,
			GroupName:           groupName,
			MetaGroupID:         rig.MetaGroupID,
			RigSize:             rig.RigSize,
			Family:              aff.Family,
			Activity:            aff.Activity,
			ProductCategoryIDs:  aff.ProductCategoryIDs,
			ProductGroupIDs:     aff.ProductGroupIDs,
			ProductMetaGroupIDs: aff.ProductMetaGroupIDs,
			AffinityDescription: aff.Description,
			MEBonus:             rig.MEBonus,
			TEBonus:             rig.TEBonus,
			CostBonus:           rig.CostBonus,
			HiSecMult:           rig.HiSecMult,
			LowSecMult:          rig.LowSecMult,
			NullSecMult:         rig.NullSecMult,
			FitsStructureGroups: rig.FitsStructureGroups,
		})
	}
	// Stable order for cache-friendly responses + tidy dropdowns.
	sort.SliceStable(dtos, func(i, j int) bool {
		if dtos[i].GroupID != dtos[j].GroupID {
			return dtos[i].GroupID < dtos[j].GroupID
		}
		if dtos[i].MetaGroupID != dtos[j].MetaGroupID {
			return dtos[i].MetaGroupID < dtos[j].MetaGroupID
		}
		return dtos[i].Name < dtos[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(structureRigsResponse{Rigs: dtos})
}
