package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

// productionBootstrapRunnerVerifier 是宿主只读观察固定 OCI runner 的窄端口。
type productionBootstrapRunnerVerifier interface {
	VerifyRunner(context.Context, gatecontract.ImageIdentity) error
}

type productionBootstrapRuntime interface {
	productionBootstrapRunnerVerifier
	ExecuteController(context.Context, productionCoordinatorConfig, productionBootstrapRoot, productionBootstrapRequest, string) (productionBootstrapAttestation, error)
	VerifyAndRemoveContainer(context.Context, productionCoordinatorConfig, productionBootstrapRoot, productionBootstrapRequest, productionBootstrapAttestation) error
	CleanupStaleContainers(context.Context, productionBootstrapRoot, string) error
}

// productionDockerBootstrapRunnerVerifier 只执行固定 docker image inspect，不启动 gate 或容器。
type productionDockerBootstrapRunnerVerifier struct{}

// productionHostDockerCommand 为宿主 Docker CLI 构造最小且显式绑定 config 的环境。
func productionHostDockerCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if ctx == nil {
		return nil, errors.New("production host Docker context is required")
	}
	if len(args) == 0 {
		return nil, errors.New("production host Docker command is required")
	}
	environment, err := productionHostDockerCLIEnvironment()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = environment
	return command, nil
}

// productionHostDockerCLIEnvironment 只传递已验证的宿主 Docker CLI 必需环境。
func productionHostDockerCLIEnvironment() ([]string, error) {
	if err := validateProductionHostDockerOverrides(); err != nil {
		return nil, err
	}
	home, err := productionHostDockerHome()
	if err != nil {
		return nil, err
	}
	configDir, err := productionHostDockerConfig(home)
	if err != nil {
		return nil, err
	}
	path, err := productionHostDockerPath()
	if err != nil {
		return nil, err
	}
	return []string{"HOME=" + home, "PATH=" + path, "LC_ALL=C", "DOCKER_CONFIG=" + configDir}, nil
}

func validateProductionHostDockerOverrides() error {
	for _, name := range []string{
		"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH",
	} {
		if _, exists := os.LookupEnv(name); exists {
			return fmt.Errorf("%s override is not allowed for production host Docker CLI", name)
		}
	}
	return nil
}

// productionHostDockerHome 校验 HOME 为真实、规范且不经过符号链接的宿主目录。
func productionHostDockerHome() (string, error) {
	home, exists := os.LookupEnv("HOME")
	if !exists || home == "" || strings.TrimSpace(home) != home ||
		!filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", errors.New("production host Docker HOME must be canonical and absolute")
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil || resolvedHome != home {
		return "", errors.Join(errors.New("production host Docker HOME must be a real non-symlink directory"), err)
	}
	homeInfo, err := os.Lstat(home)
	if err != nil || !homeInfo.IsDir() {
		return "", errors.Join(errors.New("production host Docker HOME must be a directory"), err)
	}
	return home, nil
}

func productionHostDockerConfig(home string) (string, error) {
	configDir, configured := os.LookupEnv("DOCKER_CONFIG")
	if configured && configDir == "" {
		return "", errors.New("production host Docker DOCKER_CONFIG must not be empty when set")
	}
	if !configured {
		configDir = filepath.Join(home, ".docker")
	}
	canonicalConfig, err := canonicalProductionDirectory(configDir)
	if err != nil {
		return "", fmt.Errorf("validate production host Docker config: %w", err)
	}
	return canonicalConfig, nil
}

func productionHostDockerPath() (string, error) {
	path, exists := os.LookupEnv("PATH")
	if !exists || path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("production host Docker PATH is required and canonical")
	}
	return path, nil
}

// productionBootstrapImageInspect 固化 Docker inspect 中参与 runner identity 判定的字段。
type productionBootstrapImageInspect struct {
	ID           string   `json:"Id"`
	RepoDigests  []string `json:"RepoDigests"`
	OS           string   `json:"Os"`
	Architecture string   `json:"Architecture"`
	Variant      string   `json:"Variant"`
	Descriptor   *struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations"`
	} `json:"Descriptor"`
	RootFS *struct {
		Type   string   `json:"Type"`
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
}

// loadOrBootstrapProductionAcceptedImage only enters the signed controller path
// when accepted state is absent. The cross-process lock prevents duplicate builds.
func loadOrBootstrapProductionAcceptedImage(
	ctx context.Context,
	config productionCoordinatorConfig,
	promotion *productionPromotionAuthority,
	runtime productionBootstrapRuntime,
) (gatecontract.AcceptedImageRecord, error) {
	record, err := promotion.state.Load(ctx)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, localci.ErrAcceptedImageStateNotFound) {
		return gatecontract.AcceptedImageRecord{}, err
	}
	return bootstrapProductionAcceptedImage(ctx, config, promotion, runtime)
}

// verifyProductionBootstrapPrerequisites 验签 root 并对齐 config、bare repo baseline 与 OCI runner。
func verifyProductionBootstrapPrerequisites(
	ctx context.Context,
	config productionCoordinatorConfig,
	authority *productionGitAuthority,
	verifier productionBootstrapRunnerVerifier,
) (productionBootstrapRoot, string, error) {
	if ctx == nil || authority == nil || verifier == nil {
		return productionBootstrapRoot{}, "", errors.New("production bootstrap verifier dependencies are required")
	}
	root, err := loadProductionBootstrapRoot(config.BootstrapRootFile, config.AcceptedImageSigners)
	if err != nil {
		return productionBootstrapRoot{}, "", err
	}
	if err := validateProductionBootstrapConfigIdentity(root, config); err != nil {
		return productionBootstrapRoot{}, "", err
	}
	if err := verifyProductionBootstrapRepository(ctx, authority, root); err != nil {
		return productionBootstrapRoot{}, "", err
	}
	if err := verifyProductionBootstrapBuildClosure(ctx, config, root); err != nil {
		return productionBootstrapRoot{}, "", err
	}
	if err := verifier.VerifyRunner(ctx, root.Runner); err != nil {
		return productionBootstrapRoot{}, "", err
	}
	digest, err := productionBootstrapRootDigest(root, config.AcceptedImageSigners)
	return root, digest, err
}

// verifyProductionBootstrapBuildClosure 从外部 bare baseline 重算 candidate label 真值。
func verifyProductionBootstrapBuildClosure(
	ctx context.Context,
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
) error {
	source := gatecontract.SourceSpec{
		Kind: gatecontract.SourceKindCommit, ObjectFormat: root.ObjectFormat,
		Commit: &gatecontract.CommitSource{SHA: root.BaselineCommit}, SourceTreeSHA: root.BaselineTree,
	}
	tree, err := localci.LoadReadOnlyBootstrapTree(ctx, config.TrustedRepository, source)
	if err != nil {
		return fmt.Errorf("load production bootstrap baseline closure: %w", err)
	}
	inputs, err := localci.ResolveGateImageInputs(tree, root.PolicyDigest, config.Platform)
	if err != nil {
		return fmt.Errorf("resolve production bootstrap baseline closure: %w", err)
	}
	if inputs.ImageInputDigest != root.ImageInputDigest ||
		inputs.ToolchainDigest != root.ToolchainDigest ||
		inputs.ImageSchemaVersion != root.ImageSchemaVersion {
		return errors.New("production bootstrap signed build closure drifted from bare baseline")
	}
	return nil
}

// validateProductionBootstrapConfigIdentity 拒绝 root 与 production config 的 repo/platform 漂移。
func validateProductionBootstrapConfigIdentity(
	root productionBootstrapRoot,
	config productionCoordinatorConfig,
) error {
	if root.RepoID != config.RepoID {
		return errors.New("production bootstrap root repo_id drifted from production config")
	}
	if root.TrustedRef != config.TrustedRef {
		return errors.New("production bootstrap root trusted_ref drifted from production config")
	}
	if root.Controller.Signer != root.BootstrapSigner {
		return errors.New("production bootstrap controller attestation signer drifted from bootstrap signer")
	}
	if !productionBootstrapSignerIsTrusted(root.BootstrapSigner, root.BootstrapPublicKey, config.AcceptedImageSigners) {
		return errors.New("production bootstrap execution signer is absent from accepted image trust anchors")
	}
	if err := validateAcceptedPlatform(root.Runner, config.Platform); err != nil {
		return fmt.Errorf("production bootstrap runner platform: %w", err)
	}
	return nil
}

func productionBootstrapSignerIsTrusted(
	signer gatecontract.SignerIdentity,
	publicKey string,
	trusted []productionTrustedKey,
) bool {
	for _, candidate := range trusted {
		if candidate.Signer == signer && candidate.PublicKey == publicKey {
			return true
		}
	}
	return false
}

// verifyProductionBootstrapRepository 从外部 bare repository 读取 remote、commit type 和 tree。
func verifyProductionBootstrapRepository(
	ctx context.Context,
	authority *productionGitAuthority,
	root productionBootstrapRoot,
) error {
	remote, err := authority.line(ctx, "config", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("read production bootstrap remote: %w", err)
	}
	if remote != root.RemoteURL {
		return errors.New("production bootstrap remote URL drifted from external bare repository")
	}
	objectType, err := authority.line(ctx, "cat-file", "-t", root.BaselineCommit)
	if err != nil {
		return fmt.Errorf("resolve production bootstrap baseline commit: %w", err)
	}
	if objectType != "commit" {
		return errors.New("production bootstrap baseline object is not a commit")
	}
	tree, err := authority.line(ctx, "rev-parse", "--verify", "--end-of-options", root.BaselineCommit+"^{tree}")
	if err != nil {
		return fmt.Errorf("resolve production bootstrap baseline tree: %w", err)
	}
	if tree != root.BaselineTree {
		return errors.New("production bootstrap baseline tree drifted")
	}
	return nil
}

// VerifyRunner 读取固定 digest 的单个 Docker inspect 文档并逐字段比较。
func (productionDockerBootstrapRunnerVerifier) VerifyRunner(
	ctx context.Context,
	expected gatecontract.ImageIdentity,
) error {
	if ctx == nil {
		return errors.New("production bootstrap Docker context is required")
	}
	if err := expected.Validate(); err != nil {
		return fmt.Errorf("validate production bootstrap runner identity: %w", err)
	}
	reference := expected.Registry + "@" + expected.PlatformManifestDigest
	command, err := productionHostDockerCommand(ctx, "image", "inspect", reference)
	if err != nil {
		return fmt.Errorf("prepare production bootstrap runner Docker command: %w", err)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect production bootstrap runner: %w: %s", err, strings.TrimSpace(string(output)))
	}
	document, err := decodeProductionBootstrapInspect(output)
	if err != nil {
		return err
	}
	return validateProductionBootstrapInspect(document, expected, reference)
}

// decodeProductionBootstrapInspect 要求 Docker 只返回一个严格 JSON document。
func decodeProductionBootstrapInspect(data []byte) (productionBootstrapImageInspect, error) {
	var documents []json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&documents); err != nil {
		return productionBootstrapImageInspect{}, fmt.Errorf("decode production bootstrap runner inspect: %w", err)
	}
	if len(documents) != 1 {
		return productionBootstrapImageInspect{}, errors.New("production bootstrap runner inspect must contain one document")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return productionBootstrapImageInspect{}, errors.New("production bootstrap runner inspect contains trailing JSON")
	}
	var document productionBootstrapImageInspect
	if err := json.Unmarshal(documents[0], &document); err != nil {
		return productionBootstrapImageInspect{}, fmt.Errorf("decode production bootstrap runner document: %w", err)
	}
	return document, nil
}

// validateProductionBootstrapInspect 把完整 OCI identity 与 host Docker content store 对齐。
func validateProductionBootstrapInspect(
	document productionBootstrapImageInspect,
	expected gatecontract.ImageIdentity,
	reference string,
) error {
	if err := validateProductionBootstrapInspectDigests(document, expected, reference); err != nil {
		return err
	}
	if document.RootFS == nil || document.RootFS.Type != "layers" {
		return errors.New("production bootstrap runner rootfs is missing or not layers")
	}
	if !slices.Equal(document.RootFS.Layers, expected.RootFSDiffIDs) {
		return errors.New("production bootstrap runner rootfs diff IDs drifted")
	}
	if document.OS != expected.OS || document.Architecture != expected.Architecture || document.Variant != expected.Variant {
		return errors.New("production bootstrap runner platform drifted")
	}
	return nil
}

// validateProductionBootstrapInspectDigests 校验 manifest、config、index 和展示 ID。
func validateProductionBootstrapInspectDigests(
	document productionBootstrapImageInspect,
	expected gatecontract.ImageIdentity,
	reference string,
) error {
	if document.Descriptor == nil || document.Descriptor.Digest != expected.PlatformManifestDigest {
		return errors.New("production bootstrap runner platform manifest digest drifted")
	}
	if document.Descriptor.Annotations["config.digest"] != expected.ConfigDigest {
		return errors.New("production bootstrap runner config digest drifted")
	}
	if !slices.Contains(document.RepoDigests, reference) {
		return errors.New("production bootstrap runner manifest reference is absent from RepoDigests")
	}
	indexReference := expected.Registry + "@" + expected.OCIIndexDigest
	if expected.OCIIndexDigest != expected.PlatformManifestDigest && !slices.Contains(document.RepoDigests, indexReference) {
		return errors.New("production bootstrap runner index reference is absent from RepoDigests")
	}
	if document.ID != expected.ConfigDigest && document.ID != expected.PlatformManifestDigest {
		return errors.New("production bootstrap runner display ID matches neither config nor manifest")
	}
	return nil
}
