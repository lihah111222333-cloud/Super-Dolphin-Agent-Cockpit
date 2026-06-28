package thread

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestSessionStartRequestCoversThreadStartRequestFields(t *testing.T) {
	startType := reflect.TypeFor[StartRequest]()
	sessionType := reflect.TypeFor[contract.SessionStartRequest]()
	exemptions := map[string]string{
		"PromptAssemblyRef": "injected by thread service before prompt assembly",
		"PromptVersionID":   "materialized by thread service after prompt routing",
		"AgentTitle":        "derived from prompt routing for UI metadata",
		"PromptKeyStale":    "derived by thread service from prompt routing result",
	}
	for index := 0; index < startType.NumField(); index++ {
		field := startType.Field(index)
		if _, ok := sessionType.FieldByName(field.Name); ok {
			continue
		}
		reason := strings.TrimSpace(exemptions[field.Name])
		if reason == "" {
			t.Fatalf("contract.SessionStartRequest missing StartRequest field %s; add a mapping or a documented exemption", field.Name)
		}
	}
	for field, reason := range exemptions {
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("empty SessionStartRequest field exemption for %s", field)
		}
		if _, ok := startType.FieldByName(field); !ok {
			t.Fatalf("SessionStartRequest exemption %s no longer exists on StartRequest", field)
		}
	}
}

func TestStartRequestFromSessionPreservesAllWireCriticalFields(t *testing.T) {
	req := sessionStartRequestFixture()
	got := startRequestFromSession(req)

	if want := expectedStartRequestFromFixture(req); !reflect.DeepEqual(got, want) {
		t.Fatalf("startRequestFromSession mismatch\nwant: %#v\n got: %#v", want, got)
	}
	assertStartRequestMappingDoesNotAlias(t, req, got)
}

func TestStartSessionRequestFromStartPreservesAllWireCriticalFields(t *testing.T) {
	req := sessionStartRequestFixture()
	start := expectedStartRequestFromFixture(req)
	got := startSessionRequestFromStart(start)

	if !reflect.DeepEqual(got, req) {
		t.Fatalf("startSessionRequestFromStart mismatch\nwant: %#v\n got: %#v", req, got)
	}
}

func TestNewSessionPortsImplementsContract(t *testing.T) {
	var _ contract.SessionPorts = NewSessionPorts(&stubThreadService{})
}

func TestSessionLifecyclePortStartSessionUsesAdapterMapping(t *testing.T) {
	req := sessionStartRequestFixture()
	stub := &stubThreadService{
		startResult: StartResult{
			ThreadID:      "thread-1",
			AgentID:       "agent-1",
			SessionID:     "session-1",
			Status:        "running",
			Model:         "gpt-5",
			Provider:      "codex",
			ModelProvider: "openai",
			CWD:           "/repo",
		},
	}
	port := NewSessionLifecyclePort(stub)
	got, err := port.StartSession(context.Background(), req)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if want := expectedStartRequestFromFixture(req); !reflect.DeepEqual(stub.startReq, want) {
		t.Fatalf("StartSession mapped request mismatch\nwant: %#v\n got: %#v", want, stub.startReq)
	}
	if got.ThreadID != "thread-1" || got.AgentID != "agent-1" || got.SessionID != "session-1" {
		t.Fatalf("StartSession result = %#v, want projected start result", got)
	}
}

func TestSessionLifecyclePortResumeSessionPreservesOverrides(t *testing.T) {
	stub := &stubThreadService{
		resumeResult: ResumeResult{
			ThreadID:  "thread-9",
			SessionID: "session-9",
			Status:    "resumed",
			Model:     "gpt-5",
			CWD:       "/repo",
		},
	}
	port := NewSessionLifecyclePort(stub)
	got, err := port.ResumeSession(context.Background(), contract.SessionResumeRequest{
		ThreadID: " thread-9 ",
		Path:     "/legacy/path",
		CWD:      "/repo",
		Model:    "gpt-5",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if stub.resumeReq.ThreadID != "thread-9" ||
		stub.resumeReq.Path != "/legacy/path" ||
		stub.resumeReq.CWD != "/repo" ||
		stub.resumeReq.Model != "gpt-5" ||
		stub.resumeReq.Provider != "codex" {
		t.Fatalf("ResumeSession mapped request = %#v", stub.resumeReq)
	}
	if got.ThreadID != "thread-9" || got.SessionID != "session-9" || got.Status != "resumed" {
		t.Fatalf("ResumeSession result = %#v, want projected resume result", got)
	}
}

func TestSessionLifecyclePortForkSessionPreservesResultMetadata(t *testing.T) {
	stub := &stubThreadService{
		forkResult: ForkResult{
			NewThreadID:  "thread-fork",
			ForkedFrom:   "thread-parent",
			KickoffState: ForkKickoffState("created_only"),
		},
	}
	port := NewSessionLifecyclePort(stub)
	got, err := port.ForkSession(context.Background(), " thread-parent ")
	if err != nil {
		t.Fatalf("ForkSession() error = %v", err)
	}
	if stub.forkThreadID != "thread-parent" {
		t.Fatalf("ForkSession thread id = %q, want thread-parent", stub.forkThreadID)
	}
	if got != (contract.SessionForkResult{NewThreadID: "thread-fork", ForkedFrom: "thread-parent", KickoffState: "created_only"}) {
		t.Fatalf("ForkSession result = %#v", got)
	}
}

func TestSessionStatusPortReadMessagesRejectsEmptyThreadID(t *testing.T) {
	stub := &stubThreadService{}
	port := NewSessionStatusPort(stub)
	if _, err := port.ReadMessages(context.Background(), " \t", 20, ""); err == nil || !strings.Contains(err.Error(), "thread id is required") {
		t.Fatalf("ReadMessages(empty) error = %v, want thread id required", err)
	}
	if stub.readMessagesThread != "" || stub.readMessagesLimit != 0 || stub.readMessagesBefore != "" {
		t.Fatalf("ReadMessages(empty) reached service: %#v", stub)
	}
}

func sessionStartRequestFixture() contract.SessionStartRequest {
	mcpEnabled := true
	return contract.SessionStartRequest{
		Provider:              "codex",
		AgentID:               "agent-1",
		ParentAgentID:         "parent-1",
		AgentType:             "worker",
		AgentMemoryScope:      "project",
		CWD:                   "/repo",
		Model:                 "gpt-5",
		ModelProvider:         "openai",
		Name:                  "Plan",
		Prompt:                "Prompt",
		BaseInstructions:      "base",
		BaseInstructionBlocks: []contract.BaseInstructionBlock{{Key: "base", Region: contract.PromptRegionStatic, Ordinal: 1, Body: "body", EnableWhen: []byte(`{"provider":"codex"}`)}},
		DeveloperInstructions: "dev",
		ApprovalPolicy:        "on-request",
		Sandbox:               json.RawMessage(`{"type":"workspace-write"}`),
		Summary:               "summary",
		Effort:                "high",
		Personality:           "steady",
		Language:              "zh",
		GitRoot:               "/repo",
		IsWorktree:            true,
		ToolSurfaceMode:       "chat",
		EnabledTools:          []string{"grep", "edit"},
		AdditionalWorkingDirectories: []string{
			"/repo/extra",
		},
		MCPSnapshot: sessionMCPSnapshotFixture(&mcpEnabled),
		SessionFlags: map[string]bool{
			"simple": true,
		},
		Config:            map[string]any{"sandbox": map[string]any{"mode": "workspace-write"}, "features": []any{"mcp"}},
		LaunchSkillNames:  []string{"review"},
		LaunchSkillRefs:   []dto.SkillRef{{Name: "review", Key: "skill-review", Scope: "project", Source: dto.SkillSourceManual}},
		ForceLaunchSkills: true,
		AgentKey:          "agent-key",
		PromptKey:         "prompt-key",
		OwnerThreadID:     "owner-thread",
		LaunchIntentID:    "intent-1",
		DeferSpawn:        true,
	}
}

func sessionMCPSnapshotFixture(enabled *bool) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:      []string{"server-a"},
		Tools:        []string{"tool-a"},
		Instructions: map[string]string{"server-a": "use server-a"},
		ServerConfigs: map[string]contract.MCPServerConfig{
			"server-a": {
				Transport: "stdio",
				Command:   "node",
				Args:      []string{"server.js"},
				Headers:   map[string]string{"Authorization": "x"},
				Env:       map[string]string{"TOKEN": "x"},
				Enabled:   enabled,
			},
		},
		InstructionsDeltaEnabled: true,
		InstructionAttachments:   []contract.MCPAttachmentRef{{Name: "docs", URI: "file:///docs"}},
	}
}

func expectedStartRequestFromFixture(req contract.SessionStartRequest) StartRequest {
	return StartRequest{
		Provider:                     req.Provider,
		AgentID:                      req.AgentID,
		ParentAgentID:                req.ParentAgentID,
		AgentType:                    req.AgentType,
		AgentMemoryScope:             req.AgentMemoryScope,
		CWD:                          req.CWD,
		Model:                        req.Model,
		ModelProvider:                req.ModelProvider,
		Name:                         req.Name,
		Prompt:                       req.Prompt,
		BaseInstructions:             req.BaseInstructions,
		BaseInstructionBlocks:        req.BaseInstructionBlocks,
		DeveloperInstructions:        req.DeveloperInstructions,
		ApprovalPolicy:               req.ApprovalPolicy,
		Sandbox:                      req.Sandbox,
		Summary:                      req.Summary,
		Effort:                       req.Effort,
		Personality:                  req.Personality,
		Language:                     req.Language,
		GitRoot:                      req.GitRoot,
		IsWorktree:                   req.IsWorktree,
		ToolSurfaceMode:              req.ToolSurfaceMode,
		EnabledTools:                 req.EnabledTools,
		AdditionalWorkingDirectories: req.AdditionalWorkingDirectories,
		MCPSnapshot:                  req.MCPSnapshot,
		SessionFlags:                 req.SessionFlags,
		Config:                       req.Config,
		LaunchSkillNames:             req.LaunchSkillNames,
		LaunchSkillRefs:              req.LaunchSkillRefs,
		ForceLaunchSkills:            req.ForceLaunchSkills,
		AgentKey:                     req.AgentKey,
		PromptKey:                    req.PromptKey,
		OwnerThreadID:                req.OwnerThreadID,
		LaunchIntentID:               req.LaunchIntentID,
		DeferSpawn:                   req.DeferSpawn,
	}
}

func assertStartRequestMappingDoesNotAlias(t *testing.T, req contract.SessionStartRequest, got StartRequest) {
	t.Helper()
	mutateSessionStartRequest(&req)

	assertStartRequestBufferFieldsDoNotAlias(t, got)
	assertStartRequestCollectionFieldsDoNotAlias(t, got)
	assertStartRequestMCPFieldsDoNotAlias(t, got)
	assertStartRequestConfigAndSkillsDoNotAlias(t, got)
}

func mutateSessionStartRequest(req *contract.SessionStartRequest) {
	req.BaseInstructionBlocks[0].EnableWhen[0] = 'X'
	req.Sandbox[0] = '['
	req.EnabledTools[0] = "mutated"
	req.AdditionalWorkingDirectories[0] = "/mutated"
	req.MCPSnapshot.Servers[0] = "mutated"
	req.MCPSnapshot.Instructions["server-a"] = "mutated"
	mcpConfig := req.MCPSnapshot.ServerConfigs["server-a"]
	mcpConfig.Headers["Authorization"] = "mutated"
	mcpConfig.Args[0] = "mutated"
	*mcpConfig.Enabled = false
	req.SessionFlags["simple"] = false
	req.Config["sandbox"].(map[string]any)["mode"] = "mutated"
	req.LaunchSkillNames[0] = "mutated"
	req.LaunchSkillRefs[0].Name = "mutated"
}

func assertStartRequestBufferFieldsDoNotAlias(t *testing.T, got StartRequest) {
	t.Helper()
	if got.BaseInstructionBlocks[0].EnableWhen[0] == 'X' || got.Sandbox[0] == '[' {
		t.Fatalf("base blocks or sandbox alias caller buffers: %#v", got)
	}
}

func assertStartRequestCollectionFieldsDoNotAlias(t *testing.T, got StartRequest) {
	t.Helper()
	if got.EnabledTools[0] == "mutated" || got.AdditionalWorkingDirectories[0] == "/mutated" || !got.SessionFlags["simple"] {
		t.Fatalf("mapped request aliases caller slices or maps: %#v", got)
	}
}

func assertStartRequestMCPFieldsDoNotAlias(t *testing.T, got StartRequest) {
	t.Helper()
	if got.MCPSnapshot.Servers[0] == "mutated" || got.MCPSnapshot.Instructions["server-a"] == "mutated" {
		t.Fatalf("MCP snapshot aliases caller fields: %#v", got.MCPSnapshot)
	}
	gotMCPConfig := got.MCPSnapshot.ServerConfigs["server-a"]
	if gotMCPConfig.Headers["Authorization"] == "mutated" || gotMCPConfig.Args[0] == "mutated" || gotMCPConfig.Enabled == nil || !*gotMCPConfig.Enabled {
		t.Fatalf("MCP server config aliases caller fields: %#v", gotMCPConfig)
	}
}

func assertStartRequestConfigAndSkillsDoNotAlias(t *testing.T, got StartRequest) {
	t.Helper()
	if got.Config["sandbox"].(map[string]any)["mode"] == "mutated" {
		t.Fatalf("Config was not deep-cloned: %#v", got.Config)
	}
	if got.LaunchSkillNames[0] == "mutated" || got.LaunchSkillRefs[0].Name == "mutated" {
		t.Fatalf("launch skill fields alias caller slices: %#v %#v", got.LaunchSkillNames, got.LaunchSkillRefs)
	}
}
