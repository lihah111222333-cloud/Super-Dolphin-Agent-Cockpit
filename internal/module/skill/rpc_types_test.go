package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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

func TestSkillListParamsRejectsEmptyCWD(t *testing.T) {
	t.Parallel()

	var empty skillListParams
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatalf("Unmarshal empty returned error: %v", err)
	}
	if empty.CWD != "" {
		t.Fatalf("empty cwd = %q, want empty", empty.CWD)
	}

	var scoped skillListParams
	if err := json.Unmarshal([]byte(`{"cwd":"/tmp/project"}`), &scoped); err != nil {
		t.Fatalf("Unmarshal scoped returned error: %v", err)
	}
	if scoped.CWD != "/tmp/project" {
		t.Fatalf("scoped cwd = %q, want /tmp/project", scoped.CWD)
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
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "demo-skill", "---\nname: demo-skill\ndescription: Demo desc\nsummary: Demo sum\ndisable_model_invocation: true\n---\n# Demo")
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skill/list", json.RawMessage(`{"cwd":"`+cwd+`"}`))
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
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "demo", "---\nname: demo\n---\n## Usage\nhello")
	server := newSkillRPCTestServer(t, svc)

	_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo","cwd":"`+cwd+`","if_hash":"abc"}`))
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
		cwd := filepath.Join(t.TempDir(), "repo")
		_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"ghost","cwd":"`+cwd+`"}`))
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
		cwd := filepath.Join(t.TempDir(), "repo")
		writeScopedSystemSkill(t, svc.root, cwd, "demo", "---\nname: demo\n---\nbody")
		server := newSkillRPCTestServer(t, svc)
		_, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo","cwd":"`+cwd+`","section":"../escape"}`))
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
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "demo", "---\nname: demo\nsummary: Demo sum\n---\n## Usage\nhello world")
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"demo","cwd":"`+cwd+`","section":"## Usage","max_bytes":20}`))
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

func TestSkillsListHostRPCScopesByCWD(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	for _, root := range []string{projectA, projectB} {
		if err := os.MkdirAll(filepath.Join(root, ".agent", "skills"), 0o755); err != nil {
			t.Fatalf("mkdir project skills root: %v", err)
		}
	}
	writeTestSkill(t, filepath.Join(projectB, ".agent", "skills"), "local-b", "# local b")
	writeScopedSystemSkill(t, systemRoot, projectA, "shared-a", "---\nname: shared-a\nsummary: from-a\n---\nA")
	writeScopedSystemSkill(t, systemRoot, projectB, "shared-b", "---\nname: shared-b\nsummary: from-b\n---\nB")

	svc := &service{
		root:              systemRoot,
		projectRoot:       projectB,
		projectSkillsRoot: defaultProjectSkillsRoot(projectB),
		http:              &http.Client{},
	}
	server := newSkillRPCTestServer(t, svc)

	rawScoped, err := server.Dispatch(context.Background(), "skills/list", json.RawMessage(`{"cwd":"`+projectA+`"}`))
	if err != nil {
		t.Fatalf("Dispatch scoped skills/list: %v", err)
	}
	var scoped struct {
		Skills []skillListItem `json:"skills"`
	}
	if err := json.Unmarshal(rawScoped, &scoped); err != nil {
		t.Fatalf("json.Unmarshal scoped: %v", err)
	}
	if len(scoped.Skills) != 1 || scoped.Skills[0].Name != "shared-a" {
		t.Fatalf("scoped skills = %#v, want only shared-a", scoped.Skills)
	}

	_, err = server.Dispatch(context.Background(), "skills/list", json.RawMessage(`{}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch global skills/list error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.InvalidParams {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, jrpc2.InvalidParams)
	}
}

func TestSkillExpandHostRPCScopesByCWD(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	writeScopedSystemSkill(t, systemRoot, projectA, "shared", "---\nname: shared\nsummary: from-a\n---\nproject-a")
	writeScopedSystemSkill(t, systemRoot, projectB, "shared", "---\nname: shared\nsummary: from-b\n---\nproject-b")
	svc := &service{root: systemRoot, http: &http.Client{}}
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skill/expand", json.RawMessage(`{"name":"shared","cwd":"`+projectB+`"}`))
	if err != nil {
		t.Fatalf("Dispatch skill/expand: %v", err)
	}
	var got skillExpandResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal skill/expand: %v", err)
	}
	if got.Name != "shared" || got.Summary != "from-b" || got.Content != "---\nname: shared\nsummary: from-b\n---\nproject-b" {
		t.Fatalf("scoped expand result = %#v", got)
	}
}

func TestSkillRPCRejectsEmptyCWD(t *testing.T) {
	t.Parallel()

	server := newSkillRPCTestServer(t, newTestSkillService(t))
	cases := []struct {
		method string
		params string
	}{
		{method: "skill/list", params: `{}`},
		{method: "skills/list", params: `{}`},
		{method: "skill/expand", params: `{"name":"demo"}`},
		{method: "skills/expandBody", params: `{"name":"demo"}`},
		{method: "skills/readResource", params: `{"name":"demo","path":"ref.md"}`},
		{method: "skills/match/preview", params: `{"threadId":"t1","text":"hello"}`},
		{method: "skills/local/read", params: `{"path":"/tmp/skill/SKILL.md"}`},
		{method: "skills/local/listFiles", params: `{"dir":"/tmp/skill"}`},
		{method: "skills/local/write", params: `{"path":"/tmp/skill/SKILL.md","content":"x"}`},
		{method: "skills/local/importDir", params: `{"path":"/tmp/skill"}`},
		{method: "skills/local/delete", params: `{"name":"demo"}`},
	}
	for _, tc := range cases {
		_, err := server.Dispatch(context.Background(), tc.method, json.RawMessage(tc.params))
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("%s error = %T, want *jrpc2.Error", tc.method, err)
		}
		if rpcErr.Code != jrpc2.InvalidParams {
			t.Fatalf("%s code = %v, want %v", tc.method, rpcErr.Code, jrpc2.InvalidParams)
		}
		if rpcErr.Message != ErrMissingCWD.Error() {
			t.Fatalf("%s message = %q, want %q", tc.method, rpcErr.Message, ErrMissingCWD.Error())
		}
	}
}
