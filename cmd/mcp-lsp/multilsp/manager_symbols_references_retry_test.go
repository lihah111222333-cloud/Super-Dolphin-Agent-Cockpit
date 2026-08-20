package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestDecodeDocumentSymbolUnionFlatSymbolInformationPreservesRange(t *testing.T) {
	raw := json.RawMessage(`{"name":"loadlib","kind":12,"location":{"uri":"file:///tmp/sourcing.sh","range":{"start":{"line":20,"character":0},"end":{"line":22,"character":1}}}}`)

	got, ok, err := decodeDocumentSymbolUnion(raw)
	if err != nil || !ok {
		t.Fatalf("decodeDocumentSymbolUnion() = %#v, %v, want successful flat SymbolInformation decode", got, err)
	}
	if got.Name != "loadlib" ||
		got.Range.Start.Line != 20 ||
		got.Range.Start.Character != 0 ||
		got.Range.End.Line != 22 ||
		got.Range.End.Character != 1 ||
		got.SelectionRange != got.Range {
		t.Fatalf("decoded symbol = %#v, want range 20:0-22:1", got)
	}
}

func TestReferencesRetriesFrontendColdStartUntilNonEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		ext  string
	}{
		{name: "javascript", ext: ".js"},
		{name: "javascriptreact", ext: ".jsx"},
		{name: "typescript", ext: ".ts"},
		{name: "typescriptreact", ext: ".tsx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &coldStartReferencesClient{projectReadyAfterDocumentSymbols: 2}
			mgr, target := newReferencesRetryTestManager(t, tc.ext, client)
			root := filepath.Dir(filepath.Dir(target))
			writeGenericTestFile(t, filepath.Join(root, "src", "consumer_one"+tc.ext), "export const one = value\n")
			writeGenericTestFile(t, filepath.Join(root, "src", "consumer_two"+tc.ext), "export const two = value\n")

			results, err := mgr.References(
				ctxWithCWD(filepath.Dir(filepath.Dir(target)), "agent-references-cold-start", tc.name),
				target,
				protocol.Position{Line: 0, Character: 13},
				false,
			)
			if err != nil {
				t.Fatalf("References() error = %v", err)
			}
			if len(results) != 1 || results[0].Location == nil {
				t.Fatalf("References() results = %#v, want exact non-empty location", results)
			}
			if got := client.referenceRequestCount(); got != 2 {
				t.Fatalf("reference request count = %d, want initial empty response plus ready retry", got)
			}
			if got := client.documentSymbolRequestCount(); got != 2 {
				t.Fatalf("document symbol request count = %d, want both consumers acknowledged before retry", got)
			}
		})
	}
}

func TestReferencesDoesNotRetryOtherLanguages(t *testing.T) {
	client := &coldStartReferencesClient{emptyResponses: 100}
	mgr, target := newReferencesRetryTestManager(t, ".css", client)

	results, err := mgr.References(
		ctxWithCWD(filepath.Dir(filepath.Dir(target)), "agent-references-css", "thread-css"),
		target,
		protocol.Position{Line: 0, Character: 13},
		false,
	)
	if err != nil {
		t.Fatalf("References() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("References() results = %#v, want empty", results)
	}
	if got := client.referenceRequestCount(); got != 1 {
		t.Fatalf("reference request count = %d, want one request for non-frontend language", got)
	}
}

func TestReferencesTrulyEmptyCompletesWithinBound(t *testing.T) {
	client := &coldStartReferencesClient{emptyResponses: 100}
	mgr, target := newReferencesRetryTestManager(t, ".ts", client)

	ctx, cancel := context.WithTimeout(
		ctxWithCWD(filepath.Dir(filepath.Dir(target)), "agent-references-empty", "thread-empty"),
		10*time.Second,
	)
	defer cancel()
	started := time.Now()
	results, err := mgr.References(ctx, target, protocol.Position{Line: 0, Character: 13}, false)
	if err != nil {
		t.Fatalf("References() error = %v, want bounded empty result", err)
	}
	if len(results) != 0 {
		t.Fatalf("References() results = %#v, want empty", results)
	}
	if elapsed := time.Since(started); elapsed >= 10*time.Second {
		t.Fatalf("References() elapsed = %s, want bounded completion", elapsed)
	}
	if got := client.referenceRequestCount(); got != 2 {
		t.Fatalf("reference request count = %d, want 1 initial plus 1 bounded ready retry", got)
	}
}

func TestReferencesRetryStopsImmediatelyOnCancellation(t *testing.T) {
	client := &coldStartReferencesClient{emptyResponses: 100}
	mgr, target := newReferencesRetryTestManager(t, ".tsx", client)
	ctx, cancel := context.WithCancel(
		ctxWithCWD(filepath.Dir(filepath.Dir(target)), "agent-references-cancel", "thread-cancel"),
	)
	cancel()

	started := time.Now()
	_, err := mgr.References(ctx, target, protocol.Position{Line: 0, Character: 13}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("References() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("References() cancellation elapsed = %s, want immediate exit", elapsed)
	}
	if got := client.referenceRequestCount(); got > 1 {
		t.Fatalf("reference request count = %d, want at most initial request", got)
	}
}

func newReferencesRetryTestManager(
	t *testing.T,
	ext string,
	client Client,
) (Manager, string) {
	t.Helper()
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"references-retry"}`)
	target := filepath.Join(root, "src", "declaration"+ext)
	writeGenericTestFile(t, target, "export const value = 1\n")
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	})
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})
	return mgr, target
}

type coldStartReferencesClient struct {
	noopClient

	mu                               sync.Mutex
	requests                         int
	emptyResponses                   int
	documentSymbolRequests           int
	projectReadyAfterDocumentSymbols int
	openedDocuments                  map[string]struct{}
}

func (c *coldStartReferencesClient) DidOpen(_ context.Context, uri, _ string, _ int, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.openedDocuments == nil {
		c.openedDocuments = make(map[string]struct{})
	}
	c.openedDocuments[uri] = struct{}{}
	return nil
}

func (c *coldStartReferencesClient) Request(
	_ context.Context,
	method string,
	params any,
) (json.RawMessage, error) {
	if method == protocol.MethodDocumentSymbol {
		c.mu.Lock()
		defer c.mu.Unlock()
		documentParams, ok := params.(protocol.DocumentSymbolParams)
		if !ok {
			return json.RawMessage("[]"), nil
		}
		if _, ok := c.openedDocuments[documentParams.TextDocument.URI]; !ok {
			return json.RawMessage("[]"), nil
		}
		c.documentSymbolRequests++
		return json.RawMessage("[]"), nil
	}
	if method != protocol.MethodReferences {
		return json.RawMessage("null"), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	if c.documentSymbolRequests < c.projectReadyAfterDocumentSymbols {
		return json.RawMessage("[]"), nil
	}
	if c.requests <= c.emptyResponses {
		return json.RawMessage("[]"), nil
	}
	return json.RawMessage(`[{"uri":"file:///consumer.ts","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":7}}}]`), nil
}

func (c *coldStartReferencesClient) referenceRequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func TestSwiftSemanticRequestsSynchronizeWorkspaceBeforeCompletionAndReferences(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
	}{
		{name: "completion", method: protocol.MethodCompletion},
		{name: "references", method: protocol.MethodReferences},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeGenericTestFile(t, filepath.Join(root, "Package.swift"), "// swift-tools-version: 6.0\nimport PackageDescription\nlet package = Package(name: \"LSPFixture\", targets: [.executableTarget(name: \"LSPFixture\")])\n")
			target := filepath.Join(root, "Sources", "LSPFixture", "Greeting.swift")
			writeGenericTestFile(t, target, "struct Greeting { let name: String }\n")
			client := &swiftWorkspaceSynchronizationClient{}
			mgr := NewManager(Config{
				WorkspaceRoot: root,
				ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
					return client, nil
				}),
			})
			t.Cleanup(func() {
				if err := mgr.Close(); err != nil {
					t.Errorf("close manager: %v", err)
				}
			})

			ctx, cancel := context.WithTimeout(ctxWithCWD(root, "agent-swift-semantic", "thread-swift"), 2*time.Second)
			defer cancel()
			var err error
			switch tc.method {
			case protocol.MethodCompletion:
				_, err = mgr.Completion(ctx, target, protocol.Position{Line: 0, Character: 8})
			case protocol.MethodReferences:
				_, err = mgr.References(ctx, target, protocol.Position{Line: 0, Character: 8}, false)
			}
			if err != nil {
				t.Fatalf("%s error = %v, want synchronized semantic request", tc.method, err)
			}
			if got, want := client.requestMethods(), []string{"workspace/synchronize", tc.method}; !reflect.DeepEqual(got, want) {
				t.Fatalf("request methods = %#v, want %#v", got, want)
			}
		})
	}
}

type swiftWorkspaceSynchronizationClient struct {
	noopClient

	mu           sync.Mutex
	synchronized bool
	requests     []string
}

func (c *swiftWorkspaceSynchronizationClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, method)
	switch method {
	case "workspace/synchronize":
		payload, ok := params.(map[string]any)
		if !ok || payload["index"] != true {
			return nil, errors.New("workspace/synchronize must request index=true")
		}
		c.synchronized = true
		return json.RawMessage("null"), nil
	case protocol.MethodCompletion:
		if !c.synchronized {
			return nil, context.DeadlineExceeded
		}
		return json.RawMessage(`{"isIncomplete":false,"items":[]}`), nil
	case protocol.MethodReferences:
		if !c.synchronized {
			return nil, context.DeadlineExceeded
		}
		return json.RawMessage("[]"), nil
	default:
		return json.RawMessage("null"), nil
	}
}

func (c *swiftWorkspaceSynchronizationClient) requestMethods() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requests...)
}

func (c *coldStartReferencesClient) documentSymbolRequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.documentSymbolRequests
}
