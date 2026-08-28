package thread

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	promptpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/builtinprompts"
)

func TestResolveRoutedPrompt_RecallOnlySectionsDoNotLaunchAsDefaultPrompt(t *testing.T) {
	t.Parallel()

	tpl := sqlTemplate(defaultPromptKey, "main", "default body", []string{"scope.cwd:/repo/a", "intent:recall"})
	tpl.ID = 91
	s := newServiceWithRouter(&fakePromptCatalog{
		templates: []PromptTemplate{tpl},
		sectionsByTemplateID: map[int64][]PromptTemplateSection{
			91: {
				{TemplateID: 91, SectionKey: "recall_sqlc", Region: "dynamic", Body: "recall body", TriggerType: "recall", Enabled: true},
			},
		},
	})
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
	catalog := newBuiltinPromptCatalogForTest(reg)
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
			tools:   []string{"launch_agent"},
			want:    []string{"orchestrator_launch_context"},
			notWant: []string{"orchestrator_report_context"},
		},
		{
			name:    "report only",
			tools:   []string{"get_agent_report"},
			want:    []string{"orchestrator_report_context"},
			notWant: []string{"orchestrator_launch_context"},
		},
		{
			name:  "both tools",
			tools: []string{"launch_agent", "get_agent_report"},
			want:  []string{"orchestrator_launch_context", "orchestrator_report_context"},
		},
	}

	for _, tt := range cases {
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

func TestResolveRoutedPrompt_MainGeneralZhLSPSectionsOnlyWithCanonicalTools(t *testing.T) {
	t.Parallel()
	requireNoLSPSections(t)
	requireCanonicalLSPSections(t)
	requireLSPAdvancedTriggers(t)
	requireCompactRoundState(t)
}

func requireNoLSPSections(t *testing.T) {
	t.Helper()
	for _, tools := range [][]string{
		nil,
		{"structure"},
		{"structure", "xref"},
		{"structure", "diagnostics"},
		{"xref", "diagnostics"},
	} {
		withoutLSP := resolveBuiltinPromptForToolsAllowEmpty(t, "main/general-zh", tools)
		withoutKeys := baseInstructionBlockKeys(withoutLSP.BaseInstructionBlocks)
		for _, key := range []string{"lsp_basics", "lsp_advanced"} {
			if withoutKeys[key] {
				t.Fatalf("No-LSP request with tools=%v injected %q: %#v", tools, key, withoutLSP.BaseInstructionBlocks)
			}
		}
		body := contract.TextFromBaseInstructionBlocks(withoutLSP.BaseInstructionBlocks)
		for _, token := range []string{"MCP-LSP", "structure", "xref", "diagnostics", "diff_filter", "changed_lines", "lsp_self_check"} {
			if strings.Contains(body, token) {
				t.Fatalf("No-LSP request with tools=%v contains LSP token %q: %q", tools, token, body)
			}
		}
	}
}

func requireCanonicalLSPSections(t *testing.T) {
	t.Helper()
	withLSP := resolveBuiltinPromptForToolsAllowEmpty(t, "main/general-zh", []string{"structure", "xref", "diagnostics"})
	withKeys := baseInstructionBlockKeys(withLSP.BaseInstructionBlocks)
	if !withKeys["lsp_basics"] {
		t.Fatalf("With-LSP request missing lsp_basics: %#v", withLSP.BaseInstructionBlocks)
	}
	withBody := contract.TextFromBaseInstructionBlocks(withLSP.BaseInstructionBlocks)
	for _, want := range []string{"Mandatory With-LSP gate", "With-LSP 强制门禁", "任务边界", "触发矩阵", "多生产文件", "file_paths", "diff_filter", "changed_lines", "lsp_self_check", "generated", "diagnostics_unsupported", "runner", "最终硬守卫"} {
		if !strings.Contains(withBody, want) {
			t.Fatalf("With-LSP body missing %q: %q", want, withBody)
		}
	}
}

func requireLSPAdvancedTriggers(t *testing.T) {
	t.Helper()
	advanced := resolveBuiltinPromptForToolsAndPrompt(t, "main/general-zh", []string{"structure", "xref", "diagnostics"}, "请查引用并分析这个重构的影响面")
	if !baseInstructionBlockKeys(advanced.BaseInstructionBlocks)["lsp_advanced"] {
		t.Fatalf("With-LSP semantic request missing lsp_advanced: %#v", advanced.BaseInstructionBlocks)
	}

	multiProduction := resolveBuiltinPromptForToolsAndPrompt(t, "main/general-zh", []string{"structure", "xref", "diagnostics"}, "请在多个生产文件中修改共享基类，并排查跨文件失败")
	if !baseInstructionBlockKeys(multiProduction.BaseInstructionBlocks)["lsp_advanced"] {
		t.Fatalf("multi-production trigger missing lsp_advanced: %#v", multiProduction.BaseInstructionBlocks)
	}
	localOnly := resolveBuiltinPromptForToolsAndPrompt(t, "main/general-zh", []string{"structure", "xref", "diagnostics"}, "把这个局部 if 改清晰")
	if baseInstructionBlockKeys(localOnly.BaseInstructionBlocks)["lsp_advanced"] {
		t.Fatalf("local single-file request over-injected lsp_advanced: %#v", localOnly.BaseInstructionBlocks)
	}
}

func requireCompactRoundState(t *testing.T) {
	t.Helper()
	compactNoLSP := resolveBuiltinPromptForToolsAndFlags(t, "main/general-zh", nil, map[string]bool{"round_state": true})
	compactNoLSPKeys := baseInstructionBlockKeys(compactNoLSP.BaseInstructionBlocks)
	if !compactNoLSPKeys["round_state"] {
		t.Fatalf("compact round state missing without LSP: %#v", compactNoLSP.BaseInstructionBlocks)
	}
	compactNoLSPBody := contract.TextFromBaseInstructionBlocks(compactNoLSP.BaseInstructionBlocks)
	for _, token := range []string{"MCP-LSP", "structure", "xref", "diagnostics", "lsp_self_check"} {
		if strings.Contains(compactNoLSPBody, token) {
			t.Fatalf("No-LSP compact round state contains LSP token %q: %q", token, compactNoLSPBody)
		}
	}

	compactWithLSP := resolveBuiltinPromptForToolsAndFlags(t, "main/general-zh", []string{"structure", "xref", "diagnostics"}, map[string]bool{"round_state": true})
	if !baseInstructionBlockKeys(compactWithLSP.BaseInstructionBlocks)["round_state"] {
		t.Fatalf("compact round state missing with LSP: %#v", compactWithLSP.BaseInstructionBlocks)
	}
}

func TestResolveRoutedPrompt_BuiltinDAGDesignerPromptKeyLaunchesWithToolGuidance(t *testing.T) {
	t.Parallel()

	req := resolveBuiltinPromptForTools(t, "main/dag_designer_zh", []string{
		"list_models",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"workflow_template_list",
		"workflow_template_get",
		"workflow_template_render_dag",
		"task_create_dag",
		"task_get_dag",
		"task_get_run",
		"task_list_runs",
		"task_dag_apply_ops",
		"task_dispatch_node",
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
	for _, want := range []string{"node.config.exec", "assigned_to", "waiting_for_assignee", "final_output", "task_dispatch_node", "CRON_TZ=Asia/Shanghai", "runtime append"} {
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
		"workflow_template_list",
		"workflow_template_get",
		"workflow_template_render_dag",
		"task_create_dag",
		"task_get_dag",
		"task_get_run",
		"task_list_runs",
		"task_dag_apply_ops",
		"task_start_dag",
	})
	if req.PromptKeyStale {
		t.Fatalf("PromptKeyStale = true, want builtin DAG designer prompt to resolve: %+v", req)
	}
	if req.AgentKey != "dag_designer" {
		t.Fatalf("AgentKey = %q, want dag_designer", req.AgentKey)
	}
	if baseInstructionBlockKeys(req.BaseInstructionBlocks)["dag_designer_runtime_tools"] {
		t.Fatalf("DAG designer section injected without task_dispatch_node: %#v", req.BaseInstructionBlocks)
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
	catalog := newBuiltinPromptCatalogForTest(reg)
	s := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", PromptKey: promptKey, Prompt: "hello", EnabledTools: tools}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	return req
}

func resolveBuiltinPromptForToolsAndPrompt(t *testing.T, promptKey string, tools []string, prompt string) *StartRequest {
	t.Helper()
	reg, err := builtinprompts.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	catalog := newBuiltinPromptCatalogForTest(reg)
	s := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", PromptKey: promptKey, Prompt: prompt, EnabledTools: tools}
	if err := s.resolveRoutedPrompt(context.Background(), req); err != nil {
		t.Fatalf("resolveRoutedPrompt() error = %v", err)
	}
	return req
}

func resolveBuiltinPromptForToolsAndFlags(t *testing.T, promptKey string, tools []string, flags map[string]bool) *StartRequest {
	t.Helper()
	reg, err := builtinprompts.NewDefaultRegistry()
	if err != nil {
		t.Fatalf("NewDefaultRegistry() error = %v", err)
	}
	catalog := newBuiltinPromptCatalogForTest(reg)
	s := &service{promptCatalog: catalog, enableWhenEval: promptpkg.EvaluateEnableWhen}
	req := &StartRequest{CWD: "/repo/a", PromptKey: promptKey, Prompt: "hello", EnabledTools: tools, SessionFlags: flags}
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
