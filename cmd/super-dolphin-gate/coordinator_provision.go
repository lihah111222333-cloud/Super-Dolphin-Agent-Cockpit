package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const productionProvisionSchemaVersion uint32 = 1

type productionProvisionManifest struct {
	SchemaVersion              uint32                 `json:"schema_version"`
	InstallRoot                string                 `json:"install_root"`
	LauncherPath               string                 `json:"launcher_path"`
	ControllerBinary           string                 `json:"controller_binary"`
	BootstrapRootFile          string                 `json:"bootstrap_root_file"`
	BootstrapControllerKeyFile string                 `json:"bootstrap_controller_key_file"`
	ReceiptKeyFile             string                 `json:"receipt_key_file"`
	ActionGrantKeyFile         string                 `json:"action_grant_key_file"`
	SeccompProfile             string                 `json:"seccomp_profile"`
	TrustedSourceRoot          string                 `json:"trusted_source_root"`
	Platform                   string                 `json:"platform"`
	TrustedRootKeys            []productionTrustedKey `json:"trusted_root_keys"`
	CandidateTTLSeconds        int64                  `json:"candidate_ttl_seconds"`
	PromotionPollMillis        int64                  `json:"promotion_poll_millis"`
	ActionGrantTTLSeconds      int64                  `json:"action_grant_ttl_seconds"`
}

// Validate 校验 manifest 只包含显式路径、外部 trust anchors 与生产时限。
func (manifest productionProvisionManifest) Validate() error {
	if manifest.SchemaVersion != productionProvisionSchemaVersion {
		return errors.New("production provision manifest schema version is invalid")
	}
	if len(manifest.TrustedRootKeys) == 0 {
		return errors.New("production provision trusted root keys are required")
	}
	if err := validateProductionProvisionTiming(manifest); err != nil {
		return err
	}
	for _, value := range []string{
		manifest.InstallRoot, manifest.LauncherPath, manifest.ControllerBinary, manifest.BootstrapRootFile,
		manifest.BootstrapControllerKeyFile, manifest.ReceiptKeyFile, manifest.ActionGrantKeyFile,
		manifest.SeccompProfile, manifest.TrustedSourceRoot, manifest.Platform,
	} {
		if err := validateProductionProvisionManifestValue(value); err != nil {
			return err
		}
	}
	return nil
}

// validateProductionProvisionTiming 固定 watcher、candidate 与 grant 的生产上限。
func validateProductionProvisionTiming(manifest productionProvisionManifest) error {
	if manifest.CandidateTTLSeconds <= 0 || manifest.CandidateTTLSeconds > 604_800 {
		return errors.New("production provision candidate_ttl_seconds must be within 1..604800")
	}
	if manifest.PromotionPollMillis < coordinatorPromotionPollMinMillis ||
		manifest.PromotionPollMillis > coordinatorPromotionPollMaxMillis {
		return errors.New("production provision promotion_poll_millis must be within 5000..60000")
	}
	if manifest.ActionGrantTTLSeconds <= 0 || manifest.ActionGrantTTLSeconds > 900 {
		return errors.New("production provision action_grant_ttl_seconds must be within 1..900")
	}
	return nil
}

func validateProductionProvisionManifestValue(value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("production provision manifest contains an empty or non-canonical value")
	}
	return nil
}

type productionProvisionSigningKey struct {
	Signer     gatecontract.SignerIdentity `json:"signer"`
	PublicKey  string                      `json:"public_key"`
	PrivateKey string                      `json:"private_key"`
}

// Validate 校验 authority bundle 的 Ed25519 公私钥与 signer 身份。
func (key productionProvisionSigningKey) Validate() error {
	if err := key.Signer.Validate(); err != nil {
		return err
	}
	publicKey, err := decodeProductionBootstrapPublicKey(key.PublicKey)
	if err != nil {
		return err
	}
	privateKey, err := decodeProductionProvisionPrivateKey(key.PrivateKey)
	if err != nil {
		return err
	}
	if !publicKey.Equal(privateKey.Public()) {
		return errors.New("production provision authority public and private keys do not match")
	}
	return nil
}

type productionProvisionResult struct {
	SchemaVersion        uint32 `json:"schema_version"`
	ProductionConfigFile string `json:"production_config_file"`
	LauncherPath         string `json:"launcher_path"`
}

// runProductionProvisionCLI 安装 production manifest 指定的完整外部信任边界。
func runProductionProvisionCLI(args []string, stdout io.Writer) error {
	if len(args) != 3 || args[0] != "production" || args[1] != "--manifest" {
		return protocolError("provision production requires --manifest <path>")
	}
	manifest, err := loadProductionProvisionManifest(args[2])
	if err != nil {
		return err
	}
	result, err := provisionProduction(context.Background(), manifest)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}

// loadProductionProvisionManifest 从仓库外 0600 文件严格解码安装输入。
func loadProductionProvisionManifest(path string) (productionProvisionManifest, error) {
	canonical, err := canonicalProductionFile("production provision manifest", path)
	if err != nil {
		return productionProvisionManifest{}, err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return productionProvisionManifest{}, err
	}
	var manifest productionProvisionManifest
	if err := gatecontract.DecodeStrictJSON(data, &manifest); err != nil {
		return manifest, fmt.Errorf("decode production provision manifest: %w", err)
	}
	return manifest, nil
}

// provisionProduction 先完成 staging 验证，再发布 install root 与 launcher。
func provisionProduction(
	ctx context.Context,
	manifest productionProvisionManifest,
) (productionProvisionResult, error) {
	return provisionProductionWithRuntime(ctx, manifest, productionProvisionLiveRuntime{})
}

type productionProvisionRuntime interface {
	VerifyRunner(context.Context, gatecontract.ImageIdentity) error
	CloneTrustedRepository(context.Context, productionBootstrapRoot, string) error
	VerifyTrustedRepository(context.Context, productionBootstrapRoot, string) error
}

type productionProvisionLiveRuntime struct{}

// VerifyRunner 通过 Docker content store 固定 OCI runner 完整身份。
func (productionProvisionLiveRuntime) VerifyRunner(ctx context.Context, identity gatecontract.ImageIdentity) error {
	return (productionDockerBootstrapRunnerVerifier{}).VerifyRunner(ctx, identity)
}

// CloneTrustedRepository 从 signed remote 建立并验证本机 bare authority。
func (productionProvisionLiveRuntime) CloneTrustedRepository(
	ctx context.Context,
	root productionBootstrapRoot,
	destination string,
) error {
	return cloneProductionProvisionTrustedRepository(ctx, root, destination)
}

// VerifyTrustedRepository 复核已发布恢复态仍固定到 signed bare baseline。
func (productionProvisionLiveRuntime) VerifyTrustedRepository(
	ctx context.Context,
	root productionBootstrapRoot,
	destination string,
) error {
	return verifyProductionProvisionTrustedRepository(ctx, root, destination)
}

// provisionProductionWithRuntime 保持 production 与测试共用同一原子发布状态机。
func provisionProductionWithRuntime(
	ctx context.Context,
	manifest productionProvisionManifest,
	runtime productionProvisionRuntime,
) (productionProvisionResult, error) {
	plan, err := preflightProductionProvision(ctx, manifest, runtime)
	if err != nil {
		return productionProvisionResult{}, err
	}
	if plan.rootExists {
		return completeProductionProvision(plan, manifest.LauncherPath)
	}
	staging, err := os.MkdirTemp(plan.parent, ".super-dolphin-gate-provision-")
	if err != nil {
		return productionProvisionResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staging)
		}
	}()
	config, err := stageProductionProvision(ctx, staging, plan.installRoot, manifest, plan.inputs, runtime)
	if err != nil {
		return productionProvisionResult{}, err
	}
	if err := publishProductionProvisionRoot(staging, plan.installRoot); err != nil {
		return productionProvisionResult{}, fmt.Errorf("publish production provision root: %w", err)
	}
	cleanup = false
	configPath := filepath.Join(plan.installRoot, "production.json")
	if _, err := loadProductionCoordinatorConfigFile(configPath); err != nil {
		return productionProvisionResult{}, fmt.Errorf("verify installed production config: %w", err)
	}
	if err := installProductionProvisionLauncher(manifest.LauncherPath, configPath, config.BootstrapControllerFile); err != nil {
		return productionProvisionResult{}, err
	}
	return productionProvisionResult{SchemaVersion: 1, ProductionConfigFile: configPath, LauncherPath: manifest.LauncherPath}, nil
}

func completeProductionProvision(plan productionProvisionPlan, launcherPath string) (productionProvisionResult, error) {
	if !plan.launcherReady {
		if err := installProductionProvisionLauncher(launcherPath, plan.configPath, plan.config.BootstrapControllerFile); err != nil {
			return productionProvisionResult{}, err
		}
	}
	return productionProvisionResult{SchemaVersion: 1, ProductionConfigFile: plan.configPath, LauncherPath: launcherPath}, nil
}

// preflightProductionProvision 在任何 staging 或发布副作用前固定全部输入边界。
func preflightProductionProvision(
	ctx context.Context,
	manifest productionProvisionManifest,
	runtime productionProvisionRuntime,
) (productionProvisionPlan, error) {
	if runtime == nil {
		return productionProvisionPlan{}, errors.New("production provision runtime is required")
	}
	if err := manifest.Validate(); err != nil {
		return productionProvisionPlan{}, err
	}
	if _, err := canonicalProductionLauncherDirectory(filepath.Dir(manifest.LauncherPath)); err != nil {
		return productionProvisionPlan{}, err
	}
	if _, err := canonicalProductionDirectory(manifest.TrustedSourceRoot); err != nil {
		return productionProvisionPlan{}, err
	}
	inputs, err := verifyProductionProvisionInputs(ctx, manifest, runtime)
	if err != nil {
		return productionProvisionPlan{}, err
	}
	installRoot, parent, rootExists, err := productionProvisionDestination(manifest.InstallRoot)
	if err != nil {
		return productionProvisionPlan{}, err
	}
	return planProductionProvisionRoot(ctx, manifest, inputs, installRoot, parent, rootExists, runtime)
}

func planProductionProvisionRoot(
	ctx context.Context,
	manifest productionProvisionManifest,
	inputs productionProvisionInputs,
	installRoot string,
	parent string,
	rootExists bool,
	runtime productionProvisionRuntime,
) (productionProvisionPlan, error) {
	config := productionProvisionConfig(installRoot, manifest, inputs)
	configPath := filepath.Join(installRoot, "production.json")
	launcherReady, err := inspectProductionProvisionLauncher(manifest.LauncherPath, configPath, config.BootstrapControllerFile)
	if err != nil {
		return productionProvisionPlan{}, err
	}
	if !rootExists {
		if launcherReady {
			return productionProvisionPlan{}, errors.New("production provision launcher exists without its verified install root")
		}
		return productionProvisionPlan{inputs: inputs, installRoot: installRoot, parent: parent, config: config, configPath: configPath}, nil
	}
	if err := verifyProductionProvisionResidue(ctx, installRoot, manifest, inputs, runtime); err != nil {
		return productionProvisionPlan{}, err
	}
	return productionProvisionPlan{
		inputs: inputs, installRoot: installRoot, parent: parent, config: config, configPath: configPath,
		rootExists: true, launcherReady: launcherReady,
	}, nil
}

type productionProvisionInputs struct {
	root           productionBootstrapRoot
	bootstrapKey   productionBootstrapControllerPrivateKey
	receiptKey     productionProvisionSigningKey
	actionGrantKey productionProvisionSigningKey
	controllerData []byte
	seccompData    []byte
}

// verifyProductionProvisionInputs 验证发布 root、controller、keys、runner 与 baseline remote。
func verifyProductionProvisionInputs(
	ctx context.Context,
	manifest productionProvisionManifest,
	runtime productionProvisionRuntime,
) (productionProvisionInputs, error) {
	root, err := loadProductionBootstrapRoot(manifest.BootstrapRootFile, manifest.TrustedRootKeys)
	if err != nil {
		return productionProvisionInputs{}, err
	}
	if err := validateProductionProvisionPlatform(manifest.Platform, root.Runner); err != nil {
		return productionProvisionInputs{}, err
	}
	controllerData, seccompData, err := loadProductionProvisionArtifacts(manifest, root)
	if err != nil {
		return productionProvisionInputs{}, err
	}
	bootstrapKey, err := loadProductionProvisionBootstrapKey(manifest.BootstrapControllerKeyFile, root)
	if err != nil {
		return productionProvisionInputs{}, err
	}
	receiptKey, err := loadProductionProvisionSigningKey("receipt", manifest.ReceiptKeyFile)
	if err != nil {
		return productionProvisionInputs{}, err
	}
	actionGrantKey, err := loadProductionProvisionSigningKey("action grant", manifest.ActionGrantKeyFile)
	if err != nil {
		return productionProvisionInputs{}, err
	}
	if err := validateProductionAuthoritySeparation([]productionAuthorityIdentity{
		{name: "promotion/bootstrap", signer: root.BootstrapSigner, publicKey: root.BootstrapPublicKey},
		{name: "result receipt", signer: receiptKey.Signer, publicKey: receiptKey.PublicKey},
		{name: "action grant", signer: actionGrantKey.Signer, publicKey: actionGrantKey.PublicKey},
	}); err != nil {
		return productionProvisionInputs{}, err
	}
	if err := runtime.VerifyRunner(ctx, root.Runner); err != nil {
		return productionProvisionInputs{}, err
	}
	return productionProvisionInputs{
		root: root, bootstrapKey: bootstrapKey, receiptKey: receiptKey, actionGrantKey: actionGrantKey,
		controllerData: controllerData, seccompData: seccompData,
	}, nil
}

// loadProductionProvisionArtifacts 固定 controller 与 seccomp 的已验证字节快照。
func loadProductionProvisionArtifacts(
	manifest productionProvisionManifest,
	root productionBootstrapRoot,
) ([]byte, []byte, error) {
	controllerData, err := verifyProductionProvisionController(manifest.ControllerBinary, root.Controller)
	if err != nil {
		return nil, nil, err
	}
	seccompData, err := readProductionProvisionPrivateFile("seccomp profile", manifest.SeccompProfile)
	if err != nil {
		return nil, nil, err
	}
	return controllerData, seccompData, nil
}

func validateProductionProvisionPlatform(platform string, runner gatecontract.ImageIdentity) error {
	if runner.OS+"/"+runner.Architecture != platform {
		return errors.New("production provision platform drifted from bootstrap runner")
	}
	return nil
}

// verifyProductionProvisionController 固定外部 release executable 的 digest 与 codesign requirement。
func verifyProductionProvisionController(path string, identity productionBootstrapControllerIdentity) ([]byte, error) {
	canonical, err := canonicalProductionExecutable("production provision controller", path)
	if err != nil {
		return nil, err
	}
	data, err := readProductionBootstrapController(canonical)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(digest[:]) != identity.BinaryDigest {
		return nil, errors.New("production provision controller digest drifted from signed root")
	}
	if err := verifyProductionBootstrapCodeRequirement(canonical, identity.DesignatedRequirement); err != nil {
		return nil, err
	}
	return data, nil
}

// readProductionProvisionPrivateFile 固定 pathname/fd 后读取小型私有 artifact。
func readProductionProvisionPrivateFile(name string, path string) ([]byte, error) {
	canonical, err := canonicalProductionFile("production provision "+name, path)
	if err != nil {
		return nil, err
	}
	return readProductionCoordinatorConfig(canonical)
}

// loadProductionProvisionBootstrapKey 验证本机 key 对应 signed root 的 bootstrap public key。
func loadProductionProvisionBootstrapKey(
	path string,
	root productionBootstrapRoot,
) (productionBootstrapControllerPrivateKey, error) {
	var key productionBootstrapControllerPrivateKey
	if err := decodeProductionProvisionFile("bootstrap controller key", path, &key); err != nil {
		return key, err
	}
	privateKeyData, _ := base64.StdEncoding.DecodeString(key.PrivateKey)
	privateKey := ed25519.PrivateKey(privateKeyData)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	if key.Signer != root.BootstrapSigner || base64.StdEncoding.EncodeToString(publicKey) != root.BootstrapPublicKey {
		return key, errors.New("production provision bootstrap key drifted from signed root")
	}
	return key, nil
}

// loadProductionProvisionSigningKey 读取 receipt 或 action grant 外部 authority bundle。
func loadProductionProvisionSigningKey(name string, path string) (productionProvisionSigningKey, error) {
	var key productionProvisionSigningKey
	if err := decodeProductionProvisionFile(name+" key", path, &key); err != nil {
		return key, err
	}
	return key, nil
}

// decodeProductionProvisionFile 严格读取单个仓库外 0600 JSON artifact。
func decodeProductionProvisionFile(name string, path string, target gatecontract.Validatable) error {
	canonical, err := canonicalProductionFile("production provision "+name, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return err
	}
	return gatecontract.DecodeStrictJSON(data, target)
}

// decodeProductionProvisionPrivateKey 解码固定长度 Ed25519 private key。
func decodeProductionProvisionPrivateKey(encoded string) (ed25519.PrivateKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("production provision private key must be canonical base64 Ed25519")
	}
	return ed25519.PrivateKey(append([]byte(nil), decoded...)), nil
}

// productionProvisionDestination 要求尚不存在且父目录为私有 canonical directory。
func productionProvisionDestination(path string) (string, string, bool, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", "", false, errors.New("production provision install_root must be a canonical absolute path")
	}
	parent, err := canonicalProductionDirectory(filepath.Dir(path))
	if err != nil {
		return "", "", false, err
	}
	if _, err := os.Lstat(path); err == nil {
		return path, parent, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", false, errors.New("production provision install_root cannot be inspected")
	}
	return path, parent, false, nil
}

// stageProductionProvision 写入完整私有安装树但不创建 accepted-image.json。
func stageProductionProvision(
	ctx context.Context,
	staging string,
	installRoot string,
	manifest productionProvisionManifest,
	inputs productionProvisionInputs,
	runtime productionProvisionRuntime,
) (productionCoordinatorConfig, error) {
	if err := os.Chmod(staging, 0o700); err != nil {
		return productionCoordinatorConfig{}, err
	}
	for _, name := range []string{"accepted", "candidate-state", "candidate-build"} {
		if err := os.Mkdir(filepath.Join(staging, name), 0o700); err != nil {
			return productionCoordinatorConfig{}, err
		}
	}
	if err := runtime.CloneTrustedRepository(ctx, inputs.root, filepath.Join(staging, "trusted.git")); err != nil {
		return productionCoordinatorConfig{}, err
	}
	config := productionProvisionConfig(installRoot, manifest, inputs)
	expectedFiles, err := productionProvisionExpectedFiles(config, inputs)
	if err != nil {
		return productionCoordinatorConfig{}, err
	}
	if err := writeProductionProvisionExpectedFiles(staging, expectedFiles); err != nil {
		return productionCoordinatorConfig{}, err
	}
	if err := verifyProductionBootstrapCodeRequirement(filepath.Join(staging, "bootstrap-controller"), inputs.root.Controller.DesignatedRequirement); err != nil {
		return productionCoordinatorConfig{}, err
	}
	validationConfig := productionProvisionConfig(staging, manifest, inputs)
	if err := validationConfig.Validate(); err != nil {
		return productionCoordinatorConfig{}, fmt.Errorf("validate staged production config: %w", err)
	}
	return config, nil
}

// productionProvisionConfig 生成只引用最终 install root 的 production config。
func productionProvisionConfig(
	root string,
	manifest productionProvisionManifest,
	inputs productionProvisionInputs,
) productionCoordinatorConfig {
	bootstrapTrust := productionTrustedKey{Signer: inputs.root.BootstrapSigner, PublicKey: inputs.root.BootstrapPublicKey}
	return productionCoordinatorConfig{
		AcceptedImageRoot: filepath.Join(root, "accepted"), BootstrapRootFile: filepath.Join(root, "bootstrap-root.json"),
		BootstrapControllerFile:    filepath.Join(root, "bootstrap-controller"),
		BootstrapControllerKeyFile: filepath.Join(root, "bootstrap-controller-key.json"),
		CandidateStateRoot:         filepath.Join(root, "candidate-state"), CandidateBuildRoot: filepath.Join(root, "candidate-build"),
		TrustedSourceRoot: manifest.TrustedSourceRoot, TrustedRepository: filepath.Join(root, "trusted.git"),
		SeccompProfile: filepath.Join(root, "seccomp.json"), Platform: manifest.Platform,
		RepoID: inputs.root.RepoID, TrustedRef: inputs.root.TrustedRef,
		AcceptedImageSigners: append(append([]productionTrustedKey(nil), manifest.TrustedRootKeys...), bootstrapTrust),
		PromotionSigner:      productionPromotionKey{Signer: inputs.root.BootstrapSigner, PrivateKeyFile: filepath.Join(root, "promotion-private.key")},
		ResultReceiptAuthority: productionResultReceiptAuthorityConfig{
			Signer: inputs.receiptKey.Signer, PublicKey: inputs.receiptKey.PublicKey,
			PrivateKeyFile: filepath.Join(root, "receipt-private.json"),
		},
		ActionGrantAuthority: productionActionGrantAuthorityConfig{
			Signer: inputs.actionGrantKey.Signer, PublicKey: inputs.actionGrantKey.PublicKey,
			PrivateKeyFile: filepath.Join(root, "action-grant-private.json"), TTLSeconds: manifest.ActionGrantTTLSeconds,
		},
		CandidateTTLSeconds: manifest.CandidateTTLSeconds, PromotionPollMillis: manifest.PromotionPollMillis,
	}
}

// cloneProductionProvisionTrustedRepository 建立 bare mirror 并固定 ref、commit 与 tree。
func cloneProductionProvisionTrustedRepository(
	ctx context.Context,
	root productionBootstrapRoot,
	destination string,
) error {
	command := exec.CommandContext(ctx, "git", "clone", "--bare", "--no-tags", "--", root.RemoteURL, destination)
	command.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("clone production trusted repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return err
	}
	return verifyProductionProvisionTrustedRepository(ctx, root, destination)
}

// productionProvisionGitLine 执行无 prompt 的 bare repository 只读查询。
func productionProvisionGitLine(ctx context.Context, repository string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", repository}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=" + os.Getenv("PATH"), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// installProductionProvisionLauncher 原子安装显式注入 production config 的 launcher。
func installProductionProvisionLauncher(path string, configPath string, controllerPath string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("production provision launcher_path must be a canonical absolute path")
	}
	parent, err := canonicalProductionLauncherDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	data := productionProvisionLauncherData(configPath, controllerPath)
	temp, err := os.CreateTemp(parent, ".super-dolphin-gate-launcher-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o700); err != nil {
		return errors.Join(err, temp.Close())
	}
	if _, err := temp.Write(data); err != nil {
		return errors.Join(err, temp.Close())
	}
	if err := errors.Join(temp.Sync(), temp.Close()); err != nil {
		return err
	}
	return linkProductionProvisionLauncher(tempPath, path, configPath, controllerPath)
}

func linkProductionProvisionLauncher(tempPath string, path string, configPath string, controllerPath string) error {
	if err := os.Link(tempPath, path); err != nil {
		ready, inspectErr := inspectProductionProvisionLauncher(path, configPath, controllerPath)
		if inspectErr == nil && ready {
			return nil
		}
		return errors.Join(fmt.Errorf("install production provision launcher without replacement: %w", err), inspectErr)
	}
	return nil
}

// canonicalProductionLauncherDirectory 允许标准用户 bin 权限，但拒绝非 owner 写入和路径替换。
func canonicalProductionLauncherDirectory(path string) (string, error) {
	if err := validateProductionProvisionLauncherPath(path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", errors.Join(errors.New("production provision launcher directory must be owner-controlled"), err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("production provision launcher directory must be owner-controlled")
	}
	if !productionProvisionOwnedByCurrentUser(info) {
		return "", errors.New("production provision launcher directory must be owned by the current user")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.Join(errors.New("production provision launcher directory must not traverse symlinks"), err)
	}
	if resolved != path {
		return "", errors.New("production provision launcher directory must not traverse symlinks")
	}
	if err := rejectProductionWorktreePath(path); err != nil {
		return "", err
	}
	return path, nil
}

func validateProductionProvisionLauncherPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("production provision launcher directory must be canonical and absolute")
	}
	return nil
}

func productionProvisionOwnedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

// shellQuoteProductionProvision 对 launcher 固定路径做 POSIX single-quote 编码。
func shellQuoteProductionProvision(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
