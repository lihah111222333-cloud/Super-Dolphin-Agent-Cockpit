package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/search"
)

const maxDiagnosticFileBytes = 2 << 20

var errManagerUnavailable = errors.New("LSP manager is not configured")

// Config 是 LSP 工具 handler 的公共配置，绑定工作区根目录和语言服务注册表。
type Config struct {
	WorkspaceRoot string
	Registry      lspmanager.Registry
	// EnsureASTGrep 保留给未对外注册的内部搜索实现；公开 MCP 工具不会消费它。
	EnsureASTGrep func(context.Context) (string, error)
}

type handlerBase struct {
	root          string
	registry      lspmanager.Registry
	ensureASTGrep func(context.Context) (string, error)
}

type resultMeta struct {
	Count   int    `json:"count"`
	Message string `json:"message,omitempty"`
	Source  string `json:"source,omitempty"`
}

type emptyListEnvelope struct {
	Success bool       `json:"success"`
	Data    []any      `json:"data"`
	Meta    resultMeta `json:"meta"`
}

// fileToolInput 仅是 diagnostics 内部路径路由载体，不属于 MCP 输入 schema。
type fileToolInput struct {
	Action     string
	FilePath   string
	FilePaths  []string
	LanguageID string
}

func shouldUseLanguageOverrideDiagnostics(input fileToolInput, targets []diagnosticTarget) bool {
	return normalizeLanguageIDOverride(input.LanguageID) != "" && len(targets) > 0
}

func (h handlerBase) fetchLanguageOverrideDiagnostics(ctx context.Context, input fileToolInput, targets []diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	items := make([]protocol.PublishDiagnosticsParams, 0, len(targets))
	var message string
	for _, target := range targets {
		fetched, _, fetchedMessage, err := h.fetchSingleFileLanguageOverrideDiagnostics(ctx, input, target)
		if err != nil {
			return nil, "", "", err
		}
		items = append(items, fetched...)
		message = appendMessage(message, fetchedMessage)
	}
	return items, "language_override", message, nil
}

func (h handlerBase) fetchSingleFileLanguageOverrideDiagnostics(ctx context.Context, input fileToolInput, target diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	manager, err := managerForFile(ctx, h.registry, target.AbsPath, input.LanguageID)
	if err != nil {
		return nil, "", "", err
	}
	if _, err := os.Lstat(target.AbsPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, "", "", err
		}
		return fetchDeletedLanguageOverrideDiagnostics(ctx, manager, target)
	}
	if err := h.openDiagnosticDocument(ctx, target.AbsPath, input.LanguageID); err != nil {
		return nil, "", "", err
	}
	if err := reopenManagerDocumentForDiagnostics(ctx, manager, target.URI); err != nil {
		return nil, "", "", err
	}
	if err := manager.WaitDiagnosticsStable(ctx, []string{target.URI}); err != nil {
		if !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
			return nil, "", "", err
		}
		if retryErr := h.waitSingleFileOverrideDiagnosticsStableWithStartupRetries(ctx, manager, target.URI); retryErr != nil {
			return nil, "", "", retryErr
		}
	}
	items, err := manager.Diagnostics(ctx, []string{target.URI})
	if err != nil {
		return nil, "", "", err
	}
	filtered := diagnosticsForTargetURI(target.URI, items)
	message := diagnosticsMessageAfterFetch("", []string{target.URI}, filtered)
	return filtered, "language_override", message, nil
}

// openDiagnosticDocument 把磁盘上的诊断目标同步给显式语言 manager。
func (h handlerBase) openDiagnosticDocument(ctx context.Context, rawPath, languageID string) error {
	if h.registry == nil {
		return errManagerUnavailable
	}
	root, roots, err := toolReadableRoots(ctx)
	if err != nil {
		return err
	}
	file, err := search.ReadToolFileContentInRoots(root, roots, rawPath, maxDiagnosticFileBytes)
	if err != nil {
		return err
	}
	manager, err := managerForFile(ctx, h.registry, file.Path.AbsPath, languageID)
	if err != nil {
		return err
	}
	openLanguageID := normalizeLanguageIDOverride(languageID)
	if openLanguageID == "" {
		openLanguageID = lspmanager.DetectLanguageID(file.Path.AbsPath)
	}
	if openLanguageID == sqliteSQLLanguageID {
		openLanguageID = "sql"
	}
	uri := fileURI(file.Path.AbsPath)
	if err := manager.DidOpen(ctx, uri, openLanguageID, 1, file.Content); shouldRetryDiagnosticBootstrap(ctx, err) {
		err = manager.DidOpen(ctx, uri, openLanguageID, 1, file.Content)
		if err == nil {
			return nil
		}
		return fmt.Errorf("open diagnostics document %s: %w", file.Path.DisplayPath, err)
	} else if err != nil {
		return fmt.Errorf("open diagnostics document %s: %w", file.Path.DisplayPath, err)
	}
	return nil
}

func shouldRetryDiagnosticBootstrap(ctx context.Context, err error) bool {
	return err != nil && ctx != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) && strings.Contains(err.Error(), "initialize LSP client")
}

func fetchDeletedLanguageOverrideDiagnostics(ctx context.Context, manager lspmanager.Manager, target diagnosticTarget) ([]protocol.PublishDiagnosticsParams, string, string, error) {
	items, err := manager.Diagnostics(ctx, []string{target.URI})
	if err != nil {
		return nil, "", "", err
	}
	filtered := diagnosticsForTargetURI(target.URI, items)
	message := diagnosticsMessageAfterFetch("", []string{target.URI}, filtered)
	return filtered, "language_override", message, nil
}

func reopenManagerDocumentForDiagnostics(ctx context.Context, manager lspmanager.Manager, uri string) error {
	reopener, ok := manager.(lspmanager.DiagnosticDocumentReopener)
	if !ok {
		return fmt.Errorf("%w: diagnostics document reopen", lspmanager.ErrUnsupportedCapability)
	}
	if err := reopener.ReopenDocumentForDiagnostics(ctx, uri); err != nil {
		return fmt.Errorf("reopen diagnostics document %s: %w", uri, err)
	}
	return nil
}

func diagnosticsForTargetURI(uri string, items []protocol.PublishDiagnosticsParams) []protocol.PublishDiagnosticsParams {
	for _, item := range items {
		if sameDiagnosticURI(item.URI, uri) {
			return []protocol.PublishDiagnosticsParams{item}
		}
	}
	return []protocol.PublishDiagnosticsParams{{URI: uri}}
}

func sameDiagnosticURI(left, right string) bool {
	if left == right {
		return true
	}
	leftPath, leftErr := format.AbsolutePathFromURI(left)
	rightPath, rightErr := format.AbsolutePathFromURI(right)
	return leftErr == nil && rightErr == nil && sameDiagnosticPath(leftPath, rightPath)
}

func (h handlerBase) waitSingleFileOverrideDiagnosticsStableWithStartupRetries(ctx context.Context, manager lspmanager.Manager, uri string) error {
	var lastErr error
	for retry := 1; retry <= diagnosticsStartupRetryCount; retry++ {
		if err := sleepDiagnosticsRetryBackoff(ctx, retry); err != nil {
			return err
		}
		if err := manager.WaitDiagnosticsStable(ctx, []string{uri}); err != nil {
			if !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
				return err
			}
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func resolveRoot(raw string) string {
	root, err := search.NormalizeRoot(raw)
	if err == nil {
		return root
	}
	root, _ = search.NormalizeRoot("")
	return root
}

func splitNormalizedLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

func appendMessage(current, extra string) string {
	switch {
	case strings.TrimSpace(current) == "":
		return extra
	case strings.TrimSpace(extra) == "":
		return current
	default:
		return current + "; " + extra
	}
}

func fileURI(absPath string) string {
	path := filepath.ToSlash(absPath)
	if filepath.VolumeName(absPath) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
