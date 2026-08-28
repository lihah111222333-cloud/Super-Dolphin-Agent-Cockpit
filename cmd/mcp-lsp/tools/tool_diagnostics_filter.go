package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
)

// resolveDiagnosticsFilter 将显式差量过滤路径绑定到已校验的目标 URI。
// 每个目标都必须有自己的行号映射，任何漏传、错传或越界路径均 fail-fast。
func resolveDiagnosticsFilter(ctx context.Context, targets []diagnosticTarget, request *diagnosticsFilterRequest) (*diagnosticsFilterSpec, error) {
	if request == nil {
		return nil, nil
	}
	if len(targets) == 0 {
		return nil, errors.New("diagnostics diff_filter requires at least one target")
	}
	spec := newDiagnosticsFilterSpec(request.mode, targets)
	if len(request.legacyChangedLines) > 0 {
		return resolveLegacyDiagnosticsFilter(spec, targets, request.legacyChangedLines)
	}
	return resolveMappedDiagnosticsFilter(ctx, spec, targets, request.changedLinesByPath)
}

func newDiagnosticsFilterSpec(mode string, targets []diagnosticTarget) *diagnosticsFilterSpec {
	return &diagnosticsFilterSpec{
		Mode:              mode,
		ChangedLinesByURI: make(map[string]map[int]struct{}, len(targets)),
	}
}

func resolveLegacyDiagnosticsFilter(spec *diagnosticsFilterSpec, targets []diagnosticTarget, lines []int) (*diagnosticsFilterSpec, error) {
	if len(targets) != 1 {
		return nil, errors.New("diagnostics changed_lines shorthand cannot be applied to multiple files")
	}
	spec.ChangedLinesByURI[targets[0].URI] = diagnosticLineSet(lines)
	return spec, nil
}

// resolveMappedDiagnosticsFilter 将每个外部路径解析并绑定到唯一诊断 URI。
func resolveMappedDiagnosticsFilter(ctx context.Context, spec *diagnosticsFilterSpec, targets []diagnosticTarget, changedLines map[string][]int) (*diagnosticsFilterSpec, error) {
	root, roots, err := toolReadableRoots(ctx)
	if err != nil {
		return nil, err
	}
	targetURIs := diagnosticsTargetURISet(targets)
	for rawPath, lines := range changedLines {
		uri, err := resolveChangedLinesURI(root, roots, rawPath)
		if err != nil {
			return nil, err
		}
		if _, ok := targetURIs[uri]; !ok {
			return nil, fmt.Errorf("diff_filter.changed_lines path %q is not one of the diagnostics targets", rawPath)
		}
		if _, duplicate := spec.ChangedLinesByURI[uri]; duplicate {
			return nil, fmt.Errorf("diff_filter.changed_lines maps target %q more than once", rawPath)
		}
		spec.ChangedLinesByURI[uri] = diagnosticLineSet(lines)
	}
	if len(spec.ChangedLinesByURI) != len(targetURIs) {
		return nil, fmt.Errorf("diff_filter.changed_lines must map every diagnostics target; got %d mappings for %d targets", len(spec.ChangedLinesByURI), len(targetURIs))
	}
	return spec, nil
}

func diagnosticsTargetURISet(targets []diagnosticTarget) map[string]struct{} {
	uris := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		uris[target.URI] = struct{}{}
	}
	return uris
}

func resolveChangedLinesURI(root string, roots []string, rawPath string) (string, error) {
	normalizedPath, err := normalizeFilePathTarget(rawPath)
	if err != nil {
		return "", fmt.Errorf("resolve diff_filter.changed_lines path %q: %w", rawPath, err)
	}
	pathInfo, err := search.ResolvePathInRoots(root, roots, normalizedPath)
	if err != nil {
		return "", fmt.Errorf("resolve diff_filter.changed_lines path %q: %w", rawPath, err)
	}
	return fileURI(pathInfo.AbsPath), nil
}

// diagnosticLineSet 将一基行号列表转换为过滤查找集合。
func diagnosticLineSet(lines []int) map[int]struct{} {
	set := make(map[int]struct{}, len(lines))
	for _, line := range lines {
		set[line] = struct{}{}
	}
	return set
}
