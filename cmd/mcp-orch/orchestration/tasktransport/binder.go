// Package tasktransport contains the transport-neutral task caller binding
// candidate. It is test-owned only: no MCP registry, provider, EventStore, or
// Fx wiring imports this package, so the candidate cannot enable unified_v2.
package tasktransport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

var (
	// ErrBindingInvalid indicates malformed binding input or an invalid context.
	ErrBindingInvalid = errors.New("task transport binding is invalid")
	// ErrAttributionMissing indicates that the server did not authenticate a task caller.
	ErrAttributionMissing = errors.New("task transport caller attribution is missing")
	// ErrPrincipalMissing indicates that attribution was present without a valid caller principal.
	ErrPrincipalMissing = errors.New("task transport caller principal is missing")
	// ErrOperatorPrincipal indicates that an operator profile was supplied to a task-only binder.
	ErrOperatorPrincipal = errors.New("task transport operator principal is not allowed")
	// ErrMetadataImpersonation indicates that caller-controlled scope was supplied.
	ErrMetadataImpersonation = errors.New("task transport caller metadata cannot establish scope")
	// ErrScopeMismatch indicates that a scope was already bound to this request.
	ErrScopeMismatch = errors.New("task transport scope does not match authenticated task")
)

// ScopeTemplate contains only transport-owned scope fields. Agent, thread,
// turn, and call identity are always projected from authenticated attribution.
type ScopeTemplate struct {
	Family         string
	CWD            string
	WorkspaceRoots []string
}

// BindRequest separates server-owned scope configuration from untrusted
// transport metadata. ClaimedScope is optional wire metadata and must be empty
// for a task binding to proceed.
type BindRequest struct {
	Template     ScopeTemplate
	ClaimedScope common.ToolScope
}

// Bind verifies server-bound task attribution and projects its task ID into a
// ToolScope. Existing scope values and any caller metadata fail closed; no
// client-provided identity can override the authenticated task.
// Bind 校验服务端绑定的 task attribution，并将 task ID 投影到 ToolScope；已有 scope 或调用方 metadata 均直接阻断。
func (r BindRequest) Bind(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrBindingInvalid)
	}
	if err := r.validateTemplate(); err != nil {
		return ctx, err
	}
	if hasScope(r.ClaimedScope) {
		return ctx, ErrMetadataImpersonation
	}
	if _, alreadyBound := common.ToolScopeFromContext(ctx); alreadyBound {
		return ctx, ErrScopeMismatch
	}
	attribution, err := common.RequireCallerAttribution(ctx)
	if err != nil {
		if errors.Is(err, common.ErrCallerAttributionUnavailable) {
			return ctx, ErrAttributionMissing
		}
		return ctx, fmt.Errorf("%w: attribution validation failed", ErrBindingInvalid)
	}
	principal, err := common.RequireCallerPrincipal(ctx)
	if err != nil {
		if errors.Is(err, common.ErrMissingCallerPrincipal) {
			return ctx, ErrPrincipalMissing
		}
		return ctx, fmt.Errorf("%w: principal validation failed", ErrBindingInvalid)
	}
	if strings.EqualFold(principal.Profile, "operator") {
		return ctx, ErrOperatorPrincipal
	}

	template := r.normalizedTemplate()
	scope := common.ToolScope{
		AgentID:        attribution.TaskID,
		CWD:            template.CWD,
		WorkspaceRoots: append([]string(nil), template.WorkspaceRoots...),
		Family:         template.Family,
	}
	return common.WithToolScope(ctx, scope), nil
}

// BindTaskToolScope 是 transport adapter 与测试共用的显式绑定入口，不注册传输或授权工具能力。
func BindTaskToolScope(ctx context.Context, request BindRequest) (context.Context, error) {
	return request.Bind(ctx)
}

// validateTemplate 校验仅由 transport 所有的 scope 模板，拒绝隐式 family 或相对工作区。
func (r BindRequest) validateTemplate() error {
	if err := validateFamily(r.Template.Family); err != nil {
		return err
	}
	if err := validateCWD(r.Template.CWD); err != nil {
		return err
	}
	if err := validateWorkspaceRoots(r.Template.WorkspaceRoots); err != nil {
		return err
	}
	normalized := r.normalizedTemplate()
	return validateNormalizedTemplate(r.Template, normalized)
}

// normalizedTemplate 将 scope 模板交给 common 的统一归一化逻辑，并复制 roots 避免别名修改。
func (r BindRequest) normalizedTemplate() ScopeTemplate {
	scope := common.NormalizeToolScope(common.ToolScope{
		CWD:            r.Template.CWD,
		WorkspaceRoots: append([]string(nil), r.Template.WorkspaceRoots...),
		Family:         r.Template.Family,
	})
	return ScopeTemplate{Family: scope.Family, CWD: scope.CWD, WorkspaceRoots: scope.WorkspaceRoots}
}

// hasScope 判断调用方是否提供了任何可能冒充身份或边界的 scope 字段。
func hasScope(scope common.ToolScope) bool {
	// Inspect raw values before normalization.  A malformed or relative client
	// claim must be rejected, not normalized away into an apparently empty scope.
	return scope.AgentID != "" || scope.ThreadID != "" || scope.TurnID != "" ||
		scope.CallID != "" || scope.CWD != "" || len(scope.WorkspaceRoots) != 0 ||
		scope.Family != ""
}

// validateTemplateText 拒绝 scope 模板中的空白、控制字符、非法 UTF-8 和无界文本。
func validateTemplateText(name, value string, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%w: %s is required", ErrBindingInvalid, name)
	}
	if value == "" {
		return nil
	}
	if len(value) > 512 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s must be bounded canonical text", ErrBindingInvalid, name)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("%w: %s must not contain control characters", ErrBindingInvalid, name)
		}
	}
	return nil
}

// isAbsoluteScopeRoot 判断当前平台及跨平台 peer 允许的绝对根路径。
func isAbsoluteScopeRoot(value string) bool {
	if isUNCPath(value) {
		return false
	}
	return filepath.IsAbs(value) || (runtime.GOOS == "windows" && strings.HasPrefix(value, "/"))
}

// isUNCPath 判断 Windows UNC 或双斜杠网络根，避免 task scope 越过本机工作区边界。
func isUNCPath(value string) bool {
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//")
}

// containsRoot 判断归一化后的 roots 是否保留了每个原始绝对根。
func containsRoot(roots []string, want string) bool {
	for _, root := range roots {
		if root == want {
			return true
		}
	}
	return false
}

// validateFamily 校验 family 的 canonical 文本并保留 common 的历史 alias 归一化。
func validateFamily(family string) error {
	if strings.TrimSpace(family) == "" || strings.TrimSpace(family) != family {
		return fmt.Errorf("%w: family must be canonical nonempty text", ErrBindingInvalid)
	}
	return validateTemplateText("family", family, true)
}

// validateCWD 校验 primary workspace 的文本和绝对路径边界。
func validateCWD(cwd string) error {
	if err := validateTemplateText("cwd", cwd, false); err != nil {
		return err
	}
	if cwd != "" && !isAbsoluteScopeRoot(cwd) {
		return fmt.Errorf("%w: cwd must be an absolute path", ErrBindingInvalid)
	}
	return nil
}

// validateWorkspaceRoots 校验每个 workspace root 的文本和绝对路径，不接受相对根。
func validateWorkspaceRoots(roots []string) error {
	for index, root := range roots {
		if err := validateTemplateText(fmt.Sprintf("workspace root %d", index), root, true); err != nil {
			return err
		}
		if !isAbsoluteScopeRoot(root) {
			return fmt.Errorf("%w: workspace root %d must be absolute", ErrBindingInvalid, index)
		}
	}
	return nil
}

// validateNormalizedTemplate 确认归一化没有丢弃模板字段，仅允许路径 clean 和 family alias。
func validateNormalizedTemplate(raw, normalized ScopeTemplate) error {
	if normalized.Family == "" {
		return fmt.Errorf("%w: family normalization produced an empty value", ErrBindingInvalid)
	}
	if raw.CWD != "" && normalized.CWD == "" {
		return fmt.Errorf("%w: cwd must be an absolute path", ErrBindingInvalid)
	}
	if len(raw.WorkspaceRoots) != 0 && normalized.CWD == "" {
		return fmt.Errorf("%w: workspace roots require a primary cwd", ErrBindingInvalid)
	}
	for index, root := range raw.WorkspaceRoots {
		canonical := common.NormalizeToolScope(common.ToolScope{CWD: root}).CWD
		if canonical == "" || !containsRoot(normalized.WorkspaceRoots, canonical) {
			return fmt.Errorf("%w: workspace root %d was dropped during normalization", ErrBindingInvalid, index)
		}
	}
	return nil
}
