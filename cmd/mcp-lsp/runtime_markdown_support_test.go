package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestRuntimeMarkdownConfigurationSettingsEnableOnlyRealPathCompletion(t *testing.T) {
	settings := runtimeMarkdownConfigurationSettings()
	markdown, ok := settings["markdown"].(map[string]any)
	if !ok {
		t.Fatalf("markdown settings = %#v, want nested map", settings)
	}
	suggest, ok := markdown["suggest"].(map[string]any)
	if !ok {
		t.Fatalf("markdown suggest settings = %#v, want nested map", markdown["suggest"])
	}
	paths, ok := suggest["paths"].(map[string]any)
	if !ok || paths["enabled"] != true {
		t.Fatalf("markdown path completion settings = %#v, want enabled=true", suggest["paths"])
	}
	validate, ok := markdown["validate"].(map[string]any)
	if !ok || validate["enabled"] != false {
		t.Fatalf("markdown validation settings = %#v, want enabled=false", markdown["validate"])
	}
}

func TestRuntimeMarkdownNotifyConfigurationUsesDocumentedWorkspaceNotification(t *testing.T) {
	client := &runtimeMarkdownConfigurationTestClient{}
	if err := runtimeMarkdownNotifyConfiguration(context.Background(), client); err != nil {
		t.Fatalf("runtimeMarkdownNotifyConfiguration() error = %v", err)
	}
	if len(client.notifications) != 1 {
		t.Fatalf("notification count = %d, want one", len(client.notifications))
	}
	if client.notifications[0].method != "workspace/didChangeConfiguration" {
		t.Fatalf("notification method = %q, want workspace/didChangeConfiguration", client.notifications[0].method)
	}
	params, ok := client.notifications[0].params.(map[string]any)
	if !ok {
		t.Fatalf("notification params = %#v, want map", client.notifications[0].params)
	}
	if _, ok := params["settings"].(map[string]any); !ok {
		t.Fatalf("notification settings = %#v, want nested settings map", params["settings"])
	}
}

type runtimeMarkdownConfigurationTestClient struct {
	notifications []struct {
		method string
		params any
	}
}

func (c *runtimeMarkdownConfigurationTestClient) Initialize(context.Context, string) error {
	return nil
}

func (c *runtimeMarkdownConfigurationTestClient) Shutdown(context.Context) error { return nil }

func (c *runtimeMarkdownConfigurationTestClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return nil, nil
}

func (c *runtimeMarkdownConfigurationTestClient) Notify(_ context.Context, method string, params any) error {
	c.notifications = append(c.notifications, struct {
		method string
		params any
	}{method: method, params: params})
	return nil
}

func (c *runtimeMarkdownConfigurationTestClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (c *runtimeMarkdownConfigurationTestClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}

func (c *runtimeMarkdownConfigurationTestClient) DidClose(context.Context, string) error { return nil }

func (c *runtimeMarkdownConfigurationTestClient) Close() error { return nil }

var _ multilsp.Client = (*runtimeMarkdownConfigurationTestClient)(nil)
