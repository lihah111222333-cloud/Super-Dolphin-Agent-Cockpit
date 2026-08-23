//go:build e2e && (linux || windows)

package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func callMCPToolForScopedE2E(client *mcpLSPBinaryClient, name string, args map[string]any, cwd string, workspaceRoots []string) (mcpLSPBinaryResponse, error) {
	params := map[string]any{"name": name, "arguments": args, "_cwd": cwd, "_workspaceRoots": workspaceRoots}
	req := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "tools/call", "params": params}
	raw, err := json.Marshal(req)
	if err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("marshal tools/call request: %w", err)
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("write tools/call request: %w", err)
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("read tools/call response: %w", err)
	}
	var response mcpLSPBinaryResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("unmarshal tools/call response: %w", err)
	}
	if response.Error != nil {
		return mcpLSPBinaryResponse{}, fmt.Errorf("tools/call JSON-RPC error %d: %s", response.Error.Code, response.Error.Message)
	}
	return response, nil
}

func callMcpLSPBinaryRaw(t *testing.T, client *mcpLSPBinaryClient, method string, params map[string]any) json.RawMessage {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write %s request: %v; stderr=%s", method, err, client.stderrString())
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s response: %v; stderr=%s", method, err, client.stderrString())
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode %s response: %v; raw=%s", method, err, line)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		t.Fatalf("%s returned JSON-RPC error: %s", method, response.Error)
	}
	return response.Result
}
