package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const (
	productionCoordinatorConfigEnv      = "SUPER_DOLPHIN_GATE_PRODUCTION_CONFIG"
	productionCoordinatorConfigMaxBytes = 1 << 20
)

type productionCoordinatorConfig struct {
	AcceptedImageRoot          string                                 `json:"accepted_image_root" production_path:"true"`
	BootstrapRootFile          string                                 `json:"bootstrap_root_file" production_path:"true"`
	BootstrapControllerFile    string                                 `json:"bootstrap_controller_file" production_path:"true"`
	BootstrapControllerKeyFile string                                 `json:"bootstrap_controller_key_file" production_path:"true"`
	CandidateStateRoot         string                                 `json:"candidate_state_root" production_path:"true"`
	CandidateBuildRoot         string                                 `json:"candidate_build_root" production_path:"true"`
	TrustedSourceRoot          string                                 `json:"trusted_source_root" production_path:"true"`
	SeccompProfile             string                                 `json:"seccomp_profile" production_path:"true"`
	Platform                   string                                 `json:"platform"`
	RepoID                     string                                 `json:"repo_id"`
	TrustedRef                 string                                 `json:"trusted_ref"`
	TrustedRepository          string                                 `json:"trusted_repository" production_path:"true"`
	GitExecutable              string                                 `json:"git_executable"`
	AcceptedImageSigners       []productionTrustedKey                 `json:"accepted_image_signers"`
	PromotionSigner            productionPromotionKey                 `json:"promotion_signer"`
	ResultReceiptAuthority     productionResultReceiptAuthorityConfig `json:"result_receipt_authority"`
	ActionGrantAuthority       productionActionGrantAuthorityConfig   `json:"action_grant_authority"`
	CandidateTTLSeconds        int64                                  `json:"candidate_ttl_seconds"`
	PromotionPollMillis        int64                                  `json:"promotion_poll_millis"`
	ShardsPerJob               int                                    `json:"shards_per_job"`
	MaxActiveCIWorkloads       int                                    `json:"max_active_ci_workloads"`
}

type productionTrustedKey struct {
	Signer    gatecontract.SignerIdentity `json:"signer"`
	PublicKey string                      `json:"public_key"`
}

type productionPromotionKey struct {
	Signer         gatecontract.SignerIdentity `json:"signer"`
	PrivateKeyFile string                      `json:"private_key_file" production_path:"true"`
}

type productionResultReceiptAuthorityConfig struct {
	Signer         gatecontract.SignerIdentity `json:"signer"`
	PublicKey      string                      `json:"public_key"`
	PrivateKeyFile string                      `json:"private_key_file" production_path:"true"`
}

type productionResultReceiptPrivateKey struct {
	PrivateKey string `json:"private_key"`
}

type productionActionGrantAuthorityConfig struct {
	Signer         gatecontract.SignerIdentity `json:"signer"`
	PublicKey      string                      `json:"public_key"`
	PrivateKeyFile string                      `json:"private_key_file" production_path:"true"`
	TTLSeconds     int64                       `json:"ttl_seconds"`
}

type productionActionGrantPrivateKey struct {
	PrivateKey string `json:"private_key"`
}

// Validate 校验 ActionGrant 私钥配置只携带规范的非空编码。
func (key productionActionGrantPrivateKey) Validate() error {
	if strings.TrimSpace(key.PrivateKey) == "" || strings.TrimSpace(key.PrivateKey) != key.PrivateKey {
		return errors.New("production action grant private key is required and canonical")
	}
	return nil
}

// Validate 校验 owner 私钥配置只携带规范的非空编码。
func (key productionResultReceiptPrivateKey) Validate() error {
	if strings.TrimSpace(key.PrivateKey) == "" || strings.TrimSpace(key.PrivateKey) != key.PrivateKey {
		return errors.New("production result receipt private key is required and canonical")
	}
	return nil
}

func loadProductionCoordinatorConfig() (productionCoordinatorConfig, error) {
	path, ok := os.LookupEnv(productionCoordinatorConfigEnv)
	if !ok || path == "" {
		return productionCoordinatorConfig{}, fmt.Errorf(
			"%w: %s is required",
			errCoordinatorDependency,
			productionCoordinatorConfigEnv,
		)
	}
	return loadProductionCoordinatorConfigFile(path)
}

// loadProductionCoordinatorConfigFile 从私有规范文件严格解码生产配置。
func loadProductionCoordinatorConfigFile(path string) (productionCoordinatorConfig, error) {
	canonical, err := canonicalProductionFile("production coordinator config", path)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	data, err := readProductionCoordinatorConfig(canonical)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	var stored storedProductionCoordinatorConfig
	if err := gatecontract.DecodeStrictJSON(data, &stored); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("decode production coordinator config: %w", err)
	}
	config := productionCoordinatorConfig(stored)
	if err := resolveProductionCoordinatorConfigPaths(filepath.Dir(canonical), &config); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("resolve production coordinator config paths: %w", err)
	}
	if err := config.Validate(); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("validate production coordinator config: %w", err)
	}
	return config, nil
}

// readProductionCoordinatorConfig 防止路径与已打开文件在读取期间发生身份漂移。
func readProductionCoordinatorConfig(canonical string) ([]byte, error) {
	data, err := gateprivate.ReadOwnerFile(canonical, productionCoordinatorConfigMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("read production coordinator config: %w", err)
	}
	return data, nil
}

// Validate 校验生产配置的显式信任根、执行根与签名者集合。
func (config productionCoordinatorConfig) Validate() error {
	if err := config.validateIdentity(); err != nil {
		return err
	}
	if err := config.validatePaths(); err != nil {
		return err
	}
	if err := config.validateSchedulingPolicy(); err != nil {
		return err
	}
	return validateProductionRootSeparation(config)
}

// validateSchedulingPolicy 校验 owner 与 scheduler 共享的显式并发契约。
func (config productionCoordinatorConfig) validateSchedulingPolicy() error {
	if config.ShardsPerJob < 1 {
		return errors.New("production coordinator shards_per_job must be positive")
	}
	if config.MaxActiveCIWorkloads < 1 {
		return errors.New("production coordinator max_active_ci_workloads must be positive")
	}
	if config.MaxActiveCIWorkloads < config.ShardsPerJob {
		return errors.New("production coordinator max_active_ci_workloads must be at least shards_per_job")
	}
	return nil
}

// validateIdentity 校验 repository、ref、platform 与 signer 集合均显式且规范。
func (config productionCoordinatorConfig) validateIdentity() error {
	if err := config.validateRepositoryIdentity(); err != nil {
		return err
	}
	if err := config.validatePromotionIdentity(); err != nil {
		return err
	}
	if err := config.validateReceiptAuthorityIdentity(); err != nil {
		return err
	}
	if err := config.validateActionGrantAuthorityIdentity(); err != nil {
		return err
	}
	return config.validateAuthoritySeparation()
}

// validateRepositoryIdentity 校验 repository、ref、platform 与验签根显式且规范。
func (config productionCoordinatorConfig) validateRepositoryIdentity() error {
	if strings.TrimSpace(config.RepoID) == "" || strings.TrimSpace(config.RepoID) != config.RepoID {
		return errors.New("production coordinator repo_id is required and canonical")
	}
	if !strings.HasPrefix(config.TrustedRef, "refs/") || strings.TrimSpace(config.TrustedRef) != config.TrustedRef {
		return errors.New("production coordinator trusted_ref must be a canonical full ref")
	}
	if strings.Count(config.Platform, "/") < 1 || strings.TrimSpace(config.Platform) != config.Platform {
		return errors.New("production coordinator platform must be explicit os/architecture[/variant]")
	}
	if len(config.AcceptedImageSigners) == 0 {
		return errors.New("production coordinator accepted_image_signers are required")
	}
	return nil
}

// validatePromotionIdentity 校验 signer、candidate TTL 与 watcher cadence。
func (config productionCoordinatorConfig) validatePromotionIdentity() error {
	if err := config.PromotionSigner.Signer.Validate(); err != nil {
		return fmt.Errorf("production coordinator promotion_signer: %w", err)
	}
	if config.CandidateTTLSeconds <= 0 || config.CandidateTTLSeconds > int64((7*24*time.Hour)/time.Second) {
		return errors.New("production coordinator candidate_ttl_seconds must be within 1..604800")
	}
	if config.PromotionPollMillis < coordinatorPromotionPollMinMillis ||
		config.PromotionPollMillis > coordinatorPromotionPollMaxMillis {
		return errors.New("production coordinator promotion_poll_millis must be within 5000..60000")
	}
	return nil
}

// validateReceiptAuthorityIdentity 校验 receipt signer 及其密钥位置均已显式配置。
func (config productionCoordinatorConfig) validateReceiptAuthorityIdentity() error {
	if err := config.ResultReceiptAuthority.Signer.Validate(); err != nil {
		return fmt.Errorf("production result receipt signer: %w", err)
	}
	if strings.TrimSpace(config.ResultReceiptAuthority.PublicKey) == "" ||
		strings.TrimSpace(config.ResultReceiptAuthority.PrivateKeyFile) == "" {
		return errors.New("production result receipt public key and private key file are required")
	}
	return nil
}

// validateActionGrantAuthorityIdentity 校验 grant authority 独立身份、密钥与期限。
func (config productionCoordinatorConfig) validateActionGrantAuthorityIdentity() error {
	if err := config.ActionGrantAuthority.Signer.Validate(); err != nil {
		return fmt.Errorf("production action grant signer: %w", err)
	}
	if strings.TrimSpace(config.ActionGrantAuthority.PublicKey) == "" ||
		strings.TrimSpace(config.ActionGrantAuthority.PrivateKeyFile) == "" {
		return errors.New("production action grant public key and private key file are required")
	}
	if config.ActionGrantAuthority.TTLSeconds <= 0 || config.ActionGrantAuthority.TTLSeconds > 900 {
		return errors.New("production action grant ttl_seconds must be within 1..900")
	}
	return nil
}

type productionAuthorityIdentity struct {
	name      string
	signer    gatecontract.SignerIdentity
	publicKey string
}

// validateAuthoritySeparation 拒绝 promotion/bootstrap、receipt 与 ActionGrant 复用身份或公钥。
func (config productionCoordinatorConfig) validateAuthoritySeparation() error {
	if _, err := newProductionSignatureVerifier(config.AcceptedImageSigners); err != nil {
		return err
	}
	promotionPublicKey := ""
	for _, trusted := range config.AcceptedImageSigners {
		if trusted.Signer == config.PromotionSigner.Signer {
			promotionPublicKey = trusted.PublicKey
			break
		}
	}
	if promotionPublicKey == "" {
		return errors.New("production promotion signer must have an accepted image public key")
	}
	return validateProductionAuthoritySeparation([]productionAuthorityIdentity{
		{name: "promotion/bootstrap", signer: config.PromotionSigner.Signer, publicKey: promotionPublicKey},
		{name: "result receipt", signer: config.ResultReceiptAuthority.Signer, publicKey: config.ResultReceiptAuthority.PublicKey},
		{name: "action grant", signer: config.ActionGrantAuthority.Signer, publicKey: config.ActionGrantAuthority.PublicKey},
	})
}

// validateProductionAuthoritySeparation 对三类 authority 执行身份和 Ed25519 公钥两两去重。
func validateProductionAuthoritySeparation(authorities []productionAuthorityIdentity) error {
	if len(authorities) != 3 {
		return errors.New("production authority separation requires exactly three authorities")
	}
	publicKeys := make([][]byte, len(authorities))
	for index, authority := range authorities {
		if err := authority.signer.Validate(); err != nil {
			return fmt.Errorf("validate production %s signer: %w", authority.name, err)
		}
		publicKey, err := decodeProductionBootstrapPublicKey(authority.publicKey)
		if err != nil {
			return fmt.Errorf("validate production %s public key: %w", authority.name, err)
		}
		publicKeys[index] = publicKey
	}
	for left := range authorities {
		for right := left + 1; right < len(authorities); right++ {
			if authorities[left].signer == authorities[right].signer {
				return fmt.Errorf("production %s and %s signer identities must be distinct", authorities[left].name, authorities[right].name)
			}
			if bytes.Equal(publicKeys[left], publicKeys[right]) {
				return fmt.Errorf("production %s and %s public keys must be distinct", authorities[left].name, authorities[right].name)
			}
		}
	}
	return nil
}

// validatePaths 校验所有 production roots 与私钥文件均为仓库外私有规范路径。
func (config productionCoordinatorConfig) validatePaths() error {
	for _, path := range []string{
		config.AcceptedImageRoot, config.CandidateStateRoot, config.CandidateBuildRoot,
		config.TrustedSourceRoot, config.TrustedRepository,
	} {
		if _, err := canonicalProductionDirectory(path); err != nil {
			return err
		}
	}
	if err := config.validateAuthorityFiles(); err != nil {
		return err
	}
	if _, err := canonicalProductionGitExecutable(config.GitExecutable); err != nil {
		return err
	}
	return config.validateAuthorityFilesOutsideRoots()
}

func (config productionCoordinatorConfig) validateAuthorityFiles() error {
	if _, err := canonicalProductionFile("seccomp profile", config.SeccompProfile); err != nil {
		return err
	}
	return config.validateAuthorityKeyPaths()
}

// validateAuthorityKeyPaths 校验三类 authority 私钥均为独立的仓库外私有文件。
func (config productionCoordinatorConfig) validateAuthorityKeyPaths() error {
	if _, err := canonicalProductionFile("promotion private key", config.PromotionSigner.PrivateKeyFile); err != nil {
		return err
	}
	if _, err := canonicalProductionFile("bootstrap trust root", config.BootstrapRootFile); err != nil {
		return err
	}
	if _, err := canonicalProductionExecutable("bootstrap controller", config.BootstrapControllerFile); err != nil {
		return err
	}
	if _, err := canonicalProductionFile("bootstrap controller private key", config.BootstrapControllerKeyFile); err != nil {
		return err
	}
	if _, err := canonicalProductionFile("result receipt private key", config.ResultReceiptAuthority.PrivateKeyFile); err != nil {
		return err
	}
	if _, err := canonicalProductionFile("action grant private key", config.ActionGrantAuthority.PrivateKeyFile); err != nil {
		return err
	}
	if config.ActionGrantAuthority.PrivateKeyFile == config.ResultReceiptAuthority.PrivateKeyFile ||
		config.ActionGrantAuthority.PrivateKeyFile == config.PromotionSigner.PrivateKeyFile {
		return errors.New("action grant private key file must be distinct from receipt and promotion keys")
	}
	return nil
}

// validateAuthorityFilesOutsideRoots 阻止 authority 文件落入任一生产数据根。
func (config productionCoordinatorConfig) validateAuthorityFilesOutsideRoots() error {
	for _, root := range []string{
		config.AcceptedImageRoot, config.CandidateStateRoot, config.CandidateBuildRoot,
		config.TrustedSourceRoot, config.TrustedRepository,
	} {
		if productionPathContains(root, config.PromotionSigner.PrivateKeyFile) {
			return errors.New("promotion private key must be outside all production data, build, source, and Git roots")
		}
		if productionPathContains(root, config.BootstrapRootFile) {
			return errors.New("bootstrap trust root must be outside all production data, build, source, and Git roots")
		}
		if productionPathContains(root, config.BootstrapControllerFile) {
			return errors.New("bootstrap controller must be outside all production data, build, source, and Git roots")
		}
		if productionPathContains(root, config.BootstrapControllerKeyFile) {
			return errors.New("bootstrap controller private key must be outside all production data, build, source, and Git roots")
		}
		if productionPathContains(root, config.ResultReceiptAuthority.PrivateKeyFile) {
			return errors.New("result receipt private key must be outside all production data, build, source, and Git roots")
		}
		if productionPathContains(root, config.ActionGrantAuthority.PrivateKeyFile) {
			return errors.New("action grant private key must be outside all production data, build, source, and Git roots")
		}
	}
	return nil
}

// canonicalProductionExecutable 在私有规范文件约束上追加 owner execute bit。
func canonicalProductionExecutable(name string, path string) (string, error) {
	canonical, err := canonicalProductionFile(name, path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect %s executable: %w", name, err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		return "", fmt.Errorf("%s must be owner-executable", name)
	}
	return canonical, nil
}

// validateProductionRootSeparation 阻断信任状态、候选构建、源码快照和 bare mirror 互相嵌套。
func validateProductionRootSeparation(config productionCoordinatorConfig) error {
	roots := []string{
		config.AcceptedImageRoot, config.CandidateStateRoot, config.CandidateBuildRoot,
		config.TrustedSourceRoot, config.TrustedRepository,
	}
	for left := range roots {
		for right := left + 1; right < len(roots); right++ {
			if productionPathsOverlap(roots[left], roots[right]) {
				return fmt.Errorf("production coordinator roots must not overlap: %q and %q", roots[left], roots[right])
			}
		}
	}
	return nil
}

// canonicalProductionDirectory 要求目录规范、私有、无符号链接且不在候选 worktree 内。
func canonicalProductionDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("production coordinator directory must be canonical and absolute: %q", path)
	}
	resolved, err := gateprivate.CanonicalOwnerDirectory(path)
	if err != nil {
		return "", fmt.Errorf("production coordinator directory: %w", err)
	}
	if err := rejectProductionWorktreePath(path); err != nil {
		return "", err
	}
	return resolved, nil
}

// canonicalProductionFile 要求文件与其父目录均为仓库外私有路径。
func canonicalProductionFile(name string, path string) (string, error) {
	resolved, err := gateprivate.CanonicalOwnerFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if _, err := canonicalProductionDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return resolved, nil
}

// rejectProductionWorktreePath 沿父链拒绝任何带 .git 标记的非 bare worktree。
func rejectProductionWorktreePath(path string) error {
	for directory := path; ; directory = filepath.Dir(directory) {
		if _, err := os.Lstat(filepath.Join(directory, ".git")); err == nil {
			return fmt.Errorf("production trust path must be outside a Git worktree: %q", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect production trust path ancestry: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
	}
}

func productionPathsOverlap(left string, right string) bool {
	return productionPathContains(left, right) || productionPathContains(right, left)
}

func productionPathContains(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type truthImageEnsureService interface {
	EnsureImage(context.Context, localci.TruthImageEnsureRequest) (localci.TruthImageEnsureResult, error)
}

type dockerFreshContainerService interface {
	RunFreshContainer(context.Context, localci.FreshContainerRequest) (localci.FreshContainerResult, error)
	RecoverFreshContainer(context.Context, localci.FreshContainerRecoveryRequest) (localci.FreshContainerResult, error)
	ProbeFreshContainerRecovery(context.Context, localci.FreshContainerRecoveryRequest) (localci.FreshContainerRecoveryObservation, error)
	CleanupUnprovedFreshContainer(context.Context, localci.FreshContainerCleanupRequest) (localci.FreshContainerResult, error)
}

// ensureScheduler 在锁内丢弃失效 transport adapter，只有 connector 返回可用 client 后才替换共享调度连接。
func (client *coordinatorTransportClient) ensureScheduler(ctx context.Context) error {
	if client == nil || ctx == nil {
		return fmt.Errorf("%w: client and context are required", errCoordinatorDependency)
	}
	client.schedulerMu.Lock()
	defer client.schedulerMu.Unlock()
	if client.closed {
		return localci.ErrSchedulerClosed
	}
	if schedulerConnectionUsable(client.scheduler) {
		return nil
	}
	if client.schedulerConnector == nil {
		return fmt.Errorf("%w: scheduler connector is required", errCoordinatorDependency)
	}
	stale := client.scheduler
	client.scheduler = nil
	closeCoordinatorScheduler(stale)
	scheduler, err := connectAvailableCoordinatorScheduler(ctx, client.schedulerConnector)
	if err != nil {
		return err
	}
	client.scheduler = scheduler
	return nil
}

// schedulerConnectionUsable 仅接受非空且由 adapter 明确报告可用的 scheduler client。
func schedulerConnectionUsable(scheduler coordinatorSchedulerClient) bool {
	return scheduler != nil && scheduler.Available()
}

// closeCoordinatorScheduler 关闭被替换或拒绝的 adapter，忽略 close 错误以保留原始连接失败作为主因。
func closeCoordinatorScheduler(scheduler coordinatorSchedulerClient) {
	if scheduler != nil {
		_ = scheduler.Close()
	}
}

// connectAvailableCoordinatorScheduler 拒绝 connector 返回的空或不可用 adapter，并在返回前关闭该临时连接。
func connectAvailableCoordinatorScheduler(
	ctx context.Context,
	connector func(context.Context) (coordinatorSchedulerClient, error),
) (coordinatorSchedulerClient, error) {
	scheduler, err := connector(ctx)
	if err != nil {
		return nil, err
	}
	if schedulerConnectionUsable(scheduler) {
		return scheduler, nil
	}
	closeCoordinatorScheduler(scheduler)
	return nil, fmt.Errorf("%w: scheduler connector returned an unavailable client", errCoordinatorDependency)
}

type productionImageEnsurer struct {
	truth    truthImageEnsureService
	platform string
}

// EnsureImage 从提交的 Git object tree 解析镜像输入，并且只映射 accepted runnable identity。
func (ensurer *productionImageEnsurer) EnsureImage(
	ctx context.Context,
	request imageEnsureRequest,
) (ensuredImage, error) {
	if ensurer == nil || ensurer.truth == nil || ensurer.platform == "" {
		return ensuredImage{}, errors.New("production image ensurer is not configured")
	}
	tree, err := localci.LoadReadOnlyGitTree(ctx, request.RepositoryRoot, request.Plan.Source)
	if err != nil {
		return ensuredImage{}, fmt.Errorf("load submitted image input tree: %w", err)
	}
	if tree.Source.SourceTreeSHA != request.JobSourceTreeSHA {
		return ensuredImage{}, errors.New("submitted image input tree does not match job source tree")
	}
	result, err := ensurer.truth.EnsureImage(ctx, localci.TruthImageEnsureRequest{
		Tree: tree, PolicyDigest: request.Plan.PolicyDigest, Platform: ensurer.platform,
	})
	if err != nil {
		return ensuredImage{}, err
	}
	if err := result.Validate(); err != nil {
		return ensuredImage{}, fmt.Errorf("validate truth image result: %w", err)
	}
	if result.Status != localci.TruthImageEnsureAccepted || result.SubmittedJobSourceTree != request.JobSourceTreeSHA {
		return ensuredImage{}, errors.New("truth image ensurer did not return an accepted image for the submitted tree")
	}
	return ensuredImage{
		Identity: result.Image, AcceptedRecord: result.AcceptedRecord,
		Truth: localci.FreshContainerImageTruth{
			PolicyDigest: result.PolicyDigest, BuildSourceTreeSHA: result.AcceptedImageBuildSourceTree,
			InputDigest: result.ImageInputDigest, ToolchainDigest: result.ToolchainDigest,
			SchemaVersion: result.ImageSchemaVersion,
		},
		ImageProvenanceSourceTreeSHA: result.AcceptedImageBuildSourceTree,
	}, nil
}

type productionSourceMaterializer struct {
	gitPath string
}

// Materialize 把 SourceSpec 封装为 bundle 后检出到一次性私有快照。
func (materializer *productionSourceMaterializer) Materialize(
	ctx context.Context,
	request sourceMaterializeRequest,
) (result materializedJobSource, retErr error) {
	if materializer == nil || materializer.gitPath == "" || ctx == nil {
		return materializedJobSource{}, errors.New("production source materializer is not configured")
	}
	if err := os.Mkdir(request.OutputRoot, 0o700); err != nil {
		return materializedJobSource{}, fmt.Errorf("create source output root: %w", err)
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, os.RemoveAll(request.OutputRoot))
		}
	}()
	materialized, err := localci.MaterializeSource(ctx, request.RepositoryRoot, request.Source, request.OutputRoot)
	if err != nil {
		return materializedJobSource{}, err
	}
	snapshotDir, err := materializer.checkoutSnapshot(ctx, request.OutputRoot, materialized)
	if err != nil {
		return materializedJobSource{}, err
	}
	return materializedJobSource{
		SnapshotDir: snapshotDir, SourceTreeSHA: materialized.Manifest.SourceTreeSHA,
		Cleanup: func() error { return os.RemoveAll(request.OutputRoot) },
	}, nil
}

// checkoutSnapshot 从自包含 bundle 导入固定 refs 并检出 materialized commit。
func (materializer *productionSourceMaterializer) checkoutSnapshot(
	ctx context.Context,
	outputRoot string,
	materialized localci.SourceMaterialization,
) (string, error) {
	snapshotDir := filepath.Join(outputRoot, "snapshot")
	if err := os.Mkdir(snapshotDir, 0o700); err != nil {
		return "", fmt.Errorf("create source snapshot: %w", err)
	}
	if err := materializer.git(ctx, outputRoot, "init", "-q", "--object-format="+string(materialized.Manifest.ObjectFormat), "--", snapshotDir); err != nil {
		return "", err
	}
	if err := materializer.git(
		ctx, snapshotDir, "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--",
		materialized.BundlePath, "refs/source/*:refs/source/*",
	); err != nil {
		return "", err
	}
	if err := materializer.verifySnapshotIdentity(ctx, snapshotDir, materialized.Manifest); err != nil {
		return "", err
	}
	if err := materializer.git(ctx, snapshotDir, "checkout", "-q", "--detach", materialized.Manifest.MaterializedCommitSHA); err != nil {
		return "", err
	}
	return snapshotDir, nil
}

func (materializer *productionSourceMaterializer) verifySnapshotIdentity(
	ctx context.Context,
	snapshotDir string,
	manifest localci.SourceMaterializationManifest,
) error {
	commit, err := materializer.gitLine(
		ctx, snapshotDir, "rev-parse", "--verify", "--end-of-options", "refs/source/materialized^{commit}",
	)
	if err != nil {
		return err
	}
	if commit != manifest.MaterializedCommitSHA {
		return errors.New("materialized source snapshot commit mismatch")
	}
	tree, err := materializer.gitLine(ctx, snapshotDir, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return err
	}
	if tree != manifest.SourceTreeSHA {
		return errors.New("materialized source snapshot tree mismatch")
	}
	return nil
}

func (materializer *productionSourceMaterializer) git(ctx context.Context, directory string, args ...string) error {
	output, err := materializer.gitCommand(ctx, directory, args...).CombinedOutput()
	if err != nil || len(output) != 0 {
		return errors.Join(
			fmt.Errorf("materialize source Git %s: %s", args[0], strings.TrimSpace(string(output))),
			err,
		)
	}
	return nil
}

func (materializer *productionSourceMaterializer) gitLine(
	ctx context.Context,
	directory string,
	args ...string,
) (string, error) {
	output, err := materializer.gitCommand(ctx, directory, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("materialize source Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSuffix(string(output), "\n")
	if line == "" || strings.TrimSpace(line) != line || strings.ContainsAny(line, "\r\n\x00") {
		return "", fmt.Errorf("materialize source Git %s returned non-canonical output", args[0])
	}
	return line, nil
}

func (materializer *productionSourceMaterializer) gitCommand(
	ctx context.Context,
	directory string,
	args ...string,
) *exec.Cmd {
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.CommandContext(ctx, materializer.gitPath, commandArgs...)
	command.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C", "PATH=" + os.Getenv("PATH"),
	}
	return command
}

type productionFreshContainerRunner struct {
	runner dockerFreshContainerService
}

// RunFreshContainer 校验 accepted provenance 后转交权威 Docker 一次性容器 runner。
func (runner *productionFreshContainerRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerResult{}, errors.New("production fresh container runner is not configured")
	}
	if request.ImageProvenanceSourceTreeSHA == "" ||
		request.ImageProvenanceSourceTreeSHA != request.ImageTruth.BuildSourceTreeSHA {
		return localci.FreshContainerResult{}, errors.New("accepted image provenance tree does not match runner image truth")
	}
	return runner.runner.RunFreshContainer(ctx, localci.FreshContainerRequest{
		Image: request.Image, ImageTruth: request.ImageTruth,
		SourceTreeSHA: request.JobSourceTreeSHA, SourceSnapshotDir: request.SourceSnapshotDir,
		Profile: request.Profile, Plan: request.Plan, GateID: request.GateID,
		PlanExecution:   request.PlanExecution,
		ShardGateIDs:    append([]gatecontract.GateID(nil), request.ShardGateIDs...),
		ShardIdentity:   request.ShardIdentity,
		ContainerLabels: request.ContainerLabels, Deadline: request.Deadline,
		ClaimDeadline: request.ClaimDeadline,
		LifecycleHook: request.LifecycleHook,
	})
}

// RecoverFreshContainer 将已证明身份的容器交给 Docker runner 接续观察。
func (runner *productionFreshContainerRunner) RecoverFreshContainer(
	ctx context.Context,
	request localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerResult, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerResult{}, errors.New("production fresh container runner is not configured")
	}
	return runner.runner.RecoverFreshContainer(ctx, request)
}

// ProbeFreshContainerRecovery 在 owner reconcile 阶段只读取原容器状态。
func (runner *productionFreshContainerRunner) ProbeFreshContainerRecovery(
	ctx context.Context,
	request localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerRecoveryObservation, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerRecoveryObservation{}, errors.New("production fresh container runner is not configured")
	}
	return runner.runner.ProbeFreshContainerRecovery(ctx, request)
}

// CleanupUnprovedFreshContainer 清理无法证明同一执行的旧容器。
func (runner *productionFreshContainerRunner) CleanupUnprovedFreshContainer(
	ctx context.Context,
	request localci.FreshContainerCleanupRequest,
) (localci.FreshContainerResult, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerResult{}, errors.New("production fresh container runner is not configured")
	}
	return runner.runner.CleanupUnprovedFreshContainer(ctx, request)
}
