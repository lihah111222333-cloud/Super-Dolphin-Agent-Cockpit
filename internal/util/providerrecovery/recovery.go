// Package providerrecovery owns provider thread identity recovery policy.
package providerrecovery

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/historyjsonl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/identifier"
)

// ErrorKind 是 provider recovery 的稳定错误分类。
type ErrorKind string

const (
	ErrorKindNotFound        ErrorKind = "not_found"
	ErrorKindPermission      ErrorKind = "permission"
	ErrorKindIO              ErrorKind = "io"
	ErrorKindParse           ErrorKind = "parse"
	ErrorKindUnknownProvider ErrorKind = "unknown_provider"
	ErrorKindInvalidIdentity ErrorKind = "invalid_identity"
)

// ErrNotFound 仅表示明确缺失的 provider root 或 artifact。
var ErrNotFound = errors.New("provider recovery artifact not found")

// Error 保留 recovery 分类和底层根因。
type Error struct {
	Kind     ErrorKind
	Provider string
	Cause    error
}

// Error 返回稳定分类与底层上下文。
func (e *Error) Error() string {
	return fmt.Sprintf("provider recovery %s for %q: %v", e.Kind, e.Provider, e.Cause)
}

// Unwrap 保留底层错误链。
func (e *Error) Unwrap() error {
	return e.Cause
}

// Is 允许调用方只把明确 not-found 分类降级。
func (e *Error) Is(target error) bool {
	return target == ErrNotFound && e.Kind == ErrorKindNotFound
}

// IsKind 判断 recovery error 的 typed 分类。
func IsKind(err error, kind ErrorKind) bool {
	var recoveryErr *Error
	return errors.As(err, &recoveryErr) && recoveryErr.Kind == kind
}

// IdentitySource 说明 provider thread identity 的生产字段。
type IdentitySource string

const (
	IdentitySourceProviderThreadID  IdentitySource = "provider_thread_id"
	IdentitySourceLegacySessionUUID IdentitySource = "legacy_session_uuid"
	IdentitySourceNoCandidate       IdentitySource = "no_candidate"
)

// ArtifactPolicy 说明本次恢复对本地 artifact 的判定。
type ArtifactPolicy string

const (
	ArtifactPolicyValidated       ArtifactPolicy = "validated"
	ArtifactPolicyOptionalMissing ArtifactPolicy = "optional_missing"
	ArtifactPolicyNotApplicable   ArtifactPolicy = "not_applicable"
)

// Request 是 thread、unified 和 uistate 共用的唯一 provider recovery 输入。
type Request struct {
	Provider         string
	RolloutPath      string
	PublicThreadID   string
	ProviderThreadID string
	SessionUUID      string
	CodexHome        string
	ClaudeHome       string
}

// Result 是 provider recovery 的 typed 输出。
type Result struct {
	Provider         string
	ProviderThreadID string
	IdentitySource   IdentitySource
	ArtifactPath     string
	ArtifactPolicy   ArtifactPolicy
}

// Resolve 按唯一策略选择 provider identity，并验证需要的本地 artifact。
func Resolve(req Request) (Result, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider != "codex" && provider != "claude" {
		return Result{}, recoveryError(ErrorKindUnknownProvider, provider, fmt.Errorf("unsupported provider %q", provider))
	}
	providerThreadID, source, err := resolveIdentity(req)
	if err != nil {
		return Result{}, recoveryError(ErrorKindInvalidIdentity, provider, err)
	}
	historyReq := historyjsonl.ReadRequest{
		Provider:         provider,
		RolloutPath:      strings.TrimSpace(req.RolloutPath),
		ThreadID:         strings.TrimSpace(req.PublicThreadID),
		ProviderThreadID: providerThreadID,
		SessionUUID:      providerThreadID,
		CodexHome:        strings.TrimSpace(req.CodexHome),
		ClaudeHome:       strings.TrimSpace(req.ClaudeHome),
	}
	artifactPath, err := historyjsonl.ValidateProviderArtifact(historyReq)
	if err == nil {
		return Result{
			Provider:         provider,
			ProviderThreadID: providerThreadID,
			IdentitySource:   source,
			ArtifactPath:     artifactPath,
			ArtifactPolicy:   ArtifactPolicyValidated,
		}, nil
	}
	if historyjsonl.IsMissingProviderHistory(err) {
		if provider == "codex" {
			return Result{
				Provider:         provider,
				ProviderThreadID: providerThreadID,
				IdentitySource:   source,
				ArtifactPolicy:   ArtifactPolicyOptionalMissing,
			}, nil
		}
		return Result{}, recoveryError(ErrorKindNotFound, provider, err)
	}
	return Result{}, recoveryError(classifyArtifactError(err), provider, err)
}

// ResolveOptional 供展示和恢复资格检查使用：没有官方 UUID 时表示“无恢复候选”。
// 已知 provider 的 artifact 错误仍完整传播，未知 provider 仍 fail-fast。
func ResolveOptional(req Request) (Result, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider != "codex" && provider != "claude" {
		return Result{}, recoveryError(ErrorKindUnknownProvider, provider, fmt.Errorf("unsupported provider %q", provider))
	}
	if !hasRecoveryIdentity(req) {
		return Result{
			Provider:       provider,
			IdentitySource: IdentitySourceNoCandidate,
			ArtifactPolicy: ArtifactPolicyNotApplicable,
		}, nil
	}
	return Resolve(req)
}

// hasRecoveryIdentity 判断 optional 调用是否存在官方 UUID 候选。
func hasRecoveryIdentity(req Request) bool {
	return identifier.LooksLikeUUID(strings.TrimSpace(req.ProviderThreadID)) ||
		identifier.LooksLikeUUID(strings.TrimSpace(req.SessionUUID))
}

func resolveIdentity(req Request) (string, IdentitySource, error) {
	if candidate := strings.TrimSpace(req.ProviderThreadID); identifier.LooksLikeUUID(candidate) {
		return candidate, IdentitySourceProviderThreadID, nil
	}
	if candidate := strings.TrimSpace(req.SessionUUID); identifier.LooksLikeUUID(candidate) {
		return candidate, IdentitySourceLegacySessionUUID, nil
	}
	return "", "", errors.New("official provider UUID is required")
}

func classifyArtifactError(err error) ErrorKind {
	switch {
	case errors.Is(err, os.ErrPermission):
		return ErrorKindPermission
	case historyjsonl.IsParseError(err):
		return ErrorKindParse
	default:
		return ErrorKindIO
	}
}

func recoveryError(kind ErrorKind, provider string, cause error) error {
	return &Error{Kind: kind, Provider: provider, Cause: cause}
}
