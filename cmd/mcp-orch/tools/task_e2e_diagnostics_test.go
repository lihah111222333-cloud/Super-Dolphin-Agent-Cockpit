package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestChatCreateAutomationDAGUsesBuiltinPromptWhenDBEmpty(t *testing.T) {
	ctx := automationToolTestContext()
	userRequest := "每天早上 8 点生成热点新闻简报，输出到报告文件。"
	assertUserRequestOmitsInternalFields(t, userRequest)

	prompt := discoverBuiltinAutomationPrompt(t, ctx)
	provider := discoverCodexProvider(t)
	svc := newDiagnosticStatefulService(t, nil)

	_, err := HandleCreateDAG(svc)(ctx, mustRawInput(t, CreateDAGInput{
		DagKey:       "daily-hot-news-brief",
		Title:        "每日热点新闻简报",
		Description:  userRequest,
		Schedule:     DAGScheduleInput{Trigger: "manual"},
		FinalNodeKey: "brief",
		Nodes: []CreateDAGNodeInput{{
			NodeKey:    "brief",
			Title:      "生成热点新闻简报",
			NodeType:   "agent",
			AssignedTo: "daily-hot-news-brief-runner",
			Config: automationBriefNodeConfig(
				t,
				prompt.PromptKey,
				provider.Provider,
				provider.Models[0],
			),
		}},
	}))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}

	_, err = HandleApplyOps(svc)(ctx, json.RawMessage(`{
		"dag_key":"daily-hot-news-brief",
		"base_version":1,
		"action":"update_dag",
		"trigger":"scheduled",
		"cron_expr":"CRON_TZ=Asia/Shanghai 0 8 * * *"
	}`))
	if err != nil {
		t.Fatalf("HandleApplyOps() error = %v", err)
	}
	result, err := HandleGetDAG(svc)(ctx, mustRawInput(t, DAGKeyInput{DagKey: "daily-hot-news-brief"}))
	if err != nil {
		t.Fatalf("HandleGetDAG() error = %v", err)
	}
	detail := result.(contract.DAGDetail)
	assertPersistedScheduledAutomationDAG(t, detail, prompt.PromptKey, provider.Provider, provider.Models[0])
}

func TestDiagnoseDAGPromptIdentityGapsReportsOnlyInvalidNodes(t *testing.T) {
	svc := newDiagnosticTestService(t, diagnosticDAGDetails(t))

	result, err := HandleDiagnoseDAGPromptIdentityGaps(svc)(
		context.Background(),
		mustRawInput(t, DiagnoseDAGPromptIdentityGapsInput{Limit: 10}),
	)
	if err != nil {
		t.Fatalf("HandleDiagnoseDAGPromptIdentityGaps() error = %v", err)
	}
	out := result.(DAGPromptIdentityDiagnosticsOutput)
	assertDiagnosticGapKeys(t, out.Gaps)
	assertGapMissingFields(t, out.Gaps, "bad-agent", "writer", "config.exec.prompt_key", "config.exec.agent_key")
	assertGapMissingFields(t, out.Gaps, "bad-hybrid-provider", "review", "config.exec.verifier.prompt_key", "config.exec.verifier.agent_key", "config.exec.verifier.provider")
	assertGapMissingFields(t, out.Gaps, "bad-hybrid-codex", "codex-review", "config.exec.verifier.codex_home", "config.exec.verifier.codex_instance_key", "config.exec.verifier.codex_model_provider")
	assertDiagnosticRunStatusAndRemediation(t, out)
	assertDiagnosticScanStats(t, out, 4, 10, false)
}

func TestDiagnoseDAGPromptIdentityGapsSeparatesDAGScanTruncationFromGapOutput(t *testing.T) {
	svc := newDiagnosticStatefulService(t, map[string]contract.DAGDetail{
		"bad-agent": diagnosticBadAgentDAG(t),
		"valid":     diagnosticValidDAG(t),
	})
	svc.wantListDAGsLimit = 2

	result, err := HandleDiagnoseDAGPromptIdentityGaps(svc)(
		context.Background(),
		mustRawInput(t, DiagnoseDAGPromptIdentityGapsInput{Limit: 2}),
	)
	if err != nil {
		t.Fatalf("HandleDiagnoseDAGPromptIdentityGaps() error = %v", err)
	}
	out := result.(DAGPromptIdentityDiagnosticsOutput)
	assertDiagnosticScanStats(t, out, 2, 2, true)
	if out.Truncated {
		t.Fatalf("gap output truncated = true, want false when gaps themselves were not output-limited")
	}
	if out.Total != 1 || out.Showing != 1 || len(out.Data) != 1 {
		t.Fatalf("gap output envelope = total:%d showing:%d data:%d, want single gap", out.Total, out.Showing, len(out.Data))
	}
}

func TestDiagnoseDAGPromptIdentityGapsSingleDAGDoesNotMarkScanTruncated(t *testing.T) {
	svc := newDiagnosticStatefulService(t, map[string]contract.DAGDetail{
		"bad-agent": diagnosticBadAgentDAG(t),
	})

	result, err := HandleDiagnoseDAGPromptIdentityGaps(svc)(
		context.Background(),
		mustRawInput(t, DiagnoseDAGPromptIdentityGapsInput{DagKey: "bad-agent", Limit: 1}),
	)
	if err != nil {
		t.Fatalf("HandleDiagnoseDAGPromptIdentityGaps() error = %v", err)
	}
	out := result.(DAGPromptIdentityDiagnosticsOutput)
	assertDiagnosticScanStats(t, out, 1, 0, false)
}

func TestInvalidAgentDAGCreateFailsBeforeStoreStartAndWakeup(t *testing.T) {
	svc := newDiagnosticStatefulService(t, nil)
	_, err := HandleCreateDAG(svc)(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"bad-dag",
		"title":"Bad DAG",
		"nodes":[{
			"node_key":"writer",
			"title":"Writer",
			"node_type":"agent",
			"assigned_to":"writer-runner",
			"config":{"exec":{"provider":"codex","cwd":"/repo/a","codex_home":"/tmp/codex-home","codex_instance_key":"default","codex_model_provider":"openai"}}
		}]
	}`))
	if err == nil {
		t.Fatal("HandleCreateDAG() error = nil, want create-stage validation failure")
	}
	for _, want := range []string{"nodes[0].config.exec.prompt_key", "nodes[0].config.exec.agent_key"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("HandleCreateDAG() error = %q, want %q", err.Error(), want)
		}
	}
	assertDAGNotStored(t, svc, "bad-dag")
	assertNoRunsOrWakeups(t, svc)
}

func TestInvalidHybridVerifierDAGCreateFailsBeforeStoreStartAndWakeup(t *testing.T) {
	svc := newDiagnosticStatefulService(t, nil)
	_, err := HandleCreateDAG(svc)(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"bad-hybrid",
		"title":"Bad Hybrid",
		"nodes":[{
			"node_key":"review",
			"title":"Review",
			"node_type":"hybrid",
			"assigned_to":"review-runner",
			"config":{"exec":{"automation":{"kind":"command_card","command_ref":"run_tests"},"verifier":{"provider":"codex","cwd":"/repo/a"}}}
		}]
	}`))
	if err == nil {
		t.Fatal("HandleCreateDAG() error = nil, want hybrid verifier validation failure")
	}
	for _, want := range []string{
		"nodes[0].node_type",
		"hybrid",
		"reserved",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("HandleCreateDAG() error = %q, want %q", err.Error(), want)
		}
	}
	assertDAGNotStored(t, svc, "bad-hybrid")
	assertNoRunsOrWakeups(t, svc)
}

func TestTaskDiagnoseDAGPromptIdentityGapsToolRegistered(t *testing.T) {
	registry := NewRegistry(Dependencies{ToolPorts: ToolPorts{DAGIdentityDiagnostics: &golden.OrchestrationStub{}}})
	def, ok := registry.Lookup("task_diagnose_dag_prompt_identity_gaps")
	if !ok {
		t.Fatal("task_diagnose_dag_prompt_identity_gaps tool not registered")
	}
	if !strings.Contains(def.Description, "Read-only") {
		t.Fatalf("diagnostic tool description = %q, want read-only warning", def.Description)
	}
}

func discoverBuiltinAutomationPrompt(t *testing.T, ctx context.Context) promptTemplateDTO {
	t.Helper()
	result, err := HandlePromptList(stubPromptStore{
		list: func(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			if !filter.RuntimeVisible {
				t.Fatalf("prompt list RuntimeVisible = false, want true")
			}
			return nil, nil
		},
	}, &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "main/morning-brief", "内置晨报执行者"),
	}})(ctx, mustRawInput(t, promptListInput{Keyword: "晨报"}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	prompts := result.([]promptTemplateDTO)
	if len(prompts) != 1 {
		t.Fatalf("prompt_list result = %#v, want one builtin prompt", prompts)
	}
	return prompts[0]
}

func discoverCodexProvider(t *testing.T) ProviderModels {
	t.Helper()
	result, err := HandleListModels(WithModelRegistry(stubModelRegistry{providers: []ProviderModels{
		{Provider: "codex", Models: []string{"gpt-5.5"}},
	}}))(context.Background(), mustRawInput(t, ListModelsInput{Provider: "codex"}))
	if err != nil {
		t.Fatalf("HandleListModels() error = %v", err)
	}
	providers := result.(ListModelsResult).Providers
	if len(providers) != 1 || len(providers[0].Models) != 1 {
		t.Fatalf("list_models result = %#v, want one codex model", providers)
	}
	return providers[0]
}

func automationBriefNodeConfig(t *testing.T, promptKey, provider, model string) json.RawMessage {
	t.Helper()
	return rawTestJSON(t, map[string]any{
		"exec": map[string]any{
			"prompt_key":           promptKey,
			"provider":             provider,
			"model":                model,
			"cwd":                  "/repo/a",
			"codex_home":           "/tmp/codex-home",
			"codex_instance_key":   "default",
			"codex_model_provider": "openai",
		},
		"first_turn": "生成热点新闻简报，输出 Markdown 报告文件。",
		"outputs": map[string]any{
			"to_sharedfile":  map[string]any{"path": "reports/daily-hot-news.md", "lock_mode": "exclusive"},
			"to_node_result": true,
		},
	})
}

func assertUserRequestOmitsInternalFields(t *testing.T, request string) {
	t.Helper()
	for _, forbidden := range []string{"prompt_key", "agent_key", "provider", "cwd", "assigned_to"} {
		if strings.Contains(request, forbidden) {
			t.Fatalf("user request contains internal field %q: %s", forbidden, request)
		}
	}
}

func assertPersistedScheduledAutomationDAG(t *testing.T, detail contract.DAGDetail, promptKey, provider, model string) {
	t.Helper()
	if detail.DAG.DagKey != "daily-hot-news-brief" || detail.DAG.Version != 2 {
		t.Fatalf("persisted DAG summary = %+v, want scheduled DAG version 2", detail.DAG)
	}
	if detail.DAG.Trigger != "scheduled" || detail.DAG.CronExpr != "CRON_TZ=Asia/Shanghai 0 8 * * *" {
		t.Fatalf("persisted schedule = trigger:%q cron:%q, want scheduled cron", detail.DAG.Trigger, detail.DAG.CronExpr)
	}
	if !detail.DAG.ScheduleEnabled {
		t.Fatalf("persisted schedule_enabled = false, want true")
	}
	assertCreatedDAGFinalNode(t, detail.DAG.Metadata)
	if len(detail.Nodes) != 1 {
		t.Fatalf("persisted nodes = %#v, want one node", detail.Nodes)
	}
	assertPersistedDAGNodeExec(t, detail.Nodes[0], promptKey, provider, model)
}

func assertCreatedDAGFinalNode(t *testing.T, metadataJSON json.RawMessage) {
	t.Helper()
	var metadata struct {
		FinalNodeKey string `json:"final_node_key"`
	}
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v raw=%s", err, metadataJSON)
	}
	if metadata.FinalNodeKey != "brief" {
		t.Fatalf("final_node_key = %q, want brief", metadata.FinalNodeKey)
	}
}

func assertPersistedDAGNodeExec(t *testing.T, node contract.DAGNode, promptKey, provider, model string) {
	t.Helper()
	if node.NodeKey != "brief" || node.AssignedTo != "daily-hot-news-brief-runner" {
		t.Fatalf("persisted node = %+v, want brief node assigned to runner", node)
	}
	config := decodeCreatedNodeExec(t, node.Config)
	if config.PromptKey != promptKey || config.Provider != provider || config.Model != model {
		t.Fatalf("persisted node exec = %+v, want prompt/provider/model", config)
	}
	if config.CWD != "/repo/a" || config.CodexHome == "" || config.CodexInstanceKey == "" || config.CodexModelProvider == "" {
		t.Fatalf("persisted node exec runtime identity incomplete = %+v", config)
	}
}

func decodeCreatedNodeExec(t *testing.T, configJSON json.RawMessage) createdNodeExec {
	t.Helper()
	var config struct {
		Exec createdNodeExec `json:"exec"`
	}
	if err := json.Unmarshal(configJSON, &config); err != nil {
		t.Fatalf("unmarshal node config: %v raw=%s", err, configJSON)
	}
	return config.Exec
}

type createdNodeExec struct {
	PromptKey          string `json:"prompt_key"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	CWD                string `json:"cwd"`
	CodexHome          string `json:"codex_home"`
	CodexInstanceKey   string `json:"codex_instance_key"`
	CodexModelProvider string `json:"codex_model_provider"`
}

func diagnosticDAGDetails(t *testing.T) map[string]contract.DAGDetail {
	t.Helper()
	return map[string]contract.DAGDetail{
		"valid":               diagnosticValidDAG(t),
		"bad-agent":           diagnosticBadAgentDAG(t),
		"bad-hybrid-provider": diagnosticBadHybridProviderDAG(t),
		"bad-hybrid-codex":    diagnosticBadHybridCodexDAG(t),
	}
}

func newDiagnosticTestService(t *testing.T, details map[string]contract.DAGDetail) *golden.OrchestrationStub {
	t.Helper()
	return &golden.OrchestrationStub{
		ListDAGsFunc: func(_ context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
			if filter.Limit != 10 {
				t.Fatalf("ListDAGs limit = %d, want 10", filter.Limit)
			}
			return diagnosticDAGSummaries(), nil
		},
		GetDAGFunc: func(_ context.Context, dagKey string) (contract.DAGDetail, error) {
			detail, ok := details[dagKey]
			if !ok {
				t.Fatalf("unexpected GetDAG(%q)", dagKey)
			}
			return detail, nil
		},
		ListRunsFunc: func(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
			if req.Limit != 1 {
				t.Fatalf("ListRuns(%s) limit = %d, want recent-only lookup", req.DagKey, req.Limit)
			}
			return contract.ListRunsResponse{
				Runs: []contract.Run{{RunKey: req.DagKey + "#run-1", DagKey: req.DagKey, Status: "failed"}},
			}, nil
		},
	}
}

func diagnosticDAGSummaries() []contract.DAGSummary {
	return []contract.DAGSummary{
		{DagKey: "valid"},
		{DagKey: "bad-agent"},
		{DagKey: "bad-hybrid-provider"},
		{DagKey: "bad-hybrid-codex"},
	}
}

func diagnosticValidDAG(t *testing.T) contract.DAGDetail {
	t.Helper()
	return contract.DAGDetail{
		DAG: contract.DAGSummary{DagKey: "valid"},
		Nodes: []contract.DAGNode{{
			DagKey:     "valid",
			NodeKey:    "ok-agent",
			NodeType:   "agent",
			AssignedTo: "runner",
			Config:     rawTestJSON(t, map[string]any{"exec": codexExecMap("main/brief")}),
		}},
	}
}

func diagnosticBadAgentDAG(t *testing.T) contract.DAGDetail {
	t.Helper()
	return contract.DAGDetail{
		DAG: contract.DAGSummary{DagKey: "bad-agent"},
		Nodes: []contract.DAGNode{{
			DagKey:     "bad-agent",
			NodeKey:    "writer",
			AssignedTo: "writer-runner",
			Config: rawTestJSON(t, map[string]any{"exec": map[string]any{
				"provider":             "codex",
				"cwd":                  "/repo/a",
				"codex_home":           "/tmp/codex-home",
				"codex_instance_key":   "default",
				"codex_model_provider": "openai",
			}}),
		}},
	}
}

func diagnosticBadHybridProviderDAG(t *testing.T) contract.DAGDetail {
	t.Helper()
	return contract.DAGDetail{
		DAG: contract.DAGSummary{DagKey: "bad-hybrid-provider"},
		Nodes: []contract.DAGNode{{
			DagKey:     "bad-hybrid-provider",
			NodeKey:    "review",
			NodeType:   "hybrid",
			AssignedTo: "review-runner",
			Config: rawTestJSON(t, map[string]any{"exec": map[string]any{
				"automation": map[string]any{"kind": "command_card", "command_ref": "run_tests"},
				"verifier":   map[string]any{"cwd": "/repo/a"},
			}}),
		}},
	}
}

func diagnosticBadHybridCodexDAG(t *testing.T) contract.DAGDetail {
	t.Helper()
	return contract.DAGDetail{
		DAG: contract.DAGSummary{DagKey: "bad-hybrid-codex"},
		Nodes: []contract.DAGNode{{
			DagKey:     "bad-hybrid-codex",
			NodeKey:    "codex-review",
			NodeType:   "hybrid",
			AssignedTo: "codex-review-runner",
			Config: rawTestJSON(t, map[string]any{"exec": map[string]any{
				"automation": map[string]any{"kind": "command_card", "command_ref": "run_tests"},
				"verifier":   map[string]any{"provider": "codex", "prompt_key": "main/review", "cwd": "/repo/a"},
			}}),
		}},
	}
}

func assertDiagnosticGapKeys(t *testing.T, gaps []DAGPromptIdentityGap) {
	t.Helper()
	want := []string{"bad-agent/writer", "bad-hybrid-provider/review", "bad-hybrid-codex/codex-review"}
	if got := gapNodeKeys(gaps); !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic node keys = %#v, want %#v", got, want)
	}
}

func assertDiagnosticRunStatusAndRemediation(t *testing.T, out DAGPromptIdentityDiagnosticsOutput) {
	t.Helper()
	for _, gap := range out.Gaps {
		if gap.RecentRunStatus != "failed" {
			t.Fatalf("gap %s/%s recent run status = %q, want failed", gap.DagKey, gap.NodeKey, gap.RecentRunStatus)
		}
	}
	if !out.ReadOnly || !strings.Contains(out.Remediation, "task_dag_apply_ops") || !strings.Contains(out.Remediation, "重建") {
		t.Fatalf("diagnostic remediation/read_only = %#v", out)
	}
}

func assertDiagnosticScanStats(
	t *testing.T,
	out DAGPromptIdentityDiagnosticsOutput,
	scannedDAGs int,
	dagScanLimit int,
	dagScanPossiblyTruncated bool,
) {
	t.Helper()
	if out.ScannedDAGs != scannedDAGs ||
		out.DAGScanLimit != dagScanLimit ||
		out.DAGScanPossiblyTruncated != dagScanPossiblyTruncated {
		t.Fatalf(
			"scan stats = scanned:%d limit:%d possibly_truncated:%v, want scanned:%d limit:%d possibly_truncated:%v",
			out.ScannedDAGs,
			out.DAGScanLimit,
			out.DAGScanPossiblyTruncated,
			scannedDAGs,
			dagScanLimit,
			dagScanPossiblyTruncated,
		)
	}
}

func assertGapMissingFields(t *testing.T, gaps []DAGPromptIdentityGap, dagKey, nodeKey string, fields ...string) {
	t.Helper()
	for _, gap := range gaps {
		if gap.DagKey != dagKey || gap.NodeKey != nodeKey {
			continue
		}
		for _, field := range fields {
			if !slices.Contains(gap.MissingFields, field) {
				t.Fatalf("gap %s/%s missing fields = %#v, want %q", dagKey, nodeKey, gap.MissingFields, field)
			}
		}
		return
	}
	t.Fatalf("gap %s/%s not found in %#v", dagKey, nodeKey, gaps)
}

func gapNodeKeys(gaps []DAGPromptIdentityGap) []string {
	keys := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		keys = append(keys, gap.DagKey+"/"+gap.NodeKey)
	}
	return keys
}

func codexExecMap(promptKey string) map[string]any {
	return map[string]any{
		"prompt_key":           promptKey,
		"provider":             "codex",
		"cwd":                  "/repo/a",
		"codex_home":           "/tmp/codex-home",
		"codex_instance_key":   "default",
		"codex_model_provider": "openai",
	}
}

func rawTestJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}

func automationToolTestContext() context.Context {
	return mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "designer-1", CWD: "/repo/a"})
}
