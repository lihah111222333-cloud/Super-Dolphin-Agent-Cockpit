package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

const (
	remoteWorkerRoleEnv       = "SUPER_DOLPHIN_REMOTE_WORKER_ROLE"
	remoteOSSEndpointEnv      = "SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT"
	remoteOSSBucketEnv        = "SUPER_DOLPHIN_REMOTE_OSS_BUCKET"
	remoteRequestKeyEnv       = "SUPER_DOLPHIN_REMOTE_REQUEST_KEY"
	remoteRequestSHA256Env    = "SUPER_DOLPHIN_REMOTE_REQUEST_SHA256"
	remoteAgentTokenDigestEnv = gatecontract.ExecutorAgentTokenDigestEnvironment
	remoteRequestMaxBytes     = 64 << 10
	remoteManifestMaxBytes    = 1 << 20
	remoteSourceBundleMaxSize = 1 << 30
	remoteSourceBaselineRoot  = "/opt/super-dolphin-gate/source-baseline.git"
	remoteExecutorUID         = 65532
	remoteExecutorGID         = 65532
	remoteMaterializeTimeout  = 45 * time.Minute
)

type remoteMaterializeConfig struct {
	RoleName         string
	Endpoint         string
	Bucket           string
	RequestKey       string
	RequestSHA256    string
	AgentTokenDigest string
	CAFile           string
}

type remoteObjectDownload func(context.Context, string, int64, io.Writer) (int64, error)

// runRemoteMaterialize 将内容寻址源码物化到 ECI init 容器的共享只读挂载源。
func runRemoteMaterialize(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("_remote-materialize does not accept arguments")
	}
	config, err := loadRemoteMaterializeConfig(os.LookupEnv)
	if err != nil {
		return infrastructureError("load remote materialize config: %v", err)
	}
	download := func(ctx context.Context, key string, maxBytes int64, destination io.Writer) (int64, error) {
		client, err := workerio.NewClient(workerio.Config{
			RoleName: config.RoleName, Endpoint: config.Endpoint, Bucket: config.Bucket,
			Key: key, MaxBytes: maxBytes,
		}, workerio.Dependencies{})
		if err != nil {
			return 0, err
		}
		return client.Download(ctx, destination)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := gateprivate.WithTimeout(signalCtx, remoteMaterializeTimeout)
	defer cancel()
	request, timing, err := materializeRemoteSourceWithTiming(
		ctx,
		config,
		gatecontract.ExecutorSourcePath,
		gatecontract.ExecutorWorkRoot,
		download,
	)
	if err != nil {
		return infrastructureError("materialize remote source: %v", err)
	}
	if _, err := fmt.Fprintf(stdout, "remote source materialized job=%s shard=%s tree=%s\n",
		request.JobID, request.ShardIdentity, request.SourceTreeSHA); err != nil {
		return infrastructureError("write remote materialize status: %v", err)
	}
	record, err := gatecontract.EncodeShardMaterializationTimingRecord(timing)
	if err != nil {
		return infrastructureError("encode remote materialization timing: %v", err)
	}
	if _, err := fmt.Fprintln(stdout, record); err != nil {
		return infrastructureError("write remote materialization timing: %v", err)
	}
	return nil
}

// loadRemoteMaterializeConfig 严格读取 init 容器物化请求所需的规范环境变量。
func loadRemoteMaterializeConfig(getenv func(string) (string, bool)) (remoteMaterializeConfig, error) {
	values, err := loadRequiredRemoteMaterializeValues(getenv)
	if err != nil {
		return remoteMaterializeConfig{}, err
	}
	config := remoteMaterializeConfig{
		RoleName: values[remoteWorkerRoleEnv], Endpoint: values[remoteOSSEndpointEnv],
		Bucket: values[remoteOSSBucketEnv], RequestKey: values[remoteRequestKeyEnv],
		RequestSHA256:    values[remoteRequestSHA256Env],
		AgentTokenDigest: values[remoteAgentTokenDigestEnv],
	}
	if err := validateRemoteMaterializeConfig(config); err != nil {
		return remoteMaterializeConfig{}, err
	}
	return config, nil
}

// verifyRemoteOCIProjectCache verifies the cache path supplied by the immutable
// runtime image before the main worker enables it as a GOCACHEPROG seed.
func verifyRemoteOCIProjectCache(request remoteci.ShardRequest) error {
	if request.OCIProjectCache == nil {
		return errors.New("remote OCI project cache is required")
	}
	return verifyRemoteOCIProjectCacheAtPath(request, request.OCIProjectCache.CachePath)
}

// verifyRemoteOCIProjectCacheAtPath 校验分片请求声明的 OCI 项目缓存路径，供隔离文件系统测试注入路径。
func verifyRemoteOCIProjectCacheAtPath(request remoteci.ShardRequest, cachePath string) error {
	cache := request.OCIProjectCache
	if cache == nil {
		return errors.New("remote OCI project cache is required")
	}
	if err := cache.ValidateForBaseline(request.RunnerBaseTree, request.BaselineToolchainDigest, cicontract.TargetPlatform, request.BaselineRuntimeImage); err != nil {
		return err
	}
	info, err := os.Lstat(cachePath)
	if err != nil {
		return fmt.Errorf("stat remote OCI project cache: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o222 != 0 {
		return errors.New("remote OCI project cache path is not a read-only physical directory")
	}
	return nil
}

// loadRequiredRemoteMaterializeValues 读取并规范化物化请求的必填环境变量。
func loadRequiredRemoteMaterializeValues(getenv func(string) (string, bool)) (map[string]string, error) {
	names := []string{
		remoteWorkerRoleEnv,
		remoteOSSEndpointEnv,
		remoteOSSBucketEnv,
		remoteRequestKeyEnv,
		remoteRequestSHA256Env,
		remoteAgentTokenDigestEnv,
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		value, ok := getenv(name)
		if !ok || value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("%s is required and canonical", name)
		}
		values[name] = value
	}
	return values, nil
}

// validateRemoteMaterializeConfig 校验远程请求和引导信任材料的固定身份。
func validateRemoteMaterializeConfig(config remoteMaterializeConfig) error {
	if !validRemoteRequestObjectKey(config.RequestKey) || len(config.RequestSHA256) != sha256.Size*2 {
		return errors.New("remote request object identity is invalid")
	}
	if _, err := hex.DecodeString(config.RequestSHA256); err != nil || strings.ToLower(config.RequestSHA256) != config.RequestSHA256 {
		return errors.New("remote request SHA-256 is invalid")
	}
	if err := cicontract.ValidateAgentTokenDigest(config.AgentTokenDigest); err != nil {
		return fmt.Errorf("remote agent token digest: %w", err)
	}
	return nil
}

// validRemoteRequestObjectKey 判断请求对象键为规范相对路径且具有固定后缀。
func validRemoteRequestObjectKey(key string) bool {
	return key != "" && len(key) <= 1023 && !strings.HasPrefix(key, "/") &&
		!strings.ContainsAny(key, "\\\x00\r\n?#") && path.Clean(key) == key &&
		strings.HasSuffix(key, ".request.json")
}

// materializeRemoteSourceWithTiming 校验、暂存并安装远程分片源码，同时记录基线缓存和源码物化阶段的耗时。
func materializeRemoteSourceWithTiming(
	ctx context.Context,
	config remoteMaterializeConfig,
	sourceRoot string,
	workRoot string,
	download remoteObjectDownload,
) (remoteci.ShardRequest, gatecontract.ShardMaterializationTiming, error) {
	timing := gatecontract.ShardMaterializationTiming{Measurement: gatecontract.MaterializationMeasurementMeasured}
	if download == nil {
		return remoteci.ShardRequest{}, timing, errors.New("remote object downloader is required")
	}
	request, err := loadRemoteShardRequest(ctx, config, download)
	if err != nil {
		return remoteci.ShardRequest{}, timing, err
	}
	timing.ShardIdentity = request.ShardIdentity
	baselineStarted := time.Now().UTC().UnixMilli()
	if err := verifyRemoteOCIProjectCache(request); err != nil {
		return remoteci.ShardRequest{}, timing, err
	}
	baselineCompleted := time.Now().UTC().UnixMilli()
	if baselineCompleted > baselineStarted {
		timing.Baseline = gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: baselineStarted, CompletedAtUnixMS: baselineCompleted, MaterializeMS: baselineCompleted - baselineStarted}
	}
	sourceStarted := time.Now().UTC().UnixMilli()
	downloadStarted := time.Now()
	tempRoot, _, _, err := stageRemoteSourceObjects(ctx, workRoot, request, download)
	if err != nil {
		return remoteci.ShardRequest{}, timing, err
	}
	timing.Source.DownloadMS = time.Since(downloadStarted).Milliseconds()
	defer os.RemoveAll(tempRoot)
	verifyStarted := time.Now()
	if err := verifyRemoteMaterializedSource(ctx, sourceRoot, tempRoot, request); err != nil {
		return remoteci.ShardRequest{}, timing, err
	}
	timing.Source.VerifyMS = time.Since(verifyStarted).Milliseconds()
	if err := os.RemoveAll(tempRoot); err != nil {
		return remoteci.ShardRequest{}, timing, fmt.Errorf("remove remote materialize staging root: %w", err)
	}
	tempRoot = ""
	installStarted := time.Now()
	if err := handoffRemoteWorkRoot(workRoot, os.Chmod, os.Chown); err != nil {
		return remoteci.ShardRequest{}, timing, err
	}
	timing.Source.InstallMS = time.Since(installStarted).Milliseconds()
	sourceCompleted := time.Now().UTC().UnixMilli()
	timing.Source.MaterializeMS = sourceCompleted - sourceStarted
	timing.Source.StartedAtUnixMS = sourceStarted
	timing.Source.CompletedAtUnixMS = sourceCompleted
	if err := timing.Validate(); err != nil {
		return remoteci.ShardRequest{}, timing, fmt.Errorf("validate remote materialization timing: %w", err)
	}
	return request, timing, nil
}

// verifyRemoteMaterializedSource 复核 source bundle/manifest 并安装精确 detached sourceRoot。
func verifyRemoteMaterializedSource(ctx context.Context, sourceRoot string, artifactRoot string, request remoteci.ShardRequest) error {
	return verifyRemoteMaterializedSourceAtBaselineRoot(ctx, sourceRoot, artifactRoot, request, remoteSourceBaselineRoot)
}

// verifyRemoteMaterializedSourceAtBaselineRoot 允许测试注入 baseline root，生产入口仍固定使用 accepted image 路径。
func verifyRemoteMaterializedSourceAtBaselineRoot(
	ctx context.Context,
	sourceRoot string,
	artifactRoot string,
	request remoteci.ShardRequest,
	baselineRoot string,
) error {
	if err := request.Source.Validate(); err != nil {
		return fmt.Errorf("validate remote request SourceSpec: %w", err)
	}
	if request.Source.SourceTreeSHA != request.SourceTreeSHA {
		return errors.New("remote request SourceSpec and source tree identity do not match")
	}
	baselineCommit, err := remoteci.DeterministicSourceBaselineCommitSHA(request.RunnerBaseTree, request.Source.ObjectFormat)
	if err != nil {
		return fmt.Errorf("derive accepted image source baseline commit: %w", err)
	}
	baseline := remoteci.SourceBaseline{
		RepositoryRoot: baselineRoot,
		CommitSHA:      baselineCommit,
		TreeSHA:        request.RunnerBaseTree,
		ObjectFormat:   request.Source.ObjectFormat,
	}
	manifest, err := remoteci.MaterializeVerifiedSourceBundle(ctx, artifactRoot, sourceRoot, baseline)
	if err != nil {
		return fmt.Errorf("verify and materialize remote source bundle: %w", err)
	}
	if err := verifyRemoteSourceManifestBinding(request, manifest, baseline); err != nil {
		return err
	}
	if err := verifyRemoteMaterializedGateCLICompileClosure(ctx, sourceRoot, request); err != nil {
		return err
	}
	return nil
}

// verifyRemoteSourceManifestBinding 独立复核 manifest、SourceSpec 和 shard request 的身份链。
func verifyRemoteSourceManifestBinding(
	request remoteci.ShardRequest,
	manifest remoteci.SourceMaterializationManifest,
	baseline remoteci.SourceBaseline,
) error {
	if !reflect.DeepEqual(manifest.Source, request.Source) {
		return errors.New("remote source manifest SourceSpec does not match shard request")
	}
	if manifest.Source.SourceTreeSHA != request.Source.SourceTreeSHA || manifest.SourceTreeSHA != request.SourceTreeSHA {
		return errors.New("remote source manifest SourceSpec tree does not match shard request")
	}
	if manifest.ObjectFormat != request.Source.ObjectFormat {
		return errors.New("remote source manifest object format does not match shard request")
	}
	if manifest.BaselineTreeSHA != request.RunnerBaseTree || manifest.BaselineCommitSHA != baseline.CommitSHA {
		return errors.New("remote source manifest baseline identity does not match shard request")
	}
	expectedTransport, err := remoteci.DeterministicSourceTransportCommitSHA(
		request.SourceTreeSHA,
		baseline.CommitSHA,
		request.Source.ObjectFormat,
	)
	if err != nil {
		return fmt.Errorf("derive expected source transport commit: %w", err)
	}
	if manifest.TransportCommitSHA != expectedTransport {
		return errors.New("remote source manifest transport commit does not match shard request")
	}
	return nil
}

// verifyRemoteMaterializedGateCLICompileClosure binds the init build to the exact materialized candidate tree.
func verifyRemoteMaterializedGateCLICompileClosure(ctx context.Context, sourceRoot string, request remoteci.ShardRequest) error {
	sourceDigest, toolchainDigest, _, err := remoteci.LoadGateCLICompileClosure(ctx, sourceRoot, request.SourceTreeSHA)
	if err != nil {
		return fmt.Errorf("resolve materialized gate CLI compile closure: %w", err)
	}
	if sourceDigest != request.CandidateGateSourceSHA256 || toolchainDigest != request.CandidateGateToolchainSHA256 {
		return errors.New("materialized gate CLI compile closure does not match shard request")
	}
	return nil
}

// loadRemoteShardRequest 下载并校验内容寻址的 shard 请求对象。
func loadRemoteShardRequest(ctx context.Context, config remoteMaterializeConfig, download remoteObjectDownload) (remoteci.ShardRequest, error) {
	var requestData bytes.Buffer
	if _, err := download(ctx, config.RequestKey, remoteRequestMaxBytes, &requestData); err != nil {
		return remoteci.ShardRequest{}, fmt.Errorf("download remote shard request: %w", err)
	}
	if digestBytes(requestData.Bytes()) != config.RequestSHA256 {
		return remoteci.ShardRequest{}, errors.New("remote shard request SHA-256 mismatch")
	}
	request, err := remoteci.DecodeShardRequest(requestData.Bytes())
	if err != nil {
		return remoteci.ShardRequest{}, err
	}
	if path.Dir(config.RequestKey) != path.Dir(request.SourceBundleKey) {
		return remoteci.ShardRequest{}, errors.New("remote shard request object directory does not match source objects")
	}
	if request.AgentTokenDigest != config.AgentTokenDigest {
		return remoteci.ShardRequest{}, errors.New("remote shard request agent token digest does not match init environment")
	}
	return request, nil
}

// stageRemoteSourceObjects 独占创建暂存目录并下载经摘要校验的 manifest 与 source bundle。
func stageRemoteSourceObjects(ctx context.Context, workRoot string, request remoteci.ShardRequest, download remoteObjectDownload) (string, string, string, error) {
	tempRoot, err := os.MkdirTemp(workRoot, ".remote-materialize-")
	if err != nil {
		return "", "", "", fmt.Errorf("create remote materialize root: %w", err)
	}
	bundlePath, manifestPath := filepath.Join(tempRoot, "source.bundle"), filepath.Join(tempRoot, "source-manifest.json")
	if err := downloadVerifiedFile(ctx, download, request.ManifestKey, request.ManifestSHA256, remoteManifestMaxBytes, manifestPath); err != nil {
		os.RemoveAll(tempRoot)
		return "", "", "", fmt.Errorf("download source manifest: %w", err)
	}
	if err := os.Chmod(manifestPath, 0o400); err != nil {
		os.RemoveAll(tempRoot)
		return "", "", "", fmt.Errorf("protect source manifest: %w", err)
	}
	if err := downloadVerifiedFile(ctx, download, request.SourceBundleKey, request.SourceBundleSHA256, remoteSourceBundleMaxSize, bundlePath); err != nil {
		os.RemoveAll(tempRoot)
		return "", "", "", fmt.Errorf("download source bundle: %w", err)
	}
	if err := os.Chmod(bundlePath, 0o400); err != nil {
		os.RemoveAll(tempRoot)
		return "", "", "", fmt.Errorf("protect source bundle: %w", err)
	}
	return tempRoot, bundlePath, manifestPath, nil
}

// handoffRemoteWorkRoot 在确认空目录后收紧权限并移交给固定 executor UID/GID。
func handoffRemoteWorkRoot(
	workRoot string,
	chmod func(string, os.FileMode) error,
	chown func(string, int, int) error,
) error {
	if chmod == nil || chown == nil {
		return errors.New("remote work root ownership operations are required")
	}
	entries, err := os.ReadDir(workRoot)
	if err != nil {
		return fmt.Errorf("read remote work root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("remote work root must be empty before executor handoff")
	}
	if err := chmod(workRoot, 0o700); err != nil {
		return fmt.Errorf("protect remote work root: %w", err)
	}
	if err := chown(workRoot, remoteExecutorUID, remoteExecutorGID); err != nil {
		return fmt.Errorf("assign remote work root to executor: %w", err)
	}
	return nil
}

// downloadVerifiedFile 下载、验证并原子发布带 SHA-256 声明的远端对象。
func downloadVerifiedFile(
	ctx context.Context,
	download remoteObjectDownload,
	key string,
	expectedDigest string,
	maxBytes int64,
	path string,
) (returnErr error) {
	expectedDigest, err := normalizedRemoteObjectDigest(expectedDigest)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".partial-")
	if err != nil {
		return fmt.Errorf("create remote object staging file: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, os.Remove(temporaryPath))
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure remote object staging file: %w", err)
	}
	if err := downloadRemoteObject(ctx, download, key, maxBytes, file, expectedDigest); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit remote object file: %w", err)
	}
	return nil
}

// normalizedRemoteObjectDigest 规范并校验远端对象声明的 SHA-256。
func normalizedRemoteObjectDigest(value string) (string, error) {
	digest := strings.TrimPrefix(value, "sha256:")
	if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		return "", errors.New("remote object expected SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("remote object expected SHA-256 is invalid")
	}
	return digest, nil
}

// downloadRemoteObject 下载对象并在关闭暂存文件前完成流错误检查。
func downloadRemoteObject(ctx context.Context, download remoteObjectDownload, key string, maxBytes int64, file *os.File, expectedDigest string) error {
	hash := sha256.New()
	_, downloadErr := download(ctx, key, maxBytes, io.MultiWriter(file, hash))
	closeErr := file.Close()
	if err := errors.Join(downloadErr, closeErr); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return errors.New("remote object SHA-256 mismatch")
	}
	return nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
