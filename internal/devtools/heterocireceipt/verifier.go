// Package heterocireceipt 严格读取和验证本地异构 CI 能力资格回执。
//
// 本包只消费回执和调用方注入的期望上下文，不写入任何权威存储，也不把
// LOCAL_NON_AUTHORITATIVE / NOT_VERIFIED 证据升级为远程权威结论。
package heterocireceipt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const (
	// StatusNotVerified 保留本地 Darling 资格证据的非权威状态。
	StatusNotVerified = "NOT_VERIFIED"
	maxReceiptBytes   = 64 << 10
)

// Expectations 是调用方注入的回执身份和能力期望。
//
// SourceRevision 与 ImmutableIdentity 使用指针，以区分“明确要求空值”和
// “调用方没有提供约束”；两者为 nil 时构造 verifier 必须失败。
type Expectations struct {
	NodeID             string
	Runtime            heteroci.RuntimeKind
	ProbeIdentity      string
	ProbeBootID        string
	ImageDigest        string
	SourceRevision     *string
	QualificationSuite string
	ImmutableIdentity  *string
}

// Config 是 verifier 的全部显式依赖和外部证据上下文。
//
// Authority、Status 与 ExpiresAtUnixMilli 不属于 heteroci 回执 wire schema，
// 而是由拥有外部上下文的调用方注入；本包只接受本地非权威、NOT_VERIFIED
// 状态，并要求调用方提供绝对过期时间与时钟。
type Config struct {
	Expectations       Expectations
	Authority          string
	Status             string
	ExpiresAtUnixMilli int64
	Now                func() time.Time
}

// Verifier 严格验证一个已解码的资格回执。
type Verifier struct {
	config Config
}

// New 构造带有完整显式期望和时钟依赖的 verifier。
func New(config Config) (*Verifier, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	sourceRevision := *config.Expectations.SourceRevision
	immutableIdentity := *config.Expectations.ImmutableIdentity
	config.Expectations.SourceRevision = &sourceRevision
	config.Expectations.ImmutableIdentity = &immutableIdentity
	return &Verifier{config: config}, nil
}

// Decode 从 reader 严格读取一个能力资格回执。
//
// 除已知字段集合外，缺失、null、重复对象键、非法 JSON 和尾随 JSON 值
// 均立即失败。尾随空白属于 JSON 文法的一部分，可以正常保留。
func Decode(reader io.Reader) (heteroci.CapabilityQualificationReceipt, error) {
	if reader == nil {
		return heteroci.CapabilityQualificationReceipt{}, errors.New("heterogeneous CI qualification receipt reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxReceiptBytes+1))
	if err != nil {
		return heteroci.CapabilityQualificationReceipt{}, fmt.Errorf("read heterogeneous CI qualification receipt: %w", err)
	}
	if len(data) > maxReceiptBytes {
		return heteroci.CapabilityQualificationReceipt{}, fmt.Errorf("heterogeneous CI qualification receipt exceeds %d-byte limit", maxReceiptBytes)
	}
	if !utf8.Valid(data) {
		return heteroci.CapabilityQualificationReceipt{}, errors.New("heterogeneous CI qualification receipt is not valid UTF-8")
	}
	if err := validateReceiptJSON(data); err != nil {
		return heteroci.CapabilityQualificationReceipt{}, err
	}
	if err := validateReceiptFields(data); err != nil {
		return heteroci.CapabilityQualificationReceipt{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt heteroci.CapabilityQualificationReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return heteroci.CapabilityQualificationReceipt{}, fmt.Errorf("decode heterogeneous CI qualification receipt: %w", err)
	}
	if err := validateDecodedReceipt(receipt); err != nil {
		return heteroci.CapabilityQualificationReceipt{}, err
	}
	return receipt, nil
}

// Decode 严格读取并验证 verifier 所需的一个能力资格回执。
func (v *Verifier) Decode(reader io.Reader) (heteroci.CapabilityQualificationReceipt, error) {
	if v == nil {
		return heteroci.CapabilityQualificationReceipt{}, errors.New("heterogeneous CI qualification receipt verifier is nil")
	}
	receipt, err := Decode(reader)
	if err != nil {
		return heteroci.CapabilityQualificationReceipt{}, err
	}
	if err := v.Verify(receipt); err != nil {
		return heteroci.CapabilityQualificationReceipt{}, err
	}
	return receipt, nil
}

// Verify 校验 schema、摘要、身份、非权威状态和显式时间窗口。
func (v *Verifier) Verify(receipt heteroci.CapabilityQualificationReceipt) error {
	if v == nil {
		return errors.New("heterogeneous CI qualification receipt verifier is nil")
	}
	if receipt.SchemaVersion != heteroci.CapabilityQualificationSchemaVersion {
		return fmt.Errorf("heterogeneous CI qualification receipt schema_version %q is invalid", receipt.SchemaVersion)
	}
	if err := verifyReceiptIdentity(receipt, v.config.Expectations); err != nil {
		return err
	}
	if err := verifyReceiptTiming(receipt, v.config); err != nil {
		return err
	}
	if receipt.Digest == "" || receipt.Digest != receipt.CanonicalDigest() {
		return errors.New("heterogeneous CI qualification receipt canonical digest is invalid")
	}
	return nil
}

// validateConfig 拒绝缺少显式身份、状态、时钟或过期边界的 verifier。
func validateConfig(config Config) error {
	if err := validateExpectations(config.Expectations); err != nil {
		return err
	}
	return validateEvidenceConfig(config)
}

// validateExpectations 校验调用方注入的完整身份期望。
func validateExpectations(expectations Expectations) error {
	if err := validateRequiredText("node_id", expectations.NodeID); err != nil {
		return err
	}
	if !isCanonicalRuntime(expectations.Runtime) {
		return fmt.Errorf("heterogeneous CI qualification receipt runtime %q is invalid", expectations.Runtime)
	}
	if err := validateRequiredText("probe_identity", expectations.ProbeIdentity); err != nil {
		return err
	}
	if err := validateRequiredText("probe_boot_id", expectations.ProbeBootID); err != nil {
		return err
	}
	if err := validateDigest("image_digest", expectations.ImageDigest); err != nil {
		return err
	}
	if expectations.SourceRevision == nil {
		return errors.New("heterogeneous CI qualification receipt source_revision expectation is required")
	}
	if err := validateOptionalText("source_revision", *expectations.SourceRevision); err != nil {
		return err
	}
	if err := validateRequiredText("qualification_suite", expectations.QualificationSuite); err != nil {
		return err
	}
	if expectations.ImmutableIdentity == nil {
		return errors.New("heterogeneous CI qualification receipt immutable_identity expectation is required")
	}
	return validateOptionalText("immutable_identity", *expectations.ImmutableIdentity)
}

// validateEvidenceConfig 校验外部证据必须保持本地非权威且未验证。
func validateEvidenceConfig(config Config) error {
	if config.Authority != heteroci.AuthorityLocalNonAuthoritative {
		return fmt.Errorf("heterogeneous CI qualification receipt authority %q is not local non-authoritative", config.Authority)
	}
	if config.Status != StatusNotVerified {
		return fmt.Errorf("heterogeneous CI qualification receipt status %q is not %s", config.Status, StatusNotVerified)
	}
	if config.ExpiresAtUnixMilli <= 0 {
		return errors.New("heterogeneous CI qualification receipt expiry must be positive")
	}
	if config.Now == nil {
		return errors.New("heterogeneous CI qualification receipt verifier clock is required")
	}
	return nil
}

// verifyReceiptIdentity 将回执的每个身份字段与注入期望逐一绑定。
func verifyReceiptIdentity(receipt heteroci.CapabilityQualificationReceipt, expectations Expectations) error {
	if err := verifyNodeIdentity(receipt, expectations); err != nil {
		return err
	}
	if err := verifyProbeIdentity(receipt, expectations); err != nil {
		return err
	}
	return verifyCapabilityIdentity(receipt, expectations)
}

// verifyNodeIdentity 校验回执节点和运行时绑定。
func verifyNodeIdentity(receipt heteroci.CapabilityQualificationReceipt, expectations Expectations) error {
	if receipt.NodeID != expectations.NodeID {
		return fmt.Errorf("heterogeneous CI qualification receipt node_id %q does not match expectation", receipt.NodeID)
	}
	if receipt.Runtime != expectations.Runtime {
		return fmt.Errorf("heterogeneous CI qualification receipt runtime %q does not match expectation", receipt.Runtime)
	}
	return nil
}

// verifyProbeIdentity 校验探针身份和启动代次绑定。
func verifyProbeIdentity(receipt heteroci.CapabilityQualificationReceipt, expectations Expectations) error {
	if receipt.ProbeIdentity != expectations.ProbeIdentity {
		return fmt.Errorf("heterogeneous CI qualification receipt probe_identity %q does not match expectation", receipt.ProbeIdentity)
	}
	if receipt.ProbeBootID != expectations.ProbeBootID {
		return fmt.Errorf("heterogeneous CI qualification receipt probe_boot_id %q does not match expectation", receipt.ProbeBootID)
	}
	return nil
}

// verifyCapabilityIdentity 校验镜像、来源、套件和不可变身份绑定。
func verifyCapabilityIdentity(receipt heteroci.CapabilityQualificationReceipt, expectations Expectations) error {
	if receipt.ImageDigest != expectations.ImageDigest {
		return fmt.Errorf("heterogeneous CI qualification receipt image_digest %q does not match expectation", receipt.ImageDigest)
	}
	if expectations.SourceRevision == nil || receipt.SourceRevision != *expectations.SourceRevision {
		return fmt.Errorf("heterogeneous CI qualification receipt source_revision %q does not match expectation", receipt.SourceRevision)
	}
	if receipt.QualificationSuite != expectations.QualificationSuite {
		return fmt.Errorf("heterogeneous CI qualification receipt qualification_suite %q does not match expectation", receipt.QualificationSuite)
	}
	if expectations.ImmutableIdentity == nil || receipt.ImmutableIdentity != *expectations.ImmutableIdentity {
		return fmt.Errorf("heterogeneous CI qualification receipt immutable_identity %q does not match expectation", receipt.ImmutableIdentity)
	}
	return nil
}

// verifyReceiptTiming 校验注入时钟、完成时间和绝对过期窗口。
func verifyReceiptTiming(receipt heteroci.CapabilityQualificationReceipt, config Config) error {
	now := config.Now()
	if now.IsZero() {
		return errors.New("heterogeneous CI qualification receipt verifier clock returned zero time")
	}
	nowUnixMilli := now.UnixMilli()
	if nowUnixMilli <= 0 {
		return errors.New("heterogeneous CI qualification receipt verifier clock must be after Unix epoch")
	}
	if nowUnixMilli >= config.ExpiresAtUnixMilli {
		return errors.New("heterogeneous CI qualification receipt is expired")
	}
	if receipt.CompletedAtUnixMilli <= 0 {
		return errors.New("heterogeneous CI qualification receipt completion time must be positive")
	}
	if receipt.CompletedAtUnixMilli > nowUnixMilli {
		return errors.New("heterogeneous CI qualification receipt completion time is in the future")
	}
	if receipt.CompletedAtUnixMilli >= config.ExpiresAtUnixMilli {
		return errors.New("heterogeneous CI qualification receipt completion time is outside expiry")
	}
	return nil
}

// validateDecodedReceipt 校验解码后可独立证明的 schema 与 canonical digest。
func validateDecodedReceipt(receipt heteroci.CapabilityQualificationReceipt) error {
	if receipt.SchemaVersion != heteroci.CapabilityQualificationSchemaVersion {
		return fmt.Errorf("heterogeneous CI qualification receipt schema_version %q is invalid", receipt.SchemaVersion)
	}
	if receipt.Digest == "" || receipt.Digest != receipt.CanonicalDigest() {
		return errors.New("heterogeneous CI qualification receipt canonical digest is invalid")
	}
	return nil
}

// validateRequiredText 校验必须存在且不含空白边界或控制字符的文本。
func validateRequiredText(name, value string) error {
	if value == "" {
		return fmt.Errorf("heterogeneous CI qualification receipt %s is required", name)
	}
	if value != strings.TrimSpace(value) || hasControl(value) {
		return fmt.Errorf("heterogeneous CI qualification receipt %s is not canonical", name)
	}
	return nil
}

// validateOptionalText 校验允许调用方显式注入空值的文本。
func validateOptionalText(name, value string) error {
	if value != strings.TrimSpace(value) || hasControl(value) {
		return fmt.Errorf("heterogeneous CI qualification receipt %s is not canonical", name)
	}
	return nil
}

// validateDigest 校验不可变的小写 SHA-256 摘要。
func validateDigest(name, value string) error {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return fmt.Errorf("heterogeneous CI qualification receipt %s is not a sha256 digest", name)
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return fmt.Errorf("heterogeneous CI qualification receipt %s is not a lowercase sha256 digest", name)
		}
	}
	return nil
}

// validateReceiptFields 校验回执根对象的字段集合和必填字段。
func validateReceiptFields(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("heterogeneous CI qualification receipt root must be an object: %w", err)
	}
	allowedFields := receiptFieldSet()
	for field := range fields {
		if _, ok := allowedFields[field]; !ok {
			return fmt.Errorf("heterogeneous CI qualification receipt field %q is unknown", field)
		}
	}
	for _, field := range requiredReceiptFieldNames() {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("heterogeneous CI qualification receipt field %q is required", field)
		}
	}
	return nil
}

// receiptFieldSet 返回严格 wire schema 允许的根字段集合。
func receiptFieldSet() map[string]struct{} {
	return map[string]struct{}{
		"schema_version":          {},
		"node_id":                 {},
		"runtime":                 {},
		"probe_identity":          {},
		"probe_boot_id":           {},
		"image_digest":            {},
		"source_revision":         {},
		"qualification_suite":     {},
		"immutable_identity":      {},
		"completed_at_unix_milli": {},
		"digest":                  {},
	}
}

// requiredReceiptFieldNames 返回严格 wire schema 的必填根字段。
func requiredReceiptFieldNames() []string {
	return []string{
		"schema_version",
		"node_id",
		"runtime",
		"probe_identity",
		"probe_boot_id",
		"image_digest",
		"source_revision",
		"qualification_suite",
		"immutable_identity",
		"completed_at_unix_milli",
		"digest",
	}
}

// validateReceiptJSON 递归校验 JSON 的 null、重复键、语法和尾随值边界。
func validateReceiptJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSONValue(decoder); err != nil {
		return fmt.Errorf("validate heterogeneous CI qualification receipt JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("heterogeneous CI qualification receipt contains trailing JSON values")
		}
		return fmt.Errorf("decode heterogeneous CI qualification receipt trailer: %w", err)
	}
	return nil
}

// walkJSONValue 递归拒绝 JSON null 并检查每层容器的结构。
func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("heterogeneous CI qualification receipt must not contain null")
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			return walkJSONObject(decoder)
		case '[':
			return walkJSONArray(decoder)
		default:
			return errors.New("heterogeneous CI qualification receipt has an unexpected delimiter")
		}
	}
	return nil
}

// walkJSONObject 检查对象键唯一性并递归处理对象值。
func walkJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("heterogeneous CI qualification receipt object key is not a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("heterogeneous CI qualification receipt object key %q is duplicated", key)
		}
		seen[key] = struct{}{}
		if err := walkJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectClosingDelimiter(decoder, '}')
}

// walkJSONArray 递归处理数组值并检查数组闭合分隔符。
func walkJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectClosingDelimiter(decoder, ']')
}

// expectClosingDelimiter 确认 JSON 容器以预期分隔符闭合。
func expectClosingDelimiter(decoder *json.Decoder, expected json.Delim) error {
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != expected {
		return errors.New("heterogeneous CI qualification receipt container is not closed")
	}
	return nil
}

// isCanonicalRuntime 判断运行时标识是否属于 heteroci 已注册集合。
func isCanonicalRuntime(runtime heteroci.RuntimeKind) bool {
	switch runtime {
	case heteroci.RuntimeUbuntuContainer, heteroci.RuntimeUbuntuWineContainer:
		return true
	default:
		return false
	}
}

// hasControl 判断文本是否含 JSON 字符串之外的控制字符。
func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
