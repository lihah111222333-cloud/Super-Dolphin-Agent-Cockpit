package tools

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

type fakeBuiltinPromptRegistry struct {
	templates    []contract.BuiltinPromptTemplate
	sections     map[int64][]contract.BuiltinPromptSection
	listCalls    int
	getCalls     int
	sectionCalls int
}

func (r *fakeBuiltinPromptRegistry) ListTemplates() []contract.BuiltinPromptTemplate {
	r.listCalls++
	out := make([]contract.BuiltinPromptTemplate, len(r.templates))
	copy(out, r.templates)
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...)
		out[i].MatchWhen = cloneRawTestJSON(out[i].MatchWhen)
	}
	return out
}

func (r *fakeBuiltinPromptRegistry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	r.getCalls++
	for _, template := range r.templates {
		if template.PromptKey == promptKey {
			template.Tags = append([]string(nil), template.Tags...)
			template.MatchWhen = cloneRawTestJSON(template.MatchWhen)
			return template, true
		}
	}
	return contract.BuiltinPromptTemplate{}, false
}

func (r *fakeBuiltinPromptRegistry) SectionsByTemplateID(templateID int64) []contract.BuiltinPromptSection {
	r.sectionCalls++
	sections := r.sections[templateID]
	out := make([]contract.BuiltinPromptSection, len(sections))
	copy(out, sections)
	for i := range out {
		out[i].EnableWhen = cloneRawTestJSON(out[i].EnableWhen)
	}
	return out
}

func TestHandlePromptListReturnsBuiltinWhenDBEmpty(t *testing.T) {
	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "main/test-auto-task", "测试自动化任务执行者"),
	}}
	dbListed := false
	handler := HandlePromptList(stubPromptStore{
		list: func(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			dbListed = true
			if filter.CWD != "/repo/a" || !filter.RuntimeVisible {
				t.Fatalf("List() filter = %+v, want runtime-visible /repo/a", filter)
			}
			return nil, nil
		},
	}, builtin)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	got := result.([]promptTemplateDTO)
	if !dbListed {
		t.Fatal("HandlePromptList() did not query DB")
	}
	if len(got) != 1 || got[0].PromptKey != "main/test-auto-task" {
		t.Fatalf("prompt_list result = %#v, want builtin prompt", got)
	}
}

func TestHandlePromptGetReadsBuiltinSectionPreview(t *testing.T) {
	const templateID int64 = -10
	builtin := &fakeBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			testBuiltinPrompt(templateID, "main/test-auto-task", "测试自动化任务执行者"),
		},
		sections: map[int64][]contract.BuiltinPromptSection{
			templateID: {
				{ID: -2, TemplateID: templateID, SectionKey: "workflow", Region: "dynamic", Ordinal: 0, Body: "Workflow body", TriggerType: "keyword", Enabled: true},
				{ID: -3, TemplateID: templateID, SectionKey: "recall", Region: "dynamic", Ordinal: 1, Body: "Recall body must stay hidden", TriggerType: "recall", Enabled: true},
				{ID: -1, TemplateID: templateID, SectionKey: "identity", Region: "static", Ordinal: 10, Body: "Identity body", TriggerType: "always", Enabled: true},
			},
		},
	}
	handler := HandlePromptGet(stubPromptStore{
		get: func(context.Context, string) (*promptstore.PromptTemplate, error) {
			t.Fatal("Get() must not be called when builtin prompt matches")
			return nil, nil
		},
	}, builtin)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: "main/test-auto-task"}))
	if err != nil {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	got := result.(promptTemplateDTO)
	if got.PromptText != "Identity body\n\nWorkflow body" {
		t.Fatalf("PromptText = %q, want builtin section preview", got.PromptText)
	}
	if strings.Contains(got.PromptText, "Recall body") {
		t.Fatalf("PromptText leaked recall section: %q", got.PromptText)
	}
}

func TestHandlePromptListBuiltinKeyHidesDBSameKey(t *testing.T) {
	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "main/test-auto-task", "内置执行者"),
	}}
	handler := HandlePromptList(stubPromptStore{
		list: func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			return []promptstore.PromptTemplate{
				testDBPrompt(42, "main/test-auto-task", "旧 DB seed"),
				testDBPrompt(43, "main/other", "DB other"),
			}, nil
		},
	}, builtin)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	got := result.([]promptTemplateDTO)
	if countPromptKey(got, "main/test-auto-task") != 1 {
		t.Fatalf("main/test-auto-task count = %d, result=%#v", countPromptKey(got, "main/test-auto-task"), got)
	}
	if titleForPromptKey(got, "main/test-auto-task") != "内置执行者" {
		t.Fatalf("same-key title = %q, want builtin title", titleForPromptKey(got, "main/test-auto-task"))
	}
	if countPromptKey(got, "main/other") != 1 {
		t.Fatalf("DB-only prompt missing: %#v", got)
	}
}

func TestHandlePromptListKeywordCannotBypassBuiltinPriority(t *testing.T) {
	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "main/test-auto-task", "内置执行者"),
	}}
	handler := HandlePromptList(stubPromptStore{
		list: func(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			if filter.Keyword != "" {
				t.Fatalf("List() keyword = %q, want empty keyword for tool-side filtering", filter.Keyword)
			}
			return []promptstore.PromptTemplate{
				testDBPrompt(42, "main/test-auto-task", "legacy DB seed"),
			}, nil
		},
	}, builtin)

	result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{Keyword: "legacy"}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	got := result.([]promptTemplateDTO)
	if len(got) != 0 {
		t.Fatalf("prompt_list result = %#v, want same-key DB seed hidden before keyword filtering", got)
	}
}

func TestHandlePromptListDoesNotSwallowDBError(t *testing.T) {
	dbErr := errors.New("db unavailable")
	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "main/test-auto-task", "内置执行者"),
	}}
	handler := HandlePromptList(stubPromptStore{
		list: func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			return nil, dbErr
		},
	}, builtin)

	_, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{}))
	if !errors.Is(err, dbErr) {
		t.Fatalf("HandlePromptList() error = %v, want DB error", err)
	}
}

func TestHandlePromptListAndGetUseSameBuiltinPriority(t *testing.T) {
	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "main/test-auto-task", "内置执行者"),
	}}
	store := stubPromptStore{
		list: func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			return []promptstore.PromptTemplate{
				testDBPrompt(42, "main/test-auto-task", "旧 DB seed"),
			}, nil
		},
		get: func(context.Context, string) (*promptstore.PromptTemplate, error) {
			t.Fatal("Get() must not be called when builtin prompt matches")
			return nil, nil
		},
	}
	listHandler := HandlePromptList(store, builtin)
	getHandler := HandlePromptGet(store, builtin)

	listResult, err := listHandler(promptToolTestContext(), mustRawInput(t, promptListInput{}))
	if err != nil {
		t.Fatalf("HandlePromptList() error = %v", err)
	}
	list := listResult.([]promptTemplateDTO)
	if len(list) != 1 || list[0].Title != "内置执行者" {
		t.Fatalf("prompt_list result = %#v, want builtin title", list)
	}
	getResult, err := getHandler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: "main/test-auto-task"}))
	if err != nil {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	if got := getResult.(promptTemplateDTO); got.Title != "内置执行者" {
		t.Fatalf("prompt_get title = %q, want builtin title", got.Title)
	}
}

func TestHandlePromptGetFallsBackToDBAndFailsFastOnDBErrors(t *testing.T) {
	t.Run("not found uses DB fallback", func(t *testing.T) {
		handler := HandlePromptGet(stubPromptStore{
			get: func(_ context.Context, key string) (*promptstore.PromptTemplate, error) {
				return &promptstore.PromptTemplate{
					ID:        42,
					PromptKey: key,
					Title:     "DB Prompt",
					Tags:      json.RawMessage(`["scope.global"]`),
					Enabled:   true,
				}, nil
			},
		}, &fakeBuiltinPromptRegistry{})

		result, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: "custom/prompt"}))
		if err != nil {
			t.Fatalf("HandlePromptGet() error = %v", err)
		}
		if got := result.(promptTemplateDTO); got.Title != "DB Prompt" {
			t.Fatalf("prompt_get title = %q, want DB Prompt", got.Title)
		}
	})

	t.Run("not found stays not found", func(t *testing.T) {
		handler := HandlePromptGet(stubPromptStore{
			get: func(context.Context, string) (*promptstore.PromptTemplate, error) {
				return nil, platformdb.ErrNotFound
			},
		}, &fakeBuiltinPromptRegistry{})

		_, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: "missing"}))
		if err == nil || err.Error() != "prompt missing not found" {
			t.Fatalf("HandlePromptGet() error = %v, want not found", err)
		}
	})

	t.Run("db error is returned", func(t *testing.T) {
		dbErr := errors.New("db read failed")
		handler := HandlePromptGet(stubPromptStore{
			get: func(context.Context, string) (*promptstore.PromptTemplate, error) {
				return nil, dbErr
			},
		}, &fakeBuiltinPromptRegistry{})

		_, err := handler(promptToolTestContext(), mustRawInput(t, promptGetInput{PromptKey: "missing"}))
		if !errors.Is(err, dbErr) {
			t.Fatalf("HandlePromptGet() error = %v, want DB error", err)
		}
	})
}

func TestHandlePromptListUsesPassedRegistry(t *testing.T) {
	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		testBuiltinPrompt(-1, "test/passed-registry-only", "Passed Registry Prompt"),
	}}
	handler := HandlePromptList(stubPromptStore{
		list: func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			return nil, nil
		},
	}, builtin)

	for i := 0; i < 2; i++ {
		result, err := handler(promptToolTestContext(), mustRawInput(t, promptListInput{}))
		if err != nil {
			t.Fatalf("HandlePromptList() call %d error = %v", i+1, err)
		}
		got := result.([]promptTemplateDTO)
		if len(got) != 1 || got[0].PromptKey != "test/passed-registry-only" {
			t.Fatalf("HandlePromptList() call %d result = %#v, want passed fake registry prompt", i+1, got)
		}
	}
	if builtin.listCalls != 2 {
		t.Fatalf("builtin ListTemplates calls = %d, want one read per request from passed registry", builtin.listCalls)
	}
}

func TestPromptToolsDoNotLoadDefaultBuiltinRegistryOnRequestPath(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "prompt_tools.go", nil, 0)
	if err != nil {
		t.Fatalf("parse prompt_tools.go: %v", err)
	}
	if fileContainsSelector(file, "builtinprompts", "NewDefaultRegistry") {
		t.Fatal("prompt_tools.go must use injected builtin registry instead of builtinprompts.NewDefaultRegistry on request path")
	}
}

func testBuiltinPrompt(id int64, promptKey, title string) contract.BuiltinPromptTemplate {
	return contract.BuiltinPromptTemplate{
		ID:          id,
		PromptKey:   promptKey,
		Title:       title,
		AgentKey:    "automation",
		ToolName:    "agent",
		WhenToUse:   "automation task execution",
		Description: "runtime visible builtin prompt",
		Tags:        []string{"scope.global", "intent:expert"},
		Enabled:     true,
		Scope:       "global",
		Priority:    100,
	}
}

func testDBPrompt(id int64, promptKey, title string) promptstore.PromptTemplate {
	return promptstore.PromptTemplate{
		ID:          id,
		PromptKey:   promptKey,
		Title:       title,
		AgentKey:    "automation",
		ToolName:    "agent",
		PromptText:  "DB prompt text",
		Tags:        json.RawMessage(`["scope.global"]`),
		Enabled:     true,
		CreatedBy:   "system.seed",
		UpdatedBy:   "system.seed",
		UpdatedAt:   time.Unix(100, 0),
		Description: "DB prompt",
		WhenToUse:   "legacy seed",
	}
}

func countPromptKey(prompts []promptTemplateDTO, promptKey string) int {
	count := 0
	for _, prompt := range prompts {
		if prompt.PromptKey == promptKey {
			count++
		}
	}
	return count
}

func titleForPromptKey(prompts []promptTemplateDTO, promptKey string) string {
	for _, prompt := range prompts {
		if prompt.PromptKey == promptKey {
			return prompt.Title
		}
	}
	return ""
}

func cloneRawTestJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func fileContainsSelector(file *ast.File, xName, selName string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != selName {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if ok && x.Name == xName {
			found = true
			return false
		}
		return true
	})
	return found
}
