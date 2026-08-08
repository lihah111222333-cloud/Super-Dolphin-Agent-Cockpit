package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

// remoteManifestInstallConfig 绑定 accepted bootstrap 投影、当前完整请求和候选 Gate 使用的 v2 摘要。
// Bootstrap 与 CurrentRequest 必须是两个独立对象，禁止把 v1 投影静默当作当前请求。
type remoteManifestInstallConfig struct {
	Bootstrap             remoteMaterializeConfig
	CurrentRequestKey     string
	CurrentRequestSHA256  string
	CurrentManifestDigest string
}

// loadRemoteManifestInstallConfig 读取候选 installer 使用的 bootstrap/full request 环境变量。
func loadRemoteManifestInstallConfig(getenv func(string) (string, bool)) (remoteManifestInstallConfig, error) {
	bootstrap, err := loadRemoteMaterializeConfig(getenv)
	if err != nil {
		return remoteManifestInstallConfig{}, err
	}
	values := make(map[string]string, 3)
	for _, name := range []string{
		remoteci.FullRequestKeyEnvironment,
		remoteci.FullRequestSHA256Environment,
		remoteci.FullManifestDigestEnvironment,
	} {
		value, ok := getenv(name)
		if !ok || value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return remoteManifestInstallConfig{}, fmt.Errorf("%s is required and canonical", name)
		}
		values[name] = value
	}
	config := remoteManifestInstallConfig{
		Bootstrap:             bootstrap,
		CurrentRequestKey:     values[remoteci.FullRequestKeyEnvironment],
		CurrentRequestSHA256:  values[remoteci.FullRequestSHA256Environment],
		CurrentManifestDigest: values[remoteci.FullManifestDigestEnvironment],
	}
	if err := validateRemoteManifestInstallConfig(config); err != nil {
		return remoteManifestInstallConfig{}, err
	}
	return config, nil
}

// runRemoteInstallManifest 执行候选 Gate init 的 v1 到 v2 manifest 原子替换。
func runRemoteInstallManifest(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("_remote-install-manifest does not accept arguments")
	}
	config, err := loadRemoteManifestInstallConfig(os.LookupEnv)
	if err != nil {
		return infrastructureError("load remote manifest install config: %v", err)
	}
	download := func(ctx context.Context, key string, maxBytes int64, destination io.Writer) (int64, error) {
		client, err := workerio.NewClient(workerio.Config{
			RoleName: config.Bootstrap.RoleName,
			Endpoint: config.Bootstrap.Endpoint,
			Bucket:   config.Bootstrap.Bucket,
			Key:      key,
			MaxBytes: maxBytes,
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
	if err := installCurrentRemoteShardManifest(ctx, config, gatecontract.ExecutorWorkRoot, download); err != nil {
		return infrastructureError("install current remote shard manifest: %v", err)
	}
	if stdout == nil {
		return infrastructureError("write remote manifest install status: output writer is required")
	}
	if _, err := fmt.Fprintf(stdout, "remote current shard manifest installed digest=%s\n", config.CurrentManifestDigest); err != nil {
		return infrastructureError("write remote manifest install status: %v", err)
	}
	return nil
}

// installCurrentRemoteShardManifest 严格校验两个请求投影，并原子替换 accepted v1 work manifest 为当前 v2。
// 只有该函数返回 nil 后才允许启动 worker。
func installCurrentRemoteShardManifest(
	ctx context.Context,
	config remoteManifestInstallConfig,
	workRoot string,
	download remoteObjectDownload,
) error {
	return installCurrentRemoteShardManifestWithOps(ctx, config, workRoot, download, os.Chmod, os.Chown)
}

// installCurrentRemoteShardManifestWithOps 提供可注入 ownership 操作的测试入口；生产入口固定使用 os.Chmod/os.Chown。
func installCurrentRemoteShardManifestWithOps(
	ctx context.Context,
	config remoteManifestInstallConfig,
	workRoot string,
	download remoteObjectDownload,
	chmod func(string, os.FileMode) error,
	chown func(string, int, int) error,
) error {
	if err := validateRemoteManifestInstallPreconditions(ctx, config, workRoot, download, chmod, chown); err != nil {
		return err
	}

	bootstrap, current, err := loadRemoteManifestInstallRequests(ctx, config, download)
	if err != nil {
		return err
	}

	manifestPath := filepath.Join(workRoot, filepath.Base(gatecontract.ExecutorShardExecutionManifestPath))
	oldManifest, err := readRemoteManifestInstallManifest(workRoot, manifestPath)
	if err != nil {
		return err
	}
	if err := remoteci.ValidateAcceptedBootstrapManifestBytes(oldManifest, bootstrap.ShardExecutionManifestDigest); err != nil {
		return fmt.Errorf("validate accepted bootstrap work manifest: %w", err)
	}

	currentManifest, currentDigest, err := gatecontract.EncodeShardExecutionManifest(current.ShardExecutionManifest())
	if err != nil {
		return fmt.Errorf("encode current shard execution manifest: %w", err)
	}
	if currentDigest != config.CurrentManifestDigest || currentDigest != current.ShardExecutionManifestDigest {
		return errors.New("current shard execution manifest digest does not match request")
	}
	if err := publishCurrentRemoteManifest(workRoot, manifestPath, currentManifest, chmod, chown); err != nil {
		return fmt.Errorf("publish current shard execution manifest: %w", err)
	}
	return nil
}

func validateRemoteManifestInstallPreconditions(
	ctx context.Context,
	config remoteManifestInstallConfig,
	workRoot string,
	download remoteObjectDownload,
	chmod func(string, os.FileMode) error,
	chown func(string, int, int) error,
) error {
	if ctx == nil {
		return errors.New("remote manifest install context is required")
	}
	if download == nil {
		return errors.New("remote manifest install object downloader is required")
	}
	if chmod == nil || chown == nil {
		return errors.New("remote manifest install ownership operations are required")
	}
	if err := validateRemoteManifestInstallConfig(config); err != nil {
		return err
	}
	return validateRemoteManifestWorkRoot(workRoot)
}

func loadRemoteManifestInstallRequests(
	ctx context.Context,
	config remoteManifestInstallConfig,
	download remoteObjectDownload,
) (remoteci.BootstrapShardRequest, remoteci.ShardRequest, error) {
	bootstrapBytes, err := downloadRemoteManifestRequest(ctx, download, config.Bootstrap.RequestKey, config.Bootstrap.RequestSHA256)
	if err != nil {
		return remoteci.BootstrapShardRequest{}, remoteci.ShardRequest{}, fmt.Errorf("download bootstrap shard request: %w", err)
	}
	bootstrap, err := remoteci.DecodeBootstrapShardRequest(bootstrapBytes)
	if err != nil {
		return remoteci.BootstrapShardRequest{}, remoteci.ShardRequest{}, fmt.Errorf("decode bootstrap shard request: %w", err)
	}
	if err := validateRemoteManifestBootstrapRequest(config, bootstrap); err != nil {
		return remoteci.BootstrapShardRequest{}, remoteci.ShardRequest{}, err
	}
	currentBytes, err := downloadRemoteManifestRequest(ctx, download, config.CurrentRequestKey, config.CurrentRequestSHA256)
	if err != nil {
		return remoteci.BootstrapShardRequest{}, remoteci.ShardRequest{}, fmt.Errorf("download current shard request: %w", err)
	}
	current, err := remoteci.DecodeShardRequest(currentBytes)
	if err != nil {
		return remoteci.BootstrapShardRequest{}, remoteci.ShardRequest{}, fmt.Errorf("decode current shard request: %w", err)
	}
	if err := validateRemoteManifestCurrentRequest(config, bootstrap, current); err != nil {
		return remoteci.BootstrapShardRequest{}, remoteci.ShardRequest{}, err
	}
	return bootstrap, current, nil
}

func validateRemoteManifestBootstrapRequest(config remoteManifestInstallConfig, bootstrap remoteci.BootstrapShardRequest) error {
	if bootstrap.AgentTokenDigest != config.Bootstrap.AgentTokenDigest {
		return errors.New("bootstrap shard request agent token digest does not match init environment")
	}
	if err := validateBootstrapRequestObjectDirectory(config.Bootstrap.RequestKey, bootstrap.SourceBundleKey); err != nil {
		return fmt.Errorf("bootstrap shard request object directory: %w", err)
	}
	return nil
}

func validateRemoteManifestCurrentRequest(
	config remoteManifestInstallConfig,
	bootstrap remoteci.BootstrapShardRequest,
	current remoteci.ShardRequest,
) error {
	if current.AgentTokenDigest != config.Bootstrap.AgentTokenDigest {
		return errors.New("current shard request agent token digest does not match bootstrap")
	}
	if err := validateBootstrapRequestObjectDirectory(config.CurrentRequestKey, current.SourceBundleKey); err != nil {
		return fmt.Errorf("current shard request object directory: %w", err)
	}
	if path.Dir(config.Bootstrap.RequestKey) != path.Dir(config.CurrentRequestKey) {
		return errors.New("bootstrap and current shard request object directories differ")
	}
	if path.Base(path.Dir(config.CurrentRequestKey)) != current.JobID || current.JobID != bootstrap.JobID {
		return errors.New("bootstrap and current shard request job identity drifted")
	}
	if err := remoteci.ValidateBootstrapIdentity(bootstrap, current); err != nil {
		return fmt.Errorf("bootstrap and current shard request identity: %w", err)
	}
	if current.ShardExecutionManifestDigest != config.CurrentManifestDigest {
		return errors.New("current shard request manifest digest does not match install configuration")
	}
	return nil
}

func validateRemoteManifestInstallConfig(config remoteManifestInstallConfig) error {
	if err := validateRemoteMaterializeConfig(config.Bootstrap); err != nil {
		return fmt.Errorf("bootstrap materialize config: %w", err)
	}
	if err := validateContentAddressedManifestRequestKey(config.Bootstrap.RequestKey, config.Bootstrap.RequestSHA256, ".bootstrap.request.json"); err != nil {
		return fmt.Errorf("bootstrap shard request key: %w", err)
	}
	if err := validateContentAddressedManifestRequestKey(config.CurrentRequestKey, config.CurrentRequestSHA256, ".request.json"); err != nil {
		return fmt.Errorf("current shard request key: %w", err)
	}
	if !validRemoteManifestDigest(config.CurrentManifestDigest) {
		return errors.New("current shard manifest digest is invalid")
	}
	if config.CurrentRequestKey == config.Bootstrap.RequestKey {
		return errors.New("bootstrap and current shard request object keys must differ")
	}
	return nil
}

func validateContentAddressedManifestRequestKey(key, digest, suffix string) error {
	if !validRemoteRequestObjectKey(key) {
		return errors.New("request object key is invalid")
	}
	if !validRemotePlainSHA256(digest) {
		return errors.New("request object SHA-256 is invalid")
	}
	if path.Base(key) != digest+suffix {
		return errors.New("request object key is not content addressed")
	}
	if path.Base(path.Dir(key)) == "." {
		return errors.New("request object key must include a job directory")
	}
	return nil
}

func validateBootstrapRequestObjectDirectory(requestKey, sourceBundleKey string) error {
	if !validRemoteRequestObjectKey(requestKey) {
		return errors.New("request object key is invalid")
	}
	if path.Dir(requestKey) != path.Dir(sourceBundleKey) {
		return errors.New("request object directory does not match source objects")
	}
	return nil
}

func validRemotePlainSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validRemoteManifestDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	return validRemotePlainSHA256(strings.TrimPrefix(value, "sha256:"))
}

func validateRemoteManifestWorkRoot(workRoot string) error {
	info, err := os.Lstat(workRoot)
	if err != nil {
		return fmt.Errorf("stat remote manifest work root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("remote manifest work root must be a physical directory")
	}
	return nil
}

func readRemoteManifestInstallManifest(workRoot, manifestPath string) ([]byte, error) {
	manifestName := filepath.Base(gatecontract.ExecutorShardExecutionManifestPath)
	if err := validateRemoteManifestWorkEntries(workRoot, manifestName); err != nil {
		return nil, err
	}
	if err := validateRemoteManifestFile(manifestPath); err != nil {
		return nil, err
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open accepted bootstrap work manifest: %w", err)
	}
	defer file.Close()
	var data bytes.Buffer
	limited := &remoteManifestBoundedWriter{destination: &data, limit: remoteManifestMaxBytes}
	if _, err := io.Copy(limited, file); err != nil {
		return nil, fmt.Errorf("read accepted bootstrap work manifest: %w", err)
	}
	if limited.exceeded {
		return nil, errors.New("accepted bootstrap work manifest exceeds canonical byte limit")
	}
	return data.Bytes(), nil
}

func validateRemoteManifestWorkEntries(workRoot, manifestName string) error {
	entries, err := os.ReadDir(workRoot)
	if err != nil {
		return fmt.Errorf("read remote manifest work root: %w", err)
	}
	if len(entries) != 3 {
		return errors.New("remote manifest work root contains an unsupported entry set")
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		seen[entry.Name()] = struct{}{}
	}
	if _, ok := seen[manifestName]; !ok {
		return errors.New("remote manifest work root is missing the fixed execution manifest")
	}
	for _, directoryName := range []string{"bin", "go-cache"} {
		if err := validateRemoteManifestWorkDirectory(workRoot, directoryName, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateRemoteManifestWorkDirectory(workRoot, directoryName string, seen map[string]struct{}) error {
	if _, ok := seen[directoryName]; !ok {
		return fmt.Errorf("remote manifest work root is missing %s", directoryName)
	}
	info, err := os.Lstat(filepath.Join(workRoot, directoryName))
	if err != nil {
		return fmt.Errorf("stat remote manifest work root %s: %w", directoryName, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("remote manifest work root %s must be a physical directory", directoryName)
	}
	return nil
}

// publishCurrentRemoteManifest 在旧 manifest 仍可用时准备新文件；所有可能失败的
// ownership 操作均发生在 rename 之前，避免失败路径删除或替换旧文件。
func publishCurrentRemoteManifest(
	workRoot, manifestPath string,
	data []byte,
	chmod func(string, os.FileMode) error,
	chown func(string, int, int) error,
) (returnErr error) {
	temporaryPath, err := prepareCurrentRemoteManifestTemp(workRoot, data, chmod, chown)
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil && temporaryPath != "" {
			returnErr = errors.Join(returnErr, os.Remove(temporaryPath))
		}
	}()
	if err := os.Rename(temporaryPath, manifestPath); err != nil {
		return fmt.Errorf("replace current shard execution manifest: %w", err)
	}
	temporaryPath = ""
	if err := syncRemoteManifestDirectory(workRoot); err != nil {
		return fmt.Errorf("sync current shard execution manifest directory: %w", err)
	}
	return nil
}

func prepareCurrentRemoteManifestTemp(
	workRoot string,
	data []byte,
	chmod func(string, os.FileMode) error,
	chown func(string, int, int) error,
) (string, error) {
	if chmod == nil || chown == nil {
		return "", errors.New("current manifest ownership operations are required")
	}
	if err := chmod(workRoot, 0o700); err != nil {
		return "", fmt.Errorf("protect current manifest work root: %w", err)
	}
	if err := chown(workRoot, remoteExecutorUID, remoteExecutorGID); err != nil {
		return "", fmt.Errorf("assign current manifest work root: %w", err)
	}
	temporary, err := os.CreateTemp(workRoot, ".shard-execution-manifest-current-*")
	if err != nil {
		return "", fmt.Errorf("create current shard execution manifest temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := writeRemoteManifestTemp(temporary, data); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return "", err
	}
	if err := chown(temporaryPath, remoteExecutorUID, remoteExecutorGID); err != nil {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("assign current shard execution manifest temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("close current shard execution manifest temporary file: %w", err)
	}
	return temporaryPath, nil
}

func downloadRemoteManifestRequest(ctx context.Context, download remoteObjectDownload, key, expectedDigest string) ([]byte, error) {
	if !validRemoteRequestObjectKey(key) {
		return nil, errors.New("remote request object key is invalid")
	}
	if !validRemotePlainSHA256(expectedDigest) {
		return nil, errors.New("remote request object SHA-256 is invalid")
	}
	var data bytes.Buffer
	limited := &remoteManifestBoundedWriter{destination: &data, limit: remoteRequestMaxBytes}
	if _, err := download(ctx, key, remoteRequestMaxBytes, limited); err != nil {
		return nil, err
	}
	if limited.exceeded {
		return nil, errors.New("remote request exceeds canonical byte limit")
	}
	if digestBytes(data.Bytes()) != expectedDigest {
		return nil, errors.New("remote request SHA-256 mismatch")
	}
	return data.Bytes(), nil
}

type remoteManifestBoundedWriter struct {
	destination *bytes.Buffer
	limit       int64
	written     int64
	exceeded    bool
}

// Write 实现有界字节写入，超过上限立即报错。
func (writer *remoteManifestBoundedWriter) Write(data []byte) (int, error) {
	if writer.written+int64(len(data)) > writer.limit {
		allowed := writer.limit - writer.written
		if allowed > 0 {
			_, _ = writer.destination.Write(data[:allowed])
			writer.written += allowed
		}
		writer.exceeded = true
		return int(allowed), errors.New("remote manifest byte limit exceeded")
	}
	count, err := writer.destination.Write(data)
	writer.written += int64(count)
	return count, err
}
