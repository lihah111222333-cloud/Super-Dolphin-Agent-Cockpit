package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

type taskWorkspaceFixture struct {
	trustedRoot string
	trustedCWD  string
	outsideRoot string
	outsideCWD  string
}

func TestHandleCreateDAGRejectsUntrustedAutomationWorkspaceBeforeWrite(t *testing.T) {
	tests := []struct {
		name       string
		config     func(*testing.T, taskWorkspaceFixture) json.RawMessage
		ctx        func(taskWorkspaceFixture) context.Context
		wantPhrase string
	}{
		{
			name: "outside root self authorization",
			config: func(t *testing.T, fixture taskWorkspaceFixture) json.RawMessage {
				return automationWorkspaceConfig(t, fixture.outsideCWD, []string{fixture.outsideRoot})
			},
			ctx:        trustedTaskWorkspaceContext,
			wantPhrase: "trusted workspace",
		},
		{
			name: "filesystem root self authorization",
			config: func(t *testing.T, fixture taskWorkspaceFixture) json.RawMessage {
				return automationWorkspaceConfig(t, fixture.trustedCWD, []string{string(filepath.Separator)})
			},
			ctx:        trustedTaskWorkspaceContext,
			wantPhrase: "trusted workspace",
		},
		{
			name: "missing trusted roots",
			config: func(t *testing.T, fixture taskWorkspaceFixture) json.RawMessage {
				return automationWorkspaceConfig(t, fixture.trustedCWD, []string{fixture.trustedRoot})
			},
			ctx:        func(taskWorkspaceFixture) context.Context { return context.Background() },
			wantPhrase: "trusted workspace roots",
		},
		{
			name: "symlink escape",
			config: func(t *testing.T, fixture taskWorkspaceFixture) json.RawMessage {
				link := filepath.Join(fixture.trustedRoot, "escape")
				if err := os.Symlink(fixture.outsideRoot, link); err != nil {
					t.Fatalf("create escape symlink: %v", err)
				}
				return automationWorkspaceConfig(t, filepath.Join(link, "project"), []string{link})
			},
			ctx:        trustedTaskWorkspaceContext,
			wantPhrase: "trusted workspace",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTaskWorkspaceFixture(t)
			storeCalled := false
			handler := HandleCreateDAG(&golden.OrchestrationStub{
				CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
					storeCalled = true
					return contract.DAGDetail{}, nil
				},
			})

			_, err := handler(tc.ctx(fixture), createAutomationDAGInput(t, tc.config(t, fixture)))
			if err == nil {
				t.Fatal("HandleCreateDAG() error = nil, want workspace authorization failure")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantPhrase) {
				t.Fatalf("HandleCreateDAG() error = %q, want phrase %q", err.Error(), tc.wantPhrase)
			}
			if storeCalled {
				t.Fatal("CreateDAG was called before workspace authorization rejected the request")
			}
		})
	}
}

func TestHandleCreateDAGAcceptsAutomationWorkspaceWithinTrustedRoot(t *testing.T) {
	fixture := newTaskWorkspaceFixture(t)
	storeCalled := false
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			storeCalled = true
			return contract.DAGDetail{}, nil
		},
	})

	config := automationWorkspaceConfig(t, fixture.trustedCWD, []string{fixture.trustedRoot})
	if _, err := handler(trustedTaskWorkspaceContext(fixture), createAutomationDAGInput(t, config)); err != nil {
		t.Fatalf("HandleCreateDAG() error = %v, want trusted child workspace accepted", err)
	}
	if !storeCalled {
		t.Fatal("CreateDAG was not called for trusted child workspace")
	}
}

func TestHandleApplyOpsRejectsUntrustedAutomationWorkspaceBeforeWrite(t *testing.T) {
	tests := []struct {
		name    string
		input   func(*testing.T, json.RawMessage) json.RawMessage
		partial bool
	}{
		{name: "flat add", input: flatAutomationAddInput},
		{name: "raw add", input: rawAutomationAddInput},
		{name: "flat update", input: flatAutomationUpdateInput},
		{name: "raw update", input: rawAutomationUpdateInput},
		{name: "raw partial update", input: rawAutomationUpdateInput, partial: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTaskWorkspaceFixture(t)
			config := automationWorkspaceConfig(t, fixture.outsideCWD, []string{fixture.outsideRoot})
			if tc.partial {
				config = mustTaskWorkspaceJSON(t, map[string]any{
					"exec": map[string]any{
						"cwd":             fixture.outsideCWD,
						"workspace_roots": []string{fixture.outsideRoot},
					},
				})
			}
			applyCalled := false
			handler := HandleApplyOps(&golden.OrchestrationStub{
				GetDAGFunc: func(context.Context, string) (contract.DAGDetail, error) {
					return contract.DAGDetail{Nodes: []contract.DAGNode{{
						NodeKey:  "automation-node",
						NodeType: "automation",
					}}}, nil
				},
				ApplyOpsFunc: func(context.Context, contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
					applyCalled = true
					return contract.ApplyOpsResponse{}, nil
				},
			})

			_, err := handler(trustedTaskWorkspaceContext(fixture), tc.input(t, config))
			if err == nil {
				t.Fatal("HandleApplyOps() error = nil, want workspace authorization failure")
			}
			if applyCalled {
				t.Fatal("ApplyOps was called before workspace authorization rejected the request")
			}
		})
	}
}

func TestHandleApplyOpsRejectsAutomationWorkspaceWithoutTrustedScopeBeforeWrite(t *testing.T) {
	fixture := newTaskWorkspaceFixture(t)
	applyCalled := false
	handler := HandleApplyOps(&golden.OrchestrationStub{
		ApplyOpsFunc: func(context.Context, contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
			applyCalled = true
			return contract.ApplyOpsResponse{}, nil
		},
	})
	config := automationWorkspaceConfig(t, fixture.trustedCWD, []string{fixture.trustedRoot})

	_, err := handler(context.Background(), rawAutomationAddInput(t, config))
	if err == nil || !strings.Contains(err.Error(), "trusted workspace roots") {
		t.Fatalf("HandleApplyOps() error = %v, want missing trusted roots rejected", err)
	}
	if applyCalled {
		t.Fatal("ApplyOps was called before missing trusted roots rejected the request")
	}
}

func TestHandleApplyOpsAcceptsAutomationWorkspaceWithinTrustedRoot(t *testing.T) {
	tests := []struct {
		name  string
		input func(*testing.T, json.RawMessage) json.RawMessage
	}{
		{name: "flat add", input: flatAutomationAddInput},
		{name: "raw add", input: rawAutomationAddInput},
		{name: "flat update", input: flatAutomationUpdateInput},
		{name: "raw update", input: rawAutomationUpdateInput},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTaskWorkspaceFixture(t)
			applyCalled := false
			handler := HandleApplyOps(&golden.OrchestrationStub{
				GetDAGFunc: func(context.Context, string) (contract.DAGDetail, error) {
					return contract.DAGDetail{Nodes: []contract.DAGNode{{
						NodeKey:  "automation-node",
						NodeType: "automation",
					}}}, nil
				},
				ApplyOpsFunc: func(context.Context, contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
					applyCalled = true
					return contract.ApplyOpsResponse{}, nil
				},
			})
			config := automationWorkspaceConfig(t, fixture.trustedCWD, []string{fixture.trustedRoot})

			if _, err := handler(trustedTaskWorkspaceContext(fixture), tc.input(t, config)); err != nil {
				t.Fatalf("HandleApplyOps() error = %v, want trusted child workspace accepted", err)
			}
			if !applyCalled {
				t.Fatal("ApplyOps was not called for trusted child workspace")
			}
		})
	}
}

func newTaskWorkspaceFixture(t *testing.T) taskWorkspaceFixture {
	t.Helper()
	base := t.TempDir()
	fixture := taskWorkspaceFixture{
		trustedRoot: filepath.Join(base, "trusted"),
		trustedCWD:  filepath.Join(base, "trusted", "project"),
		outsideRoot: filepath.Join(base, "outside"),
		outsideCWD:  filepath.Join(base, "outside", "project"),
	}
	for _, path := range []string{fixture.trustedCWD, fixture.outsideCWD} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("create workspace fixture %q: %v", path, err)
		}
	}
	return fixture
}

func trustedTaskWorkspaceContext(fixture taskWorkspaceFixture) context.Context {
	return mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{
		AgentID:        "trusted-agent",
		CWD:            fixture.trustedCWD,
		WorkspaceRoots: []string{fixture.trustedRoot},
	})
}

func automationWorkspaceConfig(t *testing.T, cwd string, roots []string) json.RawMessage {
	t.Helper()
	return mustTaskWorkspaceJSON(t, map[string]any{
		"exec": map[string]any{
			"kind":            "command_card",
			"command_ref":     "build",
			"cwd":             cwd,
			"workspace_roots": roots,
		},
	})
}

func createAutomationDAGInput(t *testing.T, config json.RawMessage) json.RawMessage {
	t.Helper()
	return mustTaskWorkspaceJSON(t, CreateDAGInput{
		AgentID: "trusted-agent",
		DagKey:  "workspace-security",
		Title:   "Workspace security",
		Trigger: "manual",
		Nodes: []CreateDAGNodeInput{{
			NodeKey:  "automation-node",
			Title:    "Automation",
			NodeType: "automation",
			Config:   config,
		}},
	})
}

func flatAutomationAddInput(t *testing.T, config json.RawMessage) json.RawMessage {
	t.Helper()
	return mustTaskWorkspaceJSON(t, ApplyOpsInput{
		DagKey:      "workspace-security",
		BaseVersion: 1,
		Action:      "add_node",
		NodeKey:     "automation-node",
		Title:       "Automation",
		NodeType:    "automation",
		Config:      config,
	})
}

func rawAutomationAddInput(t *testing.T, config json.RawMessage) json.RawMessage {
	t.Helper()
	ops := mustTaskWorkspaceJSON(t, []any{map[string]any{
		"op": "add_node",
		"node": map[string]any{
			"node_key":  "automation-node",
			"title":     "Automation",
			"node_type": "automation",
			"config":    config,
		},
	}})
	return mustTaskWorkspaceJSON(t, ApplyOpsInput{
		DagKey:      "workspace-security",
		BaseVersion: 1,
		Action:      "apply_ops_raw",
		Ops:         ops,
	})
}

func flatAutomationUpdateInput(t *testing.T, config json.RawMessage) json.RawMessage {
	t.Helper()
	return mustTaskWorkspaceJSON(t, ApplyOpsInput{
		DagKey:      "workspace-security",
		BaseVersion: 1,
		Action:      "update_node",
		NodeKey:     "automation-node",
		Config:      config,
	})
}

func rawAutomationUpdateInput(t *testing.T, config json.RawMessage) json.RawMessage {
	t.Helper()
	ops := mustTaskWorkspaceJSON(t, []any{map[string]any{
		"op":       "update_node",
		"node_key": "automation-node",
		"patch": map[string]any{
			"config": config,
		},
	}})
	return mustTaskWorkspaceJSON(t, ApplyOpsInput{
		DagKey:      "workspace-security",
		BaseVersion: 1,
		Ops:         ops,
	})
}

func mustTaskWorkspaceJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal workspace security fixture: %v", err)
	}
	return raw
}

func trustedTaskHandlerInput(t *testing.T, input string) (context.Context, json.RawMessage, string) {
	t.Helper()
	fixture := newTaskWorkspaceFixture(t)
	raw := json.RawMessage(strings.ReplaceAll(input, "/repo", fixture.trustedRoot))
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{
		AgentID:        "designer-1",
		CWD:            fixture.trustedCWD,
		WorkspaceRoots: []string{fixture.trustedRoot},
	})
	return ctx, raw, fixture.trustedRoot
}
