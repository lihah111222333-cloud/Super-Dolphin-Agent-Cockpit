package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type operatorCountingTools struct{ lists, calls int }

func (p *operatorCountingTools) ListTools(context.Context) ([]common.MCPTool, error) {
	p.lists++
	return []common.MCPTool{{Name: "probe"}}, nil
}

func (p *operatorCountingTools) CallTool(context.Context, string, json.RawMessage) (any, error) {
	p.calls++
	return "probe", nil
}

func TestStandaloneOperatorToolFenceContract(t *testing.T) {
	t.Run("bound context fenced after replacement and close", testOperatorToolsReplacement)
	t.Run("caller field mutations and missing dependencies", testOperatorToolsInvalidCaller)
	t.Run("production runner requires persistent source", testOperatorToolsRunner)
}

func operatorToolContext(t *testing.T, principal common.CallerPrincipal) context.Context {
	t.Helper()
	ctx, err := common.WithCallerPrincipal(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func requireOperatorToolsDenied(t *testing.T, provider common.ToolProvider, base *operatorCountingTools, ctx context.Context) {
	t.Helper()
	before := *base
	if result, err := provider.ListTools(ctx); err == nil || result != nil {
		t.Errorf("list bypassed operator fence: result=%v err=%v", result, err)
	}
	if result, err := provider.CallTool(ctx, "probe", json.RawMessage(`{"_meta":{"principal_ref":"operator/test"}}`)); err == nil || result != nil {
		t.Errorf("call bypassed operator fence: result=%v err=%v", result, err)
	}
	if *base != before {
		t.Errorf("denied request reached base: before=%+v after=%+v", before, *base)
	}
}

func testOperatorToolsReplacement(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	if err != nil {
		t.Fatal(err)
	}
	base := &operatorCountingTools{}
	provider, err := newStandaloneOperatorToolProvider(base, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := operatorToolContext(t, issuer.principal)
	if _, err := provider.ListTools(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CallTool(ctx, "probe", nil); err != nil {
		t.Fatal(err)
	}
	if base.lists != 1 || base.calls != 1 {
		t.Fatalf("active source did not reach base: %+v", base)
	}
	nextIssuer := &operatorTestIssuer{principal: issuer.principal}
	nextIssuer.principal.AuthContext = "boot/replacement"
	next, err := activateStandaloneOperatorSource(t.Context(), db, nextIssuer)
	if err != nil {
		t.Fatal(err)
	}
	// 故意复用初始化时绑定的 context，不重新 Issue 或 initialize。
	requireOperatorToolsDenied(t, provider, base, ctx)
	if err := source.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireOperatorToolsDenied(t, provider, base, ctx)
	requireReplacementCloseDenied(t, base, next, nextIssuer.principal)
}

func requireReplacementCloseDenied(t *testing.T, base *operatorCountingTools, next *standaloneOperatorSource, principal common.CallerPrincipal) {
	t.Helper()
	nextProvider, err := newStandaloneOperatorToolProvider(base, next)
	if err != nil {
		t.Fatal(err)
	}
	nextCtx := operatorToolContext(t, principal)
	if err := next.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireOperatorToolsDenied(t, nextProvider, base, nextCtx)
}

func testOperatorToolsInvalidCaller(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	if err != nil {
		t.Fatal(err)
	}
	base := &operatorCountingTools{}
	provider, err := newStandaloneOperatorToolProvider(base, source)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*common.CallerPrincipal){
		"PrincipalRef": func(p *common.CallerPrincipal) { p.PrincipalRef = "operator/forged" },
		"ScopeRef":     func(p *common.CallerPrincipal) { p.ScopeRef = "workspace/forged" },
		"AuthContext":  func(p *common.CallerPrincipal) { p.AuthContext = "boot/forged" },
		"Profile":      func(p *common.CallerPrincipal) { p.Profile = "other" },
		"ExpiresAt":    func(p *common.CallerPrincipal) { p.ExpiresAt = time.Now().UTC().Add(time.Second) },
	}
	typ := reflect.TypeOf(issuer.principal)
	if len(mutations) != typ.NumField() {
		t.Fatal("caller field coverage drift")
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		mutate, ok := mutations[name]
		if !ok {
			t.Fatalf("uncovered caller field %s", name)
		}
		t.Run(name, func(t *testing.T) {
			principal := issuer.principal
			mutate(&principal)
			ctx := operatorToolContext(t, principal)
			if name == "ExpiresAt" {
				time.Sleep(time.Until(principal.ExpiresAt) + time.Millisecond)
			}
			requireOperatorToolsDenied(t, provider, base, ctx)
		})
	}
	requireOperatorToolsDenied(t, provider, base, context.Background())
	requireOperatorToolsDenied(t, &standaloneOperatorToolProvider{base: base}, base, operatorToolContext(t, issuer.principal))
	requireOperatorToolsInvalidDependencies(t, base, source)
	if err := source.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func requireOperatorToolsInvalidDependencies(t *testing.T, base *operatorCountingTools, source *standaloneOperatorSource) {
	t.Helper()
	var nilBase *operatorCountingTools
	for _, invalid := range []common.ToolProvider{nil, nilBase} {
		if _, err := newStandaloneOperatorToolProvider(invalid, source); err == nil {
			t.Fatal("nil base accepted")
		}
	}
	if _, err := newStandaloneOperatorToolProvider(base, nil); err == nil {
		t.Fatal("nil source accepted")
	}
}

func testOperatorToolsRunner(t *testing.T) {
	db, issuer := operatorSourceFixture(t)
	source, err := activateStandaloneOperatorSource(t.Context(), db, issuer)
	if err != nil {
		t.Fatal(err)
	}
	cfg := standalone.Config{WorkspaceRoot: t.TempDir(), Listen: "127.0.0.1:0"}
	registry := newRegistry(newRegistryParamsForTest())
	logger := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	var nilSource *standaloneOperatorSource
	for _, invalid := range []common.CallerPrincipalIssuer{nil, issuer, nilSource} {
		if _, err := newStandaloneHTTPRunner(cfg, standaloneHTTPSettings{credential: "test-only-operator-token-value", principalIssuer: invalid}, registry, logger); err == nil {
			t.Fatal("production runner accepted non-persistent issuer")
		}
	}
	if _, err := newStandaloneHTTPRunner(cfg, standaloneHTTPSettings{credential: "test-only-operator-token-value", principalIssuer: source}, registry, logger); err != nil {
		t.Fatalf("production runner rejected current source: %v", err)
	}
	if err := source.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := newStandaloneHTTPRunner(cfg, standaloneHTTPSettings{credential: "test-only-operator-token-value", principalIssuer: source}, registry, logger); err == nil {
		t.Fatal("production runner accepted revoked source")
	}
}
