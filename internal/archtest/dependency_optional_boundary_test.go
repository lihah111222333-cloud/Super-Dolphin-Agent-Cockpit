package archtest_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	optionalDependencyClassAbsence  = "dependency_absence"
	optionalDependencyClassAdjunct  = "adjunct_optional"
	optionalDependencyClassTemplate = "test_or_template"
)

type optionalDependencyAnchor struct {
	Path     string
	Snippet  string
	Class    string
	Owner    string
	Evidence string
}

var allowedOptionalDependencyAnchors = []optionalDependencyAnchor{
	{Path: "internal/app/runner.go", Snippet: "RootCtx           RootCtxProvider         `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/app", Evidence: "TestBindRuntimeInheritsRootContext"},
	{Path: "internal/app/runner.go", Snippet: "Lifecycle         *uiwails.WailsLifecycle `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/app", Evidence: "BindRuntime"},
	{Path: "internal/app/runner.go", Snippet: "} `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/app", Evidence: "TestBindRuntimeRequiresExtractionDrainer"},
	{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "Service    contract.OrchestrationService `optional:\"true\"`", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "runtime_reporter.orchestration_service"},
	{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "Logger     *slog.Logger                  `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/app", Evidence: "TestNewRuntimeReporterAllowsDesktopExternalOrchestration"},
	{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "Dependency contract.DependencyConfig     `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/app", Evidence: "appDependencyProfile"},
	{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "Config     *contract.Config              `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/app", Evidence: "appDependencyProfile"},
	{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "return desktopExternalRuntimeReporter{logger: p.Logger, profile: profile}, nil", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "runtime_reporter.orchestration_service"},

	{Path: "internal/module/thread/module.go", Snippet: "fx.ParamTags(\"\", `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`, `optional:\"true\"`),", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "thread.bind_session_generation"},
	{Path: "internal/module/thread/module.go", Snippet: "fx.Annotate(NewThreadHandlers, fx.ParamTags(\"\", `optional:\"true\"`)),", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "NewThreadHandlers"},
	{Path: "internal/module/thread/module.go", Snippet: "Store threadstore.Store `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "provideThreadServiceStorePort"},
	{Path: "internal/module/thread/module.go", Snippet: "Store bindingstore.Store `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "provideBindingServiceStorePort"},
	{Path: "internal/module/thread/module.go", Snippet: "Store sharedfilestore.Store `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "provideSharedFileServiceStorePort"},
	{Path: "internal/module/thread/module.go", Snippet: "Store promptstore.Store `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "providePromptServiceStorePort"},
	{Path: "internal/module/thread/module.go", Snippet: "Catalog promptstore.RuntimePromptCatalog `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "providePromptServiceCatalogPort"},
	{Path: "internal/module/thread/module.go", Snippet: "Registrar     contract.DynamicSectionRegistrar `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "registerThreadPromptProviders"},
	{Path: "internal/module/thread/module.go", Snippet: "PromptStore   promptstore.Store                `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "registerThreadPromptProviders"},
	{Path: "internal/module/thread/module.go", Snippet: "Builtin       contract.BuiltinPromptRegistry   `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "registerThreadPromptProviders"},
	{Path: "internal/module/thread/module.go", Snippet: "PromptCatalog promptstore.RuntimePromptCatalog `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "registerThreadPromptProviders"},
	{Path: "internal/module/thread/module.go", Snippet: "PromptStore promptstore.Store              `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "provideRuntimePromptCatalog"},
	{Path: "internal/module/thread/module.go", Snippet: "Builtin     contract.BuiltinPromptRegistry `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/module/thread", Evidence: "provideRuntimePromptCatalog"},

	{Path: "internal/platform/toolbridge/module.go", Snippet: "Resolver     difftracker.WorkDirResolver `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "validateToolbridgeDependencies"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "BindingStore    agentThreadLookup         `optional:\"true\"`", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "toolbridge.agent_thread_lookup"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "ThreadStore     threadConfigOverrideStore `optional:\"true\"`", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "toolbridge.thread_config_override_store"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Preferences     uiPreferenceReader        `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "validateToolbridgeDependencies"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Config          *platformconfig.Config    `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "validateToolbridgeConfigDependency"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Logger          *pkglogger.Logger          `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "NewHandler"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Tracer          *observability.Service     `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "NewHandler"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Dispatcher      *event.Dispatcher          `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "validateToolbridgeDependencies"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Lifecycle       mcpToolLifecycleBackfiller `optional:\"true\"`", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "toolbridge.lifecycle_backfiller"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "HostTools  HostToolRegistry           `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "validateToolbridgeDependencies"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "SkillTools contract.SkillToolProvider `optional:\"true\"`", Class: optionalDependencyClassAbsence, Owner: "Lane D", Evidence: "toolbridge.skill_tools"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Reader    contract.AgentMemoryReader  `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "provideHostToolRegistry"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Writer    contract.AgentMemoryWriter  `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "provideHostToolRegistry"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "History   contract.SessionStatusPort  `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "provideHostToolRegistry"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Tracer    *observability.Service      `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "provideHostToolRegistry"},
	{Path: "internal/platform/toolbridge/module.go", Snippet: "Templates *workflowtemplates.Registry `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/platform/toolbridge", Evidence: "provideHostToolRegistry"},

	{Path: "internal/provider/claudecli/module.go", Snippet: "Dependency   contract.DependencyConfig `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/claudecli", Evidence: "ValidateProviderDependencies"},
	{Path: "internal/provider/claudecli/module.go", Snippet: "Config       *contract.Config          `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/claudecli", Evidence: "ValidateProviderDependencies"},
	{Path: "internal/provider/claudecli/module.go", Snippet: "Recovery     contract.SessionRecoveryReporter `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/claudecli", Evidence: "TestClaudeResumeSessionFailsWhenRuntimeReporterReturnsDeferredInProduction"},
	{Path: "internal/provider/claudecli/module.go", Snippet: "Tracer       *observability.Service           `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/claudecli", Evidence: "NewDriverFactory"},
	{Path: "internal/provider/codexapp/module.go", Snippet: "Dependency contract.DependencyConfig `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/codexapp", Evidence: "ValidateProviderDependencies"},
	{Path: "internal/provider/codexapp/module.go", Snippet: "Config     *contract.Config          `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/codexapp", Evidence: "ValidateProviderDependencies"},
	{Path: "internal/provider/codexapp/module.go", Snippet: "Recovery   contract.SessionRecoveryReporter `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/codexapp", Evidence: "TestCodexResumeSessionFailsWhenRuntimeReporterReturnsDeferredInProduction"},
	{Path: "internal/provider/codexapp/module.go", Snippet: "Logger      *slog.Logger          `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/codexapp", Evidence: "provideServerPool"},
	{Path: "internal/provider/codexapp/module.go", Snippet: "PIDRegistry *pidregistry.Registry `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/codexapp", Evidence: "provideServerPool"},
	{Path: "internal/provider/unified/module.go", Snippet: "Logger   *slog.Logger           `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/unified", Evidence: "NewClient"},
	{Path: "internal/provider/unified/module.go", Snippet: "Tracer   *observability.Service `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/unified", Evidence: "NewClient"},
	{Path: "internal/provider/unified/module.go", Snippet: "Logger    *slog.Logger                     `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/unified", Evidence: "NewDreamExecutor"},
	{Path: "internal/provider/unified/module.go", Snippet: "ThreadStore   contract.SessionThreadLookup    `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/unified", Evidence: "NewSessionResolver"},
	{Path: "internal/provider/unified/module.go", Snippet: "BindingStore  contract.SessionBindingLookup   `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/unified", Evidence: "NewSessionResolver"},
	{Path: "internal/provider/unified/module.go", Snippet: "BindingWriter contract.SessionBindingUpserter `optional:\"true\"`", Class: optionalDependencyClassAdjunct, Owner: "internal/provider/unified", Evidence: "NewSessionResolver"},
}

func TestOptionalDependencyBoundary(t *testing.T) {
	hits := collectOptionalDependencyHits(t, repoRoot(t))
	violations := optionalDependencyAnchorViolations(hits, allowedOptionalDependencyAnchors)
	failIfViolations(t, violations)
}

func collectOptionalDependencyHits(t *testing.T, root string) []optionalDependencyAnchor {
	t.Helper()
	var hits []optionalDependencyAnchor
	for _, relRoot := range []string{"internal/app", "internal/module/thread", "internal/platform/toolbridge", "internal/provider"} {
		hits = append(hits, collectOptionalDependencyHitsInRoot(t, root, relRoot)...)
	}
	slices.SortFunc(hits, compareOptionalDependencyAnchor)
	return hits
}

func collectOptionalDependencyHitsInRoot(t *testing.T, root string, relRoot string) []optionalDependencyAnchor {
	t.Helper()
	var hits []optionalDependencyAnchor
	err := filepath.WalkDir(filepath.Join(root, relRoot), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		relPath := filepath.ToSlash(mustRelPath(t, root, path))
		hits = append(hits, collectOptionalDependencyHitsInFile(t, relPath, path)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", relRoot, err)
	}
	return hits
}

func collectOptionalDependencyHitsInFile(t *testing.T, relPath string, path string) []optionalDependencyAnchor {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	lines := strings.Split(string(data), "\n")
	var hits []optionalDependencyAnchor
	for _, line := range lines {
		snippet := strings.TrimSpace(line)
		if snippet == "" {
			continue
		}
		if strings.Contains(snippet, "`optional:\"true\"`") || strings.Contains(snippet, "fx.Optional") {
			hits = append(hits, optionalDependencyAnchor{Path: relPath, Snippet: snippet})
		}
	}
	hits = append(hits, collectKnownDeferredDependencyHits(relPath, string(data))...)
	return hits
}

func collectKnownDeferredDependencyHits(relPath string, src string) []optionalDependencyAnchor {
	if relPath != "internal/app/runtime_reporter_adapter.go" {
		return nil
	}
	const snippet = "return desktopExternalRuntimeReporter{logger: p.Logger, profile: profile}, nil"
	if strings.Contains(src, snippet) {
		return []optionalDependencyAnchor{{Path: relPath, Snippet: snippet}}
	}
	return nil
}

func optionalDependencyAnchorViolations(hits []optionalDependencyAnchor, allowlist []optionalDependencyAnchor) []string {
	allowed := optionalDependencyAllowlistByKey(allowlist)
	seen := map[string]bool{}
	var violations []string
	for _, hit := range hits {
		key := optionalDependencyAnchorKey(hit)
		anchor, ok := allowed[key]
		if !ok {
			violations = append(violations, "unclassified optional dependency anchor: "+key)
			continue
		}
		seen[key] = true
		violations = append(violations, optionalDependencyAnchorMetadataViolations(anchor)...)
	}
	for _, anchor := range allowlist {
		key := optionalDependencyAnchorKey(anchor)
		if !seen[key] {
			violations = append(violations, "stale optional dependency allowlist anchor: "+key)
		}
	}
	slices.Sort(violations)
	return violations
}

func optionalDependencyAllowlistByKey(allowlist []optionalDependencyAnchor) map[string]optionalDependencyAnchor {
	allowed := make(map[string]optionalDependencyAnchor, len(allowlist))
	for _, anchor := range allowlist {
		allowed[optionalDependencyAnchorKey(anchor)] = anchor
	}
	return allowed
}

func optionalDependencyAnchorMetadataViolations(anchor optionalDependencyAnchor) []string {
	if strings.TrimSpace(anchor.Owner) == "" || strings.TrimSpace(anchor.Evidence) == "" {
		return []string{"incomplete optional dependency anchor metadata: " + optionalDependencyAnchorKey(anchor)}
	}
	switch anchor.Class {
	case optionalDependencyClassAbsence:
		if !registeredDependencyAbsenceEvidence(anchor.Evidence) {
			return []string{"dependency_absence anchor without registered policy evidence: " + optionalDependencyAnchorKey(anchor)}
		}
	case optionalDependencyClassAdjunct, optionalDependencyClassTemplate:
	default:
		return []string{"unknown optional dependency anchor class: " + optionalDependencyAnchorKey(anchor)}
	}
	return nil
}

func registeredDependencyAbsenceEvidence(name string) bool {
	for _, policy := range contract.RegisteredDependencyAbsencePolicies() {
		if policy.Name == name {
			return true
		}
	}
	return false
}

func optionalDependencyAnchorKey(anchor optionalDependencyAnchor) string {
	return anchor.Path + " :: " + anchor.Snippet
}

func compareOptionalDependencyAnchor(a, b optionalDependencyAnchor) int {
	if c := strings.Compare(a.Path, b.Path); c != 0 {
		return c
	}
	return strings.Compare(a.Snippet, b.Snippet)
}

func mustRelPath(t *testing.T, root string, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("rel %s from %s: %v", path, root, err)
	}
	return rel
}

func TestOptionalDependencyAnchorMetadata(t *testing.T) {
	for _, anchor := range allowedOptionalDependencyAnchors {
		if violations := optionalDependencyAnchorMetadataViolations(anchor); len(violations) > 0 {
			t.Fatalf("%s", strings.Join(violations, "\n"))
		}
	}
}

func TestOptionalDependencyAnchorCollectorFlagsUnknownAndStale(t *testing.T) {
	hits := []optionalDependencyAnchor{{Path: "internal/app/runtime_reporter_adapter.go", Snippet: "Unknown `optional:\"true\"`"}}
	allowlist := []optionalDependencyAnchor{allowedOptionalDependencyAnchors[0]}
	violations := optionalDependencyAnchorViolations(hits, allowlist)
	wantUnknown := "unclassified optional dependency anchor: internal/app/runtime_reporter_adapter.go :: Unknown `optional:\"true\"`"
	wantStale := "stale optional dependency allowlist anchor: " + optionalDependencyAnchorKey(allowedOptionalDependencyAnchors[0])
	if !slices.Contains(violations, wantUnknown) || !slices.Contains(violations, wantStale) {
		t.Fatalf("violations = %v, want unknown %q and stale %q", violations, wantUnknown, wantStale)
	}
}

func TestOptionalDependencyAnchorMetadataRequiresRegisteredPolicy(t *testing.T) {
	anchor := optionalDependencyAnchor{
		Path:     "internal/app/example.go",
		Snippet:  "Example `optional:\"true\"`",
		Class:    optionalDependencyClassAbsence,
		Owner:    "test",
		Evidence: "missing.policy",
	}
	violations := optionalDependencyAnchorMetadataViolations(anchor)
	if len(violations) != 1 || !strings.Contains(violations[0], "registered policy evidence") {
		t.Fatalf("violations = %v, want missing policy evidence", violations)
	}
}
