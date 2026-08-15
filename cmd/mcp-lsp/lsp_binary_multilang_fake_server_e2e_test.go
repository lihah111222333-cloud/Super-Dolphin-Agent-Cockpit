//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func runFakeMultilangDiagnosticsLangserver() {
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	defer goroutines.Wait()
	journal := newFakeMultilangLifecycleJournal()
	server := &fakeMultilangDiagnosticsServer{
		writer: &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines},
		opened: make(map[string]fakeMultilangOpenedDocument),
	}
	journal.append("start", "")
	for {
		raw, err := readFakeLSPFramedMessage(reader)
		if err != nil {
			return
		}
		if handleFakeMultilangMessage(raw, server, journal) {
			return
		}
	}
}

func handleFakeMultilangMessage(raw []byte, server *fakeMultilangDiagnosticsServer, journal *fakeMultilangLifecycleJournal) bool {
	var req fakeLSPRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return false
	}
	if req.Method == "exit" {
		journal.append("exit", server.rootURI())
		return true
	}
	journal.recordRequest(req, server)
	if server.handleNotification(req) || len(bytes.TrimSpace(req.ID)) == 0 {
		return false
	}
	_ = server.writer.writeResponse(req.ID, server.result(req))
	return false
}

func (j *fakeMultilangLifecycleJournal) recordRequest(req fakeLSPRequest, server *fakeMultilangDiagnosticsServer) {
	if req.Method == "initialize" {
		server.setRootURI(fakeMultilangInitializeRootURI(req.Params))
		j.append("initialize", server.rootURI())
	} else if req.Method == "shutdown" {
		j.append("shutdown", server.rootURI())
	}
	j.append("request:"+req.Method, server.rootURI())
	if req.Method == "textDocument/hover" && j.server == "pyright-langserver" && fakeMultilangPendingRequestGateMissing() {
		j.append("pending", server.rootURI())
		waitForFakeMultilangPendingRequestRelease()
	}
}

type fakeMultilangDiagnosticsServer struct {
	mu     sync.Mutex
	writer *fakeLSPWriter
	opened map[string]fakeMultilangOpenedDocument
	events []string
	root   string
}

type fakeMultilangLifecycleJournal struct {
	path   string
	server string
}

type fakeMultilangLifecycleJournalEntry struct {
	AtUnixNano int64  `json:"at_unix_nano"`
	PID        int    `json:"pid"`
	Server     string `json:"server"`
	Event      string `json:"event"`
	RootURI    string `json:"root_uri,omitempty"`
}

func newFakeMultilangLifecycleJournal() *fakeMultilangLifecycleJournal {
	return &fakeMultilangLifecycleJournal{
		path:   strings.TrimSpace(os.Getenv(fakeMultilangLifecycleJournalEnv)),
		server: strings.TrimSpace(os.Getenv(fakeMultilangServerEnv)),
	}
}

func (j *fakeMultilangLifecycleJournal) append(event, rootURI string) {
	if j == nil || j.path == "" {
		return
	}
	entry := fakeMultilangLifecycleJournalEntry{
		AtUnixNano: time.Now().UnixNano(),
		PID:        os.Getpid(),
		Server:     j.server,
		Event:      event,
		RootURI:    rootURI,
	}
	file, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fakeMultilangProtocolViolation("open lifecycle journal: %v", err)
	}
	if err := json.NewEncoder(file).Encode(entry); err != nil {
		_ = file.Close()
		fakeMultilangProtocolViolation("write lifecycle journal: %v", err)
	}
	if err := file.Close(); err != nil {
		fakeMultilangProtocolViolation("close lifecycle journal: %v", err)
	}
}

func fakeMultilangInitializeRootURI(raw json.RawMessage) string {
	var params struct {
		RootURI          string `json:"rootUri"`
		WorkspaceFolders []struct {
			URI string `json:"uri"`
		} `json:"workspaceFolders"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		fakeMultilangProtocolViolation("decode initialize for lifecycle journal: %v", err)
	}
	if rootURI := strings.TrimSpace(params.RootURI); rootURI != "" {
		return rootURI
	}
	if len(params.WorkspaceFolders) > 0 {
		return strings.TrimSpace(params.WorkspaceFolders[0].URI)
	}
	return ""
}

func (s *fakeMultilangDiagnosticsServer) setRootURI(rootURI string) {
	s.mu.Lock()
	s.root = rootURI
	s.mu.Unlock()
}

func (s *fakeMultilangDiagnosticsServer) rootURI() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root
}

type fakeMultilangOpenedDocument struct {
	languageID string
	version    int
	text       string
}

type fakeMultilangDidOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
		Version    int    `json:"version"`
		Text       string `json:"text"`
	} `json:"textDocument"`
}

type fakeMultilangDidCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

type fakeMultilangDidChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Range       json.RawMessage `json:"range"`
		RangeLength *int            `json:"rangeLength"`
		Text        string          `json:"text"`
	} `json:"contentChanges"`
}

type fakeMultilangDiagnosticParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *fakeMultilangDiagnosticsServer) handleNotification(req fakeLSPRequest) bool {
	if len(bytes.TrimSpace(req.ID)) != 0 {
		return false
	}
	switch req.Method {
	case "textDocument/didClose":
		s.handleDidClose(req.Params)
	case "textDocument/didChange":
		s.handleDidChange(req.Params)
	case "textDocument/didOpen":
		s.handleDidOpen(req.Params)
	}
	return true
}

func (s *fakeMultilangDiagnosticsServer) handleDidClose(raw json.RawMessage) {
	var params fakeMultilangDidCloseParams
	if err := json.Unmarshal(raw, &params); err != nil {
		fakeMultilangProtocolViolation("decode didClose: %v", err)
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.opened, uri)
	s.events = append(s.events, "close:"+uri)
}

func (s *fakeMultilangDiagnosticsServer) handleDidChange(raw json.RawMessage) {
	var params fakeMultilangDidChangeParams
	if err := json.Unmarshal(raw, &params); err != nil {
		fakeMultilangProtocolViolation("decode didChange: %v", err)
	}
	if len(params.ContentChanges) != 1 || len(bytes.TrimSpace(params.ContentChanges[0].Range)) != 0 || params.ContentChanges[0].RangeLength != nil {
		fakeMultilangProtocolViolation("didChange must contain one full-document change")
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	s.mu.Lock()
	defer s.mu.Unlock()
	document, ok := s.opened[uri]
	if !ok {
		fakeMultilangProtocolViolation("didChange for unopened URI %s", uri)
	}
	if params.TextDocument.Version <= document.version {
		fakeMultilangProtocolViolation("non-monotonic didChange for %s: %d <= %d", uri, params.TextDocument.Version, document.version)
	}
	document.version = params.TextDocument.Version
	document.text = params.ContentChanges[0].Text
	s.opened[uri] = document
	s.events = append(s.events, fmt.Sprintf("change:%s:%d", uri, document.version))
}

func (s *fakeMultilangDiagnosticsServer) handleDidOpen(raw json.RawMessage) {
	var params fakeMultilangDidOpenParams
	if err := json.Unmarshal(raw, &params); err != nil {
		fakeMultilangProtocolViolation("decode didOpen: %v", err)
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	languageID := strings.TrimSpace(params.TextDocument.LanguageID)
	if uri == "" || languageID == "" {
		fakeMultilangProtocolViolation("didOpen requires URI and languageId")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if document, alreadyOpen := s.opened[uri]; alreadyOpen {
		fakeMultilangProtocolViolation("duplicate didOpen for %s at version %d; current version %d", uri, params.TextDocument.Version, document.version)
	}
	s.opened[uri] = fakeMultilangOpenedDocument{
		languageID: languageID,
		version:    params.TextDocument.Version,
		text:       params.TextDocument.Text,
	}
	s.events = append(s.events, fmt.Sprintf("open:%s:%d", uri, params.TextDocument.Version))
}

func (s *fakeMultilangDiagnosticsServer) result(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		return fakeMultilangInitializeResult()
	case "textDocument/diagnostic":
		return s.fakeMultilangDiagnosticResult(req)
	default:
		if result, ok := s.fakeMultilangNavigationResult(req); ok {
			return result
		}
		return s.fakeMultilangEditingResult(req)
	}
}

func fakeMultilangInitializeResult() map[string]any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync":           1,
			"workspaceSymbolProvider":    true,
			"documentSymbolProvider":     true,
			"hoverProvider":              true,
			"definitionProvider":         true,
			"implementationProvider":     true,
			"typeDefinitionProvider":     true,
			"referencesProvider":         true,
			"completionProvider":         map[string]any{},
			"signatureHelpProvider":      map[string]any{},
			"foldingRangeProvider":       true,
			"callHierarchyProvider":      true,
			"typeHierarchyProvider":      true,
			"renameProvider":             true,
			"codeActionProvider":         true,
			"documentFormattingProvider": true,
			"semanticTokensProvider": map[string]any{
				"legend": map[string]any{"tokenTypes": []string{"variable"}, "tokenModifiers": []string{}},
				"full":   true,
			},
			"diagnosticProvider": map[string]any{
				"interFileDependencies": true,
				"workspaceDiagnostics":  false,
			},
		},
	}
}

func (s *fakeMultilangDiagnosticsServer) fakeMultilangDiagnosticResult(req fakeLSPRequest) map[string]any {
	if delay := fakeMultilangDiagnosticDelay(); delay > 0 {
		time.Sleep(delay)
	}
	uri, document := s.diagnosticTarget(req)
	return map[string]any{
		"kind":  "full",
		"items": fakeMultilangDiagnostics(uri, document),
	}
}

func (s *fakeMultilangDiagnosticsServer) fakeMultilangNavigationResult(req fakeLSPRequest) (any, bool) {
	switch req.Method {
	case "textDocument/documentSymbol":
		return []map[string]any{{
			"name": "FakeSymbol",
			"kind": 12,
			"range": map[string]any{
				"start": map[string]int{"line": 0, "character": 0},
				"end":   map[string]int{"line": 0, "character": 1},
			},
			"selectionRange": map[string]any{
				"start": map[string]int{"line": 0, "character": 0},
				"end":   map[string]int{"line": 0, "character": 1},
			},
		}}, true
	case "textDocument/hover":
		return map[string]any{"contents": map[string]any{"kind": "plaintext", "value": "FakeHover"}}, true
	case "textDocument/definition", "textDocument/implementation", "textDocument/typeDefinition":
		return []map[string]any{fakeMultilangLocation(req)}, true
	case "textDocument/references":
		return []map[string]any{fakeMultilangLocation(req)}, true
	case "textDocument/completion":
		return map[string]any{
			"isIncomplete": false,
			"items":        []map[string]any{{"label": "FakeCompletion", "kind": 3, "insertText": "FakeCompletion"}},
		}, true
	case "textDocument/signatureHelp":
		return map[string]any{"signatures": []map[string]any{{"label": "FakeSignature(value)"}}}, true
	default:
		return nil, false
	}
}

func (s *fakeMultilangDiagnosticsServer) fakeMultilangEditingResult(req fakeLSPRequest) any {
	switch req.Method {
	case "textDocument/foldingRange":
		return []map[string]any{{"startLine": 0, "endLine": 1}}
	case "textDocument/semanticTokens/full":
		return map[string]any{"data": []int{0, 0, 1, 0, 0}}
	case "textDocument/prepareCallHierarchy", "textDocument/prepareTypeHierarchy":
		return []any{}
	case "textDocument/codeAction", "textDocument/formatting":
		return []any{}
	case "textDocument/rename":
		return map[string]any{"changes": map[string]any{}}
	case "workspace/symbol":
		return s.workspaceSymbols(req)
	case "shutdown":
		return nil
	default:
		return nil
	}
}

func fakeMultilangLocation(req fakeLSPRequest) map[string]any {
	return map[string]any{
		"uri": fakeMultilangTextDocumentURI(req),
		"range": map[string]any{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": 0, "character": 1},
		},
	}
}

func fakeMultilangTextDocumentURI(req fakeLSPRequest) string {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		fakeMultilangProtocolViolation("decode %s text document: %v", req.Method, err)
	}
	if strings.TrimSpace(params.TextDocument.URI) == "" {
		fakeMultilangProtocolViolation("%s requires text document URI", req.Method)
	}
	return params.TextDocument.URI
}

func (s *fakeMultilangDiagnosticsServer) workspaceSymbols(req fakeLSPRequest) []map[string]any {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		fakeMultilangProtocolViolation("decode workspace/symbol: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "request:"+params.Query)
	result := make([]map[string]any, 0, len(s.opened))
	for uri, document := range s.opened {
		name := staleWorkspaceSymbolName(document.languageID)
		if strings.Contains(document.text, freshWorkspaceSymbolName(document.languageID)) {
			name = freshWorkspaceSymbolName(document.languageID)
		}
		if !strings.Contains(name, params.Query) {
			continue
		}
		result = append(result, map[string]any{
			"name": name,
			"kind": 13,
			"location": map[string]any{
				"uri":   uri,
				"range": map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": len(name)}},
			},
		})
	}
	return result
}

func fakeMultilangProtocolViolation(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "fake multilang LSP protocol violation: "+format+"\n", args...)
	os.Exit(3)
}

func waitForFakeMultilangPendingRequestRelease() {
	gatePath := strings.TrimSpace(os.Getenv(fakeMultilangPendingRequestGateEnv))
	if gatePath == "" {
		return
	}
	for {
		_, err := os.Stat(gatePath)
		if err == nil {
			return
		}
		if !os.IsNotExist(err) {
			fakeMultilangProtocolViolation("stat pending request gate %s: %v", gatePath, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func fakeMultilangPendingRequestGateMissing() bool {
	gatePath := strings.TrimSpace(os.Getenv(fakeMultilangPendingRequestGateEnv))
	if gatePath == "" {
		return false
	}
	_, err := os.Stat(gatePath)
	if err == nil {
		return false
	}
	if !os.IsNotExist(err) {
		fakeMultilangProtocolViolation("stat pending request gate %s: %v", gatePath, err)
	}
	return true
}

func (s *fakeMultilangDiagnosticsServer) diagnosticTarget(req fakeLSPRequest) (string, fakeMultilangOpenedDocument) {
	var params fakeMultilangDiagnosticParams
	_ = json.Unmarshal(req.Params, &params)
	uri := strings.TrimSpace(params.TextDocument.URI)
	s.mu.Lock()
	defer s.mu.Unlock()
	document := s.opened[uri]
	if strings.TrimSpace(document.languageID) == "" {
		document.languageID = "unknown"
	}
	return uri, document
}

func fakeMultilangDiagnosticDelay() time.Duration {
	raw := strings.TrimSpace(os.Getenv(fakeMultilangDiagnosticDelayEnv))
	if raw == "" {
		return 0
	}
	delay, err := time.ParseDuration(raw)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "invalid %s %q: %v\n", fakeMultilangDiagnosticDelayEnv, raw, err)
		os.Exit(2)
	}
	return delay
}

func fakeMultilangDiagnostics(uri string, document fakeMultilangOpenedDocument) []map[string]any {
	return []map[string]any{{
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 0, "character": 1},
		},
		"severity": 1,
		"source":   "fake-" + document.languageID,
		"message":  fmt.Sprintf("fake cold-start diagnostic for %s in %s: %s", document.languageID, filepath.Base(uri), strings.TrimSpace(document.text)),
		"code":     "cold-start",
	}}
}
