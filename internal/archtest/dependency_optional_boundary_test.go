package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type optionalDependencyCategory string

const (
	optionalDependencyAbsence optionalDependencyCategory = "dependency_absence"
	optionalAdjunct           optionalDependencyCategory = "adjunct_optional"
	optionalTestOrTemplate    optionalDependencyCategory = "test_or_template"
)

func TestOptionalDependencyBoundary(t *testing.T) {
	t.Parallel()

	occurrences := scanOptionalDependencyOccurrences(t, optionalDependencyRepoRoot(t))
	classifications := registeredOptionalDependencyClassifications()
	var unclassified []string
	var staleClassifications []string
	var missingAudit []string
	seenClassifications := make(map[string]bool)
	for _, occurrence := range occurrences {
		classification, ok := classifications[occurrence.key()]
		if !ok {
			unclassified = append(unclassified, occurrence.String())
			continue
		}
		seenClassifications[occurrence.key()] = true
		if violation := classification.auditViolation(occurrence); violation != "" {
			missingAudit = append(missingAudit, violation)
			continue
		}
		if classification.category != optionalDependencyAbsence {
			continue
		}
		if !contract.AllowsMissingDependency(classification.dependency, classification.profile) {
			t.Fatalf("%s is dependency_absence but policy denies %s in %s", occurrence, classification.dependency, classification.profile)
		}
	}
	staleClassifications = staleOptionalDependencyClassifications(classifications, seenClassifications)
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("unclassified optional dependency boundaries:\n%s", strings.Join(unclassified, "\n"))
	}
	if len(staleClassifications) > 0 {
		sort.Strings(staleClassifications)
		t.Fatalf("stale optional dependency classifications:\n%s", strings.Join(staleClassifications, "\n"))
	}
	if len(missingAudit) > 0 {
		sort.Strings(missingAudit)
		t.Fatalf("optional dependency classifications missing audit evidence:\n%s", strings.Join(missingAudit, "\n"))
	}
}

func TestOptionalDependencyAbsenceUsesRequireDependency(t *testing.T) {
	t.Parallel()

	classifications := registeredOptionalDependencyClassifications()
	for key, classification := range classifications {
		if classification.category != optionalDependencyAbsence {
			continue
		}
		if err := contract.RequireDependency(classification.dependency, classification.profile, nil); err != nil {
			t.Fatalf("%s: RequireDependency(%q, %s, nil) error = %v, want nil", key, classification.dependency, classification.profile, err)
		}
		if err := contract.RequireDependency(classification.dependency, contract.DependencyProfileProduction, nil); err == nil {
			t.Fatalf("%s: RequireDependency(%q, production, nil) error = nil, want production failure", key, classification.dependency)
		}
	}
}

func TestOptionalDependencyBudgetGuard(t *testing.T) {
	t.Parallel()

	classifications := registeredOptionalDependencyClassifications()
	budgets := optionalDependencyBudgets()
	actual := optionalDependencyBudgetCounts(classifications)
	var violations []string
	for key, count := range actual {
		budget, ok := budgets[key]
		if !ok {
			violations = append(violations, fmt.Sprintf("%s has %d classifications without a budget", key, count))
			continue
		}
		if count != budget {
			violations = append(violations, fmt.Sprintf("%s has %d classifications, budget %d", key, count, budget))
		}
	}
	for key, budget := range budgets {
		if _, ok := actual[key]; ok {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s budget %d is stale; no matching classifications remain", key, budget))
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("optional dependency budget drift:\n%s", strings.Join(violations, "\n"))
	}
}

func TestScanOptionalDependencyFileDetectsFxOptionalCall(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "module.go")
	source := []byte(`package optionalfixture

import "go.uber.org/fx"

var Module = fx.Options(
	fx.Provide(
		fx.Annotate(
			newService,
			fx.Optional(),
		),
	),
)

func newService() any {
	return nil
}
`)
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	occurrences, err := scanOptionalDependencyFile(path, "internal/app/module.go")
	if err != nil {
		t.Fatalf("scan fixture: %v", err)
	}

	want := optionalDependencyOccurrence{
		RelPath: "internal/app/module.go",
		Line:    9,
		Kind:    "fx_optional",
		Value:   "fx.Optional",
	}
	if slices.Contains(occurrences, want) {
		return
	}
	t.Fatalf("scanOptionalDependencyFile() occurrences = %#v, want %#v", occurrences, want)
}

func TestOptionalDependencyBudgetCountsDetectDrift(t *testing.T) {
	t.Parallel()

	classifications := map[string]optionalDependencyClassification{
		"internal/app/example.go:optional_tag:Logger": {
			category: optionalAdjunct,
			owner:    "internal/app",
			evidence: "internal/app/example.go: logger is diagnostic-only",
		},
		"internal/app/example.go:typed_unsupported:runtime_reporter.orchestration_service": {
			category:   optionalDependencyAbsence,
			dependency: "runtime_reporter.orchestration_service",
			profile:    contract.DependencyProfileDesktopHost,
			owner:      "internal/app",
			evidence:   "internal/app/example.go: missing orchestration is deferred in desktop",
		},
		"internal/app/template.go.txt:optional_tag:Tracer": {
			category: optionalTestOrTemplate,
			owner:    "internal/provider/_template",
			evidence: "internal/app/template.go.txt: template optional field",
		},
	}

	got := optionalDependencyBudgetCounts(classifications)
	want := map[optionalDependencyBudgetKey]int{
		{owner: "internal/app", category: optionalAdjunct}:                       1,
		{owner: "internal/app", category: optionalDependencyAbsence}:             1,
		{owner: "internal/provider/_template", category: optionalTestOrTemplate}: 1,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("optionalDependencyBudgetCounts() = %#v, want %#v", got, want)
	}
}

func TestStaleOptionalDependencyClassificationsDetectsUnusedRegistration(t *testing.T) {
	t.Parallel()

	classifications := map[string]optionalDependencyClassification{
		"internal/app/example.go:optional_tag:Logger": {
			category: optionalAdjunct,
			owner:    "internal/app",
			evidence: "internal/app/example.go: logger is diagnostic-only",
		},
		"internal/app/deleted.go:optional_tag:Tracer": {
			category: optionalAdjunct,
			owner:    "internal/app",
			evidence: "internal/app/deleted.go: stale registration",
		},
	}
	seen := map[string]bool{
		"internal/app/example.go:optional_tag:Logger": true,
	}

	got := staleOptionalDependencyClassifications(classifications, seen)
	want := []string{"internal/app/deleted.go:optional_tag:Tracer"}
	if !slices.Equal(got, want) {
		t.Fatalf("staleOptionalDependencyClassifications() = %#v, want %#v", got, want)
	}
}

type optionalDependencyOccurrence struct {
	RelPath string
	Line    int
	Kind    string
	Value   string
}

func (o optionalDependencyOccurrence) key() string {
	return o.RelPath + ":" + o.Kind + ":" + o.Value
}

func (o optionalDependencyOccurrence) String() string {
	return o.RelPath + ":" + strconv.Itoa(o.Line) + " " + o.Kind + " " + o.Value
}

type optionalDependencyClassification struct {
	category   optionalDependencyCategory
	dependency string
	profile    contract.DependencyProfile
	owner      string
	evidence   string
}

func (c optionalDependencyClassification) auditViolation(occurrence optionalDependencyOccurrence) string {
	if strings.TrimSpace(string(c.category)) == "" {
		return occurrence.String() + " classification category is empty"
	}
	if strings.TrimSpace(c.owner) == "" {
		return occurrence.String() + " owner is empty"
	}
	if strings.TrimSpace(c.evidence) == "" {
		return occurrence.String() + " evidence is empty"
	}
	if !strings.Contains(c.evidence, occurrence.RelPath) {
		return occurrence.String() + " evidence must reference source path " + occurrence.RelPath
	}
	if c.category == optionalDependencyAbsence {
		if strings.TrimSpace(c.dependency) == "" {
			return occurrence.String() + " dependency_absence dependency is empty"
		}
		if strings.TrimSpace(string(c.profile)) == "" {
			return occurrence.String() + " dependency_absence profile is empty"
		}
		return ""
	}
	if strings.TrimSpace(c.dependency) != "" || strings.TrimSpace(string(c.profile)) != "" {
		return occurrence.String() + " non-policy optional classification must not carry dependency policy fields"
	}
	return ""
}

type optionalDependencyBudgetKey struct {
	owner    string
	category optionalDependencyCategory
}

func (k optionalDependencyBudgetKey) String() string {
	return string(k.category) + " " + k.owner
}

func optionalDependencyBudgets() map[optionalDependencyBudgetKey]int {
	return map[optionalDependencyBudgetKey]int{
		{owner: "internal/app", category: optionalDependencyAbsence}:                 2,
		{owner: "internal/app", category: optionalAdjunct}:                           9,
		{owner: "internal/module/thread", category: optionalDependencyAbsence}:       1,
		{owner: "internal/module/thread", category: optionalAdjunct}:                 8,
		{owner: "internal/platform/toolbridge", category: optionalDependencyAbsence}: 8,
		{owner: "internal/platform/toolbridge", category: optionalAdjunct}:           11,
		{owner: "internal/provider/claudecli", category: optionalAdjunct}:            4,
		{owner: "internal/provider/codexapp", category: optionalAdjunct}:             5,
		{owner: "internal/provider/unified", category: optionalAdjunct}:              5,
		{owner: "internal/provider/_template", category: optionalTestOrTemplate}:     2,
	}
}

func optionalDependencyBudgetCounts(classifications map[string]optionalDependencyClassification) map[optionalDependencyBudgetKey]int {
	counts := make(map[optionalDependencyBudgetKey]int)
	for _, classification := range classifications {
		key := optionalDependencyBudgetKey{
			owner:    classification.owner,
			category: classification.category,
		}
		counts[key]++
	}
	return counts
}

func staleOptionalDependencyClassifications(
	classifications map[string]optionalDependencyClassification,
	seen map[string]bool,
) []string {
	var stale []string
	for key := range classifications {
		if !optionalDependencyClassificationRequiresOccurrence(key) {
			continue
		}
		if seen[key] {
			continue
		}
		stale = append(stale, key)
	}
	slices.Sort(stale)
	return stale
}

func optionalDependencyClassificationRequiresOccurrence(key string) bool {
	return strings.Contains(key, ":optional_tag:") ||
		strings.Contains(key, ":fx_optional:") ||
		strings.Contains(key, ":noop_success:")
}

func registeredOptionalDependencyClassifications() map[string]optionalDependencyClassification {
	classifications := make(map[string]optionalDependencyClassification)
	mergeOptionalDependencyClassifications(classifications, registeredOptionalDependencyAppClassifications())
	mergeOptionalDependencyClassifications(classifications, registeredOptionalDependencyThreadClassifications())
	mergeOptionalDependencyClassifications(classifications, registeredOptionalDependencyToolbridgeClassifications())
	mergeOptionalDependencyClassifications(classifications, registeredOptionalDependencyProviderClassifications())
	mergeOptionalDependencyClassifications(classifications, registeredOptionalDependencyTemplateClassifications())
	return classifications
}

func mergeOptionalDependencyClassifications(dst, src map[string]optionalDependencyClassification) {
	maps.Copy(dst, src)
}

func registeredOptionalDependencyAppClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	dependency := func(name string, profile contract.DependencyProfile, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: optionalDependencyAbsence, dependency: name, profile: profile, owner: owner, evidence: evidence}
	}
	appAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/app", path+": "+evidence)
	}
	appDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/app", path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/app/dashboard_adapter.go:optional_tag:Lifecycle":                                      appAdjunct("internal/app/dashboard_adapter.go", "provideDashboardOrchestrationReaderPort returns nil unless AgentLifecyclePort and AgentReportPort are both present; dashboard read entrypoints fail-fast on nil reader"),
		"internal/app/dashboard_adapter.go:optional_tag:Reports":                                        appAdjunct("internal/app/dashboard_adapter.go", "provideDashboardOrchestrationReaderPort returns nil unless AgentLifecyclePort and AgentReportPort are both present; dashboard read entrypoints fail-fast on nil reader"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Runtime":                                 appDependency("internal/app/runtime_reporter_adapter.go", "runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost, "provideRuntimeUpdater narrows AgentRuntimePort to UpdateRuntime before newRuntimeReporter gates absent updater through dependency policy"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Logger":                                  appAdjunct("internal/app/runtime_reporter_adapter.go", "desktopExternalRuntimeReporter uses logger only for debug diagnostics"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Dependency":                              appAdjunct("internal/app/runtime_reporter_adapter.go", "appDependencyProfile resolves the mode-aware policy from Dependency or Config"),
		"internal/app/runtime_reporter_adapter.go:optional_tag:Config":                                  appAdjunct("internal/app/runtime_reporter_adapter.go", "appDependencyProfile resolves the fallback dependency profile from Config"),
		"internal/app/runner.go:optional_tag:RootCtx":                                                   appAdjunct("internal/app/runner.go", "BindRuntime validates runtime pre-drain ownership before accepting nil-adjacent root context behavior"),
		"internal/app/runner.go:optional_tag:Lifecycle":                                                 appAdjunct("internal/app/runner.go", "reportRuntimeExit treats Wails lifecycle as notification-only adjunct"),
		"internal/app/runner.go:optional_tag:ExtractionDrainer":                                         appAdjunct("internal/app/runner.go", "registerRuntimePreDrain fail-fast requires the drainer before production runtime stop hooks run"),
		"internal/app/thread_orchestration_adapter.go:typed_unsupported:thread.bind_session_generation": appDependency("internal/app/thread_orchestration_adapter.go", "thread.bind_session_generation", contract.DependencyProfileDesktopHost, "BindSessionGeneration returns MissingDependencyModeError under desktop-host facade mode"),
		"internal/app/thread_orchestration_adapter.go:noop_success:LaunchAgent":                         appAdjunct("internal/app/thread_orchestration_adapter.go", "LaunchAgent is a documented no-op because thread Start/SpawnIfNeeded owns local provider session launch"),
	}
}

func registeredOptionalDependencyThreadClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	dependency := func(name string, profile contract.DependencyProfile, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: optionalDependencyAbsence, dependency: name, profile: profile, owner: owner, evidence: evidence}
	}
	threadAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/module/thread", path+": "+evidence)
	}
	threadDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/module/thread", path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/module/thread/lifecycle.go:typed_unsupported:thread.bind_session_generation":     threadDependency("internal/module/thread/lifecycle.go", "thread.bind_session_generation", contract.DependencyProfileDesktopHost, "BindSessionGeneration propagates MissingDependencyModeError for profiles without session-generation binding"),
		"internal/module/thread/module.go:optional_tag:NewServiceWithPromptAssemblyAndSharedFiles": threadAdjunct("internal/module/thread/module.go", "fx.Annotate(NewServiceWithPromptAssemblyAndSharedFiles, fx.ParamTags(... optional:\"true\" ...)) inventories optional constructor adjuncts"),
		"internal/module/thread/module.go:optional_tag:NewThreadHandlers":                          threadAdjunct("internal/module/thread/module.go", "fx.Annotate(NewThreadHandlers, fx.ParamTags(... optional:\"true\" ...)) inventories optional constructor adjuncts"),
		"internal/module/thread/module.go:optional_tag:Store":                                      threadAdjunct("internal/module/thread/module.go", "store port adapters preserve Fx closure while service methods fail-fast when missing"),
		"internal/module/thread/module.go:optional_tag:Catalog":                                    threadAdjunct("internal/module/thread/module.go", "catalog injection is a prompt assembly adjunct covered by runtime catalog construction"),
		"internal/module/thread/module.go:optional_tag:Registrar":                                  threadAdjunct("internal/module/thread/module.go", "thread prompt registration tolerates nil registrar as no-op registration boundary"),
		"internal/module/thread/module.go:optional_tag:PromptStore":                                threadAdjunct("internal/module/thread/module.go", "runtime prompt catalog can be built with nil store for test and desktop fallback surfaces"),
		"internal/module/thread/module.go:optional_tag:Builtin":                                    threadAdjunct("internal/module/thread/module.go", "runtime prompt catalog treats builtin prompt registry as adjunct input"),
		"internal/module/thread/module.go:optional_tag:PromptCatalog":                              threadAdjunct("internal/module/thread/module.go", "registerThreadPromptProviders synthesizes catalog from PromptStore/Builtin when omitted"),
	}
}

func registeredOptionalDependencyTemplateClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	templateAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalTestOrTemplate, "internal/provider/_template", path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/provider/_template/module.go.txt:optional_tag:Approvals": templateAdjunct("internal/provider/_template/module.go.txt", "Approvals is an optional template anchor carried into rendered module.go"),
		"internal/provider/_template/module.go.txt:optional_tag:Tracer":    templateAdjunct("internal/provider/_template/module.go.txt", "Tracer is an optional template anchor carried into rendered module.go"),
	}
}

func registeredOptionalDependencyToolbridgeClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	dependency := func(name string, profile contract.DependencyProfile, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: optionalDependencyAbsence, dependency: name, profile: profile, owner: owner, evidence: evidence}
	}
	toolbridgeAdjunct := func(path, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, "internal/platform/toolbridge", path+": "+evidence)
	}
	toolbridgeDependency := func(path, name string, profile contract.DependencyProfile, evidence string) optionalDependencyClassification {
		return dependency(name, profile, "internal/platform/toolbridge", path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.agent_thread_lookup":          toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.agent_thread_lookup", contract.DependencyProfileDesktopHost, "validateToolbridgeDependencies maps missing BindingStore to typed dependency policy outside production"),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.thread_config_override_store": toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.thread_config_override_store", contract.DependencyProfileDesktopHost, "validateToolbridgeDependencies maps missing ThreadStore to typed dependency policy outside production"),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.lifecycle_backfiller":         toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.lifecycle_backfiller", contract.DependencyProfileTest, "validateToolbridgeDependencies maps missing lifecycle backfiller to test-only typed policy"),
		"internal/platform/toolbridge/handler.go:typed_unsupported:toolbridge.skill_tools":                  toolbridgeDependency("internal/platform/toolbridge/handler.go", "toolbridge.skill_tools", contract.DependencyProfileTest, "validateToolbridgeDependencies maps missing skill tools to test-only typed policy"),
		"internal/platform/toolbridge/module.go:optional_tag:Resolver":                                      toolbridgeAdjunct("internal/platform/toolbridge/module.go", "validateToolbridgeDependencies in handler.go requires workdir resolver in production"),
		"internal/platform/toolbridge/module.go:optional_tag:BindingStore":                                  toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.agent_thread_lookup", contract.DependencyProfileDesktopHost, "handler.go validates BindingStore before Handler construction"),
		"internal/platform/toolbridge/module.go:optional_tag:ThreadStore":                                   toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.thread_config_override_store", contract.DependencyProfileDesktopHost, "handler.go validates ThreadStore before Handler construction"),
		"internal/platform/toolbridge/module.go:optional_tag:Preferences":                                   toolbridgeAdjunct("internal/platform/toolbridge/module.go", "validateToolbridgeDependencies in handler.go requires preferences in production"),
		"internal/platform/toolbridge/module.go:optional_tag:Config":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideToolbridgeDependencyConfig requires config and dependency profile before Handler"),
		"internal/platform/toolbridge/module.go:optional_tag:Logger":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "logger is diagnostic-only and NewHandler falls back to package logger"),
		"internal/platform/toolbridge/module.go:optional_tag:Tracer":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "tracer is observability adjunct and not a tool execution dependency"),
		"internal/platform/toolbridge/module.go:optional_tag:Dispatcher":                                    toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideDiffEmitter fails construction when dispatcher is nil"),
		"internal/platform/toolbridge/module.go:optional_tag:Lifecycle":                                     toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.lifecycle_backfiller", contract.DependencyProfileTest, "handler.go validates lifecycle backfiller before toolbridge startup"),
		"internal/platform/toolbridge/module.go:optional_tag:HostTools":                                     toolbridgeAdjunct("internal/platform/toolbridge/module.go", "validateToolbridgeDependencies in handler.go requires host tools in production"),
		"internal/platform/toolbridge/module.go:optional_tag:SkillTools":                                    toolbridgeDependency("internal/platform/toolbridge/module.go", "toolbridge.skill_tools", contract.DependencyProfileTest, "handler.go validates skill tools before exposing toolbridge"),
		"internal/platform/toolbridge/module.go:optional_tag:Reader":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips memory read registry when capability is absent"),
		"internal/platform/toolbridge/module.go:optional_tag:Writer":                                        toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips memory write registry when capability is absent"),
		"internal/platform/toolbridge/module.go:optional_tag:History":                                       toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips history registry when status port is absent"),
		"internal/platform/toolbridge/module.go:optional_tag:Templates":                                     toolbridgeAdjunct("internal/platform/toolbridge/module.go", "provideHostToolRegistry skips workflow template registry when template registry is absent"),
	}
}

func registeredOptionalDependencyProviderClassifications() map[string]optionalDependencyClassification {
	classify := func(category optionalDependencyCategory, owner, evidence string) optionalDependencyClassification {
		return optionalDependencyClassification{category: category, owner: owner, evidence: evidence}
	}
	providerAdjunct := func(path, owner, evidence string) optionalDependencyClassification {
		return classify(optionalAdjunct, owner, path+": "+evidence)
	}
	return map[string]optionalDependencyClassification{
		"internal/provider/claudecli/module.go:optional_tag:Dependency":  providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "newModeAwareRuntimeReporter requires an explicit dependency profile from Dependency or Config"),
		"internal/provider/claudecli/module.go:optional_tag:Config":      providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "dependencyProfileFromFactoryParams uses Config only to resolve profile"),
		"internal/provider/claudecli/module.go:optional_tag:Recovery":    providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "Recovery is passed to driver as an optional replay reporter"),
		"internal/provider/claudecli/module.go:optional_tag:Tracer":      providerAdjunct("internal/provider/claudecli/module.go", "internal/provider/claudecli", "Tracer is observability-only and firstClaudeTracer handles nil"),
		"internal/provider/codexapp/module.go:optional_tag:Dependency":   providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "newModeAwareRuntimeReporter requires an explicit dependency profile from Dependency or Config"),
		"internal/provider/codexapp/module.go:optional_tag:Config":       providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "dependencyProfileFromFactoryParams uses Config only to resolve profile"),
		"internal/provider/codexapp/module.go:optional_tag:Recovery":     providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "Recovery is optional session recovery reporting for transport reconnects"),
		"internal/provider/codexapp/module.go:optional_tag:Logger":       providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "logger is diagnostic-only for driver factory and server manager"),
		"internal/provider/codexapp/module.go:optional_tag:PIDRegistry":  providerAdjunct("internal/provider/codexapp/module.go", "internal/provider/codexapp", "server manager and transport spawner tolerate nil pid registry without masking pool errors"),
		"internal/provider/unified/module.go:optional_tag:Logger":        providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "logger is diagnostic-only for client and dream executor"),
		"internal/provider/unified/module.go:optional_tag:Tracer":        providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "tracer is observability-only for unified client"),
		"internal/provider/unified/module.go:optional_tag:ThreadStore":   providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "session resolver treats thread lookup as optional cross-module recovery surface"),
		"internal/provider/unified/module.go:optional_tag:BindingStore":  providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "session resolver treats binding lookup as optional cross-module recovery surface"),
		"internal/provider/unified/module.go:optional_tag:BindingWriter": providerAdjunct("internal/provider/unified/module.go", "internal/provider/unified", "session resolver treats binding writer as optional cross-module recovery surface"),
	}
}

func scanOptionalDependencyOccurrences(t *testing.T, root string) []optionalDependencyOccurrence {
	t.Helper()
	var out []optionalDependencyOccurrence
	for _, dir := range optionalDependencyGoDirs() {
		out = append(out, scanOptionalDependencyGoDir(t, root, dir)...)
	}
	out = append(out, scanOptionalDependencyTemplateDir(t, root)...)
	return out
}

func optionalDependencyGoDirs() []string {
	return []string{"internal/app", "internal/module/thread", "internal/platform/toolbridge", "internal/provider"}
}

func scanOptionalDependencyGoDir(t *testing.T, root, dir string) []optionalDependencyOccurrence {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(dir))
	var out []optionalDependencyOccurrence
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fileOccurrences, err := scanOptionalDependencyFile(path, rel)
		if err != nil {
			return err
		}
		out = append(out, fileOccurrences...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	return out
}

func scanOptionalDependencyTemplateDir(t *testing.T, root string) []optionalDependencyOccurrence {
	t.Helper()
	templateDir := filepath.Join(root, "internal", "provider", "_template")
	var out []optionalDependencyOccurrence
	err := filepath.WalkDir(templateDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fileOccurrences, err := scanOptionalDependencyTemplateFile(path, rel)
		if err != nil {
			return err
		}
		out = append(out, fileOccurrences...)
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/provider/_template: %v", err)
	}
	return out
}

func scanOptionalDependencyFile(path, rel string) ([]optionalDependencyOccurrence, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var out []optionalDependencyOccurrence
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Field:
			if hasOptionalTag(n.Tag) {
				out = append(out, optionalDependencyOccurrence{
					RelPath: rel,
					Line:    fset.Position(n.Pos()).Line,
					Kind:    "optional_tag",
					Value:   fieldName(n),
				})
			}
		case *ast.CallExpr:
			out = append(out, optionalDependencyCallOccurrences(fset, n, rel)...)
		case *ast.FuncDecl:
			if isNoopSuccessFunction(n) {
				out = append(out, optionalDependencyOccurrence{
					RelPath: rel,
					Line:    fset.Position(n.Pos()).Line,
					Kind:    "noop_success",
					Value:   n.Name.Name,
				})
			}
		}
		return true
	})
	return out, nil
}

func optionalDependencyCallOccurrences(fset *token.FileSet, call *ast.CallExpr, rel string) []optionalDependencyOccurrence {
	line := fset.Position(call.Pos()).Line
	out := make([]optionalDependencyOccurrence, 0, 3)
	if isFxOptionalCall(call) {
		out = append(out, optionalDependencyOccurrence{RelPath: rel, Line: line, Kind: "fx_optional", Value: "fx.Optional"})
	}
	if value, ok := optionalAnnotateValue(call); ok {
		out = append(out, optionalDependencyOccurrence{RelPath: rel, Line: line, Kind: "optional_tag", Value: value})
	}
	if dependencyName, ok := newDependencyModeErrorName(call); ok {
		out = append(out, optionalDependencyOccurrence{RelPath: rel, Line: line, Kind: "typed_unsupported", Value: dependencyName})
	}
	return out
}

func newDependencyModeErrorName(call *ast.CallExpr) (string, bool) {
	if !isNewDependencyModeErrorCall(call) {
		return "", false
	}
	return stringLiteralArg(call, 1)
}

func scanOptionalDependencyTemplateFile(path, rel string) ([]optionalDependencyOccurrence, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []optionalDependencyOccurrence
	for i, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, `optional:"true"`) {
			continue
		}
		value, ok := templateOptionalFieldName(line)
		if !ok {
			return nil, fmt.Errorf("%s line %d optional anchor is missing a field name", rel, i+1)
		}
		out = append(out, optionalDependencyOccurrence{
			RelPath: rel,
			Line:    i + 1,
			Kind:    "optional_tag",
			Value:   value,
		})
	}
	return out, nil
}

func templateOptionalFieldName(line string) (string, bool) {
	before, _, ok := strings.Cut(line, "`optional:\"true\"`")
	if !ok {
		return "", false
	}
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return "", false
	}
	return fields[0], true
}

func hasOptionalTag(tag *ast.BasicLit) bool {
	return tag != nil && strings.Contains(tag.Value, `optional:"true"`)
}

func isFxOptionalCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && isFxOptionalSelector(selector)
}

func isFxOptionalSelector(selector *ast.SelectorExpr) bool {
	if selector == nil || selector.Sel.Name != "Optional" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == "fx"
}

func optionalAnnotateValue(call *ast.CallExpr) (string, bool) {
	if !isFxAnnotateCall(call) || len(call.Args) < 2 {
		return "", false
	}
	target := annotateTargetName(call.Args[0])
	if target == "" {
		return "", false
	}
	if !callContainsOptionalDependency(call.Args[1:]) {
		return "", false
	}
	return target, true
}

func isFxAnnotateCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Annotate"
}

func annotateTargetName(expr ast.Expr) string {
	switch target := expr.(type) {
	case *ast.Ident:
		return target.Name
	case *ast.SelectorExpr:
		return target.Sel.Name
	default:
		return ""
	}
}

func callContainsOptionalDependency(exprs []ast.Expr) bool {
	return slices.ContainsFunc(exprs, exprContainsOptionalDependency)
}

func exprContainsOptionalDependency(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.CallExpr:
			if isFxParamTagsCall(n) && callHasOptionalTag(n) {
				found = true
				return false
			}
		case *ast.SelectorExpr:
			if isFxOptionalSelector(n) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isFxParamTagsCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "ParamTags"
}

func callHasOptionalTag(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if strings.Contains(lit.Value, `optional:"true"`) {
			return true
		}
	}
	return false
}

func fieldName(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "<embedded>"
	}
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ",")
}

func isNewDependencyModeErrorCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewDependencyModeError" || len(call.Args) < 3 {
		return false
	}
	arg, ok := call.Args[0].(*ast.SelectorExpr)
	return ok && arg.Sel.Name == "ErrUnsupportedDependencyMode"
}

func stringLiteralArg(call *ast.CallExpr, index int) (string, bool) {
	if len(call.Args) <= index {
		return "", false
	}
	lit, ok := call.Args[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value := strings.Trim(lit.Value, "\"`")
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func isNoopSuccessFunction(fn *ast.FuncDecl) bool {
	if fn.Name.Name != "LaunchAgent" || fn.Body == nil || len(fn.Body.List) != 1 {
		return false
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && isOptionalNilIdent(ret.Results[0])
}

func isOptionalNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}

func optionalDependencyRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}
