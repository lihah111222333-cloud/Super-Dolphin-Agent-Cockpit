package skill

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
)

func TestExecParamsUnmarshalV3Shape(t *testing.T) {
	t.Parallel()

	var got execParams
	if err := json.Unmarshal([]byte(`{"command":"git","args":["status"],"cwd":"/tmp"}`), &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Command != "git" || len(got.Args) != 1 || got.Args[0] != "status" || got.CWD != "/tmp" {
		t.Fatalf("unexpected exec params: %+v", got)
	}
	if got.Env != nil {
		t.Fatalf("unexpected env: %#v", got.Env)
	}
}

func TestExecParamsUnmarshalLegacyArgvEnvShape(t *testing.T) {
	t.Parallel()

	var got execParams
	if err := json.Unmarshal([]byte(`{"argv":["git","status"],"cwd":"/tmp","env":{"TEST_E2E_X":"1"}}`), &got); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if got.Command != "git" || len(got.Args) != 1 || got.Args[0] != "status" {
		t.Fatalf("legacy argv not mapped: %+v", got)
	}
	if got.CWD != "/tmp" {
		t.Fatalf("cwd mismatch: %q", got.CWD)
	}
	if got.Env["TEST_E2E_X"] != "1" || len(got.Env) != 1 {
		t.Fatalf("env mismatch: %#v", got.Env)
	}
}

func newSkillRPCTestServer(t *testing.T, svc Service) *platformrpc.Server {
	t.Helper()
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(newSkillHandlers(svc, nil).Handlers)
	return server
}

func TestSkillListHostRPCRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	server := newSkillRPCTestServer(t, newTestSkillService(t))
	_, err := server.Dispatch(context.Background(), "skill/list", json.RawMessage(`{"query":"demo"}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.InvalidParams {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.InvalidParams)
	}
}

func TestSkillListHostRPCResponseHidesLegacyFields(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	writeTestSkill(t, svc.root, "demo-skill", "---\nname: demo-skill\ndescription: Demo desc\nsummary: Demo sum\ndisable_model_invocation: true\n---\n# Demo")
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skill/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	var got struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(got.Skills))
	}
	entry := got.Skills[0]
	for _, key := range []string{"name", "summary", "description", "trust", "content_hash", "disable_model_invocation"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("missing key %q in %#v", key, entry)
		}
	}
	for _, key := range []string{"dir", "trigger_words", "force_words", "allowed_tools"} {
		if _, ok := entry[key]; ok {
			t.Fatalf("unexpected legacy key %q in %#v", key, entry)
		}
	}
}

func TestSkillExpandHostRPCRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	writeTestSkill(t, svc.root, "demo", "---\nname: demo\n---\n## Usage\nhello")
	server := newSkillRPCTestServer(t, svc)

	_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo","if_hash":"abc"}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.InvalidParams {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.InvalidParams)
	}
}

func TestSkillExpandHostRPCMapsErrors(t *testing.T) {
	t.Parallel()

	t.Run("not_found", func(t *testing.T) {
		server := newSkillRPCTestServer(t, newTestSkillService(t))
		_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"ghost"}`))
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
		}
		if rpcErr.Code != jrpc2.Code(platformrpc.CodeNotFound) {
			t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.Code(platformrpc.CodeNotFound))
		}
	})

	t.Run("invalid_params", func(t *testing.T) {
		svc := newTestSkillService(t)
		writeTestSkill(t, svc.root, "demo", "---\nname: demo\n---\nbody")
		server := newSkillRPCTestServer(t, svc)
		_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo","section":"../escape"}`))
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
		}
		if rpcErr.Code != jrpc2.InvalidParams {
			t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.InvalidParams)
		}
	})
}

func TestSkillExpandHostRPCResponseShape(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	writeTestSkill(t, svc.root, "demo", "---\nname: demo\nsummary: Demo sum\n---\n## Usage\nhello world")
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo","section":"## Usage","max_bytes":20}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, key := range []string{"name", "section", "path", "summary", "content", "truncated", "total_bytes", "content_hash", "trust"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing key %q in %#v", key, got)
		}
	}
	for _, key := range []string{"version", "anchor", "skill_dir"} {
		if _, ok := got[key]; ok {
			t.Fatalf("unexpected legacy key %q in %#v", key, got)
		}
	}
}
