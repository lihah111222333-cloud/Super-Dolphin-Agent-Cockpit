package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

const (
	// PortableRunContractID 是 portable remote CI 绑定协议的固定身份。
	PortableRunContractID = "portable-remote-ci-run/v1"
	// PortableExecutionProvider 是唯一允许写入绑定描述符的执行提供方。
	PortableExecutionProvider = "aliyun-eci/v1"
	// PortableRunModeValidateOnly 是只读绑定允许的唯一执行模式。
	PortableRunModeValidateOnly = "validate_only"
	// PortableBundleContractID 是 target source baseline 的唯一 bundle 合约。
	PortableBundleContractID = "git-bundle-thin/v2"
	// PortableWorkloadManifestSchemaVersion 是 workload manifest 的固定版本。
	PortableWorkloadManifestSchemaVersion = "portable-workload-manifest/v5"
)

// GitObjectResolver 解析已绑定仓库中的 commit/tree，并读取指定 tree 下的 regular blob。
// 实现不得从工作树、当前分支或 HEAD 推断对象，也不得在读取失败时回退。
type GitObjectResolver interface {
	ResolveCommitTree(ctx context.Context, repositoryRoot, commitSHA string) (treeSHA string, err error)
	ReadTreeBlob(ctx context.Context, repositoryRoot, treeSHA, repoRelativePath string) (GitTreeBlob, error)
}

// GitTreeBlob 是 exact tree 中已证明为 regular blob 的内容与 Git mode。
type GitTreeBlob struct {
	Mode string
	Data []byte
}

// FileIdentityReader 读取已绑定 authority 文件的稳定文件身份。
// 读取动作由调用方注入，绑定器本身不打开文件、不访问 SQLite。
type FileIdentityReader interface {
	ReadFileIdentity(ctx context.Context, path string) (string, error)
	ReadFileSHA256(ctx context.Context, path string) (string, error)
}

// PortableRunInput 是 portable-remote-ci-run/v1 的严格输入描述符。
type PortableRunInput struct {
	ExecutionProvider string                   `json:"execution_provider"`
	ContractID        string                   `json:"contract_id"`
	Mode              string                   `json:"mode"`
	Engine            PortableRunEngine        `json:"engine"`
	Target            PortableRunTarget        `json:"target"`
	WorkloadManifest  PortableWorkloadManifest `json:"workload_manifest"`
	Authority         PortableRunAuthority     `json:"authority"`
}

// PortableRunEngine 绑定执行引擎源码、运行时产物和工具链身份。
type PortableRunEngine struct {
	RepositoryRoot        string `json:"repository_root"`
	CommitSHA             string `json:"commit_sha"`
	TreeSHA               string `json:"tree_sha"`
	ProtocolABIDigest     string `json:"protocol_abi_digest"`
	RuntimeArtifactSHA256 string `json:"runtime_artifact_sha256"`
	ToolchainSHA256       string `json:"toolchain_sha256"`
}

// PortableRunTarget 绑定待验证目标源码及其 source baseline。
type PortableRunTarget struct {
	RepositoryRoot string                 `json:"repository_root"`
	CommitSHA      string                 `json:"commit_sha"`
	TreeSHA        string                 `json:"tree_sha"`
	BaseCommitSHA  string                 `json:"base_commit_sha"`
	RepositoryID   string                 `json:"repository_id"`
	ObjectFormat   string                 `json:"object_format"`
	SourceBaseline PortableSourceBaseline `json:"source_baseline"`
}

// PortableSourceBaseline 绑定目标使用的 immutable source baseline 对象。
type PortableSourceBaseline struct {
	ObjectFormat      string `json:"object_format"`
	BaselineTreeSHA   string `json:"baseline_tree_sha"`
	BaselineCommitSHA string `json:"baseline_commit_sha"`
	BundleContractID  string `json:"bundle_contract_id"`
	ArtifactKind      string `json:"artifact_kind"`
	ArtifactKey       string `json:"artifact_key"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	ArtifactSizeBytes int64  `json:"artifact_size_bytes"`
}

// PortableWorkloadManifest 绑定 target tree 内的 regular manifest blob。
type PortableWorkloadManifest struct {
	RepoRelativePath string `json:"repo_relative_path"`
	SHA256           string `json:"sha256"`
	ABIDigest        string `json:"abi_digest"`
	SchemaVersion    string `json:"schema_version"`
	RepositoryID     string `json:"repository_id"`
}

// PortableRunAuthority 绑定严格配置、ledger 路径和 ledger 文件身份。
type PortableRunAuthority struct {
	ConfigPath         string `json:"config_path"`
	ConfigSHA256       string `json:"config_sha256"`
	LedgerPath         string `json:"ledger_path"`
	LedgerFileIdentity string `json:"ledger_file_identity"`
}

// BoundPortableRun 是通过所有注入式验证后冻结的只读绑定描述符。
type BoundPortableRun struct {
	ExecutionProvider string                   `json:"execution_provider"`
	ContractID        string                   `json:"contract_id"`
	Mode              string                   `json:"mode"`
	Engine            PortableRunEngine        `json:"engine"`
	Target            PortableRunTarget        `json:"target"`
	WorkloadManifest  PortableWorkloadManifest `json:"workload_manifest"`
	Authority         PortableRunAuthority     `json:"authority"`
	BindingSHA256     string                   `json:"binding_sha256"`
}

// DecodePortableRunDescriptor 严格解码 portable-remote-ci-run/v1 JSON。
// 它拒绝 unknown、重复、尾随 JSON 和超过协议上限的输入，不读取任何外部状态。
func DecodePortableRunDescriptor(data []byte) (PortableRunInput, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return PortableRunInput{}, errors.New("portable run descriptor size is invalid")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return PortableRunInput{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var input PortableRunInput
	if err := decoder.Decode(&input); err != nil {
		return PortableRunInput{}, fmt.Errorf("decode portable run descriptor: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return PortableRunInput{}, errors.New("portable run descriptor has trailing JSON")
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return PortableRunInput{}, fmt.Errorf("marshal portable run descriptor: %w", err)
	}
	if !bytes.Equal(canonical, data) {
		return PortableRunInput{}, errors.New("portable run descriptor is not canonical")
	}
	return input, nil
}

// BindPortableRun 只校验并冻结 portable-remote-ci-run/v1，不执行任何 CI 副作用。
func BindPortableRun(ctx context.Context, input PortableRunInput, resolver GitObjectResolver, identityReader FileIdentityReader) (BoundPortableRun, error) {
	if err := validatePortableRunInput(ctx, input); err != nil {
		return BoundPortableRun{}, err
	}
	if interfaceValueIsNil(resolver) || interfaceValueIsNil(identityReader) {
		return BoundPortableRun{}, errors.New("portable run binding dependencies are required")
	}
	if err := validatePortableCommitTree(ctx, resolver, input.Engine.RepositoryRoot, input.Engine.CommitSHA, input.Engine.TreeSHA, "engine"); err != nil {
		return BoundPortableRun{}, err
	}
	if err := validatePortableCommitTree(ctx, resolver, input.Target.RepositoryRoot, input.Target.CommitSHA, input.Target.TreeSHA, "target"); err != nil {
		return BoundPortableRun{}, err
	}
	if err := validatePortableWorkloadManifest(ctx, resolver, input); err != nil {
		return BoundPortableRun{}, err
	}
	if err := validatePortableLedgerIdentity(ctx, identityReader, input.Authority); err != nil {
		return BoundPortableRun{}, err
	}
	bindingSHA, err := portableRunBindingSHA256(input)
	if err != nil {
		return BoundPortableRun{}, err
	}
	return BoundPortableRun{
		ExecutionProvider: input.ExecutionProvider,
		ContractID:        input.ContractID,
		Mode:              input.Mode,
		Engine:            input.Engine,
		Target:            input.Target,
		WorkloadManifest:  input.WorkloadManifest,
		Authority:         input.Authority,
		BindingSHA256:     bindingSHA,
	}, nil
}

// validatePortableRunInput 校验顶层协议身份及全部嵌套字段，不补默认值。
func validatePortableRunInput(ctx context.Context, input PortableRunInput) error {
	if ctx == nil {
		return errors.New("portable run binding context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("portable run binding context: %w", err)
	}
	if input.ExecutionProvider != PortableExecutionProvider || input.ContractID != PortableRunContractID || input.Mode != PortableRunModeValidateOnly {
		return errors.New("portable run protocol identity is invalid")
	}
	if err := validatePortableEngine(input.Engine); err != nil {
		return err
	}
	if err := validatePortableTarget(input.Target); err != nil {
		return err
	}
	if err := validatePortableWorkloadManifestShape(input.WorkloadManifest); err != nil {
		return err
	}
	if input.Target.RepositoryID != input.WorkloadManifest.RepositoryID {
		return errors.New("target and workload manifest repository identities do not match")
	}
	return validatePortableAuthority(input.Authority)
}

// validatePortableEngine 校验独立引擎源码、ABI、制品和工具链身份。
func validatePortableEngine(engine PortableRunEngine) error {
	if err := validatePortableAbsolutePath("engine.repository_root", engine.RepositoryRoot); err != nil {
		return err
	}
	if err := validatePortableOIDPair("engine", engine.CommitSHA, engine.TreeSHA); err != nil {
		return err
	}
	if !validPortableDigest(engine.ProtocolABIDigest) || !validPortableDigest(engine.RuntimeArtifactSHA256) || !validPortableDigest(engine.ToolchainSHA256) {
		return errors.New("engine protocol ABI, runtime, and toolchain SHA-256 values must be canonical")
	}
	return nil
}

// validatePortableTarget 校验目标仓库身份与内容寻址 baseline 描述符。
func validatePortableTarget(target PortableRunTarget) error {
	if err := validatePortableAbsolutePath("target.repository_root", target.RepositoryRoot); err != nil {
		return err
	}
	if err := validatePortableOIDPair("target", target.CommitSHA, target.TreeSHA); err != nil {
		return err
	}
	if err := validatePortableTargetObjectFormat(target); err != nil {
		return err
	}
	return validatePortableSourceBaseline(target)
}

// validatePortableTargetObjectFormat 校验目标 commit/tree/base 与声明的 Git object format 一致。
func validatePortableTargetObjectFormat(target PortableRunTarget) error {
	if !validPortableOID(target.BaseCommitSHA) || len(target.BaseCommitSHA) != len(target.CommitSHA) {
		return errors.New("target.base_commit_sha is not canonical or has a different object format")
	}
	if target.ObjectFormat != "sha1" && target.ObjectFormat != "sha256" || target.SourceBaseline.ObjectFormat != target.ObjectFormat {
		return errors.New("target.source_baseline.object_format is unsupported")
	}
	wantLength := 40
	if target.ObjectFormat == "sha256" {
		wantLength = 64
	}
	if len(target.CommitSHA) != wantLength || len(target.TreeSHA) != wantLength || len(target.BaseCommitSHA) != wantLength {
		return errors.New("target object identities do not match object_format")
	}
	return nil
}

// validatePortableSourceBaseline 校验 baseline Git 对象和内容寻址制品身份。
func validatePortableSourceBaseline(target PortableRunTarget) error {
	baseline := target.SourceBaseline
	wantLength := len(target.CommitSHA)
	if len(baseline.BaselineTreeSHA) != wantLength || len(baseline.BaselineCommitSHA) != wantLength ||
		!validPortableOID(baseline.BaselineTreeSHA) || !validPortableOID(baseline.BaselineCommitSHA) {
		return errors.New("target source baseline object identities are not canonical")
	}
	if !validPortableSourceArtifact(target.RepositoryID, baseline) {
		return errors.New("target source baseline identity is invalid")
	}
	return nil
}

// validPortableSourceArtifact 判断 baseline artifact 的协议和内容寻址字段是否完整。
func validPortableSourceArtifact(repositoryID string, baseline PortableSourceBaseline) bool {
	return baseline.BundleContractID == PortableBundleContractID &&
		validPortableToken(repositoryID) &&
		validPortableToken(baseline.ArtifactKind) &&
		validPortableToken(baseline.ArtifactKey) &&
		validPortableDigest(baseline.ArtifactSHA256) &&
		baseline.ArtifactSizeBytes > 0
}

// validatePortableWorkloadManifestShape 校验 descriptor 的路径、摘要、ABI 与仓库身份。
func validatePortableWorkloadManifestShape(manifest PortableWorkloadManifest) error {
	if !validPortableRepoRelativePath(manifest.RepoRelativePath) {
		return errors.New("workload_manifest.repo_relative_path must be a clean repository-relative path")
	}
	abiDigest, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		return fmt.Errorf("derive workload manifest ABI digest: %w", err)
	}
	if !validPortableDigest(manifest.SHA256) || manifest.ABIDigest != abiDigest || manifest.SchemaVersion != PortableWorkloadManifestSchemaVersion || !validPortableToken(manifest.RepositoryID) {
		return errors.New("workload manifest identity is invalid")
	}
	return nil
}

// validatePortableAuthority 校验唯一 config/ledger authority 的显式路径和身份。
func validatePortableAuthority(authority PortableRunAuthority) error {
	if err := validatePortableAbsolutePath("authority.config_path", authority.ConfigPath); err != nil {
		return err
	}
	if err := validatePortableAbsolutePath("authority.ledger_path", authority.LedgerPath); err != nil {
		return err
	}
	if authority.ConfigPath == authority.LedgerPath || !validPortableDigest(authority.ConfigSHA256) || !validPortableToken(authority.LedgerFileIdentity) {
		return errors.New("portable authority identity is invalid")
	}
	return nil
}

func validatePortableAbsolutePath(field, value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || containsControl(value) {
		return fmt.Errorf("%s must be a clean absolute path", field)
	}
	return nil
}

func validatePortableCommitTree(ctx context.Context, resolver GitObjectResolver, repositoryRoot, commitSHA, treeSHA, role string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resolve %s commit tree: %w", role, err)
	}
	resolvedTree, err := resolver.ResolveCommitTree(ctx, repositoryRoot, commitSHA)
	if err != nil {
		return fmt.Errorf("resolve %s commit tree: %w", role, err)
	}
	if !validPortableOID(resolvedTree) || resolvedTree != treeSHA {
		return fmt.Errorf("%s commit does not resolve to the bound tree", role)
	}
	return nil
}

// validatePortableWorkloadManifest 从 exact target tree 读取并校验 regular manifest blob。
func validatePortableWorkloadManifest(ctx context.Context, resolver GitObjectResolver, input PortableRunInput) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read workload manifest blob: %w", err)
	}
	blob, err := resolver.ReadTreeBlob(ctx, input.Target.RepositoryRoot, input.Target.TreeSHA, input.WorkloadManifest.RepoRelativePath)
	if err != nil {
		return fmt.Errorf("read workload manifest blob: %w", err)
	}
	if blob.Mode != "100644" && blob.Mode != "100755" {
		return errors.New("workload manifest is not a regular target-tree blob")
	}
	if len(blob.Data) == 0 || bytesDigest(blob.Data) != input.WorkloadManifest.SHA256 {
		return errors.New("workload manifest blob digest does not match the bound target tree")
	}
	manifest, err := DecodeRepositoryWorkloadManifest(blob.Data)
	if err != nil {
		return fmt.Errorf("decode workload manifest from target tree: %w", err)
	}
	if manifest.SchemaVersion != input.WorkloadManifest.SchemaVersion || manifest.RepositoryID != input.Target.RepositoryID {
		return errors.New("workload manifest identity does not match the portable run target")
	}
	return nil
}

// validatePortableLedgerIdentity 复核 ledger 文件身份与 config 内容摘要。
func validatePortableLedgerIdentity(ctx context.Context, reader FileIdentityReader, authority PortableRunAuthority) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read ledger file identity: %w", err)
	}
	identity, err := reader.ReadFileIdentity(ctx, authority.LedgerPath)
	if err != nil {
		return fmt.Errorf("read ledger file identity: %w", err)
	}
	if !validPortableToken(identity) || identity != authority.LedgerFileIdentity {
		return errors.New("ledger file identity does not match the bound authority")
	}
	configDigest, err := reader.ReadFileSHA256(ctx, authority.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config SHA-256: %w", err)
	}
	if !validPortableDigest(configDigest) || configDigest != authority.ConfigSHA256 {
		return errors.New("config SHA-256 does not match the bound authority")
	}
	return nil
}

func portableRunBindingSHA256(input PortableRunInput) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode portable run binding: %w", err)
	}
	return bytesDigest(encoded), nil
}

func validatePortableOIDPair(role, commitSHA, treeSHA string) error {
	if !validPortableOID(commitSHA) || !validPortableOID(treeSHA) || len(commitSHA) != len(treeSHA) {
		return fmt.Errorf("%s commit/tree identities are not canonical", role)
	}
	return nil
}

// validPortableOID 判断 Git object identity 是否为非零 canonical 小写十六进制。
func validPortableOID(value string) bool {
	if len(value) != 40 && len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || value != strings.ToLower(value) {
		return false
	}
	for _, byteValue := range decoded {
		if byteValue != 0 {
			return true
		}
	}
	return false
}

func validPortableDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, "sha256:")
	return found && len(encoded) == sha256.Size*2 && validPortableOID(encoded)
}

// validPortableToken 拒绝空白、控制字符和隐式默认语义。
func validPortableToken(value string) bool {
	trimmed := strings.TrimSpace(value)
	return value != "" && trimmed == value && !containsControl(value) && value != "default" && value != "cwd" && value != "HEAD"
}

// validPortableRepoRelativePath 判断路径是否严格位于目标仓库树内。
func validPortableRepoRelativePath(value string) bool {
	return validPortableToken(value) && !filepath.IsAbs(value) && !strings.ContainsRune(value, '\\') && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder); err != nil {
		return fmt.Errorf("portable run descriptor JSON is not strict: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("portable run descriptor has trailing JSON")
	}
	return nil
}

// walkJSONValue 递归读取一个 JSON 值并拒绝任意对象层级的重复 key。
func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		if err := walkJSONDelimitedValue(decoder, delimiter); err != nil {
			return err
		}
		_, err = decoder.Token()
		return err
	default:
		return nil
	}
}

// walkJSONDelimitedValue 读取对象或数组主体，结束 delimiter 由调用方消费。
func walkJSONDelimitedValue(decoder *json.Decoder, delimiter json.Delim) error {
	switch delimiter {
	case '{':
		return walkJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

// walkJSONObject 读取对象字段并拒绝重复 key。
func walkJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("object key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate object key %q", key)
		}
		seen[key] = struct{}{}
		if err := walkJSONValue(decoder); err != nil {
			return err
		}
	}
	return nil
}
