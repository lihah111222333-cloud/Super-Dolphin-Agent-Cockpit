package codexapp

import (
	"encoding/json"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildThreadStartAndResumeParamsDoNotExposeLifecycleFields(t *testing.T) {
	t.Parallel()

	start := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		CWD: " /repo ",
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled dev",
		},
		Config: map[string]any{"summary": "brief"},
	})
	assertNoLifecycleWireFields(t, "thread/start params", mustJSON(start))

	resume := buildThreadResumeParams(dto.ResumeSessionRequest{
		ThreadID: "provider-thread-1",
		CWD:      " /repo ",
		PromptSnapshot: dto.PromptAssemblySnapshot{
			BaseInstructions:      "snapshot base",
			DeveloperInstructions: "snapshot dev",
		},
	})
	assertNoLifecycleWireFields(t, "thread/resume params", mustJSON(resume))
}

func assertNoLifecycleWireFields(t *testing.T, scope string, raw json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal %s: %v", scope, err)
	}
	assertNoLifecycleWireMapFields(t, scope, fields)
}

func assertNoLifecycleWireMapFields(t *testing.T, scope string, fields map[string]json.RawMessage) {
	t.Helper()
	for _, forbidden := range []string{
		"lifecycle", "lifecycleState", "state", "reason", "source", "updatedBy",
		"createdAt", "updatedAt", "workspaceRoot", "serverName", "toolName",
	} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("%s unexpectedly exposes lifecycle field %q in %#v", scope, forbidden, fields)
		}
	}
}
