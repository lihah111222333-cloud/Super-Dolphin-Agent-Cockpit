package thread

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/builtinprompts"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

type recallOnlySectionStore struct{}

func (recallOnlySectionStore) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	tags, _ := json.Marshal([]string{"scope.cwd:/repo/a"})
	return []promptstore.PromptTemplate{{
		ID:         91,
		PromptKey:  defaultPromptKey,
		AgentKey:   "main",
		PromptText: "default body",
		Tags:       tags,
		Enabled:    true,
		UpdatedAt:  time.Now(),
	}}, nil
}
func (recallOnlySectionStore) InsertVersion(context.Context, promptstore.PromptTemplateVersion) (int64, error) {
	return 1, nil
}
func (recallOnlySectionStore) CreatePromptTemplate(context.Context, promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (recallOnlySectionStore) ListSectionsByTemplateID(context.Context, int64) ([]promptstore.PromptTemplateSection, error) {
	return []promptstore.PromptTemplateSection{
		{TemplateID: 91, SectionKey: "recall_sqlc", Region: "dynamic", Body: "recall body", TriggerType: "recall", Enabled: true},
	}, nil
}
func (recallOnlySectionStore) ListSectionsByTemplateIDs(context.Context, []int64) ([]promptstore.PromptTemplateSection, error) {
	return []promptstore.PromptTemplateSection{
		{TemplateID: 91, SectionKey: "recall_sqlc", Region: "dynamic", Body: "recall body", TriggerType: "recall", Enabled: true},
	}, nil
}
func (recallOnlySectionStore) ListRecallSections(context.Context, string) ([]promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (recallOnlySectionStore) ListDefaultRuleSections(context.Context, string) ([]promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (recallOnlySectionStore) WithTx(context.Context, func(promptstore.Store) error) error {
	panic("unused")
}
func (recallOnlySectionStore) Get(context.Context, string) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (recallOnlySectionStore) Delete(context.Context, string) error {
	panic("unused")
}
func (recallOnlySectionStore) Upsert(context.Context, promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	panic("unused")
}
func (recallOnlySectionStore) UpsertSection(context.Context, promptstore.PromptTemplateSection) (*promptstore.PromptTemplateSection, error) {
	panic("unused")
}
func (recallOnlySectionStore) DeleteSection(context.Context, int64, string) error {
	panic("unused")
}
func (recallOnlySectionStore) UpsertIntentDraft(context.Context, promptstore.PromptIntentDraft) (*promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) GetIntentDraft(context.Context, string, string) (*promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) ListIntentDrafts(context.Context, promptstore.PromptIntentDraftListFilter) ([]promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) UpdateIntentDraftStatus(context.Context, string, string, string) (*promptstore.PromptIntentDraft, error) {
	panic("unused")
}
func (recallOnlySectionStore) LockRecallTopicInCWD(context.Context, string, string) error {
	panic("unused")
}

func TestResolveRoutedPrompt_RecallOnlySectionsDoNotLaunchAsDefaultPrompt(t *testing.T) {
	t.Parallel()

	s := newServiceWithRouter(recallOnlySectionStore{})
	req := &StartRequest{CWD: "/repo/a", Prompt: "hello"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}

	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want recall-only runtime asset skipped", req.BaseInstructions)
	}
	if len(req.BaseInstructionBlocks) != 0 {
		t.Fatalf("BaseInstructionBlocks = %#v, want none", req.BaseInstructionBlocks)
	}
	if req.PromptKey != "" || req.AgentKey != "" {
		t.Fatalf("runtime asset default must not stamp launch identity: %+v", req)
	}
}

func TestResolveRoutedPrompt_BuiltinRecallSectionsDoNotEnterBaseInstructions(t *testing.T) {
	t.Parallel()

	reg, err := builtinprompts.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	catalog := threadprompt.NewRuntimeCatalog(nil, reg)
	s := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", PromptKey: "main/general-zh", Prompt: "hello"}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}

	if req.BaseInstructions != "" {
		t.Fatalf("BaseInstructions = %q, want structured builtin blocks", req.BaseInstructions)
	}
	if len(req.BaseInstructionBlocks) == 0 {
		t.Fatal("BaseInstructionBlocks empty, want non-recall builtin sections")
	}
	for _, block := range req.BaseInstructionBlocks {
		if strings.HasPrefix(block.Key, "recall_") {
			t.Fatalf("recall block %q entered base instructions: %#v", block.Key, req.BaseInstructionBlocks)
		}
		if strings.Contains(block.Body, "Prompt template 编辑") {
			t.Fatalf("recall body entered base instructions via block %q", block.Key)
		}
	}
}

func TestResolveRoutedPrompt_MainDefaultOrchestratorSectionsGateIndependently(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tools   []string
		want    []string
		notWant []string
	}{
		{
			name:    "no orchestration tools",
			tools:   nil,
			notWant: []string{"orchestrator_launch_context", "orchestrator_report_context"},
		},
		{
			name:    "launch only",
			tools:   []string{"orchestration_launch_agent"},
			want:    []string{"orchestrator_launch_context"},
			notWant: []string{"orchestrator_report_context"},
		},
		{
			name:    "report only",
			tools:   []string{"orchestration_get_agent_report"},
			want:    []string{"orchestrator_report_context"},
			notWant: []string{"orchestrator_launch_context"},
		},
		{
			name:  "both tools",
			tools: []string{"orchestration_launch_agent", "orchestration_get_agent_report"},
			want:  []string{"orchestrator_launch_context", "orchestrator_report_context"},
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := resolveBuiltinPromptForTools(t, "main/default", tt.tools)
			for _, key := range tt.want {
				if !baseInstructionBlockKeys(req.BaseInstructionBlocks)[key] {
					t.Fatalf("BaseInstructionBlocks missing %q: %#v", key, req.BaseInstructionBlocks)
				}
			}
			for _, key := range tt.notWant {
				if baseInstructionBlockKeys(req.BaseInstructionBlocks)[key] {
					t.Fatalf("BaseInstructionBlocks unexpectedly included %q: %#v", key, req.BaseInstructionBlocks)
				}
			}
		})
	}
}

func TestResolveRoutedPrompt_BuiltinDAGDesignerPromptKeyLaunchesWithToolGuidance(t *testing.T) {
	t.Parallel()

	req := resolveBuiltinPromptForTools(t, "main/dag_designer_zh", []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_get_dag",
		"task_dag_apply_ops",
		"task_start_dag",
	})
	if req.PromptKeyStale {
		t.Fatalf("PromptKeyStale = true, want builtin DAG designer prompt to resolve: %+v", req)
	}
	if req.AgentKey != "dag_designer" {
		t.Fatalf("AgentKey = %q, want dag_designer", req.AgentKey)
	}
	if !baseInstructionBlockKeys(req.BaseInstructionBlocks)["dag_designer_runtime_tools"] {
		t.Fatalf("BaseInstructionBlocks missing DAG designer section: %#v", req.BaseInstructionBlocks)
	}
	body := contract.TextFromBaseInstructionBlocks(req.BaseInstructionBlocks)
	for _, want := range []string{"node.config.exec", "assigned_to", "waiting_for_assignee", "final_output"} {
		if !strings.Contains(body, want) {
			t.Fatalf("DAG designer body missing %q:\n%s", want, body)
		}
	}
}

func TestResolveRoutedPrompt_BuiltinDAGDesignerRequiresCompleteDAGToolset(t *testing.T) {
	t.Parallel()

	req := resolveBuiltinPromptForToolsAllowEmpty(t, "main/dag_designer_zh", []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"task_create_dag",
		"task_get_dag",
		"task_start_dag",
	})
	if req.PromptKeyStale {
		t.Fatalf("PromptKeyStale = true, want builtin DAG designer prompt to resolve: %+v", req)
	}
	if req.AgentKey != "dag_designer" {
		t.Fatalf("AgentKey = %q, want dag_designer", req.AgentKey)
	}
	if baseInstructionBlockKeys(req.BaseInstructionBlocks)["dag_designer_runtime_tools"] {
		t.Fatalf("DAG designer section injected without task_dag_apply_ops: %#v", req.BaseInstructionBlocks)
	}
}

func resolveBuiltinPromptForTools(t *testing.T, promptKey string, tools []string) *StartRequest {
	t.Helper()
	req := resolveBuiltinPromptForToolsAllowEmpty(t, promptKey, tools)
	if len(req.BaseInstructionBlocks) == 0 {
		t.Fatalf("BaseInstructionBlocks empty for %s", promptKey)
	}
	return req
}

func resolveBuiltinPromptForToolsAllowEmpty(t *testing.T, promptKey string, tools []string) *StartRequest {
	t.Helper()
	reg, err := builtinprompts.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	catalog := threadprompt.NewRuntimeCatalog(nil, reg)
	s := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", PromptKey: promptKey, Prompt: "hello", EnabledTools: tools}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	return req
}

func baseInstructionBlockKeys(blocks []contract.BaseInstructionBlock) map[string]bool {
	out := make(map[string]bool, len(blocks))
	for _, block := range blocks {
		out[block.Key] = true
	}
	return out
}
