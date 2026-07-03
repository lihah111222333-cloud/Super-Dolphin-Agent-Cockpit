package skill

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(newSkillHandlers(svc).Handlers)
	return server
}

func TestSkillHandlersDoNotExposeCandidateRPC(t *testing.T) {
	t.Parallel()

	handlers := newSkillHandlers(newTestSkillService(t)).Handlers
	for _, method := range []string{
		"skills/candidate/list/pending",
		"skills/candidate/get",
		"skills/candidate/approve",
		"skills/candidate/reject",
	} {
		if _, ok := handlers[method]; ok {
			t.Fatalf("old skill candidate RPC %q is still registered", method)
		}
	}
}

type fakeSkillDreamExecutor struct {
	prompt string
	result string
	err    error
}

func (f *fakeSkillDreamExecutor) ExecuteDream(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	if f.err != nil {
		return "", f.err
	}
	return f.result, nil
}

type sequenceSkillDreamExecutor struct {
	results     []string
	calls       int
	hasDeadline bool
	deadline    time.Time
}

func (f *sequenceSkillDreamExecutor) ExecuteDream(ctx context.Context, _ string) (string, error) {
	f.calls++
	f.deadline, f.hasDeadline = ctx.Deadline()
	if f.calls <= len(f.results) {
		return f.results[f.calls-1], nil
	}
	return "", errors.New("unexpected extra dream call")
}

type optionsSkillDreamExecutor struct {
	result  string
	options []contract.DreamOptions
}

func (f *optionsSkillDreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return f.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

func (f *optionsSkillDreamExecutor) ExecuteDreamWithOptions(_ context.Context, _ string, options contract.DreamOptions) (string, error) {
	f.options = append(f.options, options)
	return f.result, nil
}

func TestSkillSummarySuggestRPCUsesDreamExecutor(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	dream := &fakeSkillDreamExecutor{result: `{"description":"当你需要编写或验证技能文件时使用。"}`}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(newSkillHandlers(svc, dream).Handlers)

	raw, err := server.Dispatch(context.Background(), "skills/summary/suggest", json.RawMessage(`{
		"cwd":"/tmp/project",
		"name":"编写技能",
		"content":"# 编写技能\n创建或修改 SKILL.md。",
		"scenario_words":["skill","@skill"]
	}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	var got struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.Description != "当你需要编写或验证技能文件时使用。" {
		t.Fatalf("description = %q", got.Description)
	}
	for _, want := range []string{"编写技能", "创建或修改 SKILL.md", "@skill"} {
		if !strings.Contains(dream.prompt, want) {
			t.Fatalf("dream prompt missing %q:\n%s", want, dream.prompt)
		}
	}
	for _, want := range []string{
		"不是总结 skill 内容",
		"LLM 什么时候应该调用",
		"description、scenario_words",
		"1 到 3 个最重要、最具体的调用场景",
		"不要使用“这个技能”“本技能”“可以帮助”“用于”",
	} {
		if !strings.Contains(dream.prompt, want) {
			t.Fatalf("dream prompt missing precision rule %q:\n%s", want, dream.prompt)
		}
	}
}

func TestSkillSummarySuggestRPCPassesRequestedDreamOptions(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	dream := &optionsSkillDreamExecutor{result: `{"description":"当你需要管理多代理工程流程时使用。"}`}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(newSkillHandlers(svc, dream).Handlers)

	_, err := server.Dispatch(context.Background(), "skills/summary/suggest", json.RawMessage(`{
		"cwd":"/tmp/project",
		"name":"Agent工程学",
		"content":"# Agent 工程学\n管理多代理工程流程。",
		"scope":"project",
		"provider":"codex",
		"model":"gpt-5.5",
		"model_provider":"openrouter"
	}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(dream.options) != 1 {
		t.Fatalf("dream options calls = %d, want 1", len(dream.options))
	}
	want := contract.DreamOptions{Provider: "codex", Model: "gpt-5.5", ModelProvider: "openrouter"}
	if dream.options[0] != want {
		t.Fatalf("dream options = %#v, want %#v", dream.options[0], want)
	}
}

func TestSkillSummarySuggestRetriesInvalidModelShapeWithinInteractiveTimeout(t *testing.T) {
	t.Parallel()

	dream := &sequenceSkillDreamExecutor{results: []string{`not-json`, `{"description":"当你需要编写或验证技能文件时使用。"}`}}
	got, err := suggestSkillSummary(context.Background(), dream, skillSummarySuggestParams{
		Name:    "编写技能",
		Content: "# 编写技能\n创建或修改 SKILL.md。",
	})
	if err != nil {
		t.Fatalf("suggestSkillSummary() error = %v", err)
	}
	if got != "当你需要编写或验证技能文件时使用。" {
		t.Fatalf("description = %q", got)
	}
	if dream.calls != 2 {
		t.Fatalf("dream calls = %d, want 2", dream.calls)
	}
	if !dream.hasDeadline {
		t.Fatal("dream executor context has no deadline")
	}
	remaining := time.Until(dream.deadline)
	if remaining <= 0 || remaining > platformconfig.RPCRequestTimeout {
		t.Fatalf("dream executor timeout = %v, want within %v", remaining, platformconfig.RPCRequestTimeout)
	}
}

func TestSkillSummarySuggestRPCRejectsGenericDescription(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	dream := &fakeSkillDreamExecutor{result: `{"description":"帮你处理各种问题。"}`}
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(newSkillHandlers(svc, dream).Handlers)

	_, err := server.Dispatch(context.Background(), "skills/summary/suggest", json.RawMessage(`{
		"name":"通用助手",
		"content":"# 通用助手\n处理很多事情。"
	}`))
	if err == nil || !strings.Contains(err.Error(), "skill summary suggestion quality") {
		t.Fatalf("Dispatch() error = %v, want quality rejection", err)
	}
}

func TestParseSkillSummarySuggestionResultValidatesShapeAndWeakPhrases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		wantIssue   string
	}{
		{
			name:        "accepts precise invocation scenario",
			description: "当你需要编写或验证技能文件时使用。",
		},
		{
			name:        "rejects missing fixed prefix",
			description: "需要编写或验证技能文件时使用。",
			wantIssue:   "invalid_shape",
		},
		{
			name:        "rejects missing fixed suffix",
			description: "当你需要编写或验证技能文件。",
			wantIssue:   "invalid_shape",
		},
		{
			name:        "rejects self-referential wording",
			description: "当你需要使用这个技能帮助你写代码时使用。",
			wantIssue:   "self_referential",
		},
		{
			name:        "rejects weak purpose wording",
			description: "当你需要用于生成技能简介时使用。",
			wantIssue:   "weak_wording",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseSkillSummarySuggestionResult(`{"description":` + strconv.Quote(tt.description) + `}`)
			if tt.wantIssue == "" {
				if err != nil {
					t.Fatalf("parseSkillSummarySuggestionResult() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "skill summary suggestion quality: "+tt.wantIssue) {
				t.Fatalf("parseSkillSummarySuggestionResult() error = %v, want issue %q", err, tt.wantIssue)
			}
		})
	}
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
	writeScopedSystemSkill(t, svc.root, cwd, "demo-skill", "---\nname: demo-skill\ndisplay_name: Demo Skill\ndescription: Demo desc\nsummary: Demo sum\ndisable_model_invocation: true\n---\n# Demo")
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skill/list", mustRawJSON(t, map[string]any{"cwd": cwd}))
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
	for _, key := range []string{"name", "display_name", "summary", "description", "trust", "content_hash", "disable_model_invocation"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("missing key %q in %#v", key, entry)
		}
	}
	if entry["display_name"] != "Demo Skill" {
		t.Fatalf("display_name = %q", entry["display_name"])
	}
	for _, key := range []string{"dir", "trigger_words", "force_words", "allowed_tools"} {
		if _, ok := entry[key]; ok {
			t.Fatalf("unexpected legacy key %q in %#v", key, entry)
		}
	}
}

func TestSkillsListHostRPCIncludesDirAndSkillFile(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "demo-skill", "---\nname: demo-skill\ndisplay_name: Demo Skill\nsummary: Demo\n---\n# Demo")
	server := newSkillRPCTestServer(t, svc)

	raw, err := server.Dispatch(context.Background(), "skills/list", mustRawJSON(t, map[string]any{"cwd": cwd}))
	if err != nil {
		t.Fatalf("Dispatch skills/list: %v", err)
	}
	var got struct {
		Skills []skillListItem `json:"skills"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(got.Skills))
	}
	item := got.Skills[0]
	if item.DisplayName != "Demo Skill" {
		t.Fatalf("display name = %q", item.DisplayName)
	}
	if item.Dir == "" || filepath.Base(item.Dir) != "demo-skill" {
		t.Fatalf("dir = %q, want canonical skill dir", item.Dir)
	}
	if filepath.Clean(item.SkillFile) != filepath.Join(item.Dir, skillMainFile) {
		t.Fatalf("skill_file = %q, want %q", item.SkillFile, filepath.Join(item.Dir, skillMainFile))
	}
}

func TestSkillExpandHostRPCIsNotRegistered(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	cwd := filepath.Join(t.TempDir(), "repo")
	writeScopedSystemSkill(t, svc.root, cwd, "demo", "---\nname: demo\n---\n## Usage\nhello")
	server := newSkillRPCTestServer(t, svc)

	_, err := server.Dispatch(context.Background(), "skill/expand", mustRawJSON(t, map[string]any{"name": "demo", "cwd": cwd, "if_hash": "abc"}))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Dispatch() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(-32601) {
		t.Fatalf("rpcErr.Code = %v, want method not found", rpcErr.Code)
	}
}

func TestSkillsListHostRPCUsesCWDForProjectSkillsOnly(t *testing.T) {
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
	assertSkillListNames(t, "project A", scoped.Skills, []string{"shared-a"}, []string{"local-b", "shared-b"})

	projectBList := dispatchSkillsListForTest(t, server, projectB)
	assertSkillListNames(t, "project B", projectBList.Skills, []string{"local-b", "shared-b"}, []string{"shared-a"})
	assertSkillsListRejectsMissingCWD(t, server)
}

func setupSkillsListCWDProjects(t *testing.T) (string, string, string) {
	t.Helper()
	systemRoot := t.TempDir()
	projectA := filepath.Join(t.TempDir(), "wj", "langgraph")
	projectB := filepath.Join(t.TempDir(), "wj", "go-agent-v2")
	for _, root := range []string{projectA, projectB} {
		if err := os.MkdirAll(filepath.Join(root, ".agents", "skills"), 0o755); err != nil {
			t.Fatalf("mkdir project skills root: %v", err)
		}
	}
	writeTestSkill(t, filepath.Join(projectB, ".agents", "skills"), "local-b", "# local b")
	writeScopedSystemSkill(t, systemRoot, projectA, "shared-a", "---\nname: shared-a\nsummary: from-a\n---\nA")
	writeScopedSystemSkill(t, systemRoot, projectB, "shared-b", "---\nname: shared-b\nsummary: from-b\n---\nB")
	return systemRoot, projectA, projectB
}

type skillsListRPCResult struct {
	Skills []skillListItem `json:"skills"`
}

func dispatchSkillsListForTest(t *testing.T, server *platformrpc.Server, cwd string) skillsListRPCResult {
	t.Helper()
	rawScoped, err := server.Dispatch(context.Background(), "skills/list", mustRawJSON(t, map[string]any{"cwd": cwd}))
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

func TestSkillLocalDeleteRPCRequiresExplicitTarget(t *testing.T) {
	skipWindowsShortMirrorIntegration(t)

	t.Parallel()

	projectRoot := filepath.Join(t.TempDir(), "repo")
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	personalRoot := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser)
	writeTestSkill(t, projectSkillsRoot, "build", "# project")
	writeTestSkill(t, personalRoot, "build", "# personal")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: projectSkillsRoot,
		superDolphinHome:  superDolphinHome,
		http:              &http.Client{},
		auditStore:        &capturingSkillAuditStore{},
	}
	server := newSkillRPCTestServer(t, svc)

	assertDeleteRPCInvalidParams(t, server, mustRawJSON(t, map[string]any{"cwd": projectRoot, "name": "build"}))
	assertDeleteRPCInvalidParams(t, server, mustRawJSON(t, map[string]any{"cwd": projectRoot, "name": "build", "scope": "personal"}))

	_, err := server.Dispatch(context.Background(), "skills/local/delete", mustRawJSON(t, map[string]any{"cwd": projectRoot, "name": "build", "scope": "project"}))
	if err != nil {
		t.Fatalf("Dispatch project delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(personalRoot, "build", skillMainFile)); err != nil {
		t.Fatalf("personal skill should remain after project delete: %v", err)
	}

	_, err = server.Dispatch(context.Background(), "skills/local/delete", mustRawJSON(t, map[string]any{"cwd": projectRoot, "name": "build", "scope": "personal", "personal_type": "user"}))
	if err != nil {
		t.Fatalf("Dispatch personal delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(personalRoot, "build", skillMainFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("personal skill should be archived after personal delete, stat err=%v", err)
	}
	assertArchiveContainsSkill(t, superDolphinHome, filepath.Join(skillScopePersonal, personalSkillTypeUser, "build"))
}

func TestSkillLocalReadAndMatchRPCMapSameNameConflict(t *testing.T) {
	t.Parallel()

	projectRoot := filepath.Join(t.TempDir(), "repo")
	superDolphinHome := filepath.Join(t.TempDir(), ".super-dolphin")
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	personalRoot := filepath.Join(superDolphinHome, "skills", "personal", personalSkillTypeUser)
	writeTestSkill(t, projectSkillsRoot, "build", "---\nname: build\n---\n# project")
	writeTestSkill(t, personalRoot, "build", "---\nname: build\n---\n# personal")
	svc := &service{
		projectRoot:       projectRoot,
		projectSkillsRoot: projectSkillsRoot,
		superDolphinHome:  superDolphinHome,
		http:              &http.Client{},
	}
	server := newSkillRPCTestServer(t, svc)

	assertRPCConflict(t, server, "skills/local/read", mustRawJSON(t, map[string]any{"cwd": projectRoot, "path": "build"}))
	assertRPCConflict(t, server, "skills/match/preview", mustRawJSON(t, map[string]any{"cwd": projectRoot, "text": "build"}))
}

func assertDeleteRPCInvalidParams(t *testing.T, server *platformrpc.Server, params json.RawMessage) {
	t.Helper()
	_, err := server.Dispatch(context.Background(), "skills/local/delete", params)
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("delete error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("delete code = %v, want invalid params", rpcErr.Code)
	}
}

func assertRPCConflict(t *testing.T, server *platformrpc.Server, method string, params json.RawMessage) {
	t.Helper()
	_, err := server.Dispatch(context.Background(), method, params)
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("%s error = %T, want *jrpc2.Error", method, err)
	}
	if rpcErr.Code != jrpc2.Code(platformrpc.CodeConflict) {
		t.Fatalf("%s code = %v, want conflict", method, rpcErr.Code)
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
		{method: "skills/resolution_list", params: `{}`},
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
