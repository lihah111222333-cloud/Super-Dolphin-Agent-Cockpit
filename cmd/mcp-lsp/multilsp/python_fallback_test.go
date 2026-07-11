package multilsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestPythonConstantDocumentSymbolsUseStaticFallbackWithoutStartingClient(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "constant.py")
	writeGenericTestFile(t, target, strings.Join([]string{
		"from typing import TypeVar",
		"",
		"import numpy as np",
		"import pandas as pd",
		"",
		`REG_CN = "cn"`,
		"EPS = 1e-12",
		`ONE_DAY = pd.Timedelta("1day")`,
		`float_or_ndarray = TypeVar("float_or_ndarray", float, np.ndarray)`,
		"",
	}, "\n"))
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol(python constants): %v", err)
	}
	assertSymbolNames(t, symbols, []string{"REG_CN", "EPS", "ONE_DAY", "float_or_ndarray"})
	if got := factory.callCount(); got != 0 {
		t.Fatalf("python constant fallback started %d LSP clients", got)
	}
}

func TestPythonRegularDocumentSymbolsUseLanguageServer(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "app.py")
	writeGenericTestFile(t, target, strings.Join([]string{
		"class Service:",
		"    def run(self):",
		"        return 1",
		"",
	}, "\n"))
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	if _, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target); err != nil {
		t.Fatalf("DocumentSymbol(python regular module): %v", err)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("regular python document_symbol started %d LSP clients, want 1", got)
	}
}

func TestPythonConstantDocumentSymbolsIgnoreTripleQuotedExamples(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "constant.py")
	writeGenericTestFile(t, target, strings.Join([]string{
		`"""`,
		"class Fake:",
		"    pass",
		"FAKE_VALUE = 1",
		"def fake():",
		"    return 1",
		`"""`,
		"",
		"REAL_VALUE = 2",
		"",
	}, "\n"))
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol(python constants with docstring): %v", err)
	}
	assertSymbolNames(t, symbols, []string{"REAL_VALUE"})
	if got := factory.callCount(); got != 0 {
		t.Fatalf("python constant fallback started %d LSP clients", got)
	}
}

func TestPythonEmptyConstantDocumentSymbolsReturnEmptyFallback(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "constant.py")
	writeGenericTestFile(t, target, strings.Join([]string{
		`"""only module documentation"""`,
		"from typing import Final",
		"",
	}, "\n"))
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol(empty python constants): %v", err)
	}
	if len(symbols) != 0 {
		t.Fatalf("symbols = %#v, want empty constant fallback", symbols)
	}
	if got := factory.callCount(); got != 0 {
		t.Fatalf("empty python constant fallback started %d LSP clients", got)
	}
}

func assertSymbolNames(t *testing.T, symbols []protocol.DocumentSymbol, names []string) {
	t.Helper()
	if len(symbols) != len(names) {
		t.Fatalf("symbols = %#v, want names %v", symbols, names)
	}
	for i, name := range names {
		if symbols[i].Name != name {
			t.Fatalf("symbol[%d] = %q, want %q; symbols=%#v", i, symbols[i].Name, name, symbols)
		}
	}
}
