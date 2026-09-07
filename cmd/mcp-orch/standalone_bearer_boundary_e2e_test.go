//go:build e2e

package main

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"
)

// TestMCPOrchServeBearerBoundary 验证真实 binary 的会话 ID 不能替代 bearer 认证。
func TestMCPOrchServeBearerBoundary(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	daemon := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 5 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	for _, authorization := range []string{"", "Bearer incorrect-test-token", "Basic " + fixture.token} {
		assertStandaloneBearerDenied(t, client, daemon.ready.Endpoint, session, authorization)
	}
	assertServeListAgents(t, client, daemon.ready.Endpoint, fixture.token, session, 7)
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatal(err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
}

func assertStandaloneBearerDenied(t *testing.T, client *http.Client, endpoint, session, authorization string) {
	t.Helper()
	request, cancel, err := newServeE2ERequest(t, client, http.MethodPost, endpoint, bytes.NewBufferString(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_agents","arguments":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Session-Id", session)
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status=%d, want 401", response.StatusCode)
	}
}
