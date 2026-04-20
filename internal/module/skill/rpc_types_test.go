package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
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

func TestSkillListStrictDecodeRejectsUnknownField(t *testing.T) {
	t.Parallel()

	server := newSkillRPCServer(t, &service{root: t.TempDir(), http: &http.Client{}})
	_, err := server.Dispatch(context.Background(), "skill/list", json.RawMessage(`{"unexpected":true}`))
	assertInvalidParamsError(t, err)
}

func TestSkillExpandStrictDecodeRejectsUnknownField(t *testing.T) {
	t.Parallel()

	svc := &service{root: t.TempDir(), http: &http.Client{}}
	writeTestSkill(t, svc.root, "demo-skill", "# demo")
	server := newSkillRPCServer(t, svc)
	_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo-skill","extra":true}`))
	assertInvalidParamsError(t, err)
}

func TestSkillExpandDispatchNotFoundUsesHostCode(t *testing.T) {
	t.Parallel()

	server := newSkillRPCServer(t, &service{root: t.TempDir(), http: &http.Client{}})
	_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"ghost"}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != -31001 {
		t.Fatalf("rpcErr.Code = %v, want -31001", rpcErr.Code)
	}
}

func TestSkillExpandDispatchRejectsPathEscape(t *testing.T) {
	t.Parallel()

	svc := &service{root: t.TempDir(), http: &http.Client{}}
	writeTestSkill(t, svc.root, "demo-skill", "# demo")
	server := newSkillRPCServer(t, svc)
	_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo-skill","section":"../secret.txt"}`))
	assertInvalidParamsError(t, err)
}

func TestSkillListResponseOmitsInternalFields(t *testing.T) {
	t.Parallel()

	svc := &service{root: t.TempDir(), http: &http.Client{}}
	writeTestSkill(t, svc.root, "demo-skill", "---\ndescription: Demo skill\ndisable_model_invocation: true\nallowed_tools: Read\ntrigger_words: hello\nforce_words: force\n---\n# demo\n")
	server := newSkillRPCServer(t, svc)
	raw, err := server.Dispatch(context.Background(), "skill/list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if bytes.Contains(raw, []byte(`"dir"`)) || bytes.Contains(raw, []byte(`"trigger_words"`)) || bytes.Contains(raw, []byte(`"force_words"`)) || bytes.Contains(raw, []byte(`"allowed_tools"`)) {
		t.Fatalf("skill/list leaked internal fields: %s", raw)
	}
	var got SkillListResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(got.Skills))
	}
	item := got.Skills[0]
	if item.Name != "demo-skill" || item.Description != "Demo skill" || !item.DisableModelInvocation {
		t.Fatalf("unexpected skill/list item: %+v", item)
	}
	if item.ContentHash == "" {
		t.Fatal("skill/list content_hash is empty")
	}
}

func newSkillRPCServer(t *testing.T, svc Service) *platformrpc.Server {
	t.Helper()
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewSkillHandlers(svc).Handlers)
	return server
}

func assertInvalidParamsError(t *testing.T, err error) {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.InvalidParams {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.InvalidParams)
	}
}
