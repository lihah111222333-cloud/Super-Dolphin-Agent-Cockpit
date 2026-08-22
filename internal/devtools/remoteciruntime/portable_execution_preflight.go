package remoteciruntime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	portableExecutionPreflightBindingDomain = "portable-remote-ci-execution-preflight-binding/v1"
	portableExecutionPreflightJSONMaxBytes  = int64(1 << 20)
)

// PortableExecutionReadHandle 是单次打开的配置或 artifact 只读快照句柄。
type PortableExecutionReadHandle interface {
	Reference() string
	ReadAll(context.Context, int64) ([]byte, error)
	Close() error
}

// PortableExecutionReadOpener 只允许按显式 path/key 打开一次只读句柄。
type PortableExecutionReadOpener interface {
	OpenReadOnly(context.Context, string) (PortableExecutionReadHandle, error)
}

// PortableExecutionAcceptedAuthority 是同一 SQLite handle 读出的 accepted singleton 快照。
type PortableExecutionAcceptedAuthority struct {
	Generation  uint64
	StateJSON   []byte
	StateSHA256 string
	State       cicontract.AcceptedBaselineState
}

// PortableExecutionLedgerHandle 是同一 SQLite v23 文件的只读查询句柄。
type PortableExecutionLedgerHandle interface {
	Reference() string
	FileIdentity(context.Context) (string, error)
	PhysicalSchemaVersion(context.Context) (uint32, error)
	ReadAcceptedAuthority(context.Context) (PortableExecutionAcceptedAuthority, error)
	Close() error
}

// PortableExecutionLedgerOpener 只读打开显式 ledger，不迁移、不 chmod、不写 authority。
type PortableExecutionLedgerOpener interface {
	OpenLedgerReadOnly(context.Context, string) (PortableExecutionLedgerHandle, error)
}

// PortableExecutionGitReader 从显式 repository identity 的 exact commit/tree 读取对象。
type PortableExecutionGitReader interface {
	ResolveCommitTree(context.Context, string, string, string) (string, error)
	ReadTreeBlob(context.Context, string, string, string, string) (remoteci.GitTreeBlob, error)
}

// PortableExecutionPreflightDependencies 只包含 binder 允许触达的四类只读依赖。
type PortableExecutionPreflightDependencies struct {
	ConfigFiles PortableExecutionReadOpener
	Ledger      PortableExecutionLedgerOpener
	TargetGit   PortableExecutionGitReader
	Artifacts   PortableExecutionReadOpener
}

// PortableExecutionRuntimeArtifactBinding 保存已验证 artifact 与 producer input provenance。
type PortableExecutionRuntimeArtifactBinding struct {
	Artifact       remoteci.PortableExecutionArtifact
	Receipt        remoteci.RuntimeArtifactProducerReceipt
	InputSHA256    string
	InputSizeBytes int64
}

// PortableExecutionPreflightBinding 是 normal runtime 后续 composition 可消费的只读绑定。
type PortableExecutionPreflightBinding struct {
	Request         remoteci.PortableExecutionRequest
	Config          Config
	Authority       PortableExecutionAcceptedAuthority
	Manifest        remoteci.RepositoryWorkloadManifest
	ProfileCatalog  remoteci.RepositoryProfileCatalogMapping
	RuntimeArtifact PortableExecutionRuntimeArtifactBinding
	BindingDigest   string
}

type portableExecutionPreflightBindingMaterial struct {
	AcceptedAuthoritySHA256 string `json:"accepted_authority_sha256"`
	AcceptedGeneration      uint64 `json:"accepted_generation"`
	ArtifactIdentityDigest  string `json:"artifact_identity_digest"`
	ArtifactSHA256          string `json:"artifact_sha256"`
	ArtifactSizeBytes       int64  `json:"artifact_size_bytes"`
	CatalogDigest           string `json:"catalog_digest"`
	ConfigSHA256            string `json:"config_sha256"`
	InputSHA256             string `json:"input_sha256"`
	InputSizeBytes          int64  `json:"input_size_bytes"`
	LedgerIdentity          string `json:"ledger_identity"`
	LedgerSchemaVersion     uint32 `json:"ledger_schema_version"`
	ManifestDigest          string `json:"manifest_digest"`
	ManifestSHA256          string `json:"manifest_sha256"`
	ReceiptSHA256           string `json:"receipt_sha256"`
	RequestDigest           string `json:"request_digest"`
}

// BindPortableExecutionPreflight 按冻结顺序完成 normal execute 的 side-effect-free 只读绑定。
func BindPortableExecutionPreflight(ctx context.Context, request remoteci.PortableExecutionRequest, dependencies PortableExecutionPreflightDependencies) (PortableExecutionPreflightBinding, error) {
	if err := request.Validate(); err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	if err := validatePortableExecutionPreflightDependencies(ctx, dependencies); err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	config, err := bindPortableExecutionConfig(ctx, dependencies.ConfigFiles, request.Authority)
	if err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	authority, err := bindPortableExecutionLedger(ctx, dependencies.Ledger, request.Authority)
	if err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	if authority.State.ExecutionProvider != cicontract.ExecutionProviderID || authority.State.RegionID != config.RegionID {
		return PortableExecutionPreflightBinding{}, errors.New("portable execution config and accepted authority identity differ")
	}
	manifest, mapping, err := bindPortableExecutionManifest(ctx, dependencies.TargetGit, request)
	if err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	runtimeArtifact, err := bindPortableExecutionRuntimeArtifact(ctx, dependencies.Artifacts, request.Engine)
	if err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	binding := PortableExecutionPreflightBinding{Request: request, Config: config, Authority: authority, Manifest: manifest, ProfileCatalog: mapping, RuntimeArtifact: runtimeArtifact}
	binding.BindingDigest, err = portableExecutionPreflightBindingDigest(binding)
	if err != nil {
		return PortableExecutionPreflightBinding{}, err
	}
	return binding, nil
}

// validatePortableExecutionPreflightDependencies 在任何 I/O 前拒绝 context、nil 依赖与字段守卫漂移。
func validatePortableExecutionPreflightDependencies(ctx context.Context, dependencies PortableExecutionPreflightDependencies) error {
	if ctx == nil {
		return errors.New("portable execution preflight context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("portable execution preflight context: %w", err)
	}
	if portableExecutionDependencyNil(dependencies.ConfigFiles) || portableExecutionDependencyNil(dependencies.Ledger) ||
		portableExecutionDependencyNil(dependencies.TargetGit) || portableExecutionDependencyNil(dependencies.Artifacts) {
		return errors.New("portable execution preflight read-only dependencies are required")
	}
	return ValidatePortableExecutionPreflightBindingConsumerFields(portableExecutionPreflightBindingConsumers())
}

// bindPortableExecutionConfig 从一次打开的 config handle 计算 SHA 并 strict decode 同一 bytes。
func bindPortableExecutionConfig(ctx context.Context, opener PortableExecutionReadOpener, authority remoteci.PortableExecutionAuthority) (Config, error) {
	data, err := readPortableExecutionSnapshot(ctx, opener, authority.ConfigPath, portableExecutionPreflightJSONMaxBytes)
	if err != nil {
		return Config{}, fmt.Errorf("read portable execution config snapshot: %w", err)
	}
	if portableExecutionBytesDigest(data) != authority.ConfigSHA256 {
		return Config{}, errors.New("portable execution config SHA-256 drifted")
	}
	config, err := DecodeConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("decode portable execution config snapshot: %w", err)
	}
	return config, nil
}

// bindPortableExecutionLedger 从同一只读 SQLite handle 核对文件、v23 与 accepted authority。
func bindPortableExecutionLedger(ctx context.Context, opener PortableExecutionLedgerOpener, expected remoteci.PortableExecutionAuthority) (PortableExecutionAcceptedAuthority, error) {
	handle, err := opener.OpenLedgerReadOnly(ctx, expected.LedgerPath)
	if err != nil {
		return PortableExecutionAcceptedAuthority{}, fmt.Errorf("open portable execution ledger read-only: %w", err)
	}
	if portableExecutionDependencyNil(handle) {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution ledger opener returned nil handle")
	}
	authority, validationErr := readPortableExecutionLedgerHandle(ctx, handle, expected)
	closeErr := handle.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close portable execution ledger handle: %w", closeErr)
	}
	if joined := errors.Join(validationErr, closeErr); joined != nil {
		return PortableExecutionAcceptedAuthority{}, joined
	}
	return authority, nil
}

// readPortableExecutionLedgerHandle 只在已打开的同一 handle 上读取全部 authority 事实。
func readPortableExecutionLedgerHandle(ctx context.Context, handle PortableExecutionLedgerHandle, expected remoteci.PortableExecutionAuthority) (PortableExecutionAcceptedAuthority, error) {
	if handle.Reference() != expected.LedgerPath {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution ledger handle reference drifted")
	}
	identity, err := handle.FileIdentity(ctx)
	if err != nil || identity != expected.LedgerIdentity {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution ledger file identity drifted")
	}
	schema, err := handle.PhysicalSchemaVersion(ctx)
	if err != nil || schema != cicontract.DurationLedgerSQLiteSchemaVersion {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution ledger physical schema is not v23")
	}
	authority, err := handle.ReadAcceptedAuthority(ctx)
	if err != nil {
		return PortableExecutionAcceptedAuthority{}, fmt.Errorf("read portable execution accepted authority: %w", err)
	}
	return validatePortableExecutionAcceptedAuthority(authority, expected)
}

// validatePortableExecutionAcceptedAuthority strict 校验 accepted singleton JSON 与请求摘要。
func validatePortableExecutionAcceptedAuthority(authority PortableExecutionAcceptedAuthority, expected remoteci.PortableExecutionAuthority) (PortableExecutionAcceptedAuthority, error) {
	if authority.Generation != expected.AcceptedGeneration || authority.StateSHA256 != expected.AcceptedAuthoritySHA256 {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution accepted authority identity drifted")
	}
	if len(authority.StateJSON) == 0 || int64(len(authority.StateJSON)) > portableExecutionPreflightJSONMaxBytes || portableExecutionBytesDigest(authority.StateJSON) != authority.StateSHA256 {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution accepted authority bytes drifted")
	}
	state, err := cicontract.DecodeAcceptedBaselineState(authority.StateJSON)
	if err != nil || state.Generation != authority.Generation {
		return PortableExecutionAcceptedAuthority{}, errors.New("portable execution accepted authority state is invalid")
	}
	authority.StateJSON = append([]byte(nil), authority.StateJSON...)
	authority.State = state
	return authority, nil
}

// bindPortableExecutionManifest 从 exact target commit/tree 读取 regular v5 manifest 并映射 profile。
func bindPortableExecutionManifest(ctx context.Context, reader PortableExecutionGitReader, request remoteci.PortableExecutionRequest) (remoteci.RepositoryWorkloadManifest, remoteci.RepositoryProfileCatalogMapping, error) {
	target := request.Target
	resolvedTree, err := reader.ResolveCommitTree(ctx, target.RepositoryID, target.ObjectFormat, target.CommitSHA)
	if err != nil || resolvedTree != target.TreeSHA {
		return remoteci.RepositoryWorkloadManifest{}, remoteci.RepositoryProfileCatalogMapping{}, errors.New("portable execution target commit/tree identity drifted")
	}
	blob, err := reader.ReadTreeBlob(ctx, target.RepositoryID, target.ObjectFormat, target.TreeSHA, target.WorkloadManifest.RepoRelativePath)
	if err != nil {
		return remoteci.RepositoryWorkloadManifest{}, remoteci.RepositoryProfileCatalogMapping{}, fmt.Errorf("read portable execution manifest blob: %w", err)
	}
	manifest, err := decodePortableExecutionManifestBlob(blob, target)
	if err != nil {
		return remoteci.RepositoryWorkloadManifest{}, remoteci.RepositoryProfileCatalogMapping{}, err
	}
	mapping, err := remoteci.MapRepositoryWorkloadProfile(manifest, request.Audit.Profile)
	if err != nil {
		return remoteci.RepositoryWorkloadManifest{}, remoteci.RepositoryProfileCatalogMapping{}, fmt.Errorf("map portable execution workload profile: %w", err)
	}
	catalogDigest, err := gate.WorkloadCatalogDigest(mapping.Catalog)
	if err != nil || catalogDigest != mapping.CatalogDigest {
		return remoteci.RepositoryWorkloadManifest{}, remoteci.RepositoryProfileCatalogMapping{}, errors.New("portable execution catalog digest drifted")
	}
	return manifest, mapping, nil
}

// decodePortableExecutionManifestBlob 校验 regular blob、bytes、schema、ABI 与 repository identity。
func decodePortableExecutionManifestBlob(blob remoteci.GitTreeBlob, target remoteci.PortableExecutionTarget) (remoteci.RepositoryWorkloadManifest, error) {
	if blob.Mode != "100644" && blob.Mode != "100755" {
		return remoteci.RepositoryWorkloadManifest{}, errors.New("portable execution workload manifest is not a regular blob")
	}
	if len(blob.Data) == 0 || portableExecutionBytesDigest(blob.Data) != target.WorkloadManifest.SHA256 {
		return remoteci.RepositoryWorkloadManifest{}, errors.New("portable execution workload manifest bytes drifted")
	}
	manifest, err := remoteci.DecodeRepositoryWorkloadManifest(blob.Data)
	if err != nil {
		return remoteci.RepositoryWorkloadManifest{}, fmt.Errorf("decode portable execution workload manifest: %w", err)
	}
	if manifest.SchemaVersion != target.WorkloadManifest.SchemaVersion || manifest.RepositoryID != target.RepositoryID {
		return remoteci.RepositoryWorkloadManifest{}, errors.New("portable execution workload manifest identity drifted")
	}
	abi, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil || abi != target.WorkloadManifest.ABIDigest {
		return remoteci.RepositoryWorkloadManifest{}, errors.New("portable execution workload manifest ABI drifted")
	}
	return manifest, nil
}

// bindPortableExecutionRuntimeArtifact strict 读取 producer receipt 与 executable bytes 并交叉绑定。
func bindPortableExecutionRuntimeArtifact(ctx context.Context, opener PortableExecutionReadOpener, engine remoteci.PortableExecutionEngine) (PortableExecutionRuntimeArtifactBinding, error) {
	expected := engine.Artifact
	receiptBytes, err := readPortableExecutionSnapshot(ctx, opener, expected.ReceiptKey, portableExecutionPreflightJSONMaxBytes)
	if err != nil || portableExecutionBytesDigest(receiptBytes) != expected.ReceiptSHA256 {
		return PortableExecutionRuntimeArtifactBinding{}, errors.New("portable execution runtime receipt bytes drifted")
	}
	receipt, err := remoteci.DecodeRuntimeArtifactProducerReceipt(receiptBytes)
	if err != nil {
		return PortableExecutionRuntimeArtifactBinding{}, fmt.Errorf("decode portable execution runtime receipt: %w", err)
	}
	if err := validatePortableExecutionRuntimeReceipt(receipt, engine); err != nil {
		return PortableExecutionRuntimeArtifactBinding{}, err
	}
	artifactBytes, err := readPortableExecutionSnapshot(ctx, opener, expected.ArtifactKey, expected.ArtifactSizeBytes)
	if err != nil || int64(len(artifactBytes)) != expected.ArtifactSizeBytes || portableExecutionBytesDigest(artifactBytes) != expected.ArtifactSHA256 {
		return PortableExecutionRuntimeArtifactBinding{}, errors.New("portable execution runtime artifact bytes drifted")
	}
	return PortableExecutionRuntimeArtifactBinding{Artifact: expected, Receipt: receipt, InputSHA256: receipt.InputSHA256, InputSizeBytes: receipt.InputSizeBytes}, nil
}

// validatePortableExecutionRuntimeReceipt 逐字段核对 source、key、bytes、identity、toolchain、ABI 与 input provenance。
func validatePortableExecutionRuntimeReceipt(receipt remoteci.RuntimeArtifactProducerReceipt, engine remoteci.PortableExecutionEngine) error {
	if err := receipt.Validate(); err != nil {
		return fmt.Errorf("validate portable execution runtime receipt: %w", err)
	}
	if err := validatePortableExecutionRuntimeReceiptSource(receipt, engine); err != nil {
		return err
	}
	if err := validatePortableExecutionRuntimeReceiptArtifact(receipt, engine.Artifact); err != nil {
		return err
	}
	if err := validatePortableExecutionRuntimeReceiptToolchain(receipt, engine.Artifact); err != nil {
		return err
	}
	return validatePortableExecutionRuntimeKeys(receipt, engine.Artifact)
}

// validatePortableExecutionRuntimeReceiptSource 核对 producer source provenance。
func validatePortableExecutionRuntimeReceiptSource(receipt remoteci.RuntimeArtifactProducerReceipt, engine remoteci.PortableExecutionEngine) error {
	if receipt.RepositoryID != engine.RepositoryID || receipt.ObjectFormat != engine.ObjectFormat ||
		receipt.SourceCommitSHA != engine.CommitSHA || receipt.SourceTreeSHA != engine.TreeSHA {
		return errors.New("portable execution runtime receipt source provenance drifted")
	}
	return nil
}

// validatePortableExecutionRuntimeReceiptArtifact 核对 executable bytes 与 identity。
func validatePortableExecutionRuntimeReceiptArtifact(receipt remoteci.RuntimeArtifactProducerReceipt, expected remoteci.PortableExecutionArtifact) error {
	if receipt.ArtifactKey != expected.ArtifactKey || receipt.ArtifactSHA256 != expected.ArtifactSHA256 ||
		receipt.ArtifactSizeBytes != expected.ArtifactSizeBytes || receipt.ArtifactIdentityDigest != expected.IdentityDigest {
		return errors.New("portable execution runtime receipt artifact identity drifted")
	}
	return nil
}

// validatePortableExecutionRuntimeReceiptToolchain 核对 observed builder/toolchain 与两个 ABI。
func validatePortableExecutionRuntimeReceiptToolchain(receipt remoteci.RuntimeArtifactProducerReceipt, expected remoteci.PortableExecutionArtifact) error {
	if receipt.ObservedBuilderDigest != expected.BuilderDigest || receipt.ObservedToolchainDigest != expected.ToolchainDigest ||
		receipt.ProtocolABIDigest != expected.ProtocolABIDigest || receipt.WorkloadManifestABIDigest != expected.WorkloadManifestABIDigest {
		return errors.New("portable execution runtime receipt toolchain or ABI drifted")
	}
	return nil
}

// validatePortableExecutionRuntimeKeys 核对 artifact/receipt 的 canonical content-addressed key 对。
func validatePortableExecutionRuntimeKeys(receipt remoteci.RuntimeArtifactProducerReceipt, expected remoteci.PortableExecutionArtifact) error {
	index := strings.Index(receipt.ArtifactKey, "runtime-artifacts/")
	if index <= 0 {
		return errors.New("portable execution runtime artifact key is not canonical")
	}
	artifactKey, receiptKey, err := remoteci.CanonicalRuntimeArtifactBuilderKeys(receipt.ArtifactKey[:index], receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceTreeSHA, receipt.ArtifactIdentityDigest)
	if err != nil || artifactKey != expected.ArtifactKey || receiptKey != expected.ReceiptKey {
		return errors.New("portable execution runtime artifact/receipt key pair drifted")
	}
	return nil
}

// readPortableExecutionSnapshot 从单次 handle 复制 bytes，禁止 path/key reopen fallback。
func readPortableExecutionSnapshot(ctx context.Context, opener PortableExecutionReadOpener, reference string, maxBytes int64) ([]byte, error) {
	handle, err := opener.OpenReadOnly(ctx, reference)
	if err != nil {
		return nil, err
	}
	if portableExecutionDependencyNil(handle) {
		return nil, errors.New("portable execution read opener returned nil handle")
	}
	if handle.Reference() != reference {
		closeErr := handle.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close drifted portable execution read handle: %w", closeErr)
		}
		return nil, errors.Join(errors.New("portable execution read handle reference drifted"), closeErr)
	}
	data, readErr := handle.ReadAll(ctx, maxBytes)
	closeErr := handle.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close portable execution read handle: %w", closeErr)
	}
	if joined := errors.Join(readErr, closeErr); joined != nil {
		return nil, joined
	}
	if len(data) == 0 || maxBytes <= 0 || int64(len(data)) > maxBytes {
		return nil, errors.New("portable execution read snapshot size is invalid")
	}
	return append([]byte(nil), data...), nil
}

// portableExecutionPreflightBindingDigest 绑定请求与全部实读 derived facts。
func portableExecutionPreflightBindingDigest(binding PortableExecutionPreflightBinding) (string, error) {
	requestBytes, err := binding.Request.CanonicalEncode()
	if err != nil {
		return "", err
	}
	material := portableExecutionPreflightBindingMaterial{
		RequestDigest: portableExecutionBytesDigest(requestBytes), ConfigSHA256: binding.Request.Authority.ConfigSHA256,
		LedgerIdentity: binding.Request.Authority.LedgerIdentity, LedgerSchemaVersion: cicontract.DurationLedgerSQLiteSchemaVersion,
		AcceptedGeneration: binding.Authority.Generation, AcceptedAuthoritySHA256: binding.Authority.StateSHA256,
		ManifestSHA256: binding.Request.Target.WorkloadManifest.SHA256, ManifestDigest: binding.Manifest.ManifestDigest,
		CatalogDigest: binding.ProfileCatalog.CatalogDigest, ArtifactSHA256: binding.RuntimeArtifact.Artifact.ArtifactSHA256,
		ArtifactSizeBytes: binding.RuntimeArtifact.Artifact.ArtifactSizeBytes, ArtifactIdentityDigest: binding.RuntimeArtifact.Artifact.IdentityDigest,
		ReceiptSHA256: binding.RuntimeArtifact.Artifact.ReceiptSHA256, InputSHA256: binding.RuntimeArtifact.InputSHA256,
		InputSizeBytes: binding.RuntimeArtifact.InputSizeBytes,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("encode portable execution preflight binding: %w", err)
	}
	return portableExecutionFramedDigest(portableExecutionPreflightBindingDomain, encoded), nil
}

// ValidatePortableExecutionPreflightBindingConsumerFields 动态拒绝 binding digest 字段 missing/stale。
func ValidatePortableExecutionPreflightBindingConsumerFields(consumers []string) error {
	producer := portableExecutionPreflightMaterialFields()
	want := append([]string(nil), consumers...)
	sort.Strings(want)
	if len(producer) != len(want) {
		return errors.New("portable execution preflight binding consumer registry size drifted")
	}
	for index := range producer {
		if producer[index] != want[index] || index > 0 && want[index] == want[index-1] {
			return errors.New("portable execution preflight binding consumer registry drifted")
		}
	}
	return nil
}

// portableExecutionPreflightBindingConsumers 登记 digest 对每个 derived field 的消费。
func portableExecutionPreflightBindingConsumers() []string {
	return []string{"accepted_authority_sha256", "accepted_generation", "artifact_identity_digest", "artifact_sha256", "artifact_size_bytes", "catalog_digest", "config_sha256", "input_sha256", "input_size_bytes", "ledger_identity", "ledger_schema_version", "manifest_digest", "manifest_sha256", "receipt_sha256", "request_digest"}
}

// portableExecutionPreflightMaterialFields 动态枚举 binding material 的 JSON producer 字段。
func portableExecutionPreflightMaterialFields() []string {
	value := reflect.TypeFor[portableExecutionPreflightBindingMaterial]()
	fields := make([]string, 0, value.NumField())
	for field := range value.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return nil
		}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

// portableExecutionDependencyNil 拒绝 nil interface 与 typed nil read-only 依赖。
func portableExecutionDependencyNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// portableExecutionBytesDigest 计算 canonical sha256: bytes identity。
func portableExecutionBytesDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// portableExecutionFramedDigest 对 domain/material 做长度帧哈希。
func portableExecutionFramedDigest(domain string, material []byte) string {
	hasher := sha256.New()
	for _, value := range [][]byte{[]byte(domain), material} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write(value)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}
