package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
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

// promoteRemoteBaselineImageCache creates and verifies the sole runtime cache
// authority before a schema-10 successor CAS. Any pre-promotion failure deletes
// its candidate cache and leaves the accepted SQLite record unchanged.
func promoteRemoteBaselineImageCache(
	ctx context.Context,
	authority remoteBaselineImageCacheAuthority,
	ledgerPath string,
	accepted remoteci.BaselineState,
	input remoteBaselineRefreshInput,
	ociCache *remoteci.BaselineOCIProjectCache,
) (state remoteci.BaselineState, returnErr error) {
	if authority == nil {
		return remoteci.BaselineState{}, errors.New("ECI ImageCache authority is required")
	}
	if ociCache == nil {
		return remoteci.BaselineState{}, errors.New("OCI baseline result cache is required")
	}
	name, err := remoteBaselineImageCacheName(accepted.Generation+1, input.Identity.MainTree)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	created, err := authority.CreateImageCache(ctx, eci.ImageCacheCreateRequest{
		ImageCacheName: name, Images: []string{ociCache.Image}, ImageCacheSize: remoteBaselineImageCacheSizeGiB,
		Tags: map[string]string{"super-dolphin-baseline-generation": fmt.Sprintf("%d", accepted.Generation+1)},
	})
	if err != nil {
		return remoteci.BaselineState{}, fmt.Errorf("create ECI ImageCache: %w", err)
	}
	if strings.TrimSpace(created.ID) == "" {
		return remoteci.BaselineState{}, errors.New("CreateImageCache returned an empty ImageCacheId")
	}
	candidateID := created.ID
	promoted := false
	defer func() {
		if !promoted {
			returnErr = errors.Join(returnErr, deleteRemoteBaselineImageCache(candidateID, authority))
		}
	}()
	ready, err := authority.WaitImageCacheReady(ctx, candidateID)
	if err != nil {
		return remoteci.BaselineState{}, fmt.Errorf("wait for ECI ImageCache %s: %w", candidateID, err)
	}
	if err := validateRemoteBaselineReadyImageCache(created, ready, ociCache.Image); err != nil {
		return remoteci.BaselineState{}, err
	}
	state, err = newRemoteOCIBaselineState(accepted, input, ociCache, ready)
	if err != nil {
		return remoteci.BaselineState{}, fmt.Errorf("construct ImageCache-bound successor: %w", err)
	}
	if err := promoteRemoteBaselineState(ledgerPath, accepted, state); err != nil {
		return remoteci.BaselineState{}, fmt.Errorf("CAS promote ImageCache successor: %w", err)
	}
	promoted = true
	if accepted.SchemaVersion != 0 && accepted.ImageCacheID != candidateID {
		if err := deleteRemoteBaselineImageCache(accepted.ImageCacheID, authority); err != nil {
			return remoteci.BaselineState{}, fmt.Errorf("retire accepted ECI ImageCache after successor promotion: %w", err)
		}
	}
	return state, nil
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
