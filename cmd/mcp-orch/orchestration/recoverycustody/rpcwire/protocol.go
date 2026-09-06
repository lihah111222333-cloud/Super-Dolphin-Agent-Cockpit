// Package rpcwire 定义 Recovery Custody JSON-RPC v1 的纯 wire 合同。
// 本包不注册 MCP tool，也不实现 transport、client、server 或 authority。
package rpcwire

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

const SchemaVersion int64 = 1

// Method 是 Recovery Custody v1 的关闭方法集合。
type Method string

const (
	MethodIdentity Method = "recovery-custody/identity"
	MethodSubmit   Method = "recovery-custody/submit"
	MethodQuery    Method = "recovery-custody/query"
)

var ErrInvalidWire = errors.New("recovery custody RPC wire value is invalid")

// AuthorityIdentity 是每个响应携带的 authority 实例与安全配置身份。
type AuthorityIdentity struct {
	InstanceID                 string `json:"instance_id"`
	AuthorityID                string `json:"authority_id"`
	AuthorityClusterID         string `json:"authority_cluster_id"`
	AuthorityProfile           string `json:"authority_profile"`
	AuthorityGeneration        int64  `json:"authority_generation"`
	AuthorityProfileGeneration int64  `json:"authority_profile_generation"`
	AuthorityProfileDigest     string `json:"authority_profile_digest"`
	SecurityDomainID           string `json:"security_domain_id"`
}

// SubmitParams 只封装完整且自验证的领域提交请求。
type SubmitParams struct {
	SchemaVersion int64                             `json:"schema_version"`
	Request       recoverycustody.SubmissionRequest `json:"request"`
}

// QueryParams 只封装完整且自验证的稳定查询句柄。
type QueryParams struct {
	SchemaVersion int64                            `json:"schema_version"`
	Handle        recoverycustody.SubmissionHandle `json:"handle"`
}

// Result 是 identity、submit 与 query 共用的 v1 响应信封。
// identity 结果只含 authority；submit/query 还必须含 outcome 或 failure。
type Result struct {
	SchemaVersion int64                    `json:"schema_version"`
	Authority     AuthorityIdentity        `json:"authority"`
	Outcome       *recoverycustody.Outcome `json:"outcome,omitempty"`
	Failure       *Failure                 `json:"failure,omitempty"`
}

// Validate 验证方法名属于冻结的 v1 集合。
func (m Method) Validate() error {
	switch m {
	case MethodIdentity, MethodSubmit, MethodQuery:
		return nil
	default:
		return fmt.Errorf("%w: unsupported method", ErrInvalidWire)
	}
}

// Validate 验证 authority 身份字段均有界、规范且代际为正。
func (a AuthorityIdentity) Validate() error {
	for _, item := range []struct {
		name, value string
		limit       int
	}{
		{"instance_id", a.InstanceID, 128},
		{"authority_id", a.AuthorityID, 256},
		{"authority_cluster_id", a.AuthorityClusterID, 256},
		{"authority_profile", a.AuthorityProfile, 128},
		{"security_domain_id", a.SecurityDomainID, 256},
	} {
		if err := validateText(item.name, item.value, item.limit); err != nil {
			return err
		}
	}
	if a.AuthorityGeneration <= 0 || a.AuthorityProfileGeneration <= 0 {
		return fmt.Errorf("%w: authority generations must be positive", ErrInvalidWire)
	}
	return validateDigest("authority_profile_digest", a.AuthorityProfileDigest)
}

// Validate 验证 submit 参数版本和领域请求。
func (p SubmitParams) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported submit schema version", ErrInvalidWire)
	}
	if err := p.Request.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWire, err)
	}
	return nil
}

// Validate 验证 query 参数版本和领域句柄。
func (p QueryParams) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported query schema version", ErrInvalidWire)
	}
	if err := p.Handle.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWire, err)
	}
	return nil
}

// Validate 验证响应信封、领域 outcome 与 failure 组合，不提升终态可信度。
func (r Result) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported result schema version", ErrInvalidWire)
	}
	if err := r.Authority.Validate(); err != nil {
		return err
	}
	if r.Outcome != nil {
		if err := r.Outcome.Validate(); err != nil {
			return fmt.Errorf("%w: invalid outcome", ErrInvalidWire)
		}
		if err := r.Authority.validateOutcomeIdentity(*r.Outcome); err != nil {
			return err
		}
	}
	if r.Failure != nil {
		if err := r.Failure.Validate(); err != nil {
			return err
		}
	}
	return validateOutcomeFailureCombination(r.Outcome, r.Failure)
}

// ValidateForMethod 收紧共用信封到指定方法的结果形状。
func (r Result) ValidateForMethod(method Method) error {
	if err := method.Validate(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if method == MethodIdentity {
		if r.Outcome != nil || r.Failure != nil {
			return fmt.Errorf("%w: identity result cannot contain outcome or failure", ErrInvalidWire)
		}
		return nil
	}
	if r.Outcome == nil && r.Failure == nil {
		return fmt.Errorf("%w: submit/query result requires outcome or failure", ErrInvalidWire)
	}
	return nil
}

// validateOutcomeIdentity 绑定 terminal receipt 与响应声明的 authority 身份。
func (a AuthorityIdentity) validateOutcomeIdentity(outcome recoverycustody.Outcome) error {
	if outcome.Receipt == nil {
		return nil
	}
	receipt := outcome.Receipt
	if a.AuthorityID != receipt.AuthorityID || a.AuthorityClusterID != receipt.AuthorityClusterID ||
		a.AuthorityProfile != receipt.AuthorityProfile || a.AuthorityGeneration != receipt.AuthorityGeneration ||
		a.AuthorityProfileGeneration != receipt.AuthorityProfileGeneration ||
		a.AuthorityProfileDigest != receipt.AuthorityProfileDigest || a.SecurityDomainID != receipt.SecurityDomainID {
		return fmt.Errorf("%w: terminal receipt authority identity mismatch", ErrInvalidWire)
	}
	return nil
}

// validateText 校验 wire 文本非空、无首尾空白且满足长度与 UTF-8 边界。
func validateText(name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit ||
		!utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrInvalidWire, name)
	}
	return nil
}

func validateDigest(name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase 256-bit digest", ErrInvalidWire, name)
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return fmt.Errorf("%w: %s is not hexadecimal", ErrInvalidWire, name)
		}
	}
	return nil
}
