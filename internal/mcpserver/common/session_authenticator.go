package common

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

const sessionCredentialRedaction = "[REDACTED]"

const (
	minSessionCredentialBytes = 32
	maxSessionCredentialBytes = 4096
)

// ErrTaskSessionIdentityInvalid 表示 authenticator 返回的 task 身份对不完整或无效。
var ErrTaskSessionIdentityInvalid = errors.New("task session identity is invalid")

// SessionCredentialDigest 是 transport 对原始 bearer credential 计算的可比较 SHA-256 摘要。
//
// 该类型的全部字符串和 JSON 表示均固定脱敏；registry 可比较或索引它，但不能从中恢复
// 原始 credential。
type SessionCredentialDigest [sha256.Size]byte

// DigestSessionCredential 严格校验一个裸 bearer credential，并返回其 SHA-256 摘要。
func DigestSessionCredential(credential string) (SessionCredentialDigest, error) {
	if !validSessionCredential(credential) {
		return SessionCredentialDigest{}, errors.New("session credential must use canonical bearer token syntax")
	}
	return sha256.Sum256([]byte(credential)), nil
}

// String 返回固定脱敏文本，禁止通过默认格式输出 digest。
func (SessionCredentialDigest) String() string { return sessionCredentialRedaction }

// GoString 返回固定脱敏文本，禁止 %#v 暴露 digest 的底层字节。
func (SessionCredentialDigest) GoString() string { return sessionCredentialRedaction }

// Format 忽略格式动词并写出固定脱敏文本，覆盖 %x、%v、%s 等表示。
func (SessionCredentialDigest) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, sessionCredentialRedaction)
}

// MarshalJSON 返回固定脱敏字符串，禁止序列化 digest 的底层数组。
func (SessionCredentialDigest) MarshalJSON() ([]byte, error) {
	return json.Marshal(sessionCredentialRedaction)
}

// MarshalText 返回固定脱敏文本，禁止 encoding.TextMarshaler 路径暴露底层数组。
func (SessionCredentialDigest) MarshalText() ([]byte, error) {
	return []byte(sessionCredentialRedaction), nil
}

// TaskSessionIdentity 是 authenticator 对 credential 当前状态解析出的 transport 身份对。
// Principal 和 CallerAttribution 必须同时存在；operator 模式不会构造该值。
type TaskSessionIdentity struct {
	Principal   CallerPrincipal
	Attribution CallerAttribution
}

// Validate 严格复验 principal 与 attribution，任一无效都阻止 session 创建或复用。
func (i TaskSessionIdentity) Validate() error {
	if err := i.Principal.Validate(); err != nil {
		return fmt.Errorf("%w: principal: %w", ErrTaskSessionIdentityInvalid, err)
	}
	if err := i.Attribution.Validate(); err != nil {
		return fmt.Errorf("%w: attribution: %w", ErrTaskSessionIdentityInvalid, err)
	}
	return nil
}

// Equal 逐字段比较两个身份对；expiry 使用时间值语义，不能比较 time.Location 指针。
func (i TaskSessionIdentity) Equal(other TaskSessionIdentity) bool {
	return i.Principal.PrincipalRef == other.Principal.PrincipalRef &&
		i.Principal.ScopeRef == other.Principal.ScopeRef &&
		i.Principal.Profile == other.Principal.Profile &&
		i.Principal.AuthContext == other.Principal.AuthContext &&
		i.Principal.ExpiresAt.Equal(other.Principal.ExpiresAt) &&
		i.Attribution == other.Attribution
}

// BindTaskSessionIdentity 在写入前复验完整身份对和两个私有 context key，确保要么同时
// 绑定 principal/attribution，要么原样返回调用方 context。
func BindTaskSessionIdentity(ctx context.Context, identity TaskSessionIdentity) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrTaskSessionIdentityInvalid)
	}
	if err := identity.Validate(); err != nil {
		return ctx, err
	}
	if ctx.Value(callerPrincipalContextKey{}) != nil || ctx.Value(callerAttributionContextKey{}) != nil {
		return ctx, fmt.Errorf("%w: transport identity is already bound", ErrTaskSessionIdentityInvalid)
	}
	bound := context.WithValue(ctx, callerPrincipalContextKey{}, identity.Principal)
	bound = context.WithValue(bound, callerAttributionContextKey{}, identity.Attribution)
	return bound, nil
}

// TaskSessionAuthenticator 在每个 task HTTP 请求边界复验 credential 的当前身份与撤销状态。
// 实现不得依赖客户端 MCP metadata；transport 只向它传递不可逆 digest。
type TaskSessionAuthenticator interface {
	Validate() error
	Authenticate(context.Context, SessionCredentialDigest) (TaskSessionIdentity, error)
}

// taskSessionAuthenticatorIsNil 同时识别 nil interface 与 typed nil，避免 profile 回退到其他模式。
func taskSessionAuthenticatorIsNil(authenticator TaskSessionAuthenticator) bool {
	if authenticator == nil {
		return true
	}
	value := reflect.ValueOf(authenticator)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// validSessionCredential 校验 RFC 6750 bearer 常用 token68 字符集，并拒绝空白与非 ASCII。
func validSessionCredential(credential string) bool {
	if len(credential) < minSessionCredentialBytes || len(credential) > maxSessionCredentialBytes {
		return false
	}
	base := credential
	if paddingIndex := strings.IndexByte(credential, '='); paddingIndex >= 0 {
		if paddingIndex == 0 || strings.Trim(credential[paddingIndex:], "=") != "" {
			return false
		}
		base = credential[:paddingIndex]
	}
	for index := range len(base) {
		if !validSessionCredentialBaseByte(base[index]) {
			return false
		}
	}
	return true
}

// validSessionCredentialBaseByte 判断非 padding 字节是否属于 token68 基础字符集。
func validSessionCredentialBaseByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		strings.ContainsRune("-._~+/", rune(value))
}
