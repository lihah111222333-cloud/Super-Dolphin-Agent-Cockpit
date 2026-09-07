//go:build e2e

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type standaloneScenario struct {
	layer string
	run   func(*testing.T)
}

// standaloneScenarios 按真实边界分类，不把进程内或模拟证据命名为 binary E2E。
func standaloneScenarios() map[string][]standaloneScenario {
	return map[string][]standaloneScenario{
		"control": {
			{"binary-operator-authorization-negative", TestStandaloneBinaryDirectPeerOperatorBoundary},
			{"binary", TestMCPOrchServeBearerBoundary},
			{"binary", TestMCPOrchServeBlackBoxSingleInstance},
			{"binary", TestMCPOrchServeRejectsSecondInstance},
			{"binary", TestMCPOrchServeReclaimsAfterOwnerKilled},
			{"binary", TestMCPOrchServeRejectsInvalidCLIAndConfig},
			{"binary", TestMCPOrchServeIndependentStateDomains},
			{"binary+db-observation", TestMCPOrchServeOperatorSourceLifecycle},
			{"binary+db-injected-revoke", TestMCPOrchServeOperatorSourceHTTPFence},
			{"in-process-fx-store", TestStandaloneDirectPeerPersistsAcrossRestart},
			{"in-process-fx-store", TestStandaloneDirectPeerSupportsBidirectionalConversation},
			{"in-process-fx-store", TestStandaloneDirectPeerOutboxProjectsEvent},
			{"simulated-process-identity", TestStandaloneManagedProcessReapingEvidence},
			{"synthetic-client-frames", TestStandaloneClaudeStreamAssertions},
			{"real-sqlite-owner-fixtures", TestStandaloneClaudeTerminalEvidenceRejectsUnrelatedPersistence},
		},
		"clients": {
			{"binary+http-managed-real-model", TestStandaloneBinaryCodexLifecycleThroughMCP},
			{"binary+official-cli-no-model", TestStandaloneOfficialClaudeConnection},
			{"official-cli-control-no-model", TestStandaloneOfficialClaudeProbe},
			{"binary+official-cli-no-model", TestStandaloneOfficialCodexClient},
			{"binary+official-cli-real-model", TestStandaloneOfficialClaudeToolCall},
			{"binary+official-cli-managed-real-model", TestStandaloneCodexClientLaunchesManagedCodexThroughMCP},
			{"binary+http-managed-real-model", TestStandaloneBinaryLaunchesManagedClaudeThroughMCP},
			{"binary+http-managed-real-model+db-observation", TestStandaloneBinaryFullClaudeLifecycle},
		},
		"lifecycle": {
			{"binary+http-managed-real-model+os-process-identity+provider-history-observation+db-observation", TestStandaloneBinaryCodexAutoRecoversProviderCrash},
			{"execution-real-cli", TestStandaloneManagedCodexTurn},
			{"execution-real-cli", TestStandaloneManagedCodexFork},
			{"execution-real-cli", TestStandaloneManagedCodexResume},
			{"execution-real-cli", TestStandaloneManagedCodexArchive},
			{"execution-real-cli", TestStandaloneManagedCodexInterrupt},
			{"execution-real-cli", TestStandaloneManagedClaudeTurn},
			{"in-process-fx-real-cli", TestStandaloneManagedClaudeStop},
			{"in-process-fx-real-cli", TestStandaloneManagedDaemonRehydratesCodexAfterExecutionLoss},
		},
		"delivery": {
			{"in-process-fx-real-cli", TestStandaloneDirectPeerSubmitsReceiverTurn},
			{"in-process-fx-real-cli", TestStandaloneDirectPeerSubmitsClaudeReceiverTurn},
			{"in-process-fx-real-cli", TestStandaloneDirectPeerCrossProviderConversation},
		},
	}
}

func TestStandaloneControlAndStoreMatrix(t *testing.T)   { runStandaloneScenarioGroup(t, "control") }
func TestStandaloneCLIClientMatrix(t *testing.T)         { runStandaloneScenarioGroup(t, "clients") }
func TestStandaloneProviderLifecycleMatrix(t *testing.T) { runStandaloneScenarioGroup(t, "lifecycle") }
func TestStandaloneNativeDeliveryMatrix(t *testing.T)    { runStandaloneScenarioGroup(t, "delivery") }

// TestStandaloneAcceptanceMatrix 在同一测试进程内串行重跑全部已登记 standalone 场景。
func TestStandaloneAcceptanceMatrix(t *testing.T) {
	for _, group := range []string{"control", "clients", "lifecycle", "delivery"} {
		t.Run(group, func(t *testing.T) { runStandaloneScenarioGroup(t, group) })
	}
}

// runStandaloneScenarioGroup 串行运行已分类场景并拒绝任何未执行的 skip。
func runStandaloneScenarioGroup(t *testing.T, group string) {
	assertStandaloneScenarioRegistry(t)
	for _, scenario := range standaloneScenarios()[group] {
		t.Run(standaloneScenarioName(scenario.run), func(t *testing.T) {
			t.Logf("evidence_layer=%s", scenario.layer)
			defer func() {
				if t.Skipped() {
					t.Error("scenario skipped; full matrix is not verified")
				}
			}()
			scenario.run(t)
		})
	}
}

func standaloneScenarioName(run func(*testing.T)) string {
	name := runtime.FuncForPC(reflect.ValueOf(run).Pointer()).Name()
	return name[strings.LastIndex(name, ".")+1:]
}

// assertStandaloneScenarioRegistry 从源码动态枚举全部 tagged E2E 入口，防止漏登记新增场景。
func assertStandaloneScenarioRegistry(t *testing.T) {
	t.Helper()
	registered := make(map[string]bool)
	for _, group := range standaloneScenarios() {
		for _, scenario := range group {
			name := standaloneScenarioName(scenario.run)
			if registered[name] {
				t.Fatalf("duplicate scenario %s", name)
			}
			registered[name] = true
		}
	}
	files, err := filepath.Glob(filepath.Join(serveE2ERepoRoot(t), "cmd", "mcp-orch", "*e2e_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("enumerate scenarios: files=%d err=%v", len(files), err)
	}
	for _, path := range files {
		checkStandaloneScenarioFile(t, path, registered)
	}
	for name := range registered {
		t.Errorf("stale scenario registration %s", name)
	}
}

func checkStandaloneScenarioFile(t *testing.T, path string, registered map[string]bool) {
	t.Helper()
	if filepath.Base(path) == "standalone_scenario_matrix_e2e_test.go" {
		return
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		if !registered[fn.Name.Name] {
			t.Errorf("unregistered scenario %s in %s", fn.Name.Name, path)
		}
		delete(registered, fn.Name.Name)
	}
}
