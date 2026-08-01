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
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/source"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

const (
	remoteWorkerRoleEnv       = "SUPER_DOLPHIN_REMOTE_WORKER_ROLE"
	remoteOSSEndpointEnv      = "SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT"
	remoteOSSBucketEnv        = "SUPER_DOLPHIN_REMOTE_OSS_BUCKET"
	remoteRequestKeyEnv       = "SUPER_DOLPHIN_REMOTE_REQUEST_KEY"
	remoteRequestSHA256Env    = "SUPER_DOLPHIN_REMOTE_REQUEST_SHA256"
	remoteBaselineManifestEnv = "SUPER_DOLPHIN_REMOTE_RUNNER_MANIFEST"
	remoteSSLCAFileEnv        = "SSL_CERT_FILE"
	remoteRequestMaxBytes     = 64 << 10
	remoteManifestMaxBytes    = 1 << 20
	remoteSourcePatchMaxSize  = 1 << 30
	remoteDataCacheRootPath   = "/bootstrap"
	remoteExpandedBasePath    = "/opt/super-dolphin-gate"
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
	BaselineManifest string
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
	if err := verifyRemoteBootstrapTLS(remoteDataCacheRootPath, config.CAFile, config.BaselineManifest); err != nil {
		return infrastructureError("verify remote bootstrap TLS: %v", err)
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
	request, err := materializeRemoteSource(
		ctx,
		config,
		remoteDataCacheRootPath,
		remoteExpandedBasePath,
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
		RequestSHA256: values[remoteRequestSHA256Env], BaselineManifest: values[remoteBaselineManifestEnv],
		CAFile: values[remoteSSLCAFileEnv],
	}
	if err := validateRemoteMaterializeConfig(config); err != nil {
		return remoteMaterializeConfig{}, err
	}
	return config, nil
}

// loadRequiredRemoteMaterializeValues 读取并规范化物化请求的必填环境变量。
func loadRequiredRemoteMaterializeValues(getenv func(string) (string, bool)) (map[string]string, error) {
	names := []string{
		remoteWorkerRoleEnv,
		remoteOSSEndpointEnv,
		remoteOSSBucketEnv,
		remoteRequestKeyEnv,
		remoteRequestSHA256Env,
		remoteBaselineManifestEnv,
		remoteSSLCAFileEnv,
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
	if !validRemoteManifestDigest(config.BaselineManifest) || config.CAFile != filepath.Join(remoteDataCacheRootPath, "ca-certificates.crt") {
		return errors.New("remote bootstrap TLS identity is invalid")
	}
	return nil
}

// validRemoteManifestDigest 判断 DataCache manifest 摘要为规范 SHA-256。
func validRemoteManifestDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	encoded := strings.TrimPrefix(value, prefix)
	_, err := hex.DecodeString(encoded)
	return err == nil && encoded == strings.ToLower(encoded)
}

// validRemoteRequestObjectKey 判断请求对象键为规范相对路径且具有固定后缀。
func validRemoteRequestObjectKey(key string) bool {
	return key != "" && len(key) <= 1023 && !strings.HasPrefix(key, "/") &&
		!strings.ContainsAny(key, "\\\x00\r\n?#") && path.Clean(key) == key &&
		strings.HasSuffix(key, ".request.json")
}

// materializeRemoteSource 校验请求和 source 对象后，从受验基线克隆并交接执行根目录。
func materializeRemoteSource(
	ctx context.Context,
	config remoteMaterializeConfig,
	cacheRoot string,
	expandedRoot string,
	sourceRoot string,
	workRoot string,
	download remoteObjectDownload,
) (remoteci.ShardRequest, error) {
	if download == nil {
		return remoteci.ShardRequest{}, errors.New("remote object downloader is required")
	}
	request, err := loadRemoteShardRequest(ctx, config, download)
	if err != nil {
		return remoteci.ShardRequest{}, err
	}
	if err := materializeRemoteBaseline(ctx, cacheRoot, expandedRoot, sourceRoot, request, download); err != nil {
		return remoteci.ShardRequest{}, err
	}
	tempRoot, manifestPath, patchPath, err := stageRemoteSourceObjects(ctx, workRoot, request, download)
	if err != nil {
		return remoteci.ShardRequest{}, err
	}
	defer os.RemoveAll(tempRoot)
	if err := verifyRemoteMaterializedSource(ctx, sourceRoot, manifestPath, patchPath, request); err != nil {
		return remoteci.ShardRequest{}, err
	}
	if err := os.RemoveAll(tempRoot); err != nil {
		return remoteci.ShardRequest{}, fmt.Errorf("remove remote materialize staging root: %w", err)
	}
	tempRoot = ""
	if err := handoffRemoteWorkRoot(workRoot, os.Chmod, os.Chown); err != nil {
		return remoteci.ShardRequest{}, err
	}
	return request, nil
}

// verifyRemoteMaterializedSource 将 job patch 全字段绑定到已经逐层物化的 source 基线。
func verifyRemoteMaterializedSource(ctx context.Context, sourceRoot string, manifestPath string, patchPath string, request remoteci.ShardRequest) error {
	manifest, err := source.Verify(ctx, manifestPath, patchPath, sourceRoot)
	if err != nil {
		return fmt.Errorf("verify remote source: %w", err)
	}
	if manifest.BaseCommit != request.RunnerBaseCommit || manifest.BaseTree != request.RunnerBaseTree || manifest.TargetTree != request.SourceTreeSHA || manifest.PatchFormat != request.PatchFormat || manifest.PatchSHA256 != request.PatchSHA256 || manifest.PatchSize != request.PatchSize {
		return errors.New("remote source manifest does not match shard request")
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
	if request.AnchorManifest != config.BaselineManifest {
		return remoteci.ShardRequest{}, errors.New("remote shard request Anchor manifest does not match bootstrap")
	}
	if path.Dir(config.RequestKey) != path.Dir(request.PatchKey) {
		return remoteci.ShardRequest{}, errors.New("remote shard request object directory does not match source objects")
	}
	return request, nil
}

// stageRemoteSourceObjects 独占创建暂存目录并下载经摘要校验的 manifest 与 patch。
func stageRemoteSourceObjects(ctx context.Context, workRoot string, request remoteci.ShardRequest, download remoteObjectDownload) (string, string, string, error) {
	tempRoot, err := os.MkdirTemp(workRoot, ".remote-materialize-")
	if err != nil {
		return "", "", "", fmt.Errorf("create remote materialize root: %w", err)
	}
	manifestPath, patchPath := filepath.Join(tempRoot, "source.manifest.json"), filepath.Join(tempRoot, "source.patch")
	if err := downloadVerifiedFile(ctx, download, request.ManifestKey, request.ManifestSHA256, remoteManifestMaxBytes, manifestPath); err != nil {
		os.RemoveAll(tempRoot)
		return "", "", "", fmt.Errorf("download source manifest: %w", err)
	}
	if err := downloadVerifiedFile(ctx, download, request.PatchKey, request.PatchSHA256, remoteSourcePatchMaxSize, patchPath); err != nil {
		os.RemoveAll(tempRoot)
		return "", "", "", fmt.Errorf("download source patch: %w", err)
	}
	return tempRoot, manifestPath, patchPath, nil
}

// extractRemoteDataCacheBase 校验 DataCache 清单、文件与解压结果后展开远程基线。
func extractRemoteDataCacheBase(
	ctx context.Context,
	cacheRoot string,
	expandedRoot string,
	expectedManifestDigest string,
) (remoteci.BaselineManifest, error) {
	if err := validateRemoteDataCacheRoots(cacheRoot, expandedRoot); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	manifest, err := readRemoteBaselineManifest(cacheRoot, expectedManifestDigest)
	if err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := verifyRemoteCachedBinaries(cacheRoot, manifest); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := materializeRemoteBaselineArchives(ctx, cacheRoot, expandedRoot, manifest); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	if err := verifyExpandedRemoteBaseline(expandedRoot, manifest); err != nil {
		return remoteci.BaselineManifest{}, err
	}
	return manifest, nil
}

// materializeRemoteBaselineArchives 兼容旧单包，并对新格式先全量验签再按隔离层并发展开。
func materializeRemoteBaselineArchives(
	ctx context.Context,
	cacheRoot string,
	expandedRoot string,
	manifest remoteci.BaselineManifest,
) error {
	if len(manifest.Layers) == 0 {
		archivePath := filepath.Join(cacheRoot, "baseline.tar.gz")
		if err := verifyRemoteBaselineFile(archivePath, manifest.ArchiveSHA256, manifest.ArchiveSize); err != nil {
			return err
		}
		return extractRemoteBaselineArchive(ctx, archivePath, expandedRoot)
	}
	if err := runRemoteBaselineLayerStage(ctx, cacheRoot, expandedRoot, manifest.Layers, "verify",
		func(_ context.Context, archivePath string, _ string, layer remoteci.BaselineLayer) error {
			return verifyRemoteBaselineFile(archivePath, layer.SHA256, layer.Size)
		}); err != nil {
		return err
	}
	if err := runRemoteBaselineLayerStage(ctx, cacheRoot, expandedRoot, manifest.Layers, "extract",
		func(ctx context.Context, archivePath string, expandedRoot string, layer remoteci.BaselineLayer) error {
			return extractRemoteBaselineLayer(ctx, archivePath, expandedRoot, layer.Name)
		}); err != nil {
		return err
	}
	return copyRemoteBaselineGateBinary(cacheRoot, expandedRoot)
}

// extractRemoteBaselineArchive 展开一个已验证的确定性归档。
func extractRemoteBaselineArchive(ctx context.Context, archivePath string, expandedRoot string) error {
	return extractValidatedRemoteArchive(ctx, archivePath, expandedRoot)
}

// copyRemoteBaselineGateBinary 将顶层已验证 CLI 复制进分层基线的运行目录。
func copyRemoteBaselineGateBinary(cacheRoot string, expandedRoot string) error {
	source, err := os.Open(filepath.Join(cacheRoot, "bin", "super-dolphin-gate"))
	if err != nil {
		return fmt.Errorf("open remote gate binary: %w", err)
	}
	defer source.Close()
	binRoot := filepath.Join(expandedRoot, "bin")
	if err := os.Mkdir(binRoot, 0o755); err != nil {
		return fmt.Errorf("create expanded remote bin root: %w", err)
	}
	destinationPath := filepath.Join(binRoot, "super-dolphin-gate")
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return fmt.Errorf("create expanded remote gate binary: %w", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("copy expanded remote gate binary: %w", err)
	}
	if err := os.Chmod(destinationPath, 0o755); err != nil {
		return fmt.Errorf("make expanded remote gate binary executable: %w", err)
	}
	return nil
}

// verifyRemoteBootstrapTLS 在首次 OSS 请求前验证 DataCache 顶层 CA 制品。
func verifyRemoteBootstrapTLS(cacheRoot, caFile, expectedManifestDigest string) error {
	if caFile != filepath.Join(cacheRoot, "ca-certificates.crt") {
		return errors.New("remote bootstrap CA path does not match DataCache root")
	}
	manifest, err := readRemoteBaselineManifest(cacheRoot, expectedManifestDigest)
	if err != nil {
		return err
	}
	return verifyRemoteBaselineFile(caFile, manifest.CABundleSHA256, manifest.CABundleSize)
}

// validateRemoteDataCacheRoots 确认物化目录是不同的规范绝对路径且目标为空。
func validateRemoteDataCacheRoots(cacheRoot string, expandedRoot string) error {
	if !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot || !filepath.IsAbs(expandedRoot) || filepath.Clean(expandedRoot) != expandedRoot || cacheRoot == expandedRoot {
		return errors.New("remote DataCache roots are invalid")
	}
	entries, err := os.ReadDir(expandedRoot)
	if err != nil {
		return fmt.Errorf("read remote expanded baseline root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("remote expanded baseline root must be empty")
	}
	return nil
}

// readRemoteBaselineManifest 读取、限长并按请求中的 digest 验证 DataCache 清单。
func readRemoteBaselineManifest(cacheRoot string, expectedDigest string) (remoteci.BaselineManifest, error) {
	manifestData, err := os.ReadFile(filepath.Join(cacheRoot, "baseline-manifest.json"))
	if err != nil {
		return remoteci.BaselineManifest{}, fmt.Errorf("read remote baseline manifest: %w", err)
	}
	if len(manifestData) == 0 || len(manifestData) > remoteManifestMaxBytes ||
		(expectedDigest != "" && remoteci.BaselineManifestDigest(manifestData) != expectedDigest) {
		return remoteci.BaselineManifest{}, errors.New("remote baseline manifest identity mismatch")
	}
	return remoteci.DecodeBaselineManifest(manifestData)
}

// verifyRemoteCachedBinaries 在解压前验证 DataCache 中唯一 gate 二进制的身份。
func verifyRemoteCachedBinaries(cacheRoot string, manifest remoteci.BaselineManifest) error {
	for _, expected := range []struct {
		path, digest string
		size         int64
	}{
		{filepath.Join(cacheRoot, "bin", "super-dolphin-gate"), manifest.GateBinarySHA256, manifest.GateBinarySize},
	} {
		if err := verifyRemoteBaselineFile(expected.path, expected.digest, expected.size); err != nil {
			return err
		}
	}
	if err := verifyRemoteBaselineFile(filepath.Join(cacheRoot, "ca-certificates.crt"), manifest.CABundleSHA256, manifest.CABundleSize); err != nil {
		return err
	}
	return nil
}

// verifyRemoteBaselineFile 校验远程基线文件的规则类型、大小和 SHA-256。
func verifyRemoteBaselineFile(path string, expectedDigest string, expectedSize int64) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open remote baseline file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat remote baseline file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || expectedSize > 0 && info.Size() != expectedSize {
		return errors.New("remote baseline file size or type mismatch")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash remote baseline file: %w", err)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return errors.New("remote baseline file SHA-256 mismatch")
	}
	return nil
}

// verifyExpandedRemoteBaseline 拒绝不完整或包含意外顶层内容的已展开基线。
func verifyExpandedRemoteBaseline(root string, manifest remoteci.BaselineManifest) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read expanded remote baseline: %w", err)
	}
	required := map[string]bool{
		"bin": false, "cache-seed": false, "frontend-embed": false, "runtime": false, "source": false,
	}
	for _, entry := range entries {
		if _, ok := required[entry.Name()]; !ok || !entry.IsDir() {
			return errors.New("expanded remote baseline contains an unexpected top-level entry")
		}
		required[entry.Name()] = true
	}
	for _, present := range required {
		if !present {
			return errors.New("expanded remote baseline is incomplete")
		}
	}
	for _, expected := range []struct {
		path   string
		digest string
		size   int64
	}{
		{
			path:   filepath.Join(root, "bin", "super-dolphin-gate"),
			digest: manifest.GateBinarySHA256, size: manifest.GateBinarySize,
		},
		{
			path:   filepath.Join(root, "runtime", "manifest.json"),
			digest: manifest.RuntimeSeedManifestSHA256,
		},
	} {
		if err := verifyRemoteBaselineFile(expected.path, expected.digest, expected.size); err != nil {
			return err
		}
	}
	return nil
}

// cloneRemoteDataCacheBase 从已展开的只读基线复制 source，且要求目标为空。
func cloneRemoteDataCacheBase(ctx context.Context, cacheBase string, destination string) error {
	if strings.TrimSpace(cacheBase) == "" || strings.TrimSpace(destination) == "" {
		return errors.New("remote DataCache base and destination are required")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return fmt.Errorf("read remote source mount: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("remote source mount must be empty before image base clone")
	}
	command := exec.CommandContext(ctx, "git", "-c", "credential.interactive=never",
		"clone", "--quiet", "--no-hardlinks", cacheBase, destination)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clone DataCache base source: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
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
