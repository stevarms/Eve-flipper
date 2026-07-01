package main

import (
	"fmt"

	"eve-flipper/internal/esi"
	"eve-flipper/internal/logger"
	"eve-flipper/internal/sde"
)

func prepareShipPackagedVolumes(dataDir string, data *sde.Data, esiClient *esi.Client) {
	if data == nil {
		return
	}
	result, err := sde.RefreshShipPackagedVolumeCache(dataDir, data, func(typeID int32) (float64, error) {
		if esiClient == nil {
			return 0, fmt.Errorf("ESI client is nil")
		}
		info, err := esiClient.TypeInfo(typeID)
		if err != nil {
			return 0, err
		}
		return info.PackagedVolume, nil
	})
	if err != nil {
		logger.Warn("SDE", fmt.Sprintf("Ship packaged-volume cache refresh failed: %v", err))
		return
	}
	if result.Applied > 0 || result.Fetched > 0 || result.Failed > 0 {
		logger.Info("SDE", fmt.Sprintf(
			"Ship packaged-volume cache: applied=%d fetched=%d failed=%d missing_after=%d path=%s",
			result.Applied,
			result.Fetched,
			result.Failed,
			len(data.MissingShipPackagedVolumeTypeIDs()),
			result.CachePath,
		))
	}
}
