package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	remoteBaselineRefreshResultSchemaVersion uint32 = 1
	remoteBaselineSeedDeadline                      = 60 * time.Minute
	remoteBaselineRefreshDeadline                   = 90 * time.Minute
	remoteBaselinePollInterval                      = 2 * time.Second
	remoteBaselineLockPollInterval                  = 100 * time.Millisecond
	remoteBaselineManifestMaxBytes           int64  = 1 << 20
	remoteBaselineToolArtifactMaxBytes       int64  = 64 << 20
	remoteBaselineToolArtifactTimeout               = 10 * time.Minute
	remoteBaselineToolArtifactAttempts              = 4
	remoteBaselineToolArtifactRetryDelay            = 250 * time.Millisecond
	remoteBaselineDeltaLimit                        = 4
)

type remoteBaselineRefreshOptions struct {
	ConfigPath     string
	StatePath      string
	RepositoryRoot string
	Remote         string
	Ref            string
	Platform       string
}

type remoteBaselineRefreshInput struct {
	Identity                        remoteci.BaselineIdentity
	GateSourceDigest                string
	RuntimeDependencyDigest         string
	AcceptedRuntimeDependencyDigest string
	RuntimeDependencySchemaVersion  string
	GoToolchain                     string
	SqruffURL                       string
	SqruffSHA256                    string
}

type remoteBaselineRefreshResult struct {
	SchemaVersion        uint32                 `json:"schema_version"`
	Reused               bool                   `json:"reused"`
	SeedContainerGroupID string                 `json:"seed_container_group_id,omitempty"`
	State                remoteci.BaselineState `json:"state"`
}

type remoteBaselineOSSStore interface {
	Create(context.Context, string, string) error
	Download(context.Context, string, string) error
	EnsurePrefix(context.Context, string) error
	DeletePrefix(context.Context, string) error
}

type remoteBaselineDataCacheClient interface {
	Create(context.Context, datacache.CreateRequest) (datacache.DataCache, error)
	Describe(context.Context, ...string) ([]datacache.DataCache, error)
	FindByPath(context.Context, string, string, map[string]string) ([]datacache.DataCache, error)
	Renew(context.Context, string, int, string) error
	Delete(context.Context, string, string, string) error
}

type remoteBaselineRefreshLock struct {
	file *os.File
}

type remoteBaselineRefreshSession struct {
	accepted                   remoteci.BaselineState
	acceptedRecommendedSizeGiB int
	cache                      remoteBaselineDataCacheClient
	config                     remoteRunConfig
	input                      remoteBaselineRefreshInput
	legacy                     *remoteLegacyBaselineMigration
	options                    remoteBaselineRefreshOptions
	resolveRef                 func(string, string, string) (string, error)
	verifySuccessor            func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error
	writeState                 func(string, remoteci.BaselineState) error
	readState                  func(string, bool) (remoteci.BaselineState, error)
	cleanupLegacy              func(context.Context, remoteBaselineDataCacheClient, remoteBaselineOSSStore, *remoteLegacyBaselineMigration) error
	cleanupRetired             func(context.Context, remoteBaselineDataCacheClient, remoteBaselineOSSStore, string, *remoteci.BaselineState) error
	statePath                  string
	store                      remoteBaselineOSSStore
}

type remoteBaselineArtifactStage struct {
	createdAt        time.Time
	generation       uint64
	generationPrefix string
	inputPrefix      string
	outputPrefix     string
	seedScriptPath   string
	sqruffPath       string
	source           remoteBaselineSourceArtifact
}

// runRemoteBaselineRefresh 仅在远端 main 或基线输入变化时创建新的 ECI DataCache generation。
func runRemoteBaselineRefresh(args []string, stdout io.Writer) (resultErr error) {
	options, err := parseRemoteBaselineRefreshOptions(args)
	if err != nil {
		return err
	}
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return protocolError("load remote CI config: %v", err)
	}
	statePath := remoteBaselineStatePath(options.ConfigPath, options.StatePath)
	ctx, stop := newRemoteBaselineRefreshContext()
	defer stop()
	lock, err := acquireRemoteBaselineRefreshLock(ctx, statePath)
	if err != nil {
		return infrastructureError("acquire remote baseline refresh lock: %v", err)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.close()) }()
	return runRemoteBaselineRefreshLocked(ctx, options, config, statePath, stdout)
}

// newRemoteBaselineRefreshContext 创建可被终止信号取消的刷新上下文。
func newRemoteBaselineRefreshContext() (context.Context, func()) {
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := gateprivate.WithTimeout(signalContext, remoteBaselineRefreshDeadline)
	return ctx, func() { cancel(); stopSignals() }
}

// runRemoteBaselineRefreshLocked 在持锁状态下选择复用或创建新的 generation。
func runRemoteBaselineRefreshLocked(ctx context.Context, options remoteBaselineRefreshOptions, config remoteRunConfig, statePath string, stdout io.Writer) error {
	session, err := newRemoteBaselineRefreshSession(ctx, options, config, statePath)
	if err != nil {
		return err
	}
	if session.accepted.HasRetiredReferences() {
		if err := cleanupRetiredRemoteBaseline(ctx, session.cache, session.store, statePath, &session.accepted); err != nil {
			return infrastructureError("finish retired remote baseline cleanup: %v", err)
		}
	}
	if remoteBaselineCanReuse(
		session.accepted,
		session.input.Identity,
		session.acceptedRecommendedSizeGiB,
	) {
		return reuseRemoteBaseline(ctx, session, stdout)
	}
	return createRemoteBaseline(ctx, session, stdout)
}

// remoteBaselineCanReuse 只允许完整历史、身份和容量均匹配的基线跳过刷新。
func remoteBaselineCanReuse(
	accepted remoteci.BaselineState,
	identity remoteci.BaselineIdentity,
	recommendedSizeGiB int,
) bool {
	return accepted.SourceHistoryVersion == remoteci.BaselineSourceHistorySchemaVersion &&
		accepted.Matches(identity) &&
		remoteBaselineCapacityMatches(accepted, recommendedSizeGiB)
}

// newRemoteBaselineRefreshSession 加载状态并创建本次刷新所需的远端客户端。
func newRemoteBaselineRefreshSession(ctx context.Context, options remoteBaselineRefreshOptions, config remoteRunConfig, statePath string) (remoteBaselineRefreshSession, error) {
	accepted, legacy, err := loadRemoteBaselineStateForRefresh(statePath, config)
	if err != nil {
		return remoteBaselineRefreshSession{}, protocolError("load remote baseline state: %v", err)
	}
	if accepted.SchemaVersion != 0 {
		if err := validateAcceptedRemoteBaseline(config, accepted); err != nil {
			return remoteBaselineRefreshSession{}, protocolError("validate accepted remote baseline: %v", err)
		}
	}
	input, err := resolveRemoteBaselineRefreshInput(ctx, options, config)
	if err != nil {
		return remoteBaselineRefreshSession{}, sourceError("resolve remote baseline input: %v", err)
	}
	input, err = bindAcceptedRemoteRuntimeDependency(ctx, options.RepositoryRoot, accepted, input)
	if err != nil {
		return remoteBaselineRefreshSession{}, sourceError("resolve accepted remote runtime dependency: %v", err)
	}
	cache, err := datacache.New(datacache.Config{Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitchID: config.VSwitchID, SecurityGroupID: config.SecurityGroupID, Profile: config.CredentialProfile})
	if err != nil {
		return remoteBaselineRefreshSession{}, infrastructureError("create DataCache client: %v", err)
	}
	store, err := newRemoteBaselineOSSStore(config)
	if err != nil {
		return remoteBaselineRefreshSession{}, infrastructureError("create remote baseline OSS client: %v", err)
	}
	recommendedSizeGiB, err := loadAcceptedRemoteBaselineRecommendedSize(ctx, config, store, accepted)
	if err != nil {
		return remoteBaselineRefreshSession{}, infrastructureError("load accepted remote baseline capacity: %v", err)
	}
	return remoteBaselineRefreshSession{
		accepted: accepted, acceptedRecommendedSizeGiB: recommendedSizeGiB,
		cache: cache, config: config, input: input, legacy: legacy,
		options: options, resolveRef: resolveRemoteRef, statePath: statePath, store: store,
	}, nil
}

// reuseRemoteBaseline 验证、续期并持久化未变化的已接受基线。
func reuseRemoteBaseline(ctx context.Context, session remoteBaselineRefreshSession, stdout io.Writer) error {
	if !remoteBaselineCapacityMatches(session.accepted, session.acceptedRecommendedSizeGiB) {
		return protocolError("remote baseline capacity changed from %d GiB to %d GiB; a new Anchor is required", session.accepted.DataCacheSizeGiB, session.acceptedRecommendedSizeGiB)
	}
	if err := renewRemoteBaselineAnchor(ctx, session); err != nil {
		return infrastructureError("renew unchanged remote baseline: %v", err)
	}
	session.accepted.AcceptedAt = time.Now().UTC()
	if err := writeRemoteBaselineState(session.statePath, session.accepted); err != nil {
		return infrastructureError("persist renewed remote baseline: %v", err)
	}
	return encodeRemoteBaselineRefreshResult(stdout, remoteBaselineRefreshResult{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, Reused: true, State: session.accepted})
}

// createRemoteBaseline 执行源工件、seed，并按 manifest 接受 Anchor 或 delta。
func createRemoteBaseline(ctx context.Context, session remoteBaselineRefreshSession, stdout io.Writer) (resultErr error) {
	stage, err := prepareRemoteBaselineArtifacts(ctx, session)
	if err != nil {
		return err
	}
	accepted := false
	defer cleanupUnacceptedRemoteArtifacts(&resultErr, session.store, stage.generationPrefix, &accepted)
	runtime, request, group, err := createRemoteBaselineSeed(ctx, session, stage)
	if err != nil {
		return err
	}
	defer cleanupRemoteBaselineSeed(&resultErr, runtime, group.ID)
	manifest, digest, err := executeRemoteBaselineSeed(ctx, session, runtime, request, group.ID, stage)
	if err != nil {
		return err
	}
	cache, cleanupCache, err := prepareRemoteBaselineStorage(ctx, session, stage, manifest)
	if cleanupCache {
		defer cleanupUnacceptedRemoteCache(&resultErr, session.cache, cache, &accepted)
	}
	if err != nil {
		return err
	}
	state, err := acceptRemoteBaseline(session, stage, manifest, digest, cache)
	if err != nil {
		return err
	}
	persisted, err := promoteRemoteBaseline(ctx, session, &state)
	if persisted {
		accepted = true
	}
	if err != nil {
		return err
	}
	return encodeRemoteBaselineRefreshResult(stdout, remoteBaselineRefreshResult{SchemaVersion: remoteBaselineRefreshResultSchemaVersion, SeedContainerGroupID: group.ID, State: state})
}

// prepareRemoteBaselineStorage 创建 Anchor DataCache 或续期 Delta 所复用的 Anchor。
func prepareRemoteBaselineStorage(ctx context.Context, session remoteBaselineRefreshSession, stage remoteBaselineArtifactStage, manifest remoteci.BaselineManifest) (datacache.DataCache, bool, error) {
	switch manifest.StorageMode {
	case remoteci.BaselineStorageModeAnchor:
		cache, err := createRemoteBaselineCache(ctx, session, stage, manifest)
		if err != nil {
			return datacache.DataCache{}, false, err
		}
		cache, err = waitRemoteDataCache(ctx, session.cache, cache.ID, cache.Path, cache.Bucket)
		if err != nil {
			return cache, true, infrastructureError("wait for remote baseline DataCache: %v", err)
		}
		return cache, true, nil
	case remoteci.BaselineStorageModeDelta:
		if session.accepted.SchemaVersion == 0 || session.legacy != nil {
			return datacache.DataCache{}, false, protocolError("remote baseline delta requires an accepted Anchor")
		}
		if err := renewRemoteBaselineAnchor(ctx, session); err != nil {
			return datacache.DataCache{}, false, infrastructureError("renew reused remote baseline Anchor: %v", err)
		}
		return datacache.DataCache{}, false, nil
	default:
		return datacache.DataCache{}, false, protocolError("remote baseline seed returned unsupported storage mode %q", manifest.StorageMode)
	}
}

// renewRemoteBaselineAnchor 验证并续期本次继续复用的唯一 DataCache。
func renewRemoteBaselineAnchor(ctx context.Context, session remoteBaselineRefreshSession) error {
	if err := verifyAvailableDataCache(ctx, session.cache, session.accepted); err != nil {
		return err
	}
	anchor := session.accepted.CurrentAnchorRef()
	if err := session.cache.Renew(ctx, anchor.DataCacheID, session.config.DataCache.RetentionDays, remoteBaselineRenewToken(anchor.Generation, time.Now().UTC())); err != nil {
		return err
	}
	return verifyAvailableDataCache(ctx, session.cache, session.accepted)
}

// prepareRemoteBaselineArtifacts 生成并上传本次 generation 的输入工件。
func prepareRemoteBaselineArtifacts(ctx context.Context, session remoteBaselineRefreshSession) (remoteBaselineArtifactStage, error) {
	generation, err := nextRemoteBaselineGeneration(session.accepted, session.legacy)
	if err != nil {
		return remoteBaselineArtifactStage{}, protocolError("%v", err)
	}
	stage := remoteBaselineArtifactStage{createdAt: time.Now().UTC(), generation: generation, generationPrefix: remoteBaselineSourcePrefix(session.config, generation), inputPrefix: remoteBaselineInputPrefix(session.config, generation), outputPrefix: remoteBaselineOutputPrefix(session.config, generation)}
	if err := cleanupStaleRemoteBaselineCandidate(ctx, session, stage); err != nil {
		return remoteBaselineArtifactStage{}, infrastructureError("recover stale remote baseline candidate: %v", err)
	}
	root, err := os.MkdirTemp("", "super-dolphin-baseline-source-*")
	if err != nil {
		return remoteBaselineArtifactStage{}, infrastructureError("create baseline source artifact directory: %v", err)
	}
	defer os.RemoveAll(root)
	stage.source, err = buildRemoteBaselineSourceArtifact(ctx, session.options.RepositoryRoot, session.accepted, session.input.Identity, root)
	if err != nil {
		return remoteBaselineArtifactStage{}, sourceError("build baseline source artifact: %v", err)
	}
	stage.seedScriptPath = filepath.Join(root, "seed.sh")
	if err := os.WriteFile(stage.seedScriptPath, []byte(remoteBaselineSeedScript), 0o600); err != nil {
		return remoteBaselineArtifactStage{}, infrastructureError("stage baseline seed script: %v", err)
	}
	if remoteBaselineSeedNeedsInternet(session.accepted, session.input) {
		stage.sqruffPath, err = downloadRemoteBaselineToolArtifact(ctx, root, session.input.SqruffURL, session.input.SqruffSHA256)
		if err != nil {
			return remoteBaselineArtifactStage{}, infrastructureError("stage sqruff artifact: %v", err)
		}
	}
	if err := uploadRemoteBaselineArtifactsWithCleanup(ctx, session.store, stage); err != nil {
		return remoteBaselineArtifactStage{}, err
	}
	return stage, nil
}

// uploadRemoteBaselineArtifactsWithCleanup 保证部分上传不会留下 generation。
func uploadRemoteBaselineArtifactsWithCleanup(ctx context.Context, store remoteBaselineOSSStore, stage remoteBaselineArtifactStage) error {
	if err := uploadRemoteBaselineArtifacts(ctx, store, stage); err != nil {
		return cleanupFailedRemoteBaselineUpload(err, store, stage.generationPrefix)
	}
	return nil
}

// uploadRemoteBaselineArtifacts 写入输出目录、校验脚本和 source manifest/bundle。
func uploadRemoteBaselineArtifacts(ctx context.Context, store remoteBaselineOSSStore, stage remoteBaselineArtifactStage) error {
	if err := store.EnsurePrefix(ctx, stage.outputPrefix); err != nil {
		return infrastructureError("create baseline output prefix: %v", err)
	}
	if stage.seedScriptPath == "" {
		return protocolError("baseline seed script path is empty")
	}
	if err := store.Create(ctx, stage.seedScriptPath, stage.inputPrefix+"seed.sh"); err != nil {
		return infrastructureError("upload baseline seed script: %v", err)
	}
	if err := store.Create(ctx, stage.source.ManifestPath, stage.inputPrefix+"source-manifest.json"); err != nil {
		return infrastructureError("upload baseline source manifest: %v", err)
	}
	if stage.source.BundlePath != "" {
		if err := store.Create(ctx, stage.source.BundlePath, stage.inputPrefix+"source.bundle"); err != nil {
			return infrastructureError("upload baseline source bundle: %v", err)
		}
	}
	if stage.sqruffPath != "" {
		if err := store.Create(ctx, stage.sqruffPath, stage.inputPrefix+"sqruff.tar.gz"); err != nil {
			return infrastructureError("upload baseline sqruff artifact: %v", err)
		}
	}
	return nil
}

// cleanupFailedRemoteBaselineUpload 补偿 prepare 阶段已写入的部分 generation。
func cleanupFailedRemoteBaselineUpload(uploadErr error, store remoteBaselineOSSStore, prefix string) error {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.DeletePrefix(ctx, prefix); err != nil {
		return errors.Join(uploadErr, infrastructureError("cleanup partial baseline upload: %v", err))
	}
	return uploadErr
}

// downloadRemoteBaselineToolArtifact 在本地协调器下载并校验固定工具，再由 OSS 输入挂载交给 Seed。
func downloadRemoteBaselineToolArtifact(ctx context.Context, root, artifactURL, expectedSHA256 string) (string, error) {
	client := &http.Client{Timeout: remoteBaselineToolArtifactTimeout}
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= remoteBaselineToolArtifactAttempts; attempt++ {
		attempts = attempt
		artifactPath, retryable, err := downloadRemoteBaselineToolArtifactOnce(ctx, client, root, artifactURL, expectedSHA256)
		if err == nil {
			return artifactPath, nil
		}
		lastErr = err
		if !retryable || attempt == remoteBaselineToolArtifactAttempts {
			break
		}
		if err := waitRemoteBaselineToolArtifactRetry(ctx, attempt); err != nil {
			return "", errors.Join(lastErr, err)
		}
	}
	return "", fmt.Errorf("download tool artifact after %d attempt(s): %w", attempts, lastErr)
}

// downloadRemoteBaselineToolArtifactOnce 执行一次下载，并区分可重试传输错误与确定性合同错误。
func downloadRemoteBaselineToolArtifactOnce(ctx context.Context, client *http.Client, root, artifactURL, expectedSHA256 string) (string, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return "", false, err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", ctx.Err() == nil, err
	}
	if response.StatusCode != http.StatusOK {
		statusErr := errors.Join(fmt.Errorf("download tool artifact: HTTP %s", response.Status), response.Body.Close())
		return "", retryableRemoteBaselineToolArtifactStatus(response.StatusCode), statusErr
	}
	artifactPath := filepath.Join(root, "sqruff.tar.gz")
	file, err := os.OpenFile(artifactPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", false, errors.Join(err, response.Body.Close())
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, remoteBaselineToolArtifactMaxBytes+1))
	bodyErr := response.Body.Close()
	fileErr := file.Close()
	transferErr := errors.Join(copyErr, bodyErr, fileErr)
	if transferErr != nil {
		return "", fileErr == nil && ctx.Err() == nil, removeRemoteBaselineToolArtifact(artifactPath, transferErr)
	}
	if size <= 0 || size > remoteBaselineToolArtifactMaxBytes {
		return "", false, removeRemoteBaselineToolArtifact(artifactPath, fmt.Errorf("tool artifact size %d is invalid", size))
	}
	actualSHA256 := fmt.Sprintf("%x", hash.Sum(nil))
	if actualSHA256 != expectedSHA256 {
		return "", false, removeRemoteBaselineToolArtifact(artifactPath, fmt.Errorf("tool artifact SHA-256 is %s, want %s", actualSHA256, expectedSHA256))
	}
	return artifactPath, false, nil
}

// retryableRemoteBaselineToolArtifactStatus 只允许临时 HTTP 状态进入有界重试。
func retryableRemoteBaselineToolArtifactStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooEarly ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError && statusCode <= 599
}

// waitRemoteBaselineToolArtifactRetry 在上下文可取消的指数退避窗口后继续下载。
func waitRemoteBaselineToolArtifactRetry(ctx context.Context, attempt int) error {
	delay := remoteBaselineToolArtifactRetryDelay * time.Duration(1<<(attempt-1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func removeRemoteBaselineToolArtifact(path string, cause error) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, err)
	}
	return cause
}

// createRemoteBaselineSeed 创建执行 seed 脚本的 ECI 容器组。
func createRemoteBaselineSeed(ctx context.Context, session remoteBaselineRefreshSession, stage remoteBaselineArtifactStage) (*eci.Client, eci.SeedRequest, eci.ContainerGroup, error) {
	runtime, err := eci.New(eci.Config{Binary: session.config.AliyunCLI, RegionID: session.config.RegionID, VSwitchID: session.config.VSwitchID, SecurityGroupID: session.config.SecurityGroupID, WorkerRoleName: session.config.WorkerRoleName, Profile: session.config.CredentialProfile, Image: session.config.Runtime.Image, Deadline: remoteBaselineSeedDeadline, SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1, FallbackToPayAsYouGo: true})
	if err != nil {
		return nil, eci.SeedRequest{}, eci.ContainerGroup{}, infrastructureError("create baseline seed ECI client: %v", err)
	}
	request, err := buildRemoteBaselineSeedRequest(
		session.config,
		session.input,
		stage.source,
		session.accepted,
		session.acceptedRecommendedSizeGiB,
		stage.generation,
	)
	if err != nil {
		return nil, eci.SeedRequest{}, eci.ContainerGroup{}, infrastructureError("build baseline seed ECI request: %v", err)
	}
	group, err := runtime.CreateSeedContainerGroup(ctx, request)
	if err != nil {
		return nil, eci.SeedRequest{}, eci.ContainerGroup{}, infrastructureError("create remote baseline seed: %v", err)
	}
	return runtime, request, group, nil
}

// executeRemoteBaselineSeed 等待 seed 完成并下载匹配的 manifest。
func executeRemoteBaselineSeed(ctx context.Context, session remoteBaselineRefreshSession, runtime *eci.Client, request eci.SeedRequest, groupID string, stage remoteBaselineArtifactStage) (remoteci.BaselineManifest, string, error) {
	if err := waitRemoteBaselineSeed(ctx, runtime, groupID, request.ContainerName, stage.generation, session.input.Identity); err != nil {
		return remoteci.BaselineManifest{}, "", infrastructureError("execute remote baseline seed: %v", err)
	}
	manifest, digest, err := downloadRemoteBaselineManifest(ctx, session.config, stage.generation, session.input.Identity, session.input.GateSourceDigest)
	if err != nil {
		return remoteci.BaselineManifest{}, "", infrastructureError("verify remote baseline manifest: %v", err)
	}
	return manifest, digest, nil
}

// createRemoteBaselineCache 仅从 full Anchor 输出创建新的 DataCache。
func createRemoteBaselineCache(ctx context.Context, session remoteBaselineRefreshSession, stage remoteBaselineArtifactStage, manifest remoteci.BaselineManifest) (datacache.DataCache, error) {
	sizeGiB, err := remoteBaselineRecommendedSizeGiB(session.config, manifest)
	if err != nil {
		return datacache.DataCache{}, protocolError("plan remote baseline DataCache capacity: %v", err)
	}
	path := remoteBaselineCachePath(session.config, stage.generation)
	clientToken, err := remoteBaselineCacheClientToken(stage.generation, sizeGiB, manifest)
	if err != nil {
		return datacache.DataCache{}, protocolError("encode remote baseline DataCache idempotence identity: %v", err)
	}
	cache, err := session.cache.Create(ctx, datacache.CreateRequest{Name: remoteBaselineResourceName(stage.generation), Bucket: session.config.DataCache.Bucket, Path: path, SizeGiB: sizeGiB, RetentionDays: session.config.DataCache.RetentionDays, ClientToken: clientToken, Source: datacache.OSSDataSource{Bucket: session.config.OSS.Bucket, Endpoint: strings.TrimPrefix(session.config.OSS.InternalEndpoint, "https://"), Path: "/" + strings.TrimSuffix(stage.outputPrefix, "/"), RoleName: session.config.WorkerRoleName}, Tags: remoteBaselineResourceTags(stage.generation, session.input.Identity.MainTree)})
	if err != nil {
		return datacache.DataCache{}, infrastructureError("create remote baseline DataCache: %v", err)
	}
	return cache, nil
}

func remoteBaselineResourceTags(generation uint64, mainTree string) map[string]string {
	return map[string]string{
		"owner":      "super-dolphin-ci",
		"generation": strconv.FormatUint(generation, 10),
		"main-tree":  mainTree[:12],
	}
}

// acceptRemoteBaseline 在远端 main 未变化时构造新的 Anchor 或 delta 状态。
func acceptRemoteBaseline(session remoteBaselineRefreshSession, stage remoteBaselineArtifactStage, manifest remoteci.BaselineManifest, digest string, cache datacache.DataCache) (remoteci.BaselineState, error) {
	if err := manifest.Validate(); err != nil {
		return remoteci.BaselineState{}, protocolError("refuse invalid remote baseline manifest: %v", err)
	}
	if session.resolveRef == nil {
		return remoteci.BaselineState{}, infrastructureError("recheck remote main after baseline build: resolver is nil")
	}
	latest, err := session.resolveRef(session.options.RepositoryRoot, session.options.Remote, session.options.Ref)
	if err != nil {
		return remoteci.BaselineState{}, sourceError("recheck remote main after baseline build: %v", err)
	}
	if latest != session.input.Identity.MainCommit {
		return remoteci.BaselineState{}, sourceError("remote main changed during baseline build: started %s, now %s", session.input.Identity.MainCommit, latest)
	}
	acceptedAt := time.Now().UTC()
	state := remoteci.BaselineState{
		SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: stage.generation,
		MainCommit: manifest.MainCommit, MainTree: manifest.MainTree, Platform: manifest.Platform,
		PolicyDigest: manifest.PolicyDigest, ToolchainDigest: manifest.ToolchainDigest,
		RuntimeImage: manifest.RuntimeImage, GateBinarySHA256: manifest.GateBinarySHA256,
		RuntimeSeedSHA256:      manifest.RuntimeSeedManifestSHA256,
		BaselineManifestDigest: digest, SourceObjectPrefix: stage.generationPrefix,
		SourceHistoryVersion: remoteci.BaselineSourceHistorySchemaVersion,
		CreatedAt:            stage.createdAt, AcceptedAt: acceptedAt,
	}
	switch manifest.StorageMode {
	case remoteci.BaselineStorageModeAnchor:
		if err := bindRemoteBaselineAnchor(&state, stage, manifest, digest, cache, acceptedAt); err != nil {
			return remoteci.BaselineState{}, err
		}
	case remoteci.BaselineStorageModeDelta:
		if err := bindRemoteBaselineDelta(&state, session.accepted, stage, manifest, digest, acceptedAt); err != nil {
			return remoteci.BaselineState{}, err
		}
	default:
		return remoteci.BaselineState{}, protocolError("refuse unsupported remote baseline storage mode %q", manifest.StorageMode)
	}
	carryRemoteBaselineHistory(&state, session.accepted)
	return state, nil
}

// promoteRemoteBaseline 仅在 successor 复验、持久化和最终读取均成功后清理退休资源。
func promoteRemoteBaseline(ctx context.Context, session remoteBaselineRefreshSession, state *remoteci.BaselineState) (bool, error) {
	if state == nil {
		return false, infrastructureError("persist accepted remote baseline: state is nil")
	}
	verify := session.verifySuccessor
	if verify == nil {
		verify = verifyRemoteBaselineSuccessor
	}
	if err := verify(ctx, session, *state); err != nil {
		return false, infrastructureError("reverify accepted remote baseline: %v", err)
	}
	write := session.writeState
	if write == nil {
		write = writeRemoteBaselineState
	}
	if err := write(session.statePath, *state); err != nil {
		return false, infrastructureError("persist accepted remote baseline: %v", err)
	}
	read := session.readState
	if read == nil {
		read = loadRemoteBaselineState
	}
	persisted, err := read(session.statePath, false)
	if err != nil {
		return true, infrastructureError("read persisted remote baseline: %v", err)
	}
	if !remoteBaselineStatesEquivalent(persisted, *state) {
		return true, infrastructureError("persisted remote baseline does not match accepted successor")
	}
	cleanupLegacy := session.cleanupLegacy
	if cleanupLegacy == nil {
		cleanupLegacy = cleanupLegacyRemoteBaselines
	}
	if err := cleanupLegacy(ctx, session.cache, session.store, session.legacy); err != nil {
		return true, infrastructureError("cleanup incompatible remote baseline: %v", err)
	}
	cleanupRetired := session.cleanupRetired
	if cleanupRetired == nil {
		cleanupRetired = cleanupRetiredRemoteBaseline
	}
	if err := cleanupRetired(ctx, session.cache, session.store, session.statePath, state); err != nil {
		return true, infrastructureError("cleanup retired remote baseline: %v", err)
	}
	return true, nil
}

// remoteBaselineStatesEquivalent 比较状态的规范 JSON 表示，忽略 Go 空切片等非持久化差异。
func remoteBaselineStatesEquivalent(left, right remoteci.BaselineState) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

// verifyRemoteBaselineSuccessor 确认 successor 仍可运行，避免先退休旧链再发现新链不可用。
func verifyRemoteBaselineSuccessor(ctx context.Context, session remoteBaselineRefreshSession, state remoteci.BaselineState) error {
	if err := validateRunnableRemoteBaseline(session.config, state); err != nil {
		return err
	}
	return verifyAvailableDataCache(ctx, session.cache, state)
}

// bindRemoteBaselineAnchor 绑定本代新建且已可用的唯一 DataCache Anchor。
func bindRemoteBaselineAnchor(state *remoteci.BaselineState, stage remoteBaselineArtifactStage, manifest remoteci.BaselineManifest, digest string, cache datacache.DataCache, acceptedAt time.Time) error {
	if cache.Status != datacache.StatusAvailable {
		return infrastructureError("refuse unready remote baseline DataCache %q", cache.Status)
	}
	anchor := remoteci.BaselineCacheRef{
		Generation: stage.generation, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: digest, MainCommit: manifest.MainCommit, MainTree: manifest.MainTree,
		DataCacheID: cache.ID, DataCacheBucket: cache.Bucket, DataCachePath: cache.Path,
		SizeGiB: cache.SizeGiB, SourceObjectPrefix: stage.generationPrefix, AcceptedAt: acceptedAt,
	}
	state.Anchor = anchor
	state.DataCacheID, state.DataCacheBucket = anchor.DataCacheID, anchor.DataCacheBucket
	state.DataCachePath, state.DataCacheSizeGiB = anchor.DataCachePath, anchor.SizeGiB
	return nil
}

// bindRemoteBaselineDelta 追加一层 OSS source/cache 差量并复用既有 Anchor。
func bindRemoteBaselineDelta(state *remoteci.BaselineState, accepted remoteci.BaselineState, stage remoteBaselineArtifactStage, manifest remoteci.BaselineManifest, digest string, acceptedAt time.Time) error {
	if !remoteBaselineDeltaAnchorReusable(accepted) {
		return protocolError("remote baseline delta has no reusable bounded Anchor chain")
	}
	if !remoteBaselineDeltaAnchorMatchesManifest(accepted, manifest) {
		return protocolError("remote baseline delta changed an Anchor identity")
	}
	sourceLayer, err := remoteBaselineManifestSourceLayer(manifest)
	if err != nil {
		return protocolError("remote baseline delta source layer: %v", err)
	}
	if !remoteBaselineDeltaSourceExtendsAccepted(sourceLayer, accepted, manifest) {
		return protocolError("remote baseline delta does not extend the accepted source")
	}
	state.Anchor = accepted.CurrentAnchorRef()
	state.DataCacheID, state.DataCacheBucket = state.Anchor.DataCacheID, state.Anchor.DataCacheBucket
	state.DataCachePath, state.DataCacheSizeGiB = state.Anchor.DataCachePath, state.Anchor.SizeGiB
	state.Deltas = append(accepted.DeltaRefs(), remoteci.BaselineDeltaRef{
		Generation: stage.generation, SourceObjectPrefix: stage.generationPrefix,
		ManifestDigest: digest, BaseCommit: sourceLayer.BaseCommit, BaseTree: sourceLayer.BaseTree,
		MainCommit: manifest.MainCommit, MainTree: manifest.MainTree, AcceptedAt: acceptedAt,
	})
	return nil
}

// remoteBaselineDeltaAnchorReusable 验证已接受 Anchor 链仍可容纳一个 Delta。
func remoteBaselineDeltaAnchorReusable(accepted remoteci.BaselineState) bool {
	return accepted.SchemaVersion != 0 && !accepted.HasRetiredReferences() &&
		len(accepted.Deltas) < remoteBaselineDeltaLimit
}

// remoteBaselineDeltaAnchorMatchesManifest 验证 Delta 不改变所复用 Anchor 的不可变身份。
func remoteBaselineDeltaAnchorMatchesManifest(accepted remoteci.BaselineState, manifest remoteci.BaselineManifest) bool {
	return accepted.Platform == manifest.Platform && accepted.ToolchainDigest == manifest.ToolchainDigest &&
		accepted.RuntimeImage == manifest.RuntimeImage && accepted.RuntimeSeedSHA256 == manifest.RuntimeSeedManifestSHA256
}

// remoteBaselineDeltaSourceExtendsAccepted 验证 source Delta 连接已接受主线和新 manifest。
func remoteBaselineDeltaSourceExtendsAccepted(layer remoteci.BaselineLayer, accepted remoteci.BaselineState, manifest remoteci.BaselineManifest) bool {
	return layer.BaseCommit == accepted.MainCommit && layer.BaseTree == accepted.MainTree &&
		layer.TargetCommit == manifest.MainCommit && layer.TargetTree == manifest.MainTree
}

func remoteBaselineManifestSourceLayer(manifest remoteci.BaselineManifest) (remoteci.BaselineLayer, error) {
	for _, layer := range manifest.Layers {
		if layer.Name == "source" {
			return layer, nil
		}
	}
	return remoteci.BaselineLayer{}, errors.New("source layer is missing")
}

// carryRemoteBaselineHistory 保留上一代完整链，并登记不再被两条 live 链引用的旧资源。
func carryRemoteBaselineHistory(state *remoteci.BaselineState, accepted remoteci.BaselineState) {
	if accepted.SchemaVersion == 0 {
		return
	}
	previousAnchor := accepted.CurrentAnchorRef()
	state.PreviousAnchor = &previousAnchor
	state.PreviousDeltas = accepted.DeltaRefs()
	if accepted.PreviousAnchor != nil && !remoteBaselineAnchorIsLive(*accepted.PreviousAnchor, *state) {
		retiredAnchor := *accepted.PreviousAnchor
		state.RetiredAnchor = &retiredAnchor
	}
	for _, delta := range accepted.PreviousDeltas {
		if !remoteBaselineDeltaIsLive(delta, *state) {
			state.RetiredDeltas = append(state.RetiredDeltas, delta)
		}
	}
}

func remoteBaselineAnchorIsLive(candidate remoteci.BaselineCacheRef, state remoteci.BaselineState) bool {
	if sameRemoteBaselineAnchor(candidate, state.Anchor) {
		return true
	}
	return state.PreviousAnchor != nil && sameRemoteBaselineAnchor(candidate, *state.PreviousAnchor)
}

func sameRemoteBaselineAnchor(left, right remoteci.BaselineCacheRef) bool {
	return left.Generation == right.Generation && left.DataCacheID == right.DataCacheID &&
		left.DataCacheBucket == right.DataCacheBucket && left.DataCachePath == right.DataCachePath
}

// remoteBaselineDeltaIsLive 判断候选 Delta 是否仍被当前或上一条链引用。
func remoteBaselineDeltaIsLive(candidate remoteci.BaselineDeltaRef, state remoteci.BaselineState) bool {
	for _, deltas := range [][]remoteci.BaselineDeltaRef{state.Deltas, state.PreviousDeltas} {
		for _, delta := range deltas {
			if candidate.Generation == delta.Generation && candidate.SourceObjectPrefix == delta.SourceObjectPrefix &&
				candidate.ManifestDigest == delta.ManifestDigest {
				return true
			}
		}
	}
	return false
}

// cleanupUnacceptedRemoteArtifacts 删除未被接受 generation 的 OSS 工件。
func cleanupUnacceptedRemoteArtifacts(resultErr *error, store remoteBaselineOSSStore, prefix string, accepted *bool) {
	if *accepted {
		return
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.DeletePrefix(ctx, prefix); err != nil {
		*resultErr = errors.Join(*resultErr, infrastructureError("delete unaccepted remote baseline artifacts: %v", err))
	}
}

// cleanupRemoteBaselineSeed 删除已结束或失败的 seed 容器组。
func cleanupRemoteBaselineSeed(resultErr *error, runtime *eci.Client, groupID string) {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runtime.DeleteContainerGroup(ctx, groupID); err != nil {
		*resultErr = errors.Join(*resultErr, infrastructureError("delete remote baseline seed: %v", err))
	}
}

// cleanupUnacceptedRemoteCache 删除未被接受的 DataCache。
func cleanupUnacceptedRemoteCache(resultErr *error, client remoteBaselineDataCacheClient, cache datacache.DataCache, accepted *bool) {
	if *accepted {
		return
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := client.Delete(ctx, cache.ID, cache.Bucket, cache.Path); err != nil {
		*resultErr = errors.Join(*resultErr, infrastructureError("delete unaccepted remote baseline DataCache: %v", err))
	}
}

// resolveRemoteSqruffArtifact 选择与目标平台匹配的 Sqruff 工件及摘要。
func resolveRemoteSqruffArtifact(args []string, platform string) (string, string, error) {
	suffix := "AMD64"
	if platform == "linux/arm64" {
		suffix = "ARM64"
	}
	values := make(map[string]string, len(args))
	for _, argument := range args {
		key, value, ok := strings.Cut(argument, "=")
		if ok {
			values[key] = value
		}
	}
	artifactURL := values["SQRUFF_ARCHIVE_URL_"+suffix]
	digest := values["SQRUFF_ARCHIVE_SHA256_"+suffix]
	if strings.TrimSpace(artifactURL) == "" || len(digest) != 64 ||
		strings.Trim(digest, "0123456789abcdef") != "" {
		return "", "", errors.New("remote baseline Sqruff artifact is invalid")
	}
	return artifactURL, digest, nil
}

func newRemoteBaselineOSSStore(config remoteRunConfig) (remoteBaselineOSSStore, error) {
	return oss.NewCLI(oss.Config{
		Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint,
		Profile: config.CredentialProfile, Prefix: config.OSS.BaselinePrefix,
	})
}

func (lock *remoteBaselineRefreshLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func resolveRemoteRef(repositoryRoot, remote, ref string) (string, error) {
	output, err := remoteGitOutput(repositoryRoot, "ls-remote", "--exit-code", remote, ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[1] != ref {
		return "", errors.New("remote Git ref response is ambiguous")
	}
	return fields[0], nil
}

func remoteBaselineStatePath(configPath, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	extension := filepath.Ext(configPath)
	return strings.TrimSuffix(configPath, extension) + ".baseline-state.json"
}

func loadRemoteBaselineState(path string, allowMissing bool) (remoteci.BaselineState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return remoteci.BaselineState{}, nil
	}
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	var state remoteci.BaselineState
	if err := gatecontract.DecodeStrictJSON(data, &state); err != nil {
		return remoteci.BaselineState{}, err
	}
	return state, nil
}

// writeRemoteBaselineState 原子写入已经通过校验的基线状态。
func writeRemoteBaselineState(path string, state remoteci.BaselineState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".baseline-state-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func loadAcceptedRemoteBaseline(configPath, statePath string) (remoteci.BaselineState, error) {
	path := remoteBaselineStatePath(configPath, statePath)
	state, err := loadRemoteBaselineState(path, false)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	return state, state.Validate()
}

func remoteBaselineResourceName(generation uint64) string {
	return fmt.Sprintf("sdci-baseline-%d", generation)
}

func remoteBaselineSourcePrefix(config remoteRunConfig, generation uint64) string {
	return config.OSS.BaselinePrefix + strconv.FormatUint(generation, 10) + "/"
}

func remoteBaselineInputPrefix(config remoteRunConfig, generation uint64) string {
	return remoteBaselineSourcePrefix(config, generation) + "input/"
}

func remoteBaselineOutputPrefix(config remoteRunConfig, generation uint64) string {
	return remoteBaselineSourcePrefix(config, generation) + "output/"
}

func remoteBaselineCachePath(config remoteRunConfig, generation uint64) string {
	return config.DataCache.PathPrefix + "/" + strconv.FormatUint(generation, 10)
}

func remoteBaselineCacheClientToken(generation uint64, sizeGiB int, manifest remoteci.BaselineManifest) (string, error) {
	payload, err := json.Marshal(struct {
		SizeGiB  int                       `json:"size_gib"`
		Manifest remoteci.BaselineManifest `json:"manifest"`
	}{SizeGiB: sizeGiB, Manifest: manifest})
	if err != nil {
		return "", err
	}
	return remoteBaselineClientToken("cache", strconv.FormatUint(generation, 10), payload), nil
}

func remoteBaselineClientToken(kind, generation string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sdci-%s-%s-%x", kind, generation, sum[:8])
}

func remoteBaselineRenewToken(generation uint64, now time.Time) string {
	return fmt.Sprintf("sdci-renew-%d-%s", generation, now.UTC().Format("20060102"))
}

func encodeRemoteBaselineRefreshResult(stdout io.Writer, result remoteBaselineRefreshResult) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return infrastructureError("encode remote baseline refresh result: %v", err)
	}
	return nil
}
