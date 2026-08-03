package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const emptyDocumentSymbolRetryDelay = 80 * time.Millisecond

// retryEmptyReferences 在前端项目消费者完成显式语义请求后重试一次空引用。
// 项目遍历复用仓库深度/条目预算，语义请求数复用 workspace refresh 上限；取消或请求错误立即返回。
func (m *manager) retryEmptyReferences(
	ctx context.Context,
	uri string,
	method string,
	params any,
) ([]protocol.LocationResult, error) {
	client, opened, err := m.prepareFrontendReferenceProject(ctx, uri)
	if err != nil {
		return nil, err
	}
	results, retryErr := m.locationQueryOnce(ctx, uri, method, params)
	return results, errors.Join(retryErr, closeFrontendReferenceProjectDocuments(ctx, client, opened))
}

// prepareFrontendReferenceProject 用 document-symbol 响应确认目标 client 已处理有界消费者集合。
// 它不依赖诊断或墙钟等待：请求响应本身就是 LSP 消息序列中的索引 barrier。
func (m *manager) prepareFrontendReferenceProject(ctx context.Context, uri string) (Client, []documentRef, error) {
	client, target, err := m.documentClientWithoutDiagnosticsWait(ctx, uri)
	if err != nil {
		return nil, nil, err
	}
	if client == nil {
		return nil, nil, fmt.Errorf("prepare frontend reference project: LSP client is unavailable")
	}
	scope, adapter, err := m.resolveLanguageScope(ctx, target.languageID, target.absPath, target.uri)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve frontend reference project: %w", err)
	}
	policy := adapter.BootstrapPolicy(scope)
	paths, err := findFrontendReferenceProjectFiles(
		ctx,
		scope.ProjectRoot,
		target.absPath,
		policy.FirstSourceExtensions,
		policy.IgnoredDirNames,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("find frontend reference project files: %w", err)
	}
	opened := make([]documentRef, 0, len(paths))
	for _, path := range paths {
		candidate, err := m.resolveDocumentRef(ctx, path, "")
		if err != nil {
			return nil, nil, errors.Join(
				fmt.Errorf("resolve frontend reference project file %s: %w", path, err),
				closeFrontendReferenceProjectDocuments(ctx, client, opened),
			)
		}
		temporary, err := m.prepareFrontendReferenceProjectFile(ctx, client, target, candidate, path)
		if temporary {
			opened = append(opened, candidate)
		}
		if err != nil {
			return nil, nil, errors.Join(
				err,
				closeFrontendReferenceProjectDocuments(ctx, client, opened),
			)
		}
	}
	return client, opened, nil
}

// prepareFrontendReferenceProjectFile 打开单个消费者并等待其 document-symbol 响应。
// 同语言文档交给既有 bootstrap 状态机去重；跨语言文档由调用方在 references 后关闭。
func (m *manager) prepareFrontendReferenceProjectFile(
	ctx context.Context,
	client Client,
	target documentRef,
	candidate documentRef,
	path string,
) (bool, error) {
	temporary := candidate.languageID != target.languageID
	if temporary {
		content, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read frontend reference project file %s: %w", path, err)
		}
		if err := client.DidOpen(ctx, candidate.uri, candidate.languageID, 1, string(content)); err != nil {
			return false, fmt.Errorf("open frontend reference project file %s: %w", path, err)
		}
	} else if err := m.bootstrapDocument(ctx, candidate.uri); err != nil {
		return false, fmt.Errorf("open frontend reference project file %s: %w", path, err)
	}
	if _, err := m.requestDocumentSymbols(ctx, client, candidate); err != nil {
		return temporary, fmt.Errorf("prepare frontend reference project file %s: %w", path, err)
	}
	return temporary, nil
}

// closeFrontendReferenceProjectDocuments 逆序关闭仅为跨语言 barrier 临时打开的文档，并保留全部关闭错误。
func closeFrontendReferenceProjectDocuments(ctx context.Context, client Client, opened []documentRef) error {
	var errs []error
	for i := len(opened) - 1; i >= 0; i-- {
		if err := client.DidClose(ctx, opened[i].uri); err != nil {
			errs = append(errs, fmt.Errorf("close frontend reference project file %s: %w", opened[i].absPath, err))
		}
	}
	return errors.Join(errs...)
}

// findFrontendReferenceProjectFiles 在既有项目遍历预算内收集前端消费者候选，并跳过目标声明文件。
func findFrontendReferenceProjectFiles(
	ctx context.Context,
	root string,
	target string,
	extensions []string,
	ignored map[string]struct{},
) ([]string, error) {
	finder := &frontendReferenceProjectFileFinder{
		target:     filepath.Clean(target),
		extensions: extensionSet(extensions),
		ignored:    ignored,
	}
	if err := boundedProjectWalk(ctx, root, projectWalkMaxDepth, projectWalkMaxEntries, finder.walk); err != nil {
		return nil, err
	}
	return finder.paths, nil
}

type frontendReferenceProjectFileFinder struct {
	target     string
	extensions map[string]struct{}
	ignored    map[string]struct{}
	paths      []string
}

// walk 按 adapter 扩展名与忽略目录收集候选，达到 workspace refresh 上限后立即停止。
func (f *frontendReferenceProjectFileFinder) walk(path string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if entry == nil {
		return nil
	}
	if entry.IsDir() {
		return projectWalkDirDecision(entry.Name(), f.ignored)
	}
	if filepath.Clean(path) == f.target {
		return nil
	}
	if _, ok := f.extensions[strings.ToLower(filepath.Ext(path))]; !ok {
		return nil
	}
	f.paths = append(f.paths, path)
	if len(f.paths) >= maxRefreshFiles {
		return filepath.SkipAll
	}
	return nil
}

// shouldRetryEmptyDocumentSymbols 只给 JS/TS 空大纲做一次二次请求。
// 这些语言的服务器冷启动时偶发先返回空数组，其他语言保持原来的请求语义。
func shouldRetryEmptyDocumentSymbols(languageID string, symbols []protocol.DocumentSymbol) bool {
	return len(symbols) == 0 && isJSTSDocumentSymbolFallbackLanguage(languageID)
}

// retryEmptyDocumentSymbols 在 JS/TS 空大纲时执行唯一一次二次 LSP 请求。
// 二次请求返回非空时保持 LSP 优先，仍为空时才交给 TypeScript navigation fallback。
func (m *manager) retryEmptyDocumentSymbols(ctx context.Context, client Client, ref documentRef, symbols []protocol.DocumentSymbol) ([]protocol.DocumentSymbol, error) {
	if !shouldRetryEmptyDocumentSymbols(ref.languageID, symbols) {
		return symbols, nil
	}
	if err := waitBeforeEmptyDocumentSymbolRetry(ctx); err != nil {
		return nil, err
	}
	return m.requestDocumentSymbols(ctx, client, ref)
}

// waitBeforeEmptyDocumentSymbolRetry 给语言服务器一个短窗口完成索引。
// 调用方 context 被取消时立即退出，避免 document_symbol 卡住工具层超时。
func waitBeforeEmptyDocumentSymbolRetry(ctx context.Context) error {
	if emptyDocumentSymbolRetryDelay <= 0 {
		return nil
	}
	timer := time.NewTimer(emptyDocumentSymbolRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
