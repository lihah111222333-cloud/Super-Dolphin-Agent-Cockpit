package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/difftracker"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestToolbridgeProductionProfileRequiresCriticalDependencies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		omit    string
		wantErr string
	}{
		{name: "dispatcher", omit: "toolbridge.dispatcher", wantErr: "toolbridge.dispatcher"},
		{name: "resolver", omit: "toolbridge.workdir_resolver", wantErr: "toolbridge.workdir_resolver"},
		{name: "preferences", omit: "toolbridge.preferences", wantErr: "toolbridge.preferences"},
		{name: "config", omit: "toolbridge.config", wantErr: "toolbridge.config"},
		{name: "lifecycle_backfiller", omit: "toolbridge.lifecycle_backfiller", wantErr: "toolbridge.lifecycle_backfiller"},
		{name: "lifecycle_policy_reader", omit: "toolbridge.lifecycle_policy_reader", wantErr: "toolbridge.lifecycle_policy_reader"},
		{name: "agent_thread_lookup", omit: "toolbridge.agent_thread_lookup", wantErr: "toolbridge.agent_thread_lookup"},
		{name: "thread_config_override_store", omit: "toolbridge.thread_config_override_store", wantErr: "toolbridge.thread_config_override_store"},
		{name: "host_tools", omit: "toolbridge.host_tools", wantErr: "toolbridge.host_tools"},
		{name: "skill_tools", omit: "toolbridge.skill_tools", wantErr: "toolbridge.skill_tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolbridgeDependencies(toolbridgeDependencyFixture{
				profile: contract.DependencyProfileProduction,
				omit:    tc.omit,
			}.handlerIn())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateToolbridgeDependencies() error = %v, want %s", err, tc.wantErr)
			}
		})
	}
}

func TestToolbridgeDesktopProfileAllowsOnlyNamedMissingDependencies(t *testing.T) {
	allowed := toolbridgeAllowedMissingDependenciesForTest(contract.DependencyProfileDesktopHost)
	for _, dependency := range allToolbridgeDependencyNamesForTest() {
		err := validateToolbridgeDependencies(toolbridgeDependencyFixture{
			profile: contract.DependencyProfileDesktopHost,
			omit:    dependency,
		}.handlerIn())
		if allowed[dependency] {
			if err != nil {
				t.Fatalf("%s error = %v, want nil for registered desktop constructor absence", dependency, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), dependency) {
			t.Fatalf("%s error = %v, want required dependency failure", dependency, err)
		}
	}
}

func TestToolbridgeTestProfileAllowsOnlyTestNamedMissingDependencies(t *testing.T) {
	allowed := toolbridgeAllowedMissingDependenciesForTest(contract.DependencyProfileTest)
	for _, dependency := range allToolbridgeDependencyNamesForTest() {
		err := validateToolbridgeDependencies(toolbridgeDependencyFixture{
			profile: contract.DependencyProfileTest,
			omit:    dependency,
		}.handlerIn())
		if allowed[dependency] {
			if err != nil {
				t.Fatalf("%s error = %v, want nil for registered test constructor absence", dependency, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), dependency) {
			t.Fatalf("%s error = %v, want required dependency failure", dependency, err)
		}
	}
}

func toolbridgeAllowedMissingDependenciesForTest(profile contract.DependencyProfile) map[string]bool {
	allowed := make(map[string]bool)
	for _, policy := range contract.RegisteredDependencyAbsencePolicies() {
		if policy.Profile == profile && strings.HasPrefix(policy.Name, "toolbridge.") {
			allowed[policy.Name] = true
		}
	}
	return allowed
}

func TestToolbridgeNewHandlerRequiresDependencyContractBeforeConstruction(t *testing.T) {
	h, err := NewHandler(toolbridgeDependencyFixture{
		profile: contract.DependencyProfileProduction,
		omit:    "toolbridge.workdir_resolver",
	}.handlerIn())
	if err == nil || !strings.Contains(err.Error(), "toolbridge.workdir_resolver") {
		t.Fatalf("NewHandler() error = %v, want missing resolver", err)
	}
	if h != nil {
		t.Fatalf("NewHandler() handler = %#v, want nil on dependency failure", h)
	}
}

func TestToolbridgeNewHandlerRequiresAuthorityOwnerAtConstruction(t *testing.T) {
	in := toolbridgeDependencyFixture{profile: contract.DependencyProfileProduction}.handlerIn()
	in.AuthorityOwner = nil
	h, err := NewHandler(in)
	if err == nil || !strings.Contains(err.Error(), "MCP authority owner") {
		t.Fatalf("NewHandler() error = %v, want missing MCP authority owner", err)
	}
	if h != nil {
		t.Fatalf("NewHandler() handler = %#v, want nil on authority owner failure", h)
	}
}

func allToolbridgeDependencyNamesForTest() []string {
	return []string{
		"toolbridge.dispatcher",
		"toolbridge.workdir_resolver",
		"toolbridge.preferences",
		"toolbridge.config",
		"toolbridge.lifecycle_backfiller",
		"toolbridge.lifecycle_policy_reader",
		"toolbridge.agent_thread_lookup",
		"toolbridge.thread_config_override_store",
		"toolbridge.host_tools",
		"toolbridge.skill_tools",
	}
}

func mustNewToolbridgeDependencyHandler(t *testing.T) *Handler {
	t.Helper()
	in := toolbridgeDependencyFixture{profile: contract.DependencyProfileProduction}.handlerIn()
	in.Config.ProjectRoot = t.TempDir()
	in.AuthorityOwner = newTask4BAuthorityOwner()
	h, err := NewHandler(in)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return h
}

type toolbridgeDependencyFixture struct {
	profile contract.DependencyProfile
	omit    string
}

func (f toolbridgeDependencyFixture) handlerIn() handlerIn {
	cfg := &platformconfig.Config{Dependency: contract.DependencyConfig{Profile: f.profile}}
	lifecycle := newFakeMCPToolLifecycleOwner()
	in := handlerIn{
		Registry:        &mcpcontrol.ToolRegistry{},
		Emitter:         func(context.Context, difftracker.DiffResult) error { return nil },
		Resolver:        &stubCWDResolver{cwd: "/repo"},
		DiffFallback:    &diffFallbackTracker{},
		BindingStore:    &toolbridgeDependencyBindingLookup{},
		ThreadStore:     &toolbridgeDependencyThreadConfigStore{},
		Preferences:     &stubUIPreferenceReader{values: map[string]any{"settings.provider.active": "codex"}},
		Config:          cfg,
		Dependency:      cfg.Dependency,
		Dispatcher:      event.NewDispatcher(),
		Lifecycle:       lifecycle,
		LifecyclePolicy: lifecycle,
		AuthorityOwner:  newTask4BAuthorityOwner(),
		HostTools: &stubHostToolRegistry{
			hasToolName: testHostToolName,
			tools:       []mcpdto.MCPTool{{Name: testHostToolName}},
		},
		SkillTools: &fakeSkillToolProvider{},
	}
	omitToolbridgeDependencyForTest(&in, f.omit)
	return in
}

func omitToolbridgeDependencyForTest(in *handlerIn, name string) {
	omits := map[string]func(){
		"toolbridge.dispatcher": func() {
			in.Dispatcher = nil
			in.Emitter = nil
		},
		"toolbridge.workdir_resolver":             func() { in.Resolver = nil },
		"toolbridge.preferences":                  func() { in.Preferences = nil },
		"toolbridge.config":                       func() { in.Config = nil },
		"toolbridge.lifecycle_backfiller":         func() { in.Lifecycle = nil },
		"toolbridge.lifecycle_policy_reader":      func() { in.LifecyclePolicy = nil },
		"toolbridge.agent_thread_lookup":          func() { in.BindingStore = nil },
		"toolbridge.thread_config_override_store": func() { in.ThreadStore = nil },
		"toolbridge.host_tools":                   func() { in.HostTools = nil },
		"toolbridge.skill_tools":                  func() { in.SkillTools = nil },
	}
	if omit := omits[name]; omit != nil {
		omit()
	}
}

type toolbridgeDependencyBindingLookup struct{}

func (toolbridgeDependencyBindingLookup) GetThreadByAgent(context.Context, string) (string, error) {
	return "thread-1", nil
}

func (toolbridgeDependencyBindingLookup) GetBindingByAgent(context.Context, string) (ToolCallBinding, error) {
	return ToolCallBinding{AgentID: "agent-1", CWD: "/repo"}, nil
}

func (toolbridgeDependencyBindingLookup) GetBindingByProviderThread(context.Context, string, string) (ToolCallBinding, error) {
	return ToolCallBinding{AgentID: "agent-1", CWD: "/repo"}, nil
}

type toolbridgeDependencyThreadConfigStore struct{}

func (toolbridgeDependencyThreadConfigStore) GetConfigOverride(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"runtime":{"tools":["launch_agent"]}}`), nil
}
