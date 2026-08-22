package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

type explicitToolWorkDirContextKey struct{}
type appManagedWriteCapabilityContextKey struct{}
type appManagedReadCapabilityContextKey struct{}
type runtimeWorkspaceRootCapabilityContextKey struct{}

// WithAppManagedWriteCapability 标记调用方已经通过应用侧授权，可写入 app-managed 数据根。
// 默认 direct tool 不带该能力，因此只能访问 workspace roots 内文件。
func WithAppManagedWriteCapability(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, appManagedWriteCapabilityContextKey{}, true)
}

// WithAppManagedReadCapability 标记调用方已经通过应用侧授权，可读取 app-managed 数据根。
// 默认 direct file/diagnostics 不带该能力，因此只能读取 workspace roots 内文件。
func WithAppManagedReadCapability(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, appManagedReadCapabilityContextKey{}, true)
}

// WithRuntimeWorkspaceRootCapability 在 context 中授予主控确认过的 runtime roots 访问能力。
func WithRuntimeWorkspaceRootCapability(ctx context.Context, roots []string) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := normalizeRuntimeWorkspaceRootCapability(roots)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, runtimeWorkspaceRootCapabilityContextKey{}, normalized), nil
}

// hasAppManagedWriteCapability 读取应用侧授予的 app-managed 写能力标记。
func hasAppManagedWriteCapability(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(appManagedWriteCapabilityContextKey{}).(bool)
	return allowed
}

// hasAppManagedReadCapability 读取应用侧授予的 app-managed 读能力标记。
func hasAppManagedReadCapability(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(appManagedReadCapabilityContextKey{}).(bool)
	return allowed
}

func runtimeWorkspaceRootCapabilityFromContext(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	roots, _ := ctx.Value(runtimeWorkspaceRootCapabilityContextKey{}).([]string)
	return append([]string(nil), roots...)
}

// normalizeRuntimeWorkspaceRootCapability 校验并规范化主控授予的 runtime roots。
func normalizeRuntimeWorkspaceRootCapability(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	seen := map[string]struct{}{}
	for _, root := range roots {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			return nil, errors.New("runtime workspace root capability contains empty root")
		}
		absolute, err := filepath.Abs(trimmed)
		if err != nil {
			return nil, fmt.Errorf("resolve runtime workspace root capability: %w", err)
		}
		resolved, err := lspplatform.CanonicalDirectoryPath(filepath.Clean(absolute))
		if err != nil {
			return nil, fmt.Errorf("resolve runtime workspace root capability: %w", err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("stat runtime workspace root capability: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("runtime workspace root capability is not a directory: %s", resolved)
		}
		clean := filepath.Clean(resolved)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	if len(out) == 0 {
		return nil, errors.New("runtime workspace root capability requires at least one root")
	}
	return out, nil
}

func toolWorkspaceRoot(ctx context.Context) (string, error) {
	return common.WorkspaceRootFromContextStrict(ctx)
}

func scopedWorkspaceRoots(ctx context.Context) ([]string, error) {
	roots, err := common.WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return nil, common.ErrMissingWorkspaceRoots
	}
	return append([]string(nil), roots...), nil
}

func appendAppManagedRoots(roots []string, capability string) ([]string, error) {
	appRoots, err := platformshared.AppManagedDataRoots()
	if err != nil {
		return nil, fmt.Errorf("resolve app-managed %s roots: %w", capability, err)
	}
	return append(append([]string(nil), roots...), appRoots...), nil
}

func toolWorkspaceRoots(ctx context.Context) (string, []string, error) {
	roots, err := scopedWorkspaceRoots(ctx)
	if err != nil {
		return "", nil, err
	}
	if hasAppManagedWriteCapability(ctx) {
		roots, err = appendAppManagedRoots(roots, "write")
		if err != nil {
			return "", nil, err
		}
	}
	return roots[0], append([]string(nil), roots[1:]...), nil
}

func toolReadableRoots(ctx context.Context) (string, []string, error) {
	roots, err := scopedWorkspaceRoots(ctx)
	if err != nil {
		return "", nil, err
	}
	if hasAppManagedReadCapability(ctx) {
		roots, err = appendAppManagedRoots(roots, "read")
		if err != nil {
			return "", nil, err
		}
	}
	return roots[0], append([]string(nil), roots[1:]...), nil
}

func toolResolvePath(ctx context.Context, target string) (search.PathInfo, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return search.PathInfo{}, err
	}
	return search.ResolvePathInRoots(root, roots, target)
}

// contextWithExplicitToolWorkDir 从工具请求参数中提取 work_dir 并写入 tool scope。
// 空参数保持原 context；非法 JSON 或越界路径会直接返回错误。
func contextWithExplicitToolWorkDir(ctx context.Context, params json.RawMessage) (context.Context, error) {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ctx, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return ctx, fmt.Errorf("parse explicit tool work_dir params: %w", err)
	}
	rawWorkDir, ok := fields["work_dir"]
	if !ok {
		return ctx, nil
	}
	var workDir string
	if err := json.Unmarshal(rawWorkDir, &workDir); err != nil {
		return ctx, fmt.Errorf("parse explicit tool work_dir: %w", err)
	}
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ctx, errors.New("work_dir is required")
	}
	scopedCtx, _, err := contextWithExplicitWorkDir(ctx, workDir)
	if err != nil {
		return ctx, err
	}
	return scopedCtx, nil
}

// contextWithExplicitWorkDir 把显式传入的 work_dir 写入 ctx 的 tool scope，并验证路径在工作区根内。
func contextWithExplicitWorkDir(ctx context.Context, workDir string) (context.Context, string, error) {
	normalized, err := normalizeExplicitWorkDir(ctx, workDir)
	if err != nil {
		return ctx, "", err
	}
	if err := ensureExplicitWorkDirWithinWorkspaceRoots(ctx, normalized); err != nil {
		return ctx, "", err
	}
	scope, _ := common.ToolScopeFromContext(ctx)
	scope.CWD = normalized
	scope.WorkspaceRoots = append(scope.WorkspaceRoots, normalized)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = "lsp"
	}
	return context.WithValue(common.WithToolScope(ctx, scope), explicitToolWorkDirContextKey{}, true), normalized, nil
}

// ensureExplicitWorkDirWithinWorkspaceRoots 确保 work_dir 在工作区根目录范围内。
func ensureExplicitWorkDirWithinWorkspaceRoots(ctx context.Context, workDir string) error {
	roots, err := common.WorkspaceRootsFromContextStrict(ctx)
	if err != nil {
		return fmt.Errorf("explicit work_dir requires trusted workspace roots: %w", err)
	}
	for _, root := range roots {
		if platformshared.ContainsPath(root, workDir) {
			return nil
		}
	}
	for _, root := range runtimeWorkspaceRootCapabilityFromContext(ctx) {
		if platformshared.ContainsPath(root, workDir) {
			return nil
		}
	}
	return fmt.Errorf("work_dir %s is outside workspace roots [%s]", workDir, strings.Join(roots, ", "))
}

// normalizeExplicitWorkDir 规范化工具请求中的显式 work_dir。
// 相对路径必须基于可信 workspace root 展开，并且最终必须指向已存在目录。
func normalizeExplicitWorkDir(ctx context.Context, workDir string) (string, error) {
	trimmed := strings.TrimSpace(workDir)
	if trimmed == "" {
		return "", errors.New("work_dir is required")
	}
	trimmed = normalizePlatformWorkDir(trimmed)
	if !filepath.IsAbs(trimmed) {
		root, err := common.WorkspaceRootFromContextStrict(ctx)
		if err != nil {
			return "", fmt.Errorf("relative work_dir requires trusted workspace root: %w", err)
		}
		trimmed = filepath.Join(root, trimmed)
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	resolved, err := lspplatform.CanonicalDirectoryPath(filepath.Clean(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve work_dir: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat work_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work_dir is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
}

// explicitToolWorkDirFromContext 标记本次调用已经用参数中的 work_dir 重建可信作用域。
// grep 的 stale fallback 只适用于缺少显式工作目录的旧运行时根，不应拦截这种调用。
func explicitToolWorkDirFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ok, _ := ctx.Value(explicitToolWorkDirContextKey{}).(bool)
	return ok
}
