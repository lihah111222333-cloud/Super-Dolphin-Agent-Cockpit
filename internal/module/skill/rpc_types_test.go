package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
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
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, platformrpc.CodeInvalidParams)
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

func TestSkillsListHostRPCIncludesDisclosureTierSnapshot(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "hot-skill", "---\nname: hot-skill\nsummary: Hot\n---\n# Hot")
	writeScopedSystemSkill(t, svc.root, cwd, "unused-skill", "---\nname: unused-skill\nsummary: Unused\n---\n# Unused")
	svc.disclosureTiers = fakeSkillDisclosureTierSource{
		snapshot: contract.SkillDisclosureSnapshot{
			Workspace: contract.SkillDisclosureStats{
				"hot-skill": {Calls: []time.Time{time.Now(), time.Now(), time.Now(), time.Now()}},
			},
			Global: contract.SkillDisclosureStats{},
			Config: contract.SkillDisclosureConfig{
				HalfLife:       7 * 24 * time.Hour,
				FrozenDuration: 90 * 24 * time.Hour,
				WSMinCalls:     1,
				WSWeight:       0.7,
			},
		},
	}
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skills/list", json.RawMessage(`{"cwd":"`+cwd+`"}`))
	if err != nil {
		t.Fatalf("Dispatch skills/list: %v", err)
	}
	var got struct {
		Skills []SkillInfo `json:"skills"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	tiers := map[string]string{}
	for _, item := range got.Skills {
		tiers[item.Name] = item.DisclosureTier
	}
	if tiers["hot-skill"] != "hot" {
		t.Fatalf("hot-skill disclosure_tier = %q, want hot; all tiers=%#v", tiers["hot-skill"], tiers)
	}
	if tiers["unused-skill"] != "frozen" {
		t.Fatalf("unused-skill disclosure_tier = %q, want frozen; all tiers=%#v", tiers["unused-skill"], tiers)
	}

	rawHost, err := server.Dispatch(context.Background(), "skill/list", json.RawMessage(`{"cwd":"`+cwd+`"}`))
	if err != nil {
		t.Fatalf("Dispatch skill/list: %v", err)
	}
	var host skillListResult
	if err := json.Unmarshal(rawHost, &host); err != nil {
		t.Fatalf("json.Unmarshal skill/list: %v", err)
	}
	hostTiers := map[string]string{}
	for _, item := range host.Skills {
		hostTiers[item.Name] = item.DisclosureTier
	}
	if hostTiers["hot-skill"] != "hot" {
		t.Fatalf("skill/list hot-skill disclosure_tier = %q, want hot; all tiers=%#v", hostTiers["hot-skill"], hostTiers)
	}
}

func TestSkillDisclosureTierForScore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		score float64
		want  string
	}{
		{name: "hot", score: 3, want: "hot"},
		{name: "warm", score: 1, want: "warm"},
		{name: "cold", score: 0.1, want: "cold"},
		{name: "frozen", score: 0, want: "frozen"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := skillDisclosureTierForScore(tc.score); got != tc.want {
				t.Fatalf("skillDisclosureTierForScore(%v) = %q, want %q", tc.score, got, tc.want)
			}
		})
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
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, platformrpc.CodeInvalidParams)
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
		if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
			t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, platformrpc.CodeInvalidParams)
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

func TestSkillsListHostRPCUsesCWDForProjectSkillsAndSharesSystem(t *testing.T) {
	t.Parallel()

	systemRoot, projectA, projectB := setupSkillsListCWDProjects(t)
	svc := &service{
		root:              systemRoot,
		projectRoot:       projectB,
		projectSkillsRoot: defaultProjectSkillsRoot(projectB),
		http:              &http.Client{},
	}
	server := newSkillRPCTestServer(t, svc)

	scoped := dispatchSkillsListForTest(t, server, projectA)
	assertSkillListNames(t, "project A", scoped.Skills, []string{"shared-a", "shared-b"}, []string{"local-b"})

	projectBList := dispatchSkillsListForTest(t, server, projectB)
	assertSkillListNames(t, "project B", projectBList.Skills, []string{"local-b", "shared-a", "shared-b"}, nil)
	assertSkillsListRejectsMissingCWD(t, server)
}

func setupSkillsListCWDProjects(t *testing.T) (string, string, string) {
	t.Helper()
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
	return systemRoot, projectA, projectB
}

type skillsListRPCResult struct {
	Skills []skillListItem `json:"skills"`
}

func dispatchSkillsListForTest(t *testing.T, server *platformrpc.Server, cwd string) skillsListRPCResult {
	t.Helper()
	rawScoped, err := server.Dispatch(context.Background(), "skills/list", json.RawMessage(`{"cwd":"`+cwd+`"}`))
	if err != nil {
		t.Fatalf("Dispatch scoped skills/list: %v", err)
	}
	var scoped skillsListRPCResult
	if err := json.Unmarshal(rawScoped, &scoped); err != nil {
		t.Fatalf("json.Unmarshal scoped: %v", err)
	}
	return scoped
}

func assertSkillListNames(t *testing.T, label string, skills []skillListItem, want, forbidden []string) {
	t.Helper()
	names := skillListNames(skills)
	if len(skills) != len(want) {
		t.Fatalf("%s skills = %#v, want %v", label, skills, want)
	}
	for _, name := range want {
		if !names[name] {
			t.Fatalf("%s skills = %#v, missing %q", label, skills, name)
		}
	}
	for _, name := range forbidden {
		if names[name] {
			t.Fatalf("%s skills = %#v, unexpectedly included %q", label, skills, name)
		}
	}
}

func skillListNames(skills []skillListItem) map[string]bool {
	names := make(map[string]bool, len(skills))
	for _, item := range skills {
		names[item.Name] = true
	}
	return names
}

func assertSkillsListRejectsMissingCWD(t *testing.T, server *platformrpc.Server) {
	t.Helper()
	_, err := server.Dispatch(context.Background(), "skills/list", json.RawMessage(`{}`))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch global skills/list error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("rpcErr.Code = %v, want %v", rpcErr.Code, platformrpc.CodeInvalidParams)
	}
}

func TestSkillExpandHostRPCSharesSystemSkillAcrossCWD(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	writeScopedSystemSkill(t, systemRoot, projectA, "shared", "---\nname: shared\nsummary: global\n---\nglobal-body")
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
	if got.Name != "shared" || got.Summary != "global" || got.Content != "---\nname: shared\nsummary: global\n---\nglobal-body" {
		t.Fatalf("global system expand result = %#v", got)
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
		{method: "skills/match/preview", params: `{"threadId":"t1","text":"hello"}`},
		{method: "skills/local/read", params: `{"path":"/tmp/skill/SKILL.md"}`},
		{method: "skills/local/listFiles", params: `{"dir":"/tmp/skill"}`},
		{method: "skills/local/write", params: `{"path":"/tmp/skill/SKILL.md","content":"x"}`},
		{method: "skills/local/importDir", params: `{"path":"/tmp/skill"}`},
		{method: "skills/local/delete", params: `{"name":"demo"}`},
		{method: "skills/create", params: `{"name":"demo","content":"# demo"}`},
	}
	for _, tc := range cases {
		_, err := server.Dispatch(context.Background(), tc.method, json.RawMessage(tc.params))
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("%s error = %T, want *jrpc2.Error", tc.method, err)
		}
		if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
			t.Fatalf("%s code = %v, want %v", tc.method, rpcErr.Code, platformrpc.CodeInvalidParams)
		}
		if rpcErr.Message != ErrMissingCWD.Error() {
			t.Fatalf("%s message = %q, want %q", tc.method, rpcErr.Message, ErrMissingCWD.Error())
		}
	}
}

type fakeSkillDisclosureTierSource struct {
	snapshot contract.SkillDisclosureSnapshot
}

func (f fakeSkillDisclosureTierSource) Enabled() bool { return true }

func (f fakeSkillDisclosureTierSource) DisclosureSnapshot() contract.SkillDisclosureSnapshot {
	return f.snapshot
}
