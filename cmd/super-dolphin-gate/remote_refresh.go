package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const (
	remoteBaselineRefreshResultSchemaVersion uint32 = 1
	remoteBaselineRefreshDeadline                   = 90 * time.Minute
	remoteBaselineLockPollInterval                  = 100 * time.Millisecond
)

type remoteBaselineRefreshOptions struct {
	ConfigPath, LedgerPath, RepositoryRoot, Remote, Ref, Platform string
}

type remoteBaselineRefreshInput struct {
	Identity                             remoteci.BaselineIdentity
	GateSourceDigest                     string
	RuntimeDependencyDigest              string
	RuntimeDependencySchemaVersion       string
	GoToolchain, SqruffURL, SqruffSHA256 string
	SourceEntries                        []sourceexport.TreeEntry
}

type remoteBaselineRefreshResult struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Reused        bool                   `json:"reused"`
	State         remoteci.BaselineState `json:"state"`
}

// remoteOCIBaselineBuilder is the only cache-production boundary. It must
// produce an immutable OCI cache identity for both cold and incremental refresh.
type remoteOCIBaselineBuilder func(context.Context, remoteRunConfig, remoteci.BaselineState, remoteBaselineRefreshInput) (*remoteci.BaselineOCIProjectCache, error)

type remoteBaselineRefreshLock struct{ file *os.File }

func (lock *remoteBaselineRefreshLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	lock.file = nil
	return err
}

// runRemoteBaselineRefresh 将生产命令绑定到唯一允许的 OCI builder。
func runRemoteBaselineRefresh(args []string, stdout io.Writer) error {
	return runRemoteBaselineRefreshWithBuilder(args, stdout, buildRemoteOCIBaseline)
}

func runRemoteBaselineRefreshWithBuilder(args []string, stdout io.Writer, builder remoteOCIBaselineBuilder) (resultErr error) {
	if builder == nil {
		return protocolError("remote baseline OCI builder is required")
	}
	options, err := parseRemoteBaselineRefreshOptions(args)
	if err != nil {
		return err
	}
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return protocolError("load remote CI config: %v", err)
	}
	ctx, stop := newRemoteBaselineRefreshContext()
	defer stop()
	lock, err := acquireRemoteBaselineRefreshLock(ctx, options.LedgerPath)
	if err != nil {
		return infrastructureError("acquire remote baseline refresh lock: %v", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()

	accepted, err := loadRemoteBaselineStateForRefresh(options.LedgerPath)
	if err != nil {
		return protocolError("load remote baseline state: %v", err)
	}
	if accepted.SchemaVersion != 0 {
		if err := accepted.Validate(); err != nil {
			return protocolError("validate accepted OCI baseline: %v", err)
		}
	} else {
		return protocolError("remote baseline refresh requires an accepted Ready ECI ImageCache; full rebuild is not supported")
	}
	input, err := resolveRemoteBaselineRefreshInput(ctx, options, config)
	if err != nil {
		return sourceError("resolve remote baseline input: %v", err)
	}
	parent := accepted
	registryMigration, err := remoteBaselineRegistryMigrationRequired(config, accepted)
	if err != nil {
		return protocolError("validate accepted OCI baseline registry: %v", err)
	}
	if registryMigration {
		return protocolError("accepted ECI ImageCache repository differs from configured OCI repository; full rebuild is not supported")
	}
	// 增量 OCI worker 只能从已接受的不可变镜像及其 ImageCache authority 继续。
	input.Identity.RuntimeImage = accepted.RuntimeImage
	if !registryMigration && accepted.Matches(input.Identity) {
		renewed, err := renewRemoteBaselineState(options.LedgerPath, accepted, time.Now().UTC())
		if err != nil {
			return infrastructureError("CAS renew accepted ECI ImageCache baseline: %v", err)
		}
		return encodeRemoteBaselineRefreshResult(stdout, remoteBaselineRefreshResult{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Reused: true, State: renewed})
	}
	cache, err := builder(ctx, config, parent, input)
	if err != nil {
		return infrastructureError("build OCI baseline cache: %v", err)
	}
	if cache == nil {
		return infrastructureError("build OCI baseline cache: builder returned nil cache")
	}
	authority, err := newRemoteBaselineImageCacheAuthority(config)
	if err != nil {
		return infrastructureError("create ECI ImageCache authority: %v", err)
	}
	state, err := promoteRemoteBaselineImageCache(ctx, authority, options.LedgerPath, accepted, input, cache)
	if err != nil {
		return infrastructureError("promote ECI ImageCache baseline: %v", err)
	}
	return encodeRemoteBaselineRefreshResult(stdout, remoteBaselineRefreshResult{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, State: state})
}

// remoteBaselineRegistryMigrationRequired 判断已接受 OCI 基线是否属于当前目标仓库。
func remoteBaselineRegistryMigrationRequired(config remoteRunConfig, accepted remoteci.BaselineState) (bool, error) {
	if accepted.SchemaVersion == 0 {
		return false, nil
	}
	if err := accepted.Validate(); err != nil {
		return false, err
	}
	return remoteRuntimeImageRepository(accepted.RuntimeImage) != config.OCICache.RegistryRepository, nil
}

func newRemoteBaselineRefreshContext() (context.Context, func()) {
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := gateprivate.WithTimeout(signalContext, remoteBaselineRefreshDeadline)
	return ctx, func() { cancel(); stopSignals() }
}

// newRemoteOCIBaselineState binds the accepted OCI cache and its ECI ImageCache readiness identity.
func newRemoteOCIBaselineState(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, cache *remoteci.BaselineOCIProjectCache, imageCaches ...eci.ImageCache) (remoteci.BaselineState, error) {
	generation := accepted.Generation + 1
	if generation == 0 {
		return remoteci.BaselineState{}, errors.New("remote baseline generation is exhausted")
	}
	if len(imageCaches) != 1 {
		return remoteci.BaselineState{}, errors.New("exactly one ready ECI ImageCache is required")
	}
	imageCache := imageCaches[0]
	now := time.Now().UTC()
	state := remoteci.BaselineState{SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: generation, MainCommit: input.Identity.MainCommit, MainTree: input.Identity.MainTree, Platform: input.Identity.Platform, PolicyDigest: input.Identity.PolicyDigest, ToolchainDigest: input.Identity.ToolchainDigest, RuntimeImage: cache.Image, OCIProjectCache: cache, ImageCacheID: imageCache.ID, ImageCacheSnapshotID: imageCache.SnapshotID, ImageCacheReady: imageCache.Status == "Ready", ImageDigest: strings.TrimPrefix(cache.Image, strings.Split(cache.Image, "@")[0]+"@"), GateBinarySHA256: input.GateSourceDigest, RuntimeSeedSHA256: input.RuntimeDependencyDigest, BaselineManifestDigest: cache.ContentManifestSHA256, CreatedAt: now, AcceptedAt: now, RenewedAt: now}
	if err := state.Validate(); err != nil {
		return remoteci.BaselineState{}, err
	}
	return state, nil
}

func remoteBaselineStatesEquivalent(left, right remoteci.BaselineState) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func encodeRemoteBaselineRefreshResult(stdout io.Writer, result remoteBaselineRefreshResult) error {
	return json.NewEncoder(stdout).Encode(result)
}
