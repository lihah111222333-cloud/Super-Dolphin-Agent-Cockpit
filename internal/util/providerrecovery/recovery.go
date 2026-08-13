// Package providerrecovery owns provider thread identity recovery policy.
package providerrecovery

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/historyjsonl"
)

// ErrorKind 是 provider recovery 的稳定错误分类。
type ErrorKind string

const (
	ErrorKindNotFound        ErrorKind = "not_found"
	ErrorKindPermission      ErrorKind = "permission"
	ErrorKindIO              ErrorKind = "io"
	ErrorKindParse           ErrorKind = "parse"
	ErrorKindArtifactRace    ErrorKind = "artifact_race"
	ErrorKindUnknownProvider ErrorKind = "unknown_provider"
	ErrorKindInvalidIdentity ErrorKind = "invalid_identity"
)

// ErrNotFound 仅表示明确缺失的 provider root 或 artifact。
var ErrNotFound = errors.New("provider recovery artifact not found")

var errNoIdentityCandidate = errors.New("official provider UUID candidate is absent")

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
		ProviderThreadID: providerThreadID,
		CodexHome:        strings.TrimSpace(req.CodexHome),
		ClaudeHome:       strings.TrimSpace(req.ClaudeHome),
	}
	artifactPath, err := historyjsonl.ValidateRecoveryArtifact(historyReq)
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
	_, _, err := resolveIdentity(req)
	if errors.Is(err, errNoIdentityCandidate) {
		return Result{
			Provider:       provider,
			IdentitySource: IdentitySourceNoCandidate,
			ArtifactPolicy: ArtifactPolicyNotApplicable,
		}, nil
	}
	if err != nil {
		return Result{}, recoveryError(ErrorKindInvalidIdentity, provider, err)
	}
	return Resolve(req)
}

func resolveIdentity(req Request) (string, IdentitySource, error) {
	if candidate := req.ProviderThreadID; candidate != "" {
		canonical, err := CanonicalizeUUID(candidate)
		if err == nil {
			return canonical, IdentitySourceProviderThreadID, nil
		}
		if !isLegacyAgentPlaceholder(candidate) {
			return "", "", fmt.Errorf("provider thread identity: %w", err)
		}
	}
	if candidate := req.SessionUUID; candidate != "" {
		canonical, err := CanonicalizeUUID(candidate)
		if err != nil {
			return "", "", fmt.Errorf("legacy session identity: %w", err)
		}
		return canonical, IdentitySourceLegacySessionUUID, nil
	}
	if req.ProviderThreadID == "" || isLegacyAgentPlaceholder(req.ProviderThreadID) {
		return "", "", errNoIdentityCandidate
	}
	return "", "", errors.New("official provider UUID is required")
}

// CanonicalizeUUID 严格解析合法历史 UUID 表示，并返回统一的小写带连字符形式。
func CanonicalizeUUID(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return "", errors.New("provider UUID must not be empty or contain surrounding whitespace")
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse provider UUID %q: %w", raw, err)
	}
	if parsed == uuid.Nil {
		return "", errors.New("nil provider UUID is invalid")
	}
	return parsed.String(), nil
}

func isLegacyAgentPlaceholder(raw string) bool {
	return strings.HasPrefix(raw, "agent_") || strings.HasPrefix(raw, "agent-")
}

func classifyArtifactError(err error) ErrorKind {
	switch {
	case historyjsonl.IsRecoveryArtifactRaceError(err):
		return ErrorKindArtifactRace
	case errors.Is(err, os.ErrPermission):
		return ErrorKindPermission
	case historyjsonl.IsParseError(err):
		return ErrorKindParse
	case historyjsonl.IsRecoveryArtifactIdentityError(err):
		return ErrorKindInvalidIdentity
	default:
		return ErrorKindIO
	}
}

func recoveryError(kind ErrorKind, provider string, cause error) error {
	return &Error{Kind: kind, Provider: provider, Cause: cause}
}
