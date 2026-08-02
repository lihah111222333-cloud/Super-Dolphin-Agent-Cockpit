package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
)

const remoteBaselineImageCacheSizeGiB int64 = 20

// remoteBaselineImageCacheAuthority is the narrow ECI ownership boundary used
// after BuildKit has produced an immutable OCI result. It prevents the refresh
// coordinator from selecting a cache by tag, registry identity, or fallback policy.
type remoteBaselineImageCacheAuthority interface {
	CreateImageCache(context.Context, eci.ImageCacheCreateRequest) (eci.ImageCache, error)
	WaitImageCacheReady(context.Context, string) (eci.ImageCache, error)
	DeleteImageCache(context.Context, string) error
}

func newRemoteBaselineImageCacheAuthority(config remoteRunConfig) (remoteBaselineImageCacheAuthority, error) {
	return eci.New(eci.Config{
		Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitchID: config.VSwitchID,
		SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName,
		Profile: config.CredentialProfile, Deadline: remoteBaselineRefreshDeadline,
		SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1,
	})
}

func remoteBaselineImageCacheName(generation uint64, tree string) (string, error) {
	if generation == 0 || len(tree) < 12 {
		return "", errors.New("remote baseline ImageCache identity is invalid")
	}
	return fmt.Sprintf("sd-baseline-%d-%s", generation, tree[:12]), nil
}

func validateRemoteBaselineReadyImageCache(created, ready eci.ImageCache, image string) error {
	if ready.ID != created.ID || ready.Name != created.Name || ready.Status != "Ready" || strings.TrimSpace(ready.SnapshotID) == "" {
		return errors.New("ECI ImageCache Ready result is incomplete or does not match the candidate")
	}
	if !slices.Contains(ready.Images, image) {
		return errors.New("ECI ImageCache Ready result does not contain the OCI result immutable digest")
	}
	return nil
}

func deleteRemoteBaselineImageCache(id string, authority remoteBaselineImageCacheAuthority) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := authority.DeleteImageCache(ctx, id); err != nil {
		return fmt.Errorf("delete ECI ImageCache %s: %w", id, err)
	}
	return nil
}
