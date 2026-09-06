package taskhttp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type providerFixture struct {
	mu        sync.Mutex
	tools     []common.MCPTool
	listErr   error
	listCalls int
	callNames []string
	call      func(context.Context, string, json.RawMessage) (any, error)
}

func (f *providerFixture) ListTools(ctx context.Context) ([]common.MCPTool, error) {
	f.mu.Lock()
	f.listCalls++
	tools, listErr := f.tools, f.listErr
	f.mu.Unlock()
	if listErr != nil {
		return nil, listErr
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return tools, nil
	}
}

func (f *providerFixture) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	f.mu.Lock()
	f.callNames = append(f.callNames, name)
	call := f.call
	f.mu.Unlock()
	if call != nil {
		return call(ctx, name, args)
	}
	return map[string]any{"name": name}, nil
}

func (f *providerFixture) counts() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, append([]string(nil), f.callNames...)
}

func TestNewExactToolProviderRequiresExplicitInputsAndStartupList(t *testing.T) {
	base := &providerFixture{}
	if _, err := NewExactToolProvider(nilTaskHTTPContext(), base, []string{}); !errors.Is(err, ErrToolProviderInvalid) {
		t.Fatalf("nil context error = %v, want ErrToolProviderInvalid", err)
	}
	var typedNil *providerFixture
	if _, err := NewExactToolProvider(t.Context(), typedNil, []string{}); !errors.Is(err, ErrToolProviderInvalid) {
		t.Fatalf("typed nil provider error = %v, want ErrToolProviderInvalid", err)
	}
	if _, err := NewExactToolProvider(t.Context(), base, nil); !errors.Is(err, ErrToolProviderInvalid) {
		t.Fatalf("nil allowlist error = %v, want ErrToolProviderInvalid", err)
	}

	exact, err := NewExactToolProvider(t.Context(), base, []string{})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := exact.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	listCalls, _ := base.counts()
	if listCalls != 1 || listed == nil || len(listed) != 0 {
		t.Fatalf("startup/list = calls:%d tools:%#v, want one startup call and explicit empty set", listCalls, listed)
	}
}

// nilTaskHTTPContext 保留 nil context 的 fail-fast 回归而避免把 nil 字面量传入生产调用。
func nilTaskHTTPContext() context.Context { return nil }

func TestExactToolProviderPreservesOrderAndDeepCopiesDefinitions(t *testing.T) {
	raw := json.RawMessage(`{"name":"alpha","description":"raw","inputSchema":{"type":"object"}}`)
	baseTools := []common.MCPTool{
		mcpdto.NewRawTool(raw),
		{Name: "beta", Description: "beta tool", InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`), OutputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	base := &providerFixture{tools: baseTools}
	exact, err := NewExactToolProvider(t.Context(), base, []string{"beta", "alpha"})
	if err != nil {
		t.Fatal(err)
	}

	baseTools[0].InputSchema[0] = '['
	baseTools[1].InputSchema[0] = '['
	first, err := exact.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{first[0].Name, first[1].Name}; !reflect.DeepEqual(got, []string{"beta", "alpha"}) {
		t.Fatalf("listed order = %v", got)
	}
	if first[0].InputSchema[0] != '{' || first[1].InputSchema[0] != '{' {
		t.Fatal("cached definitions share schema bytes with base provider")
	}
	first[0].InputSchema[0] = '['
	returnedRaw := first[1].RawJSON()
	returnedRaw[0] = '['
	second, err := exact.ListTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second[0].InputSchema[0] != '{' || second[1].RawJSON()[0] != '{' {
		t.Fatal("ListTools returned storage shared with the frozen cache")
	}
}

func TestExactToolProviderRejectsUnknownDuplicateMissingAndNameDrift(t *testing.T) {
	tests := []struct {
		name      string
		tools     []common.MCPTool
		allowlist []string
		want      error
	}{
		{name: "unknown", tools: testDefinitions("alpha"), allowlist: []string{"missing"}, want: ErrToolUnknown},
		{name: "duplicate allowlist", tools: testDefinitions("alpha"), allowlist: []string{"alpha", "alpha"}, want: ErrToolDefinitionDrift},
		{name: "duplicate base", tools: testDefinitions("alpha", "alpha"), allowlist: []string{"alpha"}, want: ErrToolDefinitionDrift},
		{name: "missing base name", tools: testDefinitions(""), allowlist: []string{}, want: ErrToolDefinitionDrift},
		{name: "canonical name drift", tools: testDefinitions(" alpha"), allowlist: []string{}, want: ErrToolDefinitionDrift},
		{name: "raw name drift", tools: []common.MCPTool{rawToolWithProjectedName("alpha", "beta")}, allowlist: []string{}, want: ErrToolDefinitionDrift},
		{name: "invalid schema", tools: []common.MCPTool{{Name: "alpha", InputSchema: json.RawMessage(`[]`)}}, allowlist: []string{"alpha"}, want: ErrToolDefinitionDrift},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewExactToolProvider(t.Context(), &providerFixture{tools: testCase.tools}, testCase.allowlist)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("NewExactToolProvider() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestExactToolProviderRejectsUnboundTaskCapabilities(t *testing.T) {
	for _, name := range []string{
		"launch_agent", "send_message", "list_agents", "get_agent_report", "get_agent_reports",
		"stop_agent", "recover_agent", "interrupt_agent", "manage_direct_peer", "send_direct_peer",
		"read_direct_peer", "read_direct_peer_events", "wait_agents",
	} {
		t.Run(name, func(t *testing.T) {
			base := &providerFixture{tools: testDefinitions(name)}
			_, err := NewExactToolProvider(t.Context(), base, []string{name})
			if !errors.Is(err, ErrTaskCapabilityManifestRequired) {
				t.Fatalf("NewExactToolProvider(%q) error = %v, want ErrTaskCapabilityManifestRequired", name, err)
			}
			listCalls, _ := base.counts()
			if listCalls != 0 {
				t.Fatalf("unbound task capability %q reached base provider before rejection", name)
			}
		})
	}
}

func TestExactToolProviderRejectsDirectUnallowedCallsBeforeBase(t *testing.T) {
	base := &providerFixture{tools: testDefinitions("read", "send_message", "wait_for_agents")}
	exact, err := NewExactToolProvider(t.Context(), base, []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"send_message", "wait_for_agents", " read ", ""} {
		if _, err := exact.CallTool(t.Context(), name, json.RawMessage(`{}`)); !errors.Is(err, ErrToolNotAllowed) {
			t.Fatalf("CallTool(%q) error = %v, want ErrToolNotAllowed", name, err)
		}
	}
	if _, names := base.counts(); len(names) != 0 {
		t.Fatalf("unallowed direct calls reached base: %v", names)
	}
	if _, err := exact.CallTool(t.Context(), "read", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, names := base.counts(); !reflect.DeepEqual(names, []string{"read"}) {
		t.Fatalf("allowed calls = %v", names)
	}
}

func TestEmptyExactToolProviderEnablesNoSendOrWaitTools(t *testing.T) {
	base := &providerFixture{tools: testDefinitions("send_message", "wait_for_agents")}
	exact, err := NewExactToolProvider(t.Context(), base, []string{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"send_message", "wait_for_agents"} {
		if _, err := exact.CallTool(t.Context(), name, nil); !errors.Is(err, ErrToolNotAllowed) {
			t.Fatalf("default CallTool(%q) error = %v", name, err)
		}
	}
	if _, names := base.counts(); len(names) != 0 {
		t.Fatalf("empty exact provider forwarded calls: %v", names)
	}
}

func TestExactToolProviderPropagatesConstructionAndCallCancellation(t *testing.T) {
	constructionCtx, cancelConstruction := context.WithCancel(t.Context())
	cancelConstruction()
	base := &providerFixture{tools: testDefinitions("read")}
	if _, err := NewExactToolProvider(constructionCtx, base, []string{"read"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("construction cancellation error = %v", err)
	}
	listCalls, _ := base.counts()
	if listCalls != 1 {
		t.Fatalf("construction list calls = %d, want 1", listCalls)
	}

	exact, err := NewExactToolProvider(t.Context(), base, []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	callCtx, cancelCall := context.WithCancel(t.Context())
	cancelCall()
	if _, err := exact.CallTool(callCtx, "read", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("call cancellation error = %v", err)
	}
	if _, names := base.counts(); len(names) != 0 {
		t.Fatalf("pre-cancelled call reached base: %v", names)
	}
}

func testDefinitions(names ...string) []common.MCPTool {
	tools := make([]common.MCPTool, 0, len(names))
	for _, name := range names {
		tools = append(tools, common.MCPTool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return tools
}

func rawToolWithProjectedName(exported, rawName string) common.MCPTool {
	tool := mcpdto.NewRawTool(json.RawMessage(`{"name":"` + rawName + `","inputSchema":{"type":"object"}}`))
	tool.Name = exported
	return tool
}
