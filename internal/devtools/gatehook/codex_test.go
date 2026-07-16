package gatehook

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestParseCodexHookUsesOnlyPublicIdentityFields(t *testing.T) {
	repository := newTestRepository(t)
	payload := strings.ReplaceAll(loadFixture(t, "codex/subagent-stop.json"), "__CWD__", repository)
	hook, err := ParseCodexHook(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("ParseCodexHook: %v", err)
	}
	if hook.CWD != repository || hook.AgentID != "agent-explicit" {
		t.Fatalf("explicit identity = cwd %q agent %q", hook.CWD, hook.AgentID)
	}
	if hook.PermissionMode != PermissionModePlan || hook.HookEventName != CodexHookEventSubagentStop {
		t.Fatalf("unexpected normalized hook: %#v", hook)
	}
}

func TestParseCodexHookRejectsMissingAndUnknownFields(t *testing.T) {
	repository := newTestRepository(t)
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{name: "cwd", payload: loadFixture(t, "codex/missing-cwd.json"), wantErr: "cwd"},
		{
			name:    "agent id",
			payload: strings.ReplaceAll(loadFixture(t, "codex/missing-agent-id.json"), "__CWD__", repository),
			wantErr: "agent_id",
		},
		{
			name: "unknown",
			payload: `{"session_id":"s","turn_id":"t","cwd":"/tmp","hook_event_name":"Stop",` +
				`"permission_mode":"default","stop_hook_active":false,"cwd_from_transcript":"/fake"}`,
			wantErr: "unknown field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseCodexHook(strings.NewReader(test.payload))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestCodexPermissionModesMatchPublishedEnum(t *testing.T) {
	repository := newTestRepository(t)
	fixture := strings.ReplaceAll(loadFixture(t, "codex/stop.json"), "__CWD__", repository)
	modes := []PermissionMode{
		PermissionModeDefault,
		PermissionModeAcceptEdits,
		PermissionModePlan,
		PermissionModeDontAsk,
		PermissionModeBypassPermissions,
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			payload := strings.Replace(fixture, `"permission_mode": "default"`, `"permission_mode": "`+string(mode)+`"`, 1)
			hook, err := ParseCodexHook(strings.NewReader(payload))
			if err != nil {
				t.Fatalf("ParseCodexHook: %v", err)
			}
			if hook.PermissionMode != mode {
				t.Fatalf("permission mode = %q, want %q", hook.PermissionMode, mode)
			}
		})
	}
}

func TestNormalizeCodexHookDeduplicatesReplayAndActiveOnlyQueriesParent(t *testing.T) {
	repository := newTestRepository(t)
	payload := strings.ReplaceAll(loadFixture(t, "codex/stop.json"), "__CWD__", repository)
	first, err := NormalizeCodexHook(context.Background(), strings.NewReader(payload))
	if err != nil {
		t.Fatalf("NormalizeCodexHook first: %v", err)
	}
	identities := normalizeCodexConcurrently(t, payload, 16)
	for _, identity := range identities {
		if identity != first.Submit.Invocation {
			t.Fatalf("competing event identity = %#v, want %#v", identity, first.Submit.Invocation)
		}
	}
	activePayload := strings.Replace(payload, `"stop_hook_active": false`, `"stop_hook_active": true`, 1)
	active, err := NormalizeCodexHook(context.Background(), strings.NewReader(activePayload))
	if err != nil {
		t.Fatalf("NormalizeCodexHook active: %v", err)
	}
	if active.Kind != RequestKindStatus || !active.Status.ParentInvocationOnly || active.Submit != nil {
		t.Fatalf("active request = %#v", active)
	}
	if active.Status.Invocation != first.Submit.Invocation {
		t.Fatalf("active identity = %#v, want parent %#v", active.Status.Invocation, first.Submit.Invocation)
	}
}

func normalizeCodexConcurrently(t *testing.T, payload string, count int) []InvocationIdentity {
	t.Helper()
	identities := make([]InvocationIdentity, count)
	errorsByIndex := make([]error, count)
	var waitGroup sync.WaitGroup
	for index := range count {
		waitGroup.Go(func() {
			request, err := NormalizeCodexHook(context.Background(), strings.NewReader(payload))
			errorsByIndex[index] = err
			if err == nil {
				identities[index] = request.Submit.Invocation
			}
		})
	}
	waitGroup.Wait()
	for _, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("NormalizeCodexHook concurrently: %v", err)
		}
	}
	return identities
}

func TestCodexWireFieldCoverageFailsOnMissingRegistration(t *testing.T) {
	producer := reflectedJSONFields(t, reflect.TypeFor[codexHookWire]())
	coverage := map[string]string{
		"session_id": "invocation owner", "transcript_path": "accepted but never used for identity",
		"cwd": "active worktree", "hook_event_name": "entrypoint", "model": "accepted provenance only",
		"permission_mode": "submit provenance", "turn_id": "invocation key", "agent_id": "subagent identity",
		"agent_type": "accepted matcher metadata", "agent_transcript_path": "accepted but never used for identity",
		"stop_hook_active": "parent status only", "last_assistant_message": "accepted but never used for identity",
	}
	delete(coverage, "cwd")
	missing, stale := fieldCoverageDiff(producer, coverage)
	if !reflect.DeepEqual(missing, []string{"cwd"}) || len(stale) != 0 {
		t.Fatalf("fail-first diff missing=%v stale=%v", missing, stale)
	}
	coverage["cwd"] = "active worktree"
	missing, stale = fieldCoverageDiff(producer, coverage)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("field coverage missing=%v stale=%v", missing, stale)
	}
}

func reflectedJSONFields(t *testing.T, producer reflect.Type) []string {
	t.Helper()
	fields := make([]string, 0, producer.NumField())
	for index := range producer.NumField() {
		name, _, _ := strings.Cut(producer.Field(index).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("producer field %s has no JSON identity", producer.Field(index).Name)
		}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func fieldCoverageDiff(producer []string, coverage map[string]string) ([]string, []string) {
	producerSet := make(map[string]struct{}, len(producer))
	for _, field := range producer {
		producerSet[field] = struct{}{}
	}
	missing := make([]string, 0)
	for field := range producerSet {
		if strings.TrimSpace(coverage[field]) == "" {
			missing = append(missing, field)
		}
	}
	stale := make([]string, 0)
	for field := range coverage {
		if _, ok := producerSet[field]; !ok {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}
