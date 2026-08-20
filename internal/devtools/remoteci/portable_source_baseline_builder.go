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
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	// SourceBaselineBuilderInputSchema identifies the builder input protocol.
	SourceBaselineBuilderInputSchema = "source-baseline-builder-input/v1"
	// SourceBaselineProducerReceiptSchema identifies the producer receipt protocol.
	SourceBaselineProducerReceiptSchema = "source-baseline-git-bundle-producer-receipt/v1"
	// SourceBaselineBuilderInputDigestDomain prevents input digests from crossing protocols.
	SourceBaselineBuilderInputDigestDomain = "source-baseline-builder-input-digest/v1"
	// SourceBaselineProducerReceiptDigestDomain prevents receipt digests from crossing protocols.
	SourceBaselineProducerReceiptDigestDomain = "source-baseline-git-bundle-producer-receipt-digest/v1"
	// SourceBaselineBuilderProtocolMaxBytes bounds every JSON protocol document.
	SourceBaselineBuilderProtocolMaxBytes = 1 << 20
	// SourceBaselineBuilderArtifactMaxBytes bounds content-addressed Git bundle materials.
	SourceBaselineBuilderArtifactMaxBytes = 1 << 30
)

// SourceBaselineBuildInput 是一次 source baseline 制品构建的完整显式输入。
type SourceBaselineBuildInput struct {
	SchemaVersion   string `json:"schema_version"`
	RepositoryID    string `json:"repository_id"`
	ObjectFormat    string `json:"object_format"`
	CommitSHA       string `json:"commit_sha"`
	TreeSHA         string `json:"tree_sha"`
	InputObjectKey  string `json:"input_object_key"`
	InputSHA256     string `json:"input_sha256"`
	InputSizeBytes  int64  `json:"input_size_bytes"`
	BuilderDigest   string `json:"builder_digest"`
	ToolchainDigest string `json:"toolchain_digest"`
}

// SourceBaselineProducerReceipt 是构建证据携带的 typed source-baseline producer receipt。
type SourceBaselineProducerReceipt struct {
	SchemaVersion     string `json:"schema_version"`
	ExecutionProvider string `json:"execution_provider"`
	BuilderDigest     string `json:"builder_digest"`
	ToolchainDigest   string `json:"toolchain_digest"`
	RepositoryID      string `json:"repository_id"`
	ObjectFormat      string `json:"object_format"`
	BaselineCommitSHA string `json:"baseline_commit_sha"`
	BaselineTreeSHA   string `json:"baseline_tree_sha"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	ArtifactSizeBytes int64  `json:"artifact_size_bytes"`
}

// SourceBaselineBuildEvidence 是制品 builder 返回的有界编排证据。
type SourceBaselineBuildEvidence struct {
	Provider         string                        `json:"provider"`
	ContainerGroupID string                        `json:"container_group_id"`
	ArtifactKey      string                        `json:"artifact_key"`
	ReceiptKey       string                        `json:"receipt_key"`
	Receipt          SourceBaselineProducerReceipt `json:"receipt"`
}

// SourceBaselineArtifactBuilderRuntime 是唯一制品 builder 的编排端口。
type SourceBaselineArtifactBuilderRuntime interface {
	Build(ctx context.Context, input SourceBaselineBuildInput) (SourceBaselineBuildEvidence, error)
}

// Validate 拒绝隐式根、可变身份和不完整的构建输入。
func (input SourceBaselineBuildInput) Validate() error {
	if input.SchemaVersion != SourceBaselineBuilderInputSchema {
		return errors.New("source baseline builder input schema is invalid")
	}
	if err := validateBuilderIdentifier("repository_id", input.RepositoryID); err != nil {
		return err
	}
	format, err := validateBuilderObjectFormat(input.ObjectFormat)
	if err != nil {
		return err
	}
	if err := validateBuilderCommitTree(input.CommitSHA, input.TreeSHA, format, "source baseline builder"); err != nil {
		return err
	}
	if err := validateBuilderObjectKey("input_object_key", input.InputObjectKey); err != nil {
		return err
	}
	if err := validateBuilderInputMaterial(input); err != nil {
		return err
	}
	return nil
}

// validateBuilderObjectFormat 校验 builder 协议支持的 Git object format。
func validateBuilderObjectFormat(value string) (gate.GitObjectFormat, error) {
	switch value {
	case string(gate.GitObjectFormatSHA1), string(gate.GitObjectFormatSHA256):
		return gate.GitObjectFormat(value), nil
	default:
		return "", errors.New("source baseline builder object format is invalid")
	}
}

// validateBuilderCommitTree 校验 exact commit/tree 的格式和身份。
func validateBuilderCommitTree(commitSHA, treeSHA string, format gate.GitObjectFormat, scope string) error {
	if !validOID(commitSHA, format) || !validOID(treeSHA, format) {
		return fmt.Errorf("%s commit/tree identity is invalid", scope)
	}
	return nil
}

// validateBuilderInputMaterial 校验输入对象材料和两个编译身份摘要。
func validateBuilderInputMaterial(input SourceBaselineBuildInput) error {
	if !validPortableDigest(input.InputSHA256) {
		return errors.New("source baseline builder input SHA-256 is invalid")
	}
	if input.InputSizeBytes <= 0 || input.InputSizeBytes > SourceBaselineBuilderArtifactMaxBytes {
		return errors.New("source baseline builder input size is invalid")
	}
	return validateBuilderDigests(input.BuilderDigest, input.ToolchainDigest, "source baseline builder")
}

// validateBuilderDigests 校验 builder 和 toolchain 的内容身份摘要。
func validateBuilderDigests(builderDigest, toolchainDigest, scope string) error {
	if !validPortableDigest(builderDigest) || !validPortableDigest(toolchainDigest) {
		return fmt.Errorf("%s builder/toolchain digest is invalid", scope)
	}
	return nil
}

// validateBuilderArtifact 校验 artifact 的内容摘要和字节数。
func validateBuilderArtifact(digest string, size int64) error {
	if !validPortableDigest(digest) {
		return errors.New("source baseline producer receipt artifact SHA-256 is invalid")
	}
	if size <= 0 || size > SourceBaselineBuilderArtifactMaxBytes {
		return errors.New("source baseline producer receipt artifact size is invalid")
	}
	return nil
}

// Validate 拒绝不完整或可变的 producer receipt 字段。
func (receipt SourceBaselineProducerReceipt) Validate() error {
	if receipt.SchemaVersion != SourceBaselineProducerReceiptSchema {
		return errors.New("source baseline producer receipt schema is invalid")
	}
	if receipt.ExecutionProvider != PortableExecutionProvider {
		return errors.New("source baseline producer receipt provider is invalid")
	}
	if err := validateBuilderDigests(receipt.BuilderDigest, receipt.ToolchainDigest, "source baseline producer receipt"); err != nil {
		return err
	}
	if err := validateBuilderIdentifier("repository_id", receipt.RepositoryID); err != nil {
		return err
	}
	format, err := validateBuilderObjectFormat(receipt.ObjectFormat)
	if err != nil {
		return err
	}
	if err := validateBuilderCommitTree(receipt.BaselineCommitSHA, receipt.BaselineTreeSHA, format, "source baseline producer receipt baseline"); err != nil {
		return err
	}
	if err := validateBuilderArtifact(receipt.ArtifactSHA256, receipt.ArtifactSizeBytes); err != nil {
		return err
	}
	expectedCommit, err := DeterministicSourceBaselineCommitSHA(receipt.BaselineTreeSHA, format)
	if err != nil {
		return fmt.Errorf("derive deterministic source baseline receipt commit: %w", err)
	}
	if receipt.BaselineCommitSHA != expectedCommit {
		return errors.New("source baseline producer receipt commit is not deterministic for its tree")
	}
	return nil
}

// Validate 拒绝跨越编排端口前的不完整证据。
func (evidence SourceBaselineBuildEvidence) Validate() error {
	if evidence.Provider != PortableExecutionProvider {
		return errors.New("source baseline builder evidence provider is invalid")
	}
	if err := validateBuilderObjectKey("container_group_id", evidence.ContainerGroupID); err != nil {
		return err
	}
	if err := validateBuilderObjectKey("artifact_key", evidence.ArtifactKey); err != nil {
		return err
	}
	if err := validateBuilderObjectKey("receipt_key", evidence.ReceiptKey); err != nil {
		return err
	}
	if err := evidence.Receipt.Validate(); err != nil {
		return err
	}
	if evidence.Receipt.ExecutionProvider != evidence.Provider {
		return errors.New("source baseline builder evidence and receipt providers differ")
	}
	return nil
}

// ValidateAgainst 证明证据绑定到本次精确提交的输入。
func (evidence SourceBaselineBuildEvidence) ValidateAgainst(input SourceBaselineBuildInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if err := evidence.Validate(); err != nil {
		return err
	}
	receipt := evidence.Receipt
	if receipt.BuilderDigest != input.BuilderDigest || receipt.ToolchainDigest != input.ToolchainDigest ||
		receipt.RepositoryID != input.RepositoryID || receipt.ObjectFormat != input.ObjectFormat ||
		receipt.BaselineTreeSHA != input.TreeSHA {
		return errors.New("source baseline builder evidence does not match input")
	}
	return nil
}

// CanonicalEncode 返回 receipt 唯一允许的紧凑 JSON 表示。
func (receipt SourceBaselineProducerReceipt) CanonicalEncode() ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode source baseline producer receipt: %w", err)
	}
	if len(data) > SourceBaselineBuilderProtocolMaxBytes {
		return nil, errors.New("source baseline producer receipt exceeds 1 MiB")
	}
	return data, nil
}

// DecodeSourceBaselineProducerReceipt 严格解码并校验 canonical receipt。
func DecodeSourceBaselineProducerReceipt(data []byte) (SourceBaselineProducerReceipt, error) {
	var receipt SourceBaselineProducerReceipt
	if err := decodeSourceBaselineBuilderJSON(data, &receipt); err != nil {
		return receipt, fmt.Errorf("decode source baseline producer receipt: %w", err)
	}
	canonical, err := receipt.CanonicalEncode()
	if err != nil {
		return receipt, err
	}
	if !bytes.Equal(canonical, data) {
		return SourceBaselineProducerReceipt{}, errors.New("source baseline producer receipt is not canonical")
	}
	return receipt, nil
}

// EncodeSourceBaselineProducerReceipt 返回 canonical receipt 字节及其域摘要。
func EncodeSourceBaselineProducerReceipt(receipt SourceBaselineProducerReceipt) ([]byte, string, error) {
	data, err := receipt.CanonicalEncode()
	if err != nil {
		return nil, "", err
	}
	digest, err := SourceBaselineProducerReceiptDigest(receipt)
	if err != nil {
		return nil, "", err
	}
	return data, digest, nil
}

// SourceBaselineProducerReceiptDigest 计算 domain-separated receipt 摘要。
func SourceBaselineProducerReceiptDigest(receipt SourceBaselineProducerReceipt) (string, error) {
	data, err := receipt.CanonicalEncode()
	if err != nil {
		return "", err
	}
	return sourceBaselineBuilderDomainDigest(SourceBaselineProducerReceiptDigestDomain, data), nil
}

// CanonicalEncode 返回 builder input 的 canonical 字节。
func (input SourceBaselineBuildInput) CanonicalEncode() ([]byte, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode source baseline builder input: %w", err)
	}
	if len(data) > SourceBaselineBuilderProtocolMaxBytes {
		return nil, errors.New("source baseline builder input exceeds 1 MiB")
	}
	return data, nil
}

// DecodeSourceBaselineBuilderInput 严格解码 builder input。
func DecodeSourceBaselineBuilderInput(data []byte) (SourceBaselineBuildInput, error) {
	var input SourceBaselineBuildInput
	if err := decodeSourceBaselineBuilderJSON(data, &input); err != nil {
		return input, fmt.Errorf("decode source baseline builder input: %w", err)
	}
	canonical, err := input.CanonicalEncode()
	if err != nil {
		return input, err
	}
	if !bytes.Equal(canonical, data) {
		return SourceBaselineBuildInput{}, errors.New("source baseline builder input is not canonical")
	}
	return input, nil
}

// SourceBaselineBuilderInputDigest 计算 domain-separated input 摘要。
func SourceBaselineBuilderInputDigest(input SourceBaselineBuildInput) (string, error) {
	data, err := input.CanonicalEncode()
	if err != nil {
		return "", err
	}
	return sourceBaselineBuilderDomainDigest(SourceBaselineBuilderInputDigestDomain, data), nil
}

// decodeSourceBaselineBuilderJSON 对 builder 文档执行有界 strict JSON 解码。
func decodeSourceBaselineBuilderJSON(data []byte, target any) error {
	if len(data) == 0 || len(data) > SourceBaselineBuilderProtocolMaxBytes {
		return errors.New("source baseline builder JSON size is invalid")
	}
	if err := rejectSourceBaselineBuilderJSON(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode strict JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("source baseline builder JSON has trailing value")
	}
	return nil
}

func rejectSourceBaselineBuilderJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkSourceBaselineBuilderJSONValue(decoder); err != nil {
		return fmt.Errorf("source baseline builder JSON is not strict: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("source baseline builder JSON has trailing value")
	}
	return nil
}

// walkSourceBaselineBuilderJSONValue 递归检查 null、重复键和 JSON 结构。
func walkSourceBaselineBuilderJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is not allowed")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return walkSourceBaselineBuilderJSONObject(decoder)
	case '[':
		return walkSourceBaselineBuilderJSONArray(decoder)
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

// walkSourceBaselineBuilderJSONObject 检查对象键唯一且拒绝 null 值。
func walkSourceBaselineBuilderJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is not a string")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate JSON object key %q", name)
		}
		seen[name] = struct{}{}
		if err := walkSourceBaselineBuilderJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON object is unterminated")
	}
	return nil
}

// walkSourceBaselineBuilderJSONArray 检查数组值并拒绝 null 值。
func walkSourceBaselineBuilderJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkSourceBaselineBuilderJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return errors.New("JSON array is unterminated")
	}
	return nil
}

func sourceBaselineBuilderDomainDigest(domain string, data []byte) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, domain)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func validateBuilderIdentifier(field, value string) error {
	if err := validateBuilderToken(field, value); err != nil {
		return err
	}
	if strings.Contains(value, "://") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") {
		return fmt.Errorf("%s must not be a URL or path", field)
	}
	return nil
}

// validateBuilderObjectKey 校验不含路径语义的 opaque object key。
func validateBuilderObjectKey(field, value string) error {
	if err := validateBuilderIdentifier(field, value); err != nil {
		return err
	}
	if path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.ContainsRune(value, '\\') {
		return fmt.Errorf("%s must be an opaque object key", field)
	}
	if len(value) > 1 && value[1] == ':' {
		return fmt.Errorf("%s must not be a filesystem path", field)
	}
	return nil
}

func validateBuilderToken(field, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is empty or surrounded by whitespace", field)
	}
	if builderTokenIsForbidden(value) {
		return fmt.Errorf("%s contains a forbidden implicit or credential identity", field)
	}
	if builderTokenHasWhitespaceOrControl(value) {
		return fmt.Errorf("%s contains whitespace or control characters", field)
	}
	return nil
}

// builderTokenIsForbidden 判断 token 是否包含隐式根或凭据语义。
func builderTokenIsForbidden(value string) bool {
	lower := strings.ToLower(value)
	return lower == "cwd" || lower == "head" || lower == "default" || strings.Contains(lower, "credential") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token")
}

// builderTokenHasWhitespaceOrControl 判断 token 是否带空白或控制字符。
func builderTokenHasWhitespaceOrControl(value string) bool {
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || unicode.IsSpace(runeValue) {
			return true
		}
	}
	return false
}
