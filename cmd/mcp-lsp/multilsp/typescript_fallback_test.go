package multilsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestTypeScriptDocumentSymbolUsesSyntacticFallbackWhenLSPReturnsEmpty(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "user-ui-v2", "src", "lib", "market", "Datafeed.ts")
	writeGenericTestFile(t, target, strings.Join([]string{
		"type TradeCallback = (data: DatafeedTrade[]) => void;",
		"const MOCK_REALTIME_MODE = false;",
		"",
		"export class WJDatafeed implements Datafeed {",
		"    private static _instance: WJDatafeed | null = null;",
		"    private _ws: WebSocket | null = null;",
		"",
		"    private constructor() { if (!MOCK_REALTIME_MODE) this._connect(); }",
		"",
		"    public static getInstance(): WJDatafeed { if (!this._instance) this._instance = new WJDatafeed(); return this._instance; }",
		"",
		"    private _connect() {",
		"        this._ws = new WebSocket('wss://example.test/ws');",
		"        this._ws.onmessage = (event) => {",
		"            const rawPayload = JSON.parse(event.data) as unknown;",
		"            if (!rawPayload) throw new Error('datafeed payload is required');",
		"        };",
		"    }",
		"",
		"    async searchSymbols(search?: string): Promise<SymbolInfo[]> {",
		"        return [];",
		"    }",
		"}",
		"",
	}, "\n"))
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
	}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol(TypeScript Datafeed): %v", err)
	}
	requireSymbolNamesContain(t, collectDocumentSymbolNames(symbols), []string{
		"TradeCallback",
		"MOCK_REALTIME_MODE",
		"WJDatafeed",
		"constructor",
		"getInstance",
		"_connect",
		"searchSymbols",
	})
	if got := factory.callCount(); got != 1 {
		t.Fatalf("TypeScript syntactic fallback should run only after one LSP client attempt, got %d client(s)", got)
	}
}

func TestTypeScriptFallbackTracksClassWithNextLineBrace(t *testing.T) {
	symbols := parseJSTSSymbols(splitLines(strings.Join([]string{
		"export class WJDatafeed",
		"{",
		"    private _ws: WebSocket | null = null;",
		"",
		"    private _connect() {",
		"        this._ws = new WebSocket('wss://example.test/ws');",
		"    }",
		"}",
		"",
	}, "\n")))

	requireSymbolNamesContain(t, collectDocumentSymbolNames(symbols), []string{
		"WJDatafeed",
		"_ws",
		"_connect",
	})
}

func TestTypeScriptFallbackIgnoresSymbolsInsideMultilineTemplate(t *testing.T) {
	symbols := parseJSTSSymbols(splitLines(strings.Join([]string{
		"const template = `",
		"class FakeFromTemplate {",
		"    fakeMethod() {}",
		"}",
		"function fakeFunction() {}",
		"type FakeAlias = string;",
		"`;",
		"",
		"export class RealDatafeed {",
		"    realMethod() { return template; }",
		"}",
		"",
	}, "\n")))

	names := collectDocumentSymbolNames(symbols)
	requireSymbolNamesContain(t, names, []string{
		"template",
		"RealDatafeed",
		"realMethod",
	})
	requireSymbolNamesNotContain(t, names, []string{
		"FakeFromTemplate",
		"fakeMethod",
		"fakeFunction",
		"FakeAlias",
	})
}

func collectDocumentSymbolNames(symbols []protocol.DocumentSymbol) []string {
	names := make([]string, 0, len(symbols))
	var walk func([]protocol.DocumentSymbol)
	walk = func(items []protocol.DocumentSymbol) {
		for _, symbol := range items {
			names = append(names, symbol.Name)
			walk(symbol.Children)
		}
	}
	walk(symbols)
	return names
}

func requireSymbolNamesContain(t *testing.T, got []string, want []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(got))
	for _, name := range got {
		seen[name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := seen[name]; !ok {
			t.Fatalf("symbols = %v, missing %q", got, name)
		}
	}
}

func requireSymbolNamesNotContain(t *testing.T, got []string, want []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(got))
	for _, name := range got {
		seen[name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := seen[name]; ok {
			t.Fatalf("symbols = %v, unexpected %q", got, name)
		}
	}
}
