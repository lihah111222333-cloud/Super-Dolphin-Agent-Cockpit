package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	// PortableExecutionContractID 是 normal portable execute 请求的 additive 契约身份。
	PortableExecutionContractID = "remote-ci-portable-execution/v1"
	// PortableExecutionRequestSchema 是 normal portable execute 请求的唯一 wire schema。
	PortableExecutionRequestSchema = "portable-remote-ci-execution/v1"
	// PortableExecutionProtocolABIDomain 将 execute DTO ABI 与 builder、manifest 协议隔离。
	PortableExecutionProtocolABIDomain = "portable-remote-ci-execution-protocol-abi/v1"
	// PortableExecutionRuntimeArtifactContractID 是 normal runtime artifact 的固定契约身份。
	PortableExecutionRuntimeArtifactContractID = "remote-ci-portable-runtime-artifact/v1"
	// PortableExecutionRuntimeArtifactKind 将可执行 runtime 与 source-baseline bundle 隔离。
	PortableExecutionRuntimeArtifactKind = "portable-remote-ci-runtime/v1"
	// PortableExecutionRuntimeArtifactIdentityDomain 隔离 normal runtime artifact identity。
	PortableExecutionRuntimeArtifactIdentityDomain = "portable-remote-ci-runtime-artifact-identity/v1"
	portableExecutionRequestMaxBytes               = 1 << 20
)

// PortableExecutionRequest 冻结尚未接线的 normal portable execute 显式输入。
// 该 DTO 只表达协议，不打开 SQLite、不调用 Coordinator、OSS 或 ECI。
type PortableExecutionRequest struct {
	SchemaVersion     string                     `json:"schema_version"`
	ContractID        string                     `json:"contract_id"`
	ExecutionProvider string                     `json:"execution_provider"`
	Engine            PortableExecutionEngine    `json:"engine"`
	Target            PortableExecutionTarget    `json:"target"`
	Authority         PortableExecutionAuthority `json:"authority"`
	Audit             PortableExecutionAudit     `json:"audit"`
}

// PortableExecutionEngine 绑定 exact engine Git identity 与 builder 产物。
type PortableExecutionEngine struct {
	RepositoryID string                    `json:"repository_id"`
	ObjectFormat string                    `json:"object_format"`
	CommitSHA    string                    `json:"commit_sha"`
	TreeSHA      string                    `json:"tree_sha"`
	Artifact     PortableExecutionArtifact `json:"artifact"`
}

// PortableExecutionArtifact 绑定 immutable artifact、receipt 与 observed builder/toolchain identity。
type PortableExecutionArtifact struct {
	ContractID                string `json:"contract_id"`
	Kind                      string `json:"kind"`
	ArtifactKey               string `json:"artifact_key"`
	ArtifactSHA256            string `json:"artifact_sha256"`
	ArtifactSizeBytes         int64  `json:"artifact_size_bytes"`
	ReceiptKey                string `json:"receipt_key"`
	ReceiptSHA256             string `json:"receipt_sha256"`
	IdentityDigest            string `json:"identity_digest"`
	BuilderDigest             string `json:"builder_digest"`
	ToolchainDigest           string `json:"toolchain_digest"`
	ProtocolABIDigest         string `json:"protocol_abi_digest"`
	WorkloadManifestABIDigest string `json:"workload_manifest_abi_digest"`
}

// PortableExecutionTarget 绑定 exact target Git identity 与 target-tree manifest。
type PortableExecutionTarget struct {
	RepositoryID     string                   `json:"repository_id"`
	ObjectFormat     string                   `json:"object_format"`
	CommitSHA        string                   `json:"commit_sha"`
	TreeSHA          string                   `json:"tree_sha"`
	MountPath        string                   `json:"mount_path"`
	WorkloadManifest PortableWorkloadManifest `json:"workload_manifest"`
}

// PortableExecutionAuthority 绑定配置、ledger 文件身份与 accepted authority 摘要。
type PortableExecutionAuthority struct {
	ConfigPath              string `json:"config_path"`
	ConfigSHA256            string `json:"config_sha256"`
	LedgerPath              string `json:"ledger_path"`
	LedgerIdentity          string `json:"ledger_identity"`
	AcceptedGeneration      uint64 `json:"accepted_generation"`
	AcceptedAuthoritySHA256 string `json:"accepted_authority_sha256"`
}

// PortableExecutionAudit 绑定本次调用审计身份，不进入 workload PASS identity。
type PortableExecutionAudit struct {
	AgentTokenDigest string              `json:"agent_token_digest"`
	Profile          gate.Profile        `json:"profile"`
	Entrypoint       gate.CIEntrypointID `json:"entrypoint"`
	Force            bool                `json:"force"`
}

// Validate 在任何 normal runtime 接线前校验全部显式身份与交叉绑定。
func (request PortableExecutionRequest) Validate() error {
	if request.SchemaVersion != PortableExecutionRequestSchema ||
		request.ContractID != PortableExecutionContractID ||
		request.ExecutionProvider != cicontract.ExecutionProviderID {
		return errors.New("portable execution request header is invalid")
	}
	for _, validate := range []func() error{
		func() error { return validatePortableExecutionEngine(request.Engine) },
		func() error { return validatePortableExecutionTarget(request.Target) },
		func() error { return validatePortableExecutionAuthority(request.Authority) },
		func() error { return validatePortableExecutionAudit(request.Audit) },
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	if request.Engine.Artifact.WorkloadManifestABIDigest != request.Target.WorkloadManifest.ABIDigest {
		return errors.New("portable execution engine artifact and target manifest ABI identities differ")
	}
	return nil
}

// CanonicalEncode 校验并生成 bounded canonical execute request JSON。
func (request PortableExecutionRequest) CanonicalEncode() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("encode portable execution request")
	}
	if len(data) == 0 || len(data) > portableExecutionRequestMaxBytes {
		return nil, errors.New("portable execution request exceeds bounded size")
	}
	return data, nil
}

// DecodePortableExecutionRequest 严格拒绝 duplicate、unknown、null、trailing 与非 canonical JSON。
func DecodePortableExecutionRequest(data []byte) (PortableExecutionRequest, error) {
	if len(data) == 0 || len(data) > portableExecutionRequestMaxBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return PortableExecutionRequest{}, errors.New("portable execution request JSON size or root is invalid")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return PortableExecutionRequest{}, errors.New("portable execution request JSON has duplicate fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request PortableExecutionRequest
	if err := decoder.Decode(&request); err != nil {
		return PortableExecutionRequest{}, errors.New("decode portable execution request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return PortableExecutionRequest{}, errors.New("portable execution request JSON has trailing data")
	}
	canonical, err := request.CanonicalEncode()
	if err != nil || !bytes.Equal(canonical, data) {
		return PortableExecutionRequest{}, errors.New("portable execution request JSON is not canonical")
	}
	return request, nil
}

// validatePortableExecutionEngine 校验 exact engine Git identity 与 artifact 全链绑定。
func validatePortableExecutionEngine(engine PortableExecutionEngine) error {
	if !validBuilderSimulationToken(engine.RepositoryID, 128) ||
		!validBuilderSimulationObjectIdentity(engine.ObjectFormat, engine.CommitSHA, engine.TreeSHA) {
		return errors.New("portable execution engine repository identity is invalid")
	}
	artifact := engine.Artifact
	if err := validatePortableExecutionArtifactReferences(artifact); err != nil {
		return err
	}
	if err := validatePortableExecutionArtifactABI(artifact); err != nil {
		return err
	}
	return validatePortableExecutionArtifactIdentity(engine)
}

// validatePortableExecutionArtifactReferences 校验 artifact/receipt key 与显式摘要引用。
func validatePortableExecutionArtifactReferences(artifact PortableExecutionArtifact) error {
	if err := validateBuilderObjectKey("portable execution artifact_key", artifact.ArtifactKey); err != nil {
		return err
	}
	if err := validateBuilderObjectKey("portable execution receipt_key", artifact.ReceiptKey); err != nil {
		return err
	}
	if artifact.ArtifactKey == artifact.ReceiptKey {
		return errors.New("portable execution artifact and receipt keys must differ")
	}
	if !validPortableDigest(artifact.ReceiptSHA256) || !validPortableDigest(artifact.IdentityDigest) {
		return errors.New("portable execution artifact receipt or identity digest is invalid")
	}
	return nil
}

// validatePortableExecutionArtifactIdentity 用独立 runtime artifact 域重算 identity digest。
func validatePortableExecutionArtifactIdentity(engine PortableExecutionEngine) error {
	identity, err := PortableExecutionRuntimeArtifactIdentityDigest(
		engine.RepositoryID,
		engine.ObjectFormat,
		engine.CommitSHA,
		engine.TreeSHA,
		engine.Artifact,
	)
	if err != nil || identity != engine.Artifact.IdentityDigest {
		return errors.New("portable execution artifact identity digest drifted")
	}
	return nil
}

// PortableExecutionRuntimeArtifactIdentityDigest 计算独立 normal runtime artifact identity。
// kind/contract 被纳入域分离材料，source-baseline 或 builder-simulation artifact 无法冒充。
func PortableExecutionRuntimeArtifactIdentityDigest(
	repositoryID string,
	objectFormat string,
	commitSHA string,
	treeSHA string,
	artifact PortableExecutionArtifact,
) (string, error) {
	if !validBuilderSimulationToken(repositoryID, 128) ||
		!validBuilderSimulationObjectIdentity(objectFormat, commitSHA, treeSHA) {
		return "", errors.New("portable execution runtime artifact repository identity is invalid")
	}
	if err := validatePortableExecutionRuntimeArtifactShape(artifact); err != nil {
		return "", err
	}
	hasher := sha256.New()
	for _, value := range []string{
		PortableExecutionRuntimeArtifactIdentityDomain,
		PortableExecutionRuntimeArtifactContractID,
		PortableExecutionRuntimeArtifactKind,
		repositoryID,
		objectFormat,
		commitSHA,
		treeSHA,
		artifact.ArtifactSHA256,
		fmt.Sprintf("%d", artifact.ArtifactSizeBytes),
		artifact.BuilderDigest,
		artifact.ToolchainDigest,
		artifact.ProtocolABIDigest,
		artifact.WorkloadManifestABIDigest,
	} {
		writeArtifactIdentityFrame(hasher, value)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// validatePortableExecutionRuntimeArtifactShape 校验独立 runtime kind、bytes 与 observed identity。
func validatePortableExecutionRuntimeArtifactShape(artifact PortableExecutionArtifact) error {
	if artifact.ContractID != PortableExecutionRuntimeArtifactContractID || artifact.Kind != PortableExecutionRuntimeArtifactKind {
		return errors.New("portable execution runtime artifact contract or kind is invalid")
	}
	if err := validateBuilderArtifact(artifact.ArtifactSHA256, artifact.ArtifactSizeBytes); err != nil {
		return err
	}
	if err := validateBuilderDigests(artifact.BuilderDigest, artifact.ToolchainDigest, "portable execution runtime artifact"); err != nil {
		return err
	}
	if !validPortableDigest(artifact.ProtocolABIDigest) || !validPortableDigest(artifact.WorkloadManifestABIDigest) {
		return errors.New("portable execution runtime artifact ABI digest is invalid")
	}
	return nil
}

// validatePortableExecutionArtifactABI 校验 artifact 使用已冻结的 builder 与 workload ABI。
func validatePortableExecutionArtifactABI(artifact PortableExecutionArtifact) error {
	protocolABI, err := PortableExecutionProtocolABIDigest()
	if err != nil || artifact.ProtocolABIDigest != protocolABI {
		return errors.New("portable execution artifact protocol ABI identity is invalid")
	}
	workloadABI, err := RepositoryWorkloadManifestABIDigest()
	if err != nil || artifact.WorkloadManifestABIDigest != workloadABI {
		return errors.New("portable execution artifact workload manifest ABI identity is invalid")
	}
	return nil
}

// validatePortableExecutionTarget 校验 exact target Git identity 与 manifest repository binding。
func validatePortableExecutionTarget(target PortableExecutionTarget) error {
	if !validBuilderSimulationToken(target.RepositoryID, 128) ||
		!validBuilderSimulationObjectIdentity(target.ObjectFormat, target.CommitSHA, target.TreeSHA) {
		return errors.New("portable execution target repository identity is invalid")
	}
	if !validPortableRepoRelativePath(target.MountPath) {
		return errors.New("portable execution target mount_path is invalid")
	}
	if target.WorkloadManifest.RepositoryID != target.RepositoryID {
		return errors.New("portable execution target manifest repository identity drifted")
	}
	return validatePortableWorkloadManifestShape(target.WorkloadManifest)
}

// validatePortableExecutionAuthority 校验 global config、ledger 与 accepted authority identity。
func validatePortableExecutionAuthority(authority PortableExecutionAuthority) error {
	if err := validatePortableAbsolutePath("portable execution authority.config_path", authority.ConfigPath); err != nil {
		return err
	}
	if err := validatePortableAbsolutePath("portable execution authority.ledger_path", authority.LedgerPath); err != nil {
		return err
	}
	if authority.ConfigPath == authority.LedgerPath {
		return errors.New("portable execution config and ledger paths must differ")
	}
	if !validPortableDigest(authority.ConfigSHA256) ||
		!validPortableDigest(authority.AcceptedAuthoritySHA256) ||
		!validBuilderSimulationToken(authority.LedgerIdentity, 512) ||
		authority.AcceptedGeneration == 0 {
		return errors.New("portable execution global authority identity is invalid")
	}
	return nil
}

// validatePortableExecutionAudit 校验 token digest、profile 与 entrypoint 审计枚举。
func validatePortableExecutionAudit(audit PortableExecutionAudit) error {
	if err := cicontract.ValidateAgentTokenDigest(audit.AgentTokenDigest); err != nil {
		return fmt.Errorf("portable execution agent token digest: %w", err)
	}
	if err := audit.Profile.Validate(); err != nil {
		return fmt.Errorf("portable execution profile: %w", err)
	}
	if err := audit.Entrypoint.Validate(); err != nil {
		return fmt.Errorf("portable execution entrypoint: %w", err)
	}
	if audit.Entrypoint == gate.CIEntrypointGitPreCommit {
		return errors.New("portable execution cannot use the local-only pre-commit entrypoint")
	}
	return nil
}

// PortableExecutionProtocolABIDigest 动态派生 execute request DTO 与枚举身份的 ABI 摘要。
func PortableExecutionProtocolABIDigest() (string, error) {
	material, err := portableExecutionProtocolABIMaterial()
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	writeRepositoryFrame(hasher, []byte(PortableExecutionProtocolABIDomain))
	writeRepositoryFrame(hasher, material)
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// portableExecutionProtocolABIMaterial 动态拼接 DTO、枚举和协议域材料。
func portableExecutionProtocolABIMaterial() ([]byte, error) {
	var material bytes.Buffer
	for _, value := range []string{
		PortableExecutionContractID,
		PortableExecutionRequestSchema,
		cicontract.ExecutionProviderID,
		PortableExecutionRuntimeArtifactContractID,
		PortableExecutionRuntimeArtifactKind,
		PortableExecutionRuntimeArtifactIdentityDomain,
		RepositoryWorkloadManifestABIDigestDomain,
		string(gate.ProfileLocalFast), string(gate.ProfilePush), string(gate.ProfileRemoteRequired),
		string(gate.ProfilePromotion), string(gate.ProfileRelease),
		string(gate.CIEntrypointGitPrePush), string(gate.CIEntrypointManualCLI), string(gate.CIEntrypointRelease),
	} {
		writeRepositoryFrame(&material, []byte(value))
	}
	for _, root := range portableExecutionProtocolABIRootTypes() {
		if err := appendBuilderSimulationABIType(&material, root); err != nil {
			return nil, err
		}
	}
	return material.Bytes(), nil
}

// portableExecutionProtocolABIRootTypes 返回 execute wire 的全部动态 DTO 根。
func portableExecutionProtocolABIRootTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[PortableExecutionRequest](),
		reflect.TypeFor[PortableExecutionEngine](),
		reflect.TypeFor[PortableExecutionArtifact](),
		reflect.TypeFor[PortableExecutionTarget](),
		reflect.TypeFor[PortableExecutionAuthority](),
		reflect.TypeFor[PortableExecutionAudit](),
		reflect.TypeFor[PortableWorkloadManifest](),
	}
}
