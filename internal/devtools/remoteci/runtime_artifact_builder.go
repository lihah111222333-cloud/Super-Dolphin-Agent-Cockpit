package remoteci

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const (
	RuntimeArtifactBuilderInputSchema         = "portable-runtime-artifact-builder-input/v1"
	RuntimeArtifactBuilderReceiptSchema       = "portable-runtime-artifact-builder-receipt/v1"
	RuntimeArtifactBuilderInputDigestDomain   = "portable-runtime-artifact-builder-input-digest/v1"
	RuntimeArtifactBuilderReceiptDigestDomain = "portable-runtime-artifact-builder-receipt-digest/v1"
	RuntimeArtifactExecutableName             = "super-dolphin-remote-ci-linux-amd64"
)

// RuntimeArtifactBuildInput 显式绑定 exact engine tree、staging bytes 与 normal ABI。
type RuntimeArtifactBuildInput struct {
	SchemaVersion             string `json:"schema_version"`
	ContractID                string `json:"contract_id"`
	ArtifactKind              string `json:"artifact_kind"`
	ExecutionProvider         string `json:"execution_provider"`
	ArtifactPrefix            string `json:"artifact_prefix"`
	RepositoryID              string `json:"repository_id"`
	ObjectFormat              string `json:"object_format"`
	CommitSHA                 string `json:"commit_sha"`
	TreeSHA                   string `json:"tree_sha"`
	InputObjectKey            string `json:"input_object_key"`
	InputSHA256               string `json:"input_sha256"`
	InputSizeBytes            int64  `json:"input_size_bytes"`
	ExpectedBuilderDigest     string `json:"expected_builder_digest"`
	ExpectedToolchainDigest   string `json:"expected_toolchain_digest"`
	ProtocolABIDigest         string `json:"protocol_abi_digest"`
	WorkloadManifestABIDigest string `json:"workload_manifest_abi_digest"`
}

// RuntimeArtifactProducerReceipt 绑定 executable bytes 与 ECI 内 observed identity。
type RuntimeArtifactProducerReceipt struct {
	SchemaVersion             string `json:"schema_version"`
	ContractID                string `json:"contract_id"`
	ArtifactKind              string `json:"artifact_kind"`
	ExecutionProvider         string `json:"execution_provider"`
	RepositoryID              string `json:"repository_id"`
	ObjectFormat              string `json:"object_format"`
	SourceCommitSHA           string `json:"source_commit_sha"`
	SourceTreeSHA             string `json:"source_tree_sha"`
	InputSHA256               string `json:"input_sha256"`
	InputSizeBytes            int64  `json:"input_size_bytes"`
	ObservedBuilderDigest     string `json:"observed_builder_digest"`
	ObservedToolchainDigest   string `json:"observed_toolchain_digest"`
	ProtocolABIDigest         string `json:"protocol_abi_digest"`
	WorkloadManifestABIDigest string `json:"workload_manifest_abi_digest"`
	ArtifactKey               string `json:"artifact_key"`
	ArtifactSHA256            string `json:"artifact_sha256"`
	ArtifactSizeBytes         int64  `json:"artifact_size_bytes"`
	ArtifactIdentityDigest    string `json:"artifact_identity_digest"`
}

// RuntimeArtifactBuilderWorkerResult 是 worker stdout 的有界 artifact/receipt readback 索引。
type RuntimeArtifactBuilderWorkerResult struct {
	ArtifactKey            string `json:"artifact_key"`
	ReceiptKey             string `json:"receipt_key"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ArtifactSizeBytes      int64  `json:"artifact_size_bytes"`
	ReceiptSHA256          string `json:"receipt_sha256"`
	ArtifactIdentityDigest string `json:"artifact_identity_digest"`
	InputDigest            string `json:"input_digest"`
}

// RuntimeArtifactBuildEvidence 是 fake/real host lifecycle 的非 PASS 供给证据。
type RuntimeArtifactBuildEvidence struct {
	Request                                                       RuntimeArtifactBuildInput
	InputDigest, Provider, ContainerGroupID, ContainerGroupStatus string
	ContainerExitCode                                             int64
	StartedAt, FinishedAt                                         string
	Artifact                                                      PortableExecutionArtifact
	Receipt                                                       RuntimeArtifactProducerReceipt
}

// RuntimeArtifactBuilderInputConsumerFields 返回显式 input consumer registry 的副本。
func RuntimeArtifactBuilderInputConsumerFields() []string {
	return runtimeArtifactBuilderInputConsumers()
}

func runtimeArtifactBuilderInputConsumers() []string {
	return []string{"artifact_kind", "artifact_prefix", "commit_sha", "contract_id", "execution_provider", "expected_builder_digest", "expected_toolchain_digest", "input_object_key", "input_sha256", "input_size_bytes", "object_format", "protocol_abi_digest", "repository_id", "schema_version", "tree_sha", "workload_manifest_abi_digest"}
}

// ValidateRuntimeArtifactBuilderInputConsumerFields 动态枚举 producer 并拒绝 missing/stale consumer。
func ValidateRuntimeArtifactBuilderInputConsumerFields(consumers []string) error {
	producer := runtimeArtifactJSONFields(reflect.TypeFor[RuntimeArtifactBuildInput]())
	want := append([]string(nil), consumers...)
	sort.Strings(want)
	if len(producer) != len(want) {
		return errors.New("runtime artifact builder input consumer registry size drifted")
	}
	for index := range producer {
		if producer[index] != want[index] || index > 0 && want[index] == want[index-1] {
			return errors.New("runtime artifact builder input consumer registry drifted")
		}
	}
	return nil
}

func runtimeArtifactJSONFields(value reflect.Type) []string {
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

// Validate 分段校验 header、source、staging bytes、observed expected 与两个 ABI。
func (input RuntimeArtifactBuildInput) Validate() error {
	if err := ValidateRuntimeArtifactBuilderInputConsumerFields(runtimeArtifactBuilderInputConsumers()); err != nil {
		return err
	}
	if err := validateRuntimeArtifactInputHeader(input); err != nil {
		return err
	}
	if err := validateRuntimeArtifactSource(input); err != nil {
		return err
	}
	return validateRuntimeArtifactInputMaterial(input)
}

func validateRuntimeArtifactInputHeader(input RuntimeArtifactBuildInput) error {
	if input.SchemaVersion != RuntimeArtifactBuilderInputSchema || input.ContractID != PortableExecutionRuntimeArtifactContractID || input.ArtifactKind != PortableExecutionRuntimeArtifactKind || input.ExecutionProvider != PortableExecutionProvider {
		return errors.New("runtime artifact builder input header is invalid")
	}
	return nil
}

// validateRuntimeArtifactInputMaterial 校验 staging object、expected builder/toolchain 与 normal ABI。
func validateRuntimeArtifactInputMaterial(input RuntimeArtifactBuildInput) error {
	if validateBuilderObjectKey("input_object_key", input.InputObjectKey) != nil || !strings.HasPrefix(input.InputObjectKey, input.ArtifactPrefix) {
		return errors.New("runtime artifact builder input key is invalid")
	}
	if !validPortableDigest(input.InputSHA256) || input.InputSizeBytes <= 0 || input.InputSizeBytes > SourceBaselineBuilderArtifactMaxBytes {
		return errors.New("runtime artifact builder input bytes are invalid")
	}
	if err := validateBuilderDigests(input.ExpectedBuilderDigest, input.ExpectedToolchainDigest, "runtime artifact builder expected"); err != nil {
		return err
	}
	return validateRuntimeArtifactInputABI(input)
}

func validateRuntimeArtifactInputABI(input RuntimeArtifactBuildInput) error {
	protocolABI, protocolErr := PortableExecutionProtocolABIDigest()
	workloadABI, workloadErr := RepositoryWorkloadManifestABIDigest()
	if protocolErr != nil || workloadErr != nil {
		return errors.New("derive runtime artifact ABI")
	}
	if input.ProtocolABIDigest != protocolABI || input.WorkloadManifestABIDigest != workloadABI {
		return errors.New("runtime artifact builder ABI drifted")
	}
	return nil
}

// CanonicalEncode 返回 runtime builder input 的唯一紧凑 JSON。
func (input RuntimeArtifactBuildInput) CanonicalEncode() ([]byte, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	return runtimeArtifactCanonicalJSON(input)
}

// DecodeRuntimeArtifactBuilderInput 严格拒绝 unknown、duplicate、null、trailing 和非 canonical JSON。
func DecodeRuntimeArtifactBuilderInput(data []byte) (RuntimeArtifactBuildInput, error) {
	var input RuntimeArtifactBuildInput
	if err := decodeSourceBaselineBuilderJSON(data, &input); err != nil {
		return input, fmt.Errorf("decode runtime artifact builder input: %w", err)
	}
	canonical, err := input.CanonicalEncode()
	if err != nil || !bytes.Equal(data, canonical) {
		return RuntimeArtifactBuildInput{}, errors.New("runtime artifact builder input is invalid or non-canonical")
	}
	return input, nil
}

// RuntimeArtifactBuilderInputDigest 计算 normal runtime builder input 的域分离摘要。
func RuntimeArtifactBuilderInputDigest(input RuntimeArtifactBuildInput) (string, error) {
	data, err := input.CanonicalEncode()
	if err != nil {
		return "", err
	}
	return runtimeArtifactDomainDigest(RuntimeArtifactBuilderInputDigestDomain, data), nil
}

func (receipt RuntimeArtifactProducerReceipt) artifact(receiptKey, receiptSHA string) PortableExecutionArtifact {
	return PortableExecutionArtifact{ContractID: receipt.ContractID, Kind: receipt.ArtifactKind, ArtifactKey: receipt.ArtifactKey, ArtifactSHA256: receipt.ArtifactSHA256, ArtifactSizeBytes: receipt.ArtifactSizeBytes, ReceiptKey: receiptKey, ReceiptSHA256: receiptSHA, IdentityDigest: receipt.ArtifactIdentityDigest, BuilderDigest: receipt.ObservedBuilderDigest, ToolchainDigest: receipt.ObservedToolchainDigest, ProtocolABIDigest: receipt.ProtocolABIDigest, WorkloadManifestABIDigest: receipt.WorkloadManifestABIDigest}
}

// Validate 复核 receipt header、source、artifact bytes、observed identity、ABI 与 canonical key。
func (receipt RuntimeArtifactProducerReceipt) Validate() error {
	if err := validateRuntimeArtifactReceiptMaterial(receipt); err != nil {
		return err
	}
	prefix := runtimeArtifactPrefixFromKey(receipt.ArtifactKey)
	artifact := receipt.artifact("", "")
	identity, err := PortableExecutionRuntimeArtifactIdentityDigest(receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceCommitSHA, receipt.SourceTreeSHA, artifact)
	if err != nil || identity != receipt.ArtifactIdentityDigest {
		return errors.New("runtime artifact receipt identity drifted")
	}
	expected, _, err := CanonicalRuntimeArtifactBuilderKeys(prefix, receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceTreeSHA, identity)
	if err != nil || expected != receipt.ArtifactKey {
		return errors.New("runtime artifact receipt key drifted")
	}
	return validatePortableExecutionArtifactABI(artifact)
}

// validateRuntimeArtifactReceiptMaterial 校验 header、source、observed identity 与 artifact bytes。
func validateRuntimeArtifactReceiptMaterial(receipt RuntimeArtifactProducerReceipt) error {
	if receipt.SchemaVersion != RuntimeArtifactBuilderReceiptSchema || receipt.ContractID != PortableExecutionRuntimeArtifactContractID || receipt.ArtifactKind != PortableExecutionRuntimeArtifactKind || receipt.ExecutionProvider != PortableExecutionProvider {
		return errors.New("runtime artifact receipt header is invalid")
	}
	input := RuntimeArtifactBuildInput{ArtifactPrefix: runtimeArtifactPrefixFromKey(receipt.ArtifactKey), RepositoryID: receipt.RepositoryID, ObjectFormat: receipt.ObjectFormat, CommitSHA: receipt.SourceCommitSHA, TreeSHA: receipt.SourceTreeSHA}
	if err := validateRuntimeArtifactSource(input); err != nil {
		return err
	}
	if err := validateBuilderDigests(receipt.ObservedBuilderDigest, receipt.ObservedToolchainDigest, "runtime artifact observed"); err != nil {
		return err
	}
	if !validPortableDigest(receipt.InputSHA256) || receipt.InputSizeBytes <= 0 || receipt.InputSizeBytes > SourceBaselineBuilderArtifactMaxBytes {
		return errors.New("runtime artifact receipt input bundle bytes are invalid")
	}
	return validateBuilderArtifact(receipt.ArtifactSHA256, receipt.ArtifactSizeBytes)
}

func validateRuntimeArtifactSource(input RuntimeArtifactBuildInput) error {
	if err := validateBuilderArtifactPrefix(input.ArtifactPrefix); err != nil {
		return err
	}
	if err := validateBuilderIdentifier("repository_id", input.RepositoryID); err != nil {
		return err
	}
	format, err := validateBuilderObjectFormat(input.ObjectFormat)
	if err != nil {
		return err
	}
	return validateBuilderCommitTree(input.CommitSHA, input.TreeSHA, format, "runtime artifact builder")
}

// CanonicalEncode 返回 runtime artifact receipt 的唯一紧凑 JSON。
func (receipt RuntimeArtifactProducerReceipt) CanonicalEncode() ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return runtimeArtifactCanonicalJSON(receipt)
}

// DecodeRuntimeArtifactProducerReceipt 严格解码 canonical executable receipt。
func DecodeRuntimeArtifactProducerReceipt(data []byte) (RuntimeArtifactProducerReceipt, error) {
	var receipt RuntimeArtifactProducerReceipt
	if err := decodeSourceBaselineBuilderJSON(data, &receipt); err != nil {
		return receipt, err
	}
	canonical, err := receipt.CanonicalEncode()
	if err != nil || !bytes.Equal(data, canonical) {
		return RuntimeArtifactProducerReceipt{}, errors.New("runtime artifact receipt is invalid or non-canonical")
	}
	return receipt, nil
}

// EncodeRuntimeArtifactProducerReceipt 返回 canonical receipt bytes 与 raw byte SHA-256。
func EncodeRuntimeArtifactProducerReceipt(receipt RuntimeArtifactProducerReceipt) ([]byte, string, error) {
	data, err := receipt.CanonicalEncode()
	if err != nil {
		return nil, "", err
	}
	return data, bytesDigest(data), nil
}

// CanonicalRuntimeArtifactBuilderKeys 返回 executable 与 receipt 的唯一 content-addressed keys。
func CanonicalRuntimeArtifactBuilderKeys(prefix, repositoryID, objectFormat, treeSHA, identity string) (string, string, error) {
	if err := validateBuilderArtifactPrefix(prefix); err != nil {
		return "", "", err
	}
	if err := validateBuilderIdentifier("repository_id", repositoryID); err != nil {
		return "", "", err
	}
	format, err := validateBuilderObjectFormat(objectFormat)
	if err != nil || !validOID(treeSHA, format) || !validPortableDigest(identity) {
		return "", "", errors.New("runtime artifact key identity is invalid")
	}
	root := strings.Join([]string{repositoryID, objectFormat, treeSHA, strings.TrimPrefix(identity, "sha256:")}, "/")
	artifactKey := prefix + "runtime-artifacts/" + root + "/" + RuntimeArtifactExecutableName
	receiptKey := prefix + "runtime-receipts/" + root + ".json"
	if validateBuilderObjectKey("artifact_key", artifactKey) != nil || validateBuilderObjectKey("receipt_key", receiptKey) != nil {
		return "", "", errors.New("runtime artifact object key is invalid")
	}
	return artifactKey, receiptKey, nil
}

func runtimeArtifactPrefixFromKey(key string) string {
	index := strings.Index(key, "runtime-artifacts/")
	if index <= 0 {
		return ""
	}
	return key[:index]
}

func runtimeArtifactCanonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 || len(data) > SourceBaselineBuilderProtocolMaxBytes {
		return nil, errors.New("runtime artifact JSON is invalid")
	}
	return data, nil
}

func runtimeArtifactDomainDigest(domain string, data []byte) string {
	hash := sha256.New()
	writeArtifactIdentityFrame(hash, domain)
	writeArtifactIdentityFrame(hash, string(data))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// ValidateAgainst 复核 worker stdout 索引与 exact input 及 canonical keys。
func (result RuntimeArtifactBuilderWorkerResult) ValidateAgainst(input RuntimeArtifactBuildInput) error {
	inputDigest, err := RuntimeArtifactBuilderInputDigest(input)
	if err != nil || result.InputDigest != inputDigest || !validPortableDigest(result.ArtifactSHA256) || result.ArtifactSizeBytes <= 0 || !validPortableDigest(result.ReceiptSHA256) || !validPortableDigest(result.ArtifactIdentityDigest) {
		return errors.New("runtime artifact worker result identity is invalid")
	}
	artifactKey, receiptKey, err := CanonicalRuntimeArtifactBuilderKeys(input.ArtifactPrefix, input.RepositoryID, input.ObjectFormat, input.TreeSHA, result.ArtifactIdentityDigest)
	if err != nil || result.ArtifactKey != artifactKey || result.ReceiptKey != receiptKey {
		return errors.New("runtime artifact worker result keys drifted")
	}
	return nil
}
