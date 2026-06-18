package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAddServersRejectsUnsafeHTTPHeaders(t *testing.T) {
	svc := NewServiceWithStore(newMemoryMCPServerStore())
	t.Chdir(t.TempDir())

	_, err := svc.AddServers(context.Background(), AddServersRequest{
		MCPServers: map[string]ServerConfig{
			"bad": {
				Transport: "http",
				URL:       "https://example.com/mcp",
				Headers:   map[string]string{"Host": "169.254.169.254"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe header") {
		t.Fatalf("AddServers() error = %v, want unsafe header rejection", err)
	}
}

func TestListServerToolsRequestsHTTPMCPServer(t *testing.T) {
	store := newMemoryMCPServerStore()
	client := &scriptedMCPHTTPDoer{t: t, wantAuth: "Bearer YOUR_API_KEY"}
	svc := NewServiceWithStore(store).(*service)
	svc.httpClient = client
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
		Headers:   map[string]string{"Authorization": "Bearer YOUR_API_KEY"},
	})

	got, err := svc.ListServerTools(context.Background(), ListServerToolsRequest{ServerName: "my-search"})
	if err != nil {
		t.Fatalf("ListServerTools() error = %v", err)
	}

	if got.ConfigPath != filepath.Join(project, ".agent", "mcp_server", "config.json") {
		t.Fatalf("ConfigPath = %q", got.ConfigPath)
	}
	if got.ServerName != "my-search" {
		t.Fatalf("ServerName = %q, want my-search", got.ServerName)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "remote_search" {
		t.Fatalf("Tools = %#v, want remote_search", got.Tools)
	}
	if !slices.Equal(client.methods, []string{"initialize", "notifications/initialized", "tools/list"}) {
		t.Fatalf("methods = %#v", client.methods)
	}
}

func TestListServerToolsRejectsLoopbackHTTPURLBeforeRequest(t *testing.T) {
	store := newMemoryMCPServerStore()
	client := &recordingFailHTTPDoer{}
	svc := NewServiceWithStore(store).(*service)
	svc.httpClient = client
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "private", ServerConfig{
		Transport: "http",
		URL:       "http://127.0.0.1:8080/mcp",
	})

	_, err := svc.ListServerTools(context.Background(), ListServerToolsRequest{ServerName: "private"})
	if err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatalf("ListServerTools() error = %v, want private network rejection", err)
	}
	if client.called {
		t.Fatal("ListServerTools() sent HTTP request before rejecting private URL")
	}
}

type recordingFailHTTPDoer struct {
	called bool
}

func (d *recordingFailHTTPDoer) Do(*http.Request) (*http.Response, error) {
	d.called = true
	return nil, errors.New("unexpected HTTP request")
}

type scriptedMCPHTTPDoer struct {
	t              *testing.T
	wantAuth       string
	toolsListError bool
	methods        []string
}

func (d *scriptedMCPHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	d.t.Helper()
	d.requireMCPHTTPRequest(req)
	call, err := decodeScriptedMCPHTTPCall(req)
	if err != nil {
		return nil, err
	}
	d.methods = append(d.methods, call.Method)
	return d.responseForMCPMethod(call, req)
}

func (d *scriptedMCPHTTPDoer) requireMCPHTTPRequest(req *http.Request) {
	d.t.Helper()
	if req.Method != http.MethodPost {
		d.t.Fatalf("method = %q, want POST", req.Method)
	}
	if d.wantAuth != "" && req.Header.Get("Authorization") != d.wantAuth {
		d.t.Fatalf("Authorization = %q, want %q", req.Header.Get("Authorization"), d.wantAuth)
	}
	accept := req.Header.Get("Accept")
	if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
		d.t.Fatalf("Accept = %q, want JSON and event stream", accept)
	}
}

type scriptedMCPHTTPCall struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
}

func decodeScriptedMCPHTTPCall(req *http.Request) (scriptedMCPHTTPCall, error) {
	var call scriptedMCPHTTPCall
	if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
		return scriptedMCPHTTPCall{}, err
	}
	return call, nil
}

func (d *scriptedMCPHTTPDoer) responseForMCPMethod(
	call scriptedMCPHTTPCall,
	req *http.Request,
) (*http.Response, error) {
	switch call.Method {
	case "initialize":
		return scriptedMCPHTTPResponse(call.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "test-mcp", "version": "dev"},
		}, false), nil
	case "notifications/initialized":
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	case "tools/list":
		if d.toolsListError {
			return scriptedMCPHTTPResponse(call.ID, map[string]any{
				"code":    -32603,
				"message": "boom",
			}, true), nil
		}
		return scriptedMCPHTTPResponse(call.ID, map[string]any{
			"tools": []map[string]any{{
				"name":        "remote_search",
				"description": "search remote docs",
				"inputSchema": map[string]any{"type": "object"},
			}},
		}, false), nil
	default:
		d.t.Fatalf("unexpected JSON-RPC method %q", call.Method)
		return nil, errors.New("unexpected method")
	}
}

func scriptedMCPHTTPResponse(id json.RawMessage, payload map[string]any, isError bool) *http.Response {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
	}
	if isError {
		resp["error"] = payload
	} else {
		resp["result"] = payload
	}
	raw, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(string(raw))),
		Header:     make(http.Header),
	}
}
