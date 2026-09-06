// Package orchestrationcommand 拥有统一命令的持久意图；创建意图不等于接受或授权派发。
package orchestrationcommand

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidIntent       = errors.New("command intent is invalid")
	ErrIdempotencyConflict = errors.New("command intent idempotency conflict")
	ErrCommandIDConflict   = errors.New("command intent id is already in use")
	ErrNotFound            = errors.New("command intent not found")
)

// Intent 只持久化摘要和已有内容/身份引用；不包含明文输入或可执行命令。
// Namespace 与摘要必须由可信上游绑定 caller/source/fence；本存储不签发授权。
type Intent struct {
	CommandID            string `json:"command_id"`
	CommandKind          string `json:"command_kind"`
	CallerPrincipalRef   string `json:"caller_principal_ref"`
	ScopeRef             string `json:"scope_ref"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	RequestDigest        string `json:"request_digest"`
	DigestKeyID          string `json:"digest_key_id"`
	RequestContentRef    string `json:"request_content_ref"`
	SourceFenceRef       string `json:"source_fence_ref"`
	TargetFenceRef       string `json:"target_fence_ref"`
}

// Record 是不可变意图的读取结果，不携带 accepted、dispatch 或 provider 已执行标记。
type Record struct {
	Intent         Intent    `json:"intent"`
	SchemaVersion  int64     `json:"schema_version"`
	CommandVersion int64     `json:"command_version"`
	RecordedAt     time.Time `json:"recorded_at"`
}

// Key 只查询原 namespace/key-digest，不把新请求或新代际混入旧命令。
type Key struct {
	Namespace string `json:"namespace"`
	Digest    string `json:"digest"`
}

// Validate 拒绝空白归一化、未知动作、无效摘要及超长引用，不替上游补默认身份。
func (i Intent) Validate() error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"command_id", i.CommandID, 128}, {"digest_key_id", i.DigestKeyID, 128},
		{"caller_principal_ref", i.CallerPrincipalRef, 512}, {"scope_ref", i.ScopeRef, 512},
		{"request_content_ref", i.RequestContentRef, 512},
		{"source_fence_ref", i.SourceFenceRef, 512}, {"target_fence_ref", i.TargetFenceRef, 512},
	} {
		if err := validateText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	if err := (Key{Namespace: i.IdempotencyNamespace, Digest: i.IdempotencyKeyDigest}).Validate(); err != nil {
		return err
	}
	if err := validateDigest("request_digest", i.RequestDigest); err != nil {
		return err
	}
	switch i.CommandKind {
	case "launch_agent", "fork_agent", "send_message", "interrupt_agent", "stop_agent", "recover_agent", "room_broadcast", "room_round":
		return nil
	default:
		return fmt.Errorf("%w: command_kind is unsupported", ErrInvalidIntent)
	}
}

// Validate 验证查询键的完整性；空 namespace 或非规范摘要不能查询其他意图。
func (k Key) Validate() error {
	if err := validateText("idempotency_namespace", k.Namespace, 256); err != nil {
		return err
	}
	return validateDigest("idempotency_key_digest", k.Digest)
}

// validateText 保留调用方原始身份，拒绝空白别名、非法编码和超出字节上限的文本。
func validateText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidIntent, name)
	}
	return nil
}

// validateDigest 只接受固定长度小写十六进制，算法和密钥的真实性仍由上游 owner 验证。
func validateDigest(name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase 256-bit digest", ErrInvalidIntent, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s is not hexadecimal", ErrInvalidIntent, name)
	}
	return nil
}
