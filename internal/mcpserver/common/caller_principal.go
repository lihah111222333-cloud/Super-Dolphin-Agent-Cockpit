package common

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalidCallerPrincipal 表示 CallerPrincipal 缺少受验证的身份边界或已过期。
var ErrInvalidCallerPrincipal = errors.New("invalid caller principal")

// ErrMissingCallerPrincipal 表示当前 context 没有 transport/auth owner 写入的 principal。
var ErrMissingCallerPrincipal = errors.New("caller principal missing")

const callerPrincipalTextLimit = 512

// CallerPrincipal 是 transport/auth owner 产生的不可变调用者快照。
//
// 该值只描述已验证的 principal ref、scope、server profile、auth context 和
// expiry；它不解析 MCP metadata，也不由 agent_id、thread id 或工具参数推导。
// WithCallerPrincipal 写入的是值副本，因此调用方之后修改原值不会改变 context
// 中的快照。该类型不是认证器、grant 或 capability lease。
type CallerPrincipal struct {
	PrincipalRef string
	ScopeRef     string
	Profile      string
	AuthContext  string
	ExpiresAt    time.Time
}

// CallerPrincipalIssuer 为每个新 transport session 签发短期调用者快照。
// 实现必须绑定服务端认证事实，不能读取或相信客户端 metadata。
type CallerPrincipalIssuer interface {
	Validate() error
	Issue() (CallerPrincipal, error)
}

func callerPrincipalIssuerIsNil(issuer CallerPrincipalIssuer) bool {
	if issuer == nil {
		return true
	}
	value := reflect.ValueOf(issuer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// IsZero 判断是否未配置调用者 principal；零值是 legacy HTTP 调用方的显式兼容模式。
func (p CallerPrincipal) IsZero() bool {
	return p == (CallerPrincipal{})
}

// NewCallerPrincipal 构造并严格校验一个 principal 快照。
// 所有文本必须是有界 canonical UTF-8；expiry 必须是 UTC 且晚于当前时间。
func NewCallerPrincipal(principalRef, scopeRef, profile, authContext string, expiresAt time.Time) (CallerPrincipal, error) {
	principal := CallerPrincipal{
		PrincipalRef: principalRef,
		ScopeRef:     scopeRef,
		Profile:      profile,
		AuthContext:  authContext,
		ExpiresAt:    expiresAt,
	}
	if err := principal.Validate(); err != nil {
		return CallerPrincipal{}, err
	}
	return principal, nil
}

// Validate 检查快照相对于当前 UTC 时间仍然有效。
func (p CallerPrincipal) Validate() error {
	return p.ValidateAt(time.Now().UTC())
}

// ValidateAt 用指定观察时间检查快照，供授权 owner 和确定性测试复用。
func (p CallerPrincipal) ValidateAt(now time.Time) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "principal_ref", value: p.PrincipalRef},
		{name: "scope_ref", value: p.ScopeRef},
		{name: "profile", value: p.Profile},
		{name: "auth_context", value: p.AuthContext},
	}
	for _, field := range fields {
		if err := validateCallerPrincipalText(field.name, field.value); err != nil {
			return err
		}
	}
	if p.ExpiresAt.IsZero() || p.ExpiresAt.Location() != time.UTC {
		return fmt.Errorf("%w: expiry must be non-zero UTC", ErrInvalidCallerPrincipal)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: validation time must be non-zero", ErrInvalidCallerPrincipal)
	}
	if !p.ExpiresAt.After(now) {
		return fmt.Errorf("%w: expiry must be in the future", ErrInvalidCallerPrincipal)
	}
	return nil
}

// BindCallerPrincipal 校验后把 principal 值快照绑定到 context。
// 它是无 panic 的显式入口；未经认证 owner 验证的输入不会进入 context。
func BindCallerPrincipal(ctx context.Context, principal CallerPrincipal) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidCallerPrincipal)
	}
	if err := principal.Validate(); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, callerPrincipalContextKey{}, principal), nil
}

// WithCallerPrincipal 将已验证 principal 的值副本写入 context；非法值返回错误，
// 不用 panic 或匿名 context 作为隐式兜底。
func WithCallerPrincipal(ctx context.Context, principal CallerPrincipal) (context.Context, error) {
	return BindCallerPrincipal(ctx, principal)
}

// CallerPrincipalFromContext 读取未过期的 principal 快照；缺失、篡改或过期均返回 false。
func CallerPrincipalFromContext(ctx context.Context) (CallerPrincipal, bool) {
	if ctx == nil {
		return CallerPrincipal{}, false
	}
	principal, ok := ctx.Value(callerPrincipalContextKey{}).(CallerPrincipal)
	if !ok || principal.Validate() != nil {
		return CallerPrincipal{}, false
	}
	return principal, true
}

// RequireCallerPrincipal 是授权入口的 fail-fast guard，不把 context 缺失降级成匿名主体。
func RequireCallerPrincipal(ctx context.Context) (CallerPrincipal, error) {
	if ctx == nil {
		return CallerPrincipal{}, ErrMissingCallerPrincipal
	}
	principal, ok := ctx.Value(callerPrincipalContextKey{}).(CallerPrincipal)
	if !ok {
		return CallerPrincipal{}, ErrMissingCallerPrincipal
	}
	if err := principal.Validate(); err != nil {
		return CallerPrincipal{}, fmt.Errorf("%w: %w", ErrInvalidCallerPrincipal, err)
	}
	return principal, nil
}

// callerPrincipalContextKey 使用私有 key，阻断外部包伪造 context value。
type callerPrincipalContextKey struct{}

// validateCallerPrincipalText 检查 principal 快照中的 canonical 文本边界。
func validateCallerPrincipalText(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > callerPrincipalTextLimit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidCallerPrincipal, name)
	}
	return nil
}
