package appupdatefailure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	Filename     = "pre-journal-failure.json"
	LockFilename = ".pre-journal-failure.lock"
	Version      = 2
	maxSize      = 4096
	statePending = "pending"
	stateFailure = "failure"
)

var (
	ErrUnsupported       = errors.New("app update pre-journal sidecar is unsupported on this platform")
	errGenerationMissing = errors.New("app update pre-journal generation does not match an active attempt")
	generationPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	recordFields         = [...]string{"version", "generation", "state", "code", "retryable", "action", "transaction_id"}
)

type record struct {
	Version       int    `json:"version"`
	Generation    string `json:"generation"`
	State         string `json:"state"`
	Code          string `json:"code"`
	Retryable     bool   `json:"retryable"`
	Action        string `json:"action"`
	TransactionID string `json:"transaction_id"`
}

func pendingRecord(generation string) record {
	return record{Version: Version, Generation: generation, State: statePending}
}

func failureRecord(generation string, failure contract.RecoveryFailure) record {
	return record{
		Version:       Version,
		Generation:    generation,
		State:         stateFailure,
		Code:          failure.Code,
		Retryable:     failure.Retryable,
		Action:        string(failure.Action),
		TransactionID: failure.TransactionID,
	}
}

// validateRecord 校验版本、代际、状态和恢复字段的完整一致性。
func validateRecord(value record) error {
	if value.Version != Version || validateGeneration(value.Generation) != nil || value.TransactionID != "" {
		return errors.New("app update failure sidecar record is invalid")
	}
	if value.State == statePending {
		if value.Code != "" || value.Retryable || value.Action != "" {
			return errors.New("app update failure sidecar pending record is inconsistent")
		}
		return nil
	}
	if value.State != stateFailure {
		return errors.New("app update failure sidecar state is unsupported")
	}
	return validateFailure(contract.RecoveryFailure{
		Code:          value.Code,
		Retryable:     value.Retryable,
		Action:        contract.RecoveryAction(value.Action),
		TransactionID: value.TransactionID,
	})
}

func encodeRecord(value record) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode app update failure sidecar: %w", err)
	}
	return raw, nil
}

// decodeRecord 以 token 级解析拒绝重复键、嵌套值与尾随 JSON。
func decodeRecord(raw []byte) (record, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return record{}, errors.New("app update failure sidecar must be a flat JSON object")
	}
	value, seen, err := decodeRecordObject(decoder)
	if err != nil {
		return record{}, err
	}
	if err := requireRecordFields(seen); err != nil {
		return record{}, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return record{}, err
	}
	if err := validateRecord(value); err != nil {
		return record{}, err
	}
	return value, nil
}

// decodeRecordObject 解析顶层对象并对每个字段执行唯一性检查。
func decodeRecordObject(decoder *json.Decoder) (record, map[string]struct{}, error) {
	value := record{}
	seen := make(map[string]struct{}, len(recordFields))
	for decoder.More() {
		field, err := nextUniqueField(decoder, seen)
		if err != nil {
			return record{}, nil, err
		}
		if err := decodeRecordField(decoder, field, &value); err != nil {
			return record{}, nil, errors.New("app update failure sidecar field is malformed")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return record{}, nil, errors.New("app update failure sidecar object is not closed")
	}
	return value, seen, nil
}

func nextUniqueField(decoder *json.Decoder, seen map[string]struct{}) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", errors.New("app update failure sidecar field name is malformed")
	}
	field, ok := token.(string)
	if !ok {
		return "", errors.New("app update failure sidecar field name is not a string")
	}
	if _, duplicate := seen[field]; duplicate {
		return "", errors.New("app update failure sidecar contains a duplicate field")
	}
	if !knownRecordField(field) {
		return "", errors.New("app update failure sidecar contains an unknown field")
	}
	seen[field] = struct{}{}
	return field, nil
}

// decodeRecordField 按固定原始类型解码一个已知字段，从而拒绝嵌套值。
func decodeRecordField(decoder *json.Decoder, field string, value *record) error {
	switch field {
	case "version":
		return decoder.Decode(&value.Version)
	case "generation":
		return decoder.Decode(&value.Generation)
	case "state":
		return decoder.Decode(&value.State)
	case "code":
		return decoder.Decode(&value.Code)
	case "retryable":
		return decoder.Decode(&value.Retryable)
	case "action":
		return decoder.Decode(&value.Action)
	case "transaction_id":
		return decoder.Decode(&value.TransactionID)
	default:
		return errors.New("app update failure sidecar field is unknown")
	}
}

func requireRecordFields(seen map[string]struct{}) error {
	if len(seen) != len(recordFields) {
		return errors.New("app update failure sidecar schema is incomplete")
	}
	for _, field := range recordFields {
		if _, ok := seen[field]; !ok {
			return errors.New("app update failure sidecar schema is incomplete")
		}
	}
	return nil
}

func knownRecordField(candidate string) bool {
	for _, field := range recordFields {
		if candidate == field {
			return true
		}
	}
	return false
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("app update failure sidecar contains trailing data")
	}
	return nil
}

// Error 仅携带 registry 校验后的恢复元数据跨越 appupdate RPC 边界。
type Error struct{ failure contract.RecoveryFailure }

// Error 返回固定分类文本，避免泄露 updater 原始输出。
func (Error) Error() string { return "app update pre-journal recovery action is required" }

// RecoveryFailure 返回最小恢复元数据。
func (err Error) RecoveryFailure() contract.RecoveryFailure { return err.failure }

// NewError 从合法 sidecar 元数据构造恢复错误。
func NewError(failure contract.RecoveryFailure) (Error, error) {
	if err := validateFailure(failure); err != nil {
		return Error{}, err
	}
	return Error{failure: failure}, nil
}

// CanonicalPath 只接受干净绝对 StageDir 并派生固定 sidecar 路径。
func CanonicalPath(stageDir string) (string, error) {
	if stageDir == "" || stageDir == "/" || !filepath.IsAbs(stageDir) || filepath.Clean(stageDir) != stageDir {
		return "", errors.New("app update stage dir must be a clean absolute non-root path")
	}
	return filepath.Join(stageDir, Filename), nil
}

func validateGeneration(generation string) error {
	if !generationPattern.MatchString(generation) {
		return errors.New("app update pre-journal generation is invalid")
	}
	return nil
}

// ValidateGeneration 校验跨进程传递的代际标识格式，不访问 sidecar。
func ValidateGeneration(generation string) error {
	return validateGeneration(generation)
}

// FailCode 从唯一恢复 registry 构造安全字段后执行 matching-generation 失败迁移。
func FailCode(stageDir string, generation string, code string) error {
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		return errors.New("app update pre-journal recovery code is not registered")
	}
	return Fail(stageDir, generation, failure)
}

// validateFailure 仅接受 registry 中两类无事务恢复信号。
func validateFailure(failure contract.RecoveryFailure) error {
	if failure.TransactionID != "" || (failure.Code != "UPDATE_SIGNATURE_INVALID" && failure.Code != "UPDATE_INTEGRITY_INVALID") {
		return errors.New("app update failure sidecar recovery metadata is invalid")
	}
	want, ok := contract.RecoveryFailureForCode(failure.Code, "")
	if !ok || want != failure {
		return errors.New("app update failure sidecar recovery metadata is inconsistent")
	}
	return nil
}
