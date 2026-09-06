package standalone

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const (
	// ConfiguredPrincipalProfile identifies the local configured operator profile.
	ConfiguredPrincipalProfile = "standalone_http_local_v1"
	configuredPrincipalTTL     = 15 * time.Minute
)

// ConfiguredPrincipalIssuer 将启动配置中的 canonical refs 绑定到本次 server boot。
// 它不读取 bearer、不解析请求，也不从路径、agent 或 thread 标识派生主体。
type ConfiguredPrincipalIssuer struct {
	principalRef string
	scopeRef     string
	authContext  string
}

var _ common.CallerPrincipalIssuer = (*ConfiguredPrincipalIssuer)(nil)

// NewConfiguredPrincipalIssuer 在任何 credential、lock、DB 或 Fx 副作用前校验身份配置。
func NewConfiguredPrincipalIssuer(cfg Config) (*ConfiguredPrincipalIssuer, error) {
	if err := cfg.ValidateServeIdentity(); err != nil {
		return nil, err
	}
	authContext, err := newPrincipalAuthContext()
	if err != nil {
		return nil, err
	}
	return &ConfiguredPrincipalIssuer{principalRef: cfg.PrincipalRef, scopeRef: cfg.ScopeRef, authContext: authContext}, nil
}

// newPrincipalAuthContext 为本次 server boot 生成不可预测且可校验失败的认证上下文。
func newPrincipalAuthContext() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate configured principal auth context: %w", err)
	}
	return "orch-auth:" + hex.EncodeToString(raw[:]), nil
}

// Validate 检查 issuer 已绑定完整配置和本次 server boot 的非秘密 auth context。
func (i *ConfiguredPrincipalIssuer) Validate() error {
	if i == nil || i.authContext == "" {
		return errors.New("configured principal issuer is unavailable")
	}
	if err := validateCanonicalRef("principal_ref", i.principalRef); err != nil {
		return err
	}
	return validateCanonicalRef("scope_ref", i.scopeRef)
}

// Issue 为当前 server boot 产生短期 principal 快照；返回值不含任何 secret。
func (i *ConfiguredPrincipalIssuer) Issue() (common.CallerPrincipal, error) {
	return i.IssueAt(time.Now().UTC())
}

// IssueAt 为确定性测试提供显式时钟，生产调用使用 Issue。
func (i *ConfiguredPrincipalIssuer) IssueAt(now time.Time) (common.CallerPrincipal, error) {
	if err := i.Validate(); err != nil {
		return common.CallerPrincipal{}, err
	}
	if now.IsZero() {
		return common.CallerPrincipal{}, errors.New("configured principal issuer requires a non-zero clock")
	}
	now = now.UTC()
	principal := common.CallerPrincipal{
		PrincipalRef: i.principalRef, ScopeRef: i.scopeRef,
		Profile: ConfiguredPrincipalProfile, AuthContext: i.authContext,
		ExpiresAt: now.Add(configuredPrincipalTTL),
	}
	if err := principal.ValidateAt(now); err != nil {
		return common.CallerPrincipal{}, err
	}
	return principal, nil
}
