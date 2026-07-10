package prompt_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasource "github.com/anthropic-ai/super-agent-v3/internal/module/datasource"
	memory "github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	thread "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	turnpkg "github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"go.uber.org/fx"
)

type fxHarness struct {
	assembly      promptpkg.PromptAssemblyService
	registry      promptpkg.PromptRegistry
	memorySvc     memory.Service
	threadSvc     thread.Service
	bridge        *capturingSessionBridge
	threadStore   *capturingThreadStore
	bindingStore  *capturingBindingStore
	orchestration *capturingOrchestration
	projectRoot   string
	codexHome     string
}

func TestFxMemoryPromptIntegration(t *testing.T) {
	h := newFxHarness(t)
	if h.assembly == nil || h.registry == nil || h.memorySvc == nil || h.threadSvc == nil {
		t.Fatalf("fx wiring incomplete: assembly=%v registry=%v memory=%v thread=%v", h.assembly != nil, h.registry != nil, h.memorySvc != nil, h.threadSvc != nil)
	}
	start, err := h.assembly.AssembleStart(context.Background(), promptpkg.StartInput{Provider: "codex", CWD: h.projectRoot})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !hasSection(start.ResolvedSections, promptpkg.DynamicSectionMemory) {
		t.Fatalf("ResolvedSections missing %q: %#v", promptpkg.DynamicSectionMemory, start.ResolvedSections)
	}
}

func TestAssembleStartProducesValidOutput(t *testing.T) {
	h := newFxHarness(t)
	start, err := h.assembly.AssembleStart(context.Background(), promptpkg.StartInput{
		Name:                  "Feature Thread",
		Prompt:                "legacy prompt",
		BaseInstructions:      "existing base tail",
		DeveloperInstructions: "developer tail",
		Provider:              "codex",
		CWD:                   h.projectRoot,
		Language:              "Go",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	for _, section := range promptpkg.StaticSections() {
		if !hasSection(start.ResolvedSections, section.Name) {
			t.Fatalf("ResolvedSections missing %q: %#v", section.Name, start.ResolvedSections)
		}
		mustContain(t, start.BaseInstructions, sectionContent(start.ResolvedSections, section.Name))
	}
	mustContain(t, start.BaseInstructions, "existing base tail")
	if start.DisplayName != "Feature Thread" {
		t.Fatalf("DisplayName = %q, want %q", start.DisplayName, "Feature Thread")
	}
	if start.Snapshot.Provider != "codex" || start.Snapshot.Hash == "" {
		t.Fatalf("unexpected snapshot = %#v", start.Snapshot)
	}
}

func TestAssembleTurnProducesUserContext(t *testing.T) {
	h := newFxHarness(t)
	mustRegisterProvider(t, h.registry, promptpkg.DynamicSectionSessionGuidance, func(in promptpkg.SectionContext) string {
		return fmt.Sprintf("user=%s cwd=%s", in.Turn.UserText, in.BuildCtx.CWD)
	})
	turn, err := h.assembly.AssembleTurn(context.Background(), promptpkg.TurnInput{
		UserText: "please verify the cache",
		CWD:      h.projectRoot,
		RuntimeUserContext: map[string]string{
			"workerToolsContext": "Workers can use bash and read tools.",
			"terminalFocus":      "The terminal is unfocused — the user is not actively watching.",
		},
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	mustContain(t, contract.RenderUserContextMessage(turn), "<system-reminder>")
	mustContain(t, turn.UserContextText, "# currentDate")
	mustContain(t, turn.UserContextText, "# workerToolsContext")
	mustContain(t, turn.UserContextText, "# terminalFocus")
	mustContain(t, turn.UserContextText, "# runtimeExtras")
	mustContain(t, turn.UserContext["workerToolsContext"], "Workers can use bash and read tools.")
	mustContain(t, turn.UserContext["terminalFocus"], "The terminal is unfocused")
	mustContain(t, sectionContent(turn.ResolvedSections, promptpkg.DynamicSectionSessionGuidance), "please verify the cache")
	mustContain(t, sectionContent(turn.ResolvedSections, promptpkg.DynamicSectionSessionGuidance), h.projectRoot)
}

func TestDatasourceInjectsIntoTurnPrompt(t *testing.T) {
	h := newFxHarness(t)

	turn, err := h.assembly.AssembleTurn(context.Background(), promptpkg.TurnInput{
		UserText: "use the uploaded datasource",
		CWD:      h.projectRoot,
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}

	datasourceSection := sectionContent(turn.ResolvedSections, promptpkg.DynamicSectionDatasource)
	mustContain(t, datasourceSection, "### launch-notes.txt")
	mustContain(t, datasourceSection, "datasource text from FX wiring")
}

func TestMemoryRulesInjectIntoPrompt(t *testing.T) {
	h := newFxHarness(t)
	start, err := h.assembly.AssembleStart(context.Background(), promptpkg.StartInput{Provider: "codex", CWD: h.projectRoot})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !hasSection(start.ResolvedSections, promptpkg.DynamicSectionMemory) {
		t.Fatalf("ResolvedSections missing %q: %#v", promptpkg.DynamicSectionMemory, start.ResolvedSections)
	}
	mustContain(t, start.BaseInstructions, sectionContent(start.ResolvedSections, promptpkg.DynamicSectionMemory))
	// newFxHarness wires both AutoMemPath and TeamMemPath, which triggers
	// buildCombinedMemoryPrompt (memory/rules.go): combined mode inserts
	// "### 2. memory scope" after "### 1. memory system", pushing taxonomy
	// to section #3. Standard mode (BuildMemoryLines, exercised in
	// internal/module/memory/rules_test.go) still keeps taxonomy at #2.
	mustContain(t, sectionContent(start.ResolvedSections, promptpkg.DynamicSectionMemory), "### 3. taxonomy")
}

func TestSectionCacheInvalidation(t *testing.T) {
	h := newFxHarness(t)
	calls := 0
	mustRegisterProvider(t, h.registry, promptpkg.DynamicSectionSessionGuidance, func(promptpkg.SectionContext) string {
		calls++
		return fmt.Sprintf("session guidance build #%d", calls)
	})
	first, err := h.assembly.AssembleTurn(context.Background(), promptpkg.TurnInput{CWD: h.projectRoot})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	second, err := h.assembly.AssembleTurn(context.Background(), promptpkg.TurnInput{CWD: h.projectRoot})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("dynamic provider calls = %d, want 1 before invalidate", calls)
	}
	firstContent := sectionContent(first.ResolvedSections, promptpkg.DynamicSectionSessionGuidance)
	secondContent := sectionContent(second.ResolvedSections, promptpkg.DynamicSectionSessionGuidance)
	if firstContent != secondContent {
		t.Fatalf("cached turn mismatch: first=%q second=%q", firstContent, secondContent)
	}
	if err := h.assembly.Invalidate(context.Background(), promptpkg.InvalidateClear); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	third, err := h.assembly.AssembleTurn(context.Background(), promptpkg.TurnInput{CWD: h.projectRoot})
	if err != nil {
		t.Fatalf("third AssembleTurn() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("dynamic provider calls = %d, want 2 after invalidate", calls)
	}
	thirdContent := sectionContent(third.ResolvedSections, promptpkg.DynamicSectionSessionGuidance)
	if thirdContent == firstContent {
		t.Fatalf("resolved section did not rebuild after invalidate: %q", thirdContent)
	}
}

func TestFullChainFromThreadToProvider(t *testing.T) {
	h := newFxHarness(t)
	in := promptpkg.StartInput{
		Name:                  "E2E Thread",
		Prompt:                "ship the feature",
		BaseInstructions:      "existing base tail",
		DeveloperInstructions: "be concise",
		Provider:              "codex",
		CWD:                   h.projectRoot,
		Model:                 "gpt-5",
		Language:              "Go",
	}
	result, err := h.threadSvc.Start(context.Background(), thread.StartRequest{
		Provider:                     in.Provider,
		ModelProvider:                in.Provider,
		Name:                         in.Name,
		Prompt:                       in.Prompt,
		BaseInstructions:             in.BaseInstructions,
		DeveloperInstructions:        in.DeveloperInstructions,
		CWD:                          in.CWD,
		Model:                        in.Model,
		Language:                     in.Language,
		GitRoot:                      in.GitRoot,
		IsWorktree:                   in.IsWorktree,
		EnabledTools:                 append([]string(nil), in.EnabledTools...),
		AdditionalWorkingDirectories: append([]string(nil), in.AdditionalWorkingDirectories...),
		MCPSnapshot:                  in.MCPSnapshot,
		SessionFlags:                 maps.Clone(in.SessionFlags),
		Config: map[string]any{
			contract.CodexHomeKey:          h.codexHome,
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err != nil {
		t.Fatalf("thread.Start() error = %v", err)
	}
	start := h.bridge.startReq.StartAssembly
	mustContain(t, start.BaseInstructions, sectionContent(start.ResolvedSections, promptpkg.SectionIdentity))
	mustContain(t, start.BaseInstructions, sectionContent(start.ResolvedSections, promptpkg.DynamicSectionMemory))
	if h.bridge.startReq.Instructions != h.bridge.startReq.StartAssembly.BaseInstructions {
		t.Fatalf("provider Instructions = %q, want provider StartAssembly.BaseInstructions %q", h.bridge.startReq.Instructions, h.bridge.startReq.StartAssembly.BaseInstructions)
	}
	if h.bridge.startReq.StartAssembly.DisplayName != start.DisplayName {
		t.Fatalf("provider display name = %q, want %q", h.bridge.startReq.StartAssembly.DisplayName, start.DisplayName)
	}
	mustContain(t, h.bridge.startReq.StartAssembly.BaseInstructions, sectionContent(start.ResolvedSections, promptpkg.SectionIdentity))
	mustContain(t, h.bridge.startReq.StartAssembly.BaseInstructions, sectionContent(start.ResolvedSections, promptpkg.DynamicSectionMemory))
	mustContain(t, h.bridge.startReq.StartAssembly.BaseInstructions, "existing base tail")
	if got := configString(h.bridge.startReq.Config, "developerInstructions"); got != "be concise" {
		t.Fatalf("developerInstructions = %q, want developer tail only", got)
	}
	assertFullChainCodexIdentity(t, h, h.codexHome)
	assertFullChainStartResult(t, result)
	assertFullChainSideEffects(t, h, start.DisplayName)
}

func assertFullChainCodexIdentity(t *testing.T, h *fxHarness, wantHome string) {
	t.Helper()
	if got := configString(h.bridge.startReq.Config, contract.CodexHomeKey); got != wantHome {
		t.Fatalf("provider codexHome = %q, want %q", got, wantHome)
	}
	if got := configString(h.bridge.startReq.Config, contract.CodexInstanceKeyKey); got != "default" {
		t.Fatalf("provider codexInstanceKey = %q, want default", got)
	}
	if got := configString(h.bridge.startReq.Config, contract.CodexModelProviderKey); got != "openai" {
		t.Fatalf("provider codexModelProvider = %q, want openai", got)
	}
	if h.bindingStore.upsert.CodexHome != wantHome ||
		h.bindingStore.upsert.CodexInstanceKey != "default" ||
		h.bindingStore.upsert.CodexModelProvider != "openai" {
		t.Fatalf("binding codex identity = (%q,%q,%q), want (%q,default,openai)",
			h.bindingStore.upsert.CodexHome,
			h.bindingStore.upsert.CodexInstanceKey,
			h.bindingStore.upsert.CodexModelProvider,
			wantHome)
	}
}

func assertFullChainStartResult(t *testing.T, result thread.StartResult) {
	t.Helper()
	if result.Status != "running" || result.Provider != "codex" {
		t.Fatalf("unexpected StartResult = %#v", result)
	}
}

func assertFullChainSideEffects(t *testing.T, h *fxHarness, wantDisplayName string) {
	t.Helper()
	if h.orchestration.launchReq.Name != wantDisplayName {
		t.Fatalf("launch display name = %q, want %q", h.orchestration.launchReq.Name, wantDisplayName)
	}
	if h.threadStore.upsert.Prompt != wantDisplayName {
		t.Fatalf("persisted prompt = %q, want %q", h.threadStore.upsert.Prompt, wantDisplayName)
	}
	if h.bindingStore.upsert.ProviderThreadID != "019e0bcb-0cf7-7982-964f-c2654783ba17" {
		t.Fatalf("binding provider thread id = %q, want %q", h.bindingStore.upsert.ProviderThreadID, "019e0bcb-0cf7-7982-964f-c2654783ba17")
	}
	if !containsString(h.orchestration.launchReq.Env, "AGENT_PROVIDER=codex") || !containsString(h.orchestration.launchReq.Env, "AGENT_MODEL=gpt-5") {
		t.Fatalf("launch env = %#v, want provider/model injection", h.orchestration.launchReq.Env)
	}
}

func newFxHarness(t *testing.T) *fxHarness {
	t.Helper()
	h := &fxHarness{projectRoot: newGitProjectRoot(t)}
	codexHome := filepath.Join(h.projectRoot, "codex-home")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("create codex home fixture: %v", err)
	}
	canonicalCodexHome, err := contract.CanonicalizeCodexHome(codexHome)
	if err != nil {
		t.Fatalf("canonicalize codex home fixture: %v", err)
	}
	h.codexHome = canonicalCodexHome
	t.Setenv("ENABLE_MEMORY_SYSTEM", "1")
	t.Setenv("MULTI_AGENT_MEMORY_FEATURE_TEAMMEM", "1")
	t.Setenv("MULTI_AGENT_MEMORY_DIR", filepath.Join(h.projectRoot, "memory"))
	rolloutPath := filepath.Join(h.projectRoot, "rollout-019e0bcb-0cf7-7982-964f-c2654783ba17.jsonl")
	if err := os.WriteFile(rolloutPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write rollout fixture: %v", err)
	}
	h.bridge = &capturingSessionBridge{session: newMockSessionWithRolloutPath("019e0bcb-0cf7-7982-964f-c2654783ba17", rolloutPath)}
	h.threadStore = &capturingThreadStore{}
	h.bindingStore = &capturingBindingStore{}
	h.orchestration = &capturingOrchestration{}
	datasourceStore := &fxDatasourceStore{documents: []datasource.DatasourceDocument{
		{
			WorkspaceRoot: h.projectRoot,
			Name:          "launch-notes.txt",
			Extension:     ".txt",
			Content:       "datasource text from FX wiring",
		},
	}}
	app := fx.New(
		fx.NopLogger,
		fx.Supply(testHarnessConfig(h.projectRoot)),
		fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil))),
		fx.Supply(fx.Annotate(datasourceStore, fx.As(new(datasource.DatasourceDocumentStore)))),
		fx.Provide(
			func() thread.SessionStarter { return h.bridge },
			func() thread.SessionProvider { return h.bridge },
			func() thread.ThreadStore { return h.threadStore },
			func() thread.BindingStore { return h.bindingStore },
			newE2EPromptStore,
			func() turnpkg.Service { return &noopTurnService{} },
			func() thread.OrchestrationFacade { return h.orchestration },
			func(store datasource.DatasourceDocumentStore) *datasource.PromptProvider {
				return datasource.NewPromptProvider(datasource.NewServiceWithStore(store))
			},
		),
		fx.Invoke(func(reg contract.DynamicSectionRegistrar, provider *datasource.PromptProvider) error {
			if unregister, ok := reg.(interface {
				UnregisterDynamicProvider(string) bool
			}); ok {
				unregister.UnregisterDynamicProvider(provider.SectionName())
			}
			return reg.RegisterDynamicProvider(provider)
		}),
		promptpkg.Module,
		memory.Module,
		thread.Module,
		fx.Populate(&h.assembly, &h.registry, &h.memorySvc, &h.threadSvc),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("app.Stop() error = %v", err)
		}
	})
	return h
}

func testHarnessConfig(projectRoot string) *contract.Config {
	return &contract.Config{
		ProjectRoot: projectRoot,
		Dependency:  contract.DependencyConfig{Profile: contract.DependencyProfileTest},
	}
}

func newGitProjectRoot(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	cmd := exec.Command("git", "init", projectRoot)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", projectRoot, err, string(output))
	}
	return projectRoot
}

func mustRegisterProvider(t *testing.T, registry promptpkg.PromptRegistry, name string, build func(promptpkg.SectionContext) string) {
	t.Helper()
	registry.UnregisterDynamicProvider(name)
	err := registry.RegisterDynamicProvider(promptpkg.DynamicTextProvider{
		Name: name,
		ResolveFunc: func(_ context.Context, in promptpkg.SectionContext) (*string, error) {
			text := strings.TrimSpace(build(in))
			if text == "" {
				return nil, nil
			}
			return &text, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterDynamicProvider(%q) error = %v", name, err)
	}
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("text missing %q:\n%s", want, got)
	}
}

func hasSection(sections []promptpkg.ResolvedPromptSection, name string) bool {
	return sectionContent(sections, name) != ""
}

func sectionContent(sections []promptpkg.ResolvedPromptSection, name string) string {
	for _, section := range sections {
		if section.Name == name {
			return section.Content
		}
	}
	return ""
}

func configString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	value, _ := cfg[key].(string)
	return strings.TrimSpace(value)
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

type fxDatasourceStore struct {
	documents []datasource.DatasourceDocument
}

func (s *fxDatasourceStore) UpsertDocument(context.Context, datasource.UpsertDatasourceDocumentParams) error {
	return nil
}

func (s *fxDatasourceStore) ListDocuments(_ context.Context, workspaceRoot string) ([]datasource.DatasourceDocument, error) {
	out := make([]datasource.DatasourceDocument, 0, len(s.documents))
	for _, document := range s.documents {
		if strings.TrimSpace(document.WorkspaceRoot) == strings.TrimSpace(workspaceRoot) {
			out = append(out, document)
		}
	}
	return out, nil
}

func (s *fxDatasourceStore) ListPromptDocuments(ctx context.Context, workspaceRoot string, maxDocuments int, maxWorkspaceBytes int64, maxDocumentBytes int64) ([]datasource.DatasourceDocument, error) {
	documents, err := s.ListDocuments(ctx, workspaceRoot)
	if err != nil {
		return nil, err
	}
	if len(documents) > maxDocuments {
		return nil, fmt.Errorf("datasource prompt documents exceed count cap: %d > %d", len(documents), maxDocuments)
	}
	var totalBytes int64
	for _, document := range documents {
		documentBytes := int64(len([]byte(document.Content)))
		if documentBytes > maxDocumentBytes {
			return nil, fmt.Errorf("datasource prompt document %q exceeds byte cap: %d > %d", document.Name, documentBytes, maxDocumentBytes)
		}
		totalBytes += documentBytes
		if totalBytes > maxWorkspaceBytes {
			return nil, fmt.Errorf("datasource prompt documents exceed byte cap: %d > %d", totalBytes, maxWorkspaceBytes)
		}
	}
	return documents, nil
}

func (s *fxDatasourceStore) DeleteDocument(context.Context, string, string) error {
	return nil
}
