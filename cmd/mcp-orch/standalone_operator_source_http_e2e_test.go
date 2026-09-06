//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/operatorsource"
)

// TestMCPOrchServeOperatorSourceHTTPFence proves revoked source blocks existing sessions.
func TestMCPOrchServeOperatorSourceHTTPFence(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	process := startServeE2E(t, ctx, binary, fixture)
	clientA, clientB := &http.Client{Timeout: 5 * time.Second}, &http.Client{Timeout: 5 * time.Second}
	sessionA, _ := initializeServeE2E(t, clientA, process.ready.Endpoint, fixture.token)
	sessionB, _ := initializeServeE2E(t, clientB, process.ready.Endpoint, fixture.token)
	assertServeTools(t, clientA, process.ready.Endpoint, fixture.token, sessionA, 2)
	assertServeTools(t, clientB, process.ready.Endpoint, fixture.token, sessionB, 2)
	cfg, err := standalone.LoadConfig(fixture.config)
	operatorSourceRequireNoError(t, err)
	db, err := newStandaloneDB(newStandalonePlatformConfig(cfg))
	operatorSourceRequireNoError(t, err)
	t.Cleanup(func() { operatorSourceRequireNoError(t, db.Close()) })
	store, err := operatorsource.NewStore(db)
	operatorSourceRequireNoError(t, err)
	source, err := store.GetLatest(ctx)
	operatorSourceRequireNoError(t, err)
	selector := operatorsource.Selector{PrincipalRef: source.PrincipalRef, ScopeRef: source.ScopeRef, AuthContext: source.AuthContext, SourceID: source.SourceID, SourceFenceRef: source.SourceFenceRef, Generation: source.Generation}
	revoked, err := store.Revoke(ctx, selector)
	operatorSourceRequireNoError(t, err)
	if revoked.State != "revoked" || revoked.SourceID != source.SourceID {
		t.Fatalf("unexpected revoke: %+v", revoked)
	}
	assertServeFenceDenied(t, clientA, process.ready.Endpoint, fixture.token, sessionA, 3)
	assertServeFenceDenied(t, clientB, process.ready.Endpoint, fixture.token, sessionB, 3)
	third := &http.Client{Timeout: 5 * time.Second}
	thirdReply := requestServeE2E(t, third, process.ready.Endpoint, fixture.token, "", 4, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "after-revoke", "version": "1"}})
	if thirdReply.status == http.StatusOK {
		t.Fatalf("initialize succeeded after source revoke: %s", thirdReply.body)
	}
	if err := stopServeProcessE2E(t, process, nil); err != nil {
		t.Fatal(err)
	}
}

func assertServeFenceDenied(t *testing.T, client *http.Client, endpoint, token, session string, id int) {
	t.Helper()
	reply := requestServeE2E(t, client, endpoint, token, session, id, "tools/call", map[string]any{"name": "list_agents", "arguments": map[string]any{"_meta": map[string]any{"principal_ref": "forged", "source_id": "forged"}}})
	decoded := decodeServeE2E(t, reply)
	if decoded.Error == nil && len(decoded.Result) == 0 {
		t.Fatalf("empty non-error denial response")
	}
	if decoded.Error == nil {
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(decoded.Result, &result); err != nil || !result.IsError {
			t.Fatalf("request was not denied: status=%d body=%s", reply.status, reply.body)
		}
	}
}
