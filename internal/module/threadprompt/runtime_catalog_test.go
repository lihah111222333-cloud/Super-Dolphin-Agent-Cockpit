package threadprompt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestRuntimeCatalogListsBuiltinAndUserExperts(t *testing.T) {
	t.Parallel()

	builtin := &fakeBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:        -101,
				PromptKey: "main/expert/builtin-review",
				Title:     "Builtin Review",
				AgentKey:  "main",
				WhenToUse: "Use for builtin code review.",
				Tags:      []string{"intent:expert", "review"},
				Enabled:   true,
				Scope:     "global",
			},
		},
	}
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{
		{
			ID:        7,
			PromptKey: "main/expert/user-sql",
			Title:     "User SQL",
			AgentKey:  "main",
			WhenToUse: "Use for SQL work.",
			Tags:      mustJSONTags("intent:expert", "scope.cwd:/repo/a"),
			Enabled:   true,
			CreatedBy: "prompt.rpc",
			UpdatedBy: "prompt.rpc",
		},
	}}

	catalog := newRuntimeCatalogForStore(store, builtin)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{
		AgentKey: "main",
		CWD:      "/repo/a",
	})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}

	if !runtimeCatalogHasPromptKey(templates, "main/expert/builtin-review") ||
		!runtimeCatalogHasPromptKey(templates, "main/expert/user-sql") {
		t.Fatalf("ListTemplates() = %#v, want builtin and user experts", templates)
	}
	got := runtimeCatalogTemplateByKey(templates, "main/expert/builtin-review")
	if got == nil {
		t.Fatal("builtin template missing")
	}
	if got.ID >= 0 || got.CreatedBy != "builtin.registry" || got.UpdatedBy != "builtin.registry" {
		t.Fatalf("builtin template metadata = %#v, want negative ID and builtin.registry authors", *got)
	}
	tags := promptstore.TemplateTags(got.Tags)
	for _, want := range []string{"intent:expert", "review", "builtin:system", "scope.global"} {
		if !runtimeCatalogStringSliceContains(tags, want) {
			t.Fatalf("builtin tags = %#v, want %q", tags, want)
		}
	}
}

func TestRuntimeCatalogZeroLimitStillListsStoreTemplates(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{templates: []promptstore.PromptTemplate{
		{
			ID:        7,
			PromptKey: "main/expert/user-sql",
			Title:     "User SQL",
			AgentKey:  "main",
			WhenToUse: "Use for SQL work.",
			Tags:      mustJSONTags("intent:expert", "scope.cwd:/repo/a"),
			Enabled:   true,
			CreatedBy: "prompt.rpc",
			UpdatedBy: "prompt.rpc",
		},
	}}

	catalog := newRuntimeCatalogForStore(store, nil)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{
		AgentKey: "main",
		CWD:      "/repo/a",
	})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(store.listFilters) != 1 || store.listFilters[0].Limit == 0 {
		t.Fatalf("store List() filter = %#v, want non-zero store limit for runtime zero-limit request", store.listFilters)
	}
	if !runtimeCatalogHasPromptKey(templates, "main/expert/user-sql") {
		t.Fatalf("ListTemplates() = %#v, want DB user template retained", templates)
	}
}

func TestRuntimeCatalogRegistryWinsOverHistoricalSystemSeedSameKey(t *testing.T) {
	t.Parallel()

	builtin := builtinReviewRegistryForTest()
	seedTemplate := seedReviewPromptTemplateForTest()
	seedStore := &fakePromptStore{
		templates:    []promptstore.PromptTemplate{seedTemplate},
		getTemplates: map[string]promptstore.PromptTemplate{runtimeCatalogReviewPromptKey: seedTemplate},
		sectionsByTemplateID: map[int64][]promptstore.PromptTemplateSection{
			42: {{ID: 99, TemplateID: 42, SectionKey: "identity", Body: "seed section", Enabled: true}},
		},
	}

	catalog := newRuntimeCatalogForStore(seedStore, builtin)
	templates := requireRuntimeCatalogList(t, catalog)
	requireRuntimePromptKeyCount(t, templates, runtimeCatalogReviewPromptKey, 1)
	requireRuntimeTemplateText(t, templates, runtimeCatalogReviewPromptKey, -11, "builtin prompt text")

	got := requireRuntimeCatalogGet(t, catalog, runtimeCatalogReviewPromptKey)
	requireRuntimeStoreTemplate(t, got, -11, "Builtin Review")
	requireBuiltinReviewSection(t, catalog, got.ID)
}

func TestRuntimeCatalogRegistryWinsOverHistoricalSystemSeedBeforeFilters(t *testing.T) {
	t.Parallel()

	seedTemplate := seedReviewPromptTemplateForTest()
	seedTemplate.Title = "Legacy Seed Title"
	seedTemplate.PromptText = "legacy-only-keyword"
	seedTemplate.WhenToUse = "legacy-only-keyword"
	store := &fakePromptStore{
		templates:    []promptstore.PromptTemplate{seedTemplate},
		getTemplates: map[string]promptstore.PromptTemplate{runtimeCatalogReviewPromptKey: seedTemplate},
	}

	catalog := newRuntimeCatalogForStore(store, builtinReviewRegistryForTest())
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{
		AgentKey: "main",
		CWD:      "/repo/a",
		Keyword:  "legacy-only-keyword",
	})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if got := runtimeCatalogPromptKeyCount(templates, runtimeCatalogReviewPromptKey); got != 0 {
		t.Fatalf("ListTemplates() prompt key count = %d, want stale seed hidden before keyword filter: %#v", got, templates)
	}
}

func TestRuntimeCatalogBuiltinKeyHidesUserSameKeyRows(t *testing.T) {
	t.Parallel()

	userTemplate := userReviewPromptTemplateForTest()
	userStore := &fakePromptStore{
		templates:    []promptstore.PromptTemplate{userTemplate},
		getTemplates: map[string]promptstore.PromptTemplate{runtimeCatalogReviewPromptKey: userTemplate},
	}
	userCatalog := newRuntimeCatalogForStore(userStore, builtinReviewRegistryForTest())
	userTemplates, err := userCatalog.ListTemplates(context.Background(), RuntimeListFilter{AgentKey: "main", CWD: "/repo/a"})
	if err != nil {
		t.Fatalf("ListTemplates() user row error = %v", err)
	}
	if got := runtimeCatalogPromptKeyCount(userTemplates, runtimeCatalogReviewPromptKey); got != 1 {
		t.Fatalf("ListTemplates() user row prompt key count = %d, want builtin only", got)
	}
	userGot, err := userCatalog.GetTemplate(context.Background(), runtimeCatalogReviewPromptKey, "/repo/a")
	if err != nil {
		t.Fatalf("GetTemplate() user row error = %v", err)
	}
	if userGot.ID != -11 || userGot.Title != "Builtin Review" {
		t.Fatalf("GetTemplate() user row = %#v, want builtin to own same prompt_key", *userGot)
	}
}

func TestRuntimeCatalogBuiltinKeyHidesManuallyEditedSystemSeedSameKey(t *testing.T) {
	t.Parallel()

	editedSeed := seedReviewPromptTemplateForTest()
	editedSeed.ID = 88
	editedSeed.Title = "Edited Seed Review"
	editedSeed.ManuallyEdited = true
	editedSeed.UpdatedBy = "prompt.rpc"
	store := &fakePromptStore{
		templates:    []promptstore.PromptTemplate{editedSeed},
		getTemplates: map[string]promptstore.PromptTemplate{runtimeCatalogReviewPromptKey: editedSeed},
	}

	catalog := newRuntimeCatalogForStore(store, builtinReviewRegistryForTest())
	templates := requireRuntimeCatalogList(t, catalog)
	requireRuntimePromptKeyCount(t, templates, runtimeCatalogReviewPromptKey, 1)

	got := requireRuntimeCatalogGet(t, catalog, runtimeCatalogReviewPromptKey)
	requireRuntimeStoreTemplate(t, got, -11, "Builtin Review")
}

func TestRuntimeCatalogBuiltinKeyHidesSeedKeyRowsUpdatedByUser(t *testing.T) {
	t.Parallel()

	userUpdatedSeed := seedReviewPromptTemplateForTest()
	userUpdatedSeed.ID = 89
	userUpdatedSeed.Title = "User Updated Seed Review"
	userUpdatedSeed.ManuallyEdited = false
	userUpdatedSeed.UpdatedBy = "rpc.prompts"
	store := &fakePromptStore{
		templates:    []promptstore.PromptTemplate{userUpdatedSeed},
		getTemplates: map[string]promptstore.PromptTemplate{runtimeCatalogReviewPromptKey: userUpdatedSeed},
	}

	catalog := newRuntimeCatalogForStore(store, builtinReviewRegistryForTest())
	templates := requireRuntimeCatalogList(t, catalog)
	requireRuntimePromptKeyCount(t, templates, runtimeCatalogReviewPromptKey, 1)

	got := requireRuntimeCatalogGet(t, catalog, runtimeCatalogReviewPromptKey)
	requireRuntimeStoreTemplate(t, got, -11, "Builtin Review")
}

func TestRuntimeCatalogLimitKeepsBuiltinWhenDBRowsAreNewer(t *testing.T) {
	t.Parallel()

	newerDBTemplate := userReviewPromptTemplateForTest()
	newerDBTemplate.PromptKey = "main/expert/newer-db"
	newerDBTemplate.UpdatedAt = time.Now()
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{newerDBTemplate}}

	catalog := newRuntimeCatalogForStore(store, builtinReviewRegistryForTest())
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{
		AgentKey: "main",
		CWD:      "/repo/a",
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if !runtimeCatalogHasPromptKey(templates, runtimeCatalogReviewPromptKey) {
		t.Fatalf("ListTemplates() = %#v, want builtin retained under limit", templates)
	}
}

func TestRuntimeCatalogKeywordFiltersUserRowsAfterStoreList(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{templates: []promptstore.PromptTemplate{
		{
			ID:        91,
			PromptKey: "main/expert/when-only",
			Title:     "When Only",
			AgentKey:  "main",
			WhenToUse: "needle appears only in when_to_use",
			Tags:      mustJSONTags("intent:expert", "scope.cwd:/repo/a"),
			Enabled:   true,
			CreatedBy: "prompt.rpc",
			UpdatedBy: "prompt.rpc",
		},
	}}

	catalog := newRuntimeCatalogForStore(store, nil)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{
		AgentKey: "main",
		CWD:      "/repo/a",
		Keyword:  "needle",
	})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(store.listFilters) != 1 || store.listFilters[0].Keyword != "" {
		t.Fatalf("store List() keyword = %#v, want runtime catalog to filter expanded fields itself", store.listFilters)
	}
	if !runtimeCatalogHasPromptKey(templates, "main/expert/when-only") {
		t.Fatalf("ListTemplates() = %#v, want when_to_use-only keyword match retained", templates)
	}
}

const runtimeCatalogReviewPromptKey = "main/expert/review"

func builtinReviewRegistryForTest() *fakeBuiltinPromptRegistry {
	return &fakeBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:          -11,
				PromptKey:   runtimeCatalogReviewPromptKey,
				Title:       "Builtin Review",
				AgentKey:    "main",
				PromptText:  "builtin prompt text",
				WhenToUse:   "Use builtin review.",
				Description: "Builtin review description.",
				Tags:        []string{"intent:expert"},
				Enabled:     true,
				Scope:       "global",
			},
		},
		sectionsByTemplateID: map[int64][]contract.BuiltinPromptSection{
			-11: {{ID: -101, TemplateID: -11, SectionKey: "identity", Region: "static", Ordinal: 1, Body: "builtin section", Enabled: true}},
		},
	}
}

func seedReviewPromptTemplateForTest() promptstore.PromptTemplate {
	return promptstore.PromptTemplate{
		ID:         42,
		PromptKey:  runtimeCatalogReviewPromptKey,
		Title:      "Historical Seed",
		AgentKey:   "main",
		PromptText: "stale seed text",
		WhenToUse:  "stale seed",
		Tags:       mustJSONTags("intent:expert", "scope.global"),
		Enabled:    true,
		CreatedBy:  "system.seed",
		UpdatedBy:  "migration",
	}
}

func userReviewPromptTemplateForTest() promptstore.PromptTemplate {
	return promptstore.PromptTemplate{
		ID:        77,
		PromptKey: runtimeCatalogReviewPromptKey,
		Title:     "User Review",
		AgentKey:  "main",
		WhenToUse: "Use user review.",
		Tags:      mustJSONTags("intent:expert", "scope.cwd:/repo/a"),
		Enabled:   true,
		CreatedBy: "prompt.rpc",
		UpdatedBy: "prompt.rpc",
	}
}

func TestRuntimeCatalogListTemplatesAppliesCWDVisibility(t *testing.T) {
	t.Parallel()

	builtin := &fakeBuiltinPromptRegistry{templates: []contract.BuiltinPromptTemplate{
		{ID: -1, PromptKey: "main/builtin-global", Title: "Builtin Global", AgentKey: "main", Enabled: true, Scope: "global"},
	}}
	store := &fakePromptStore{templates: []promptstore.PromptTemplate{
		{ID: 1, PromptKey: "main/db-global", Title: "DB Global", AgentKey: "main", Tags: mustJSONTags("scope.global"), Enabled: true},
		{ID: 2, PromptKey: "main/db-other", Title: "DB Other", AgentKey: "main", Tags: mustJSONTags("scope.cwd:/repo/b"), Enabled: true},
		{ID: 3, PromptKey: "main/db-legacy", Title: "DB Legacy", AgentKey: "main", Tags: mustJSONTags("intent:expert"), Enabled: true},
		{ID: 4, PromptKey: "main/db-disabled", Title: "DB Disabled", AgentKey: "main", Tags: mustJSONTags("scope.global"), Enabled: false},
	}}

	catalog := newRuntimeCatalogForStore(store, builtin)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{AgentKey: "main", CWD: "/repo/a"})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	for _, want := range []string{"main/builtin-global", "main/db-global", "main/db-legacy"} {
		if !runtimeCatalogHasPromptKey(templates, want) {
			t.Fatalf("ListTemplates() = %#v, want %q visible", templates, want)
		}
	}
	for _, absent := range []string{"main/db-other", "main/db-disabled"} {
		if runtimeCatalogHasPromptKey(templates, absent) {
			t.Fatalf("ListTemplates() = %#v, want %q hidden", templates, absent)
		}
	}
}

func TestRuntimeCatalogListTemplatesWithEmptyCWDHidesProjectScopedRows(t *testing.T) {
	t.Parallel()

	store := &fakePromptStore{templates: []promptstore.PromptTemplate{
		{ID: 1, PromptKey: "main/db-global", Title: "DB Global", AgentKey: "main", Tags: mustJSONTags("scope.global"), Enabled: true},
		{ID: 2, PromptKey: "main/db-project", Title: "DB Project", AgentKey: "main", Tags: mustJSONTags("scope.cwd:/repo/a"), Enabled: true},
		{ID: 3, PromptKey: "main/db-legacy", Title: "DB Legacy", AgentKey: "main", Tags: mustJSONTags("intent:expert"), Enabled: true},
	}}

	catalog := newRuntimeCatalogForStore(store, nil)
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{AgentKey: "main"})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	if len(store.listFilters) != 0 {
		t.Fatalf("store List() filters = %#v, want no DB read without trusted cwd", store.listFilters)
	}
	if len(templates) != 0 {
		t.Fatalf("ListTemplates() = %#v, want no DB rows without trusted cwd", templates)
	}
}

func TestRuntimeCatalogListRecallSectionsMergesBuiltinAndDBAndAllowsScopedPreference(t *testing.T) {
	t.Parallel()

	builtin := &fakeBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:          -21,
				PromptKey:   "main/knowledge/builtin-sqlc",
				Title:       "Builtin SQLC",
				AgentKey:    "main",
				Description: "Builtin SQLC global guidance.",
				Tags:        []string{"intent:recall"},
				Enabled:     true,
				Scope:       "global",
			},
			{
				ID:          -22,
				PromptKey:   "main/knowledge/builtin-only",
				Title:       "Builtin Only",
				AgentKey:    "main",
				Description: "Builtin only guidance.",
				Tags:        []string{"intent:recall"},
				Enabled:     true,
				Scope:       "global",
			},
		},
		sectionsByTemplateID: map[int64][]contract.BuiltinPromptSection{
			-21: {{ID: -201, TemplateID: -21, SectionKey: "recall_sqlc", TriggerType: "recall", RecallTopic: "sqlc-workflow", Enabled: true}},
			-22: {{ID: -202, TemplateID: -22, SectionKey: "recall_only", TriggerType: "recall", RecallTopic: "builtin-only", Enabled: true}},
		},
	}
	store := &fakePromptStore{
		recallSections: []promptstore.PromptTemplateSection{
			{
				ID:                  8,
				TemplateID:          5,
				TemplatePromptKey:   "main/knowledge/project-sqlc",
				TemplateTitle:       "Project SQLC",
				TemplateDescription: "Project SQLC scoped guidance.",
				TemplateTags:        mustJSONTags("intent:recall", "scope.cwd:/repo/a"),
				TriggerType:         "recall",
				RecallTopic:         "sqlc-workflow",
				Enabled:             true,
			},
		},
	}
	catalog := newRuntimeCatalogForStore(store, builtin)

	sections, err := catalog.ListRecallSections(context.Background(), "/repo/a")
	if err != nil {
		t.Fatalf("ListRecallSections() error = %v", err)
	}
	if got := runtimeCatalogRecallTopicCount(sections, "sqlc-workflow"); got != 1 {
		t.Fatalf("ListRecallSections() sqlc topic count = %d, want scoped DB over global builtin", got)
	}
	if got := runtimeCatalogRecallTopicCount(sections, "builtin-only"); got != 1 {
		t.Fatalf("ListRecallSections() builtin-only topic count = %d, want builtin-only section", got)
	}
	rendered := renderRecallCatalog(sections)
	if !strings.Contains(rendered, "Project SQLC scoped guidance.") || strings.Contains(rendered, "Builtin SQLC global guidance.") {
		t.Fatalf("renderRecallCatalog() = %q, want scoped DB topic over global builtin", rendered)
	}
}

func TestRuntimeCatalogListDefaultRuleSectionsPrefersProjectOverGlobal(t *testing.T) {
	t.Parallel()

	builtin := &fakeBuiltinPromptRegistry{
		templates: []contract.BuiltinPromptTemplate{
			{
				ID:        -31,
				PromptKey: "main/rules/global",
				Title:     "Daily Rule",
				AgentKey:  "default_rule",
				Tags:      []string{"intent:default_rule"},
				Enabled:   true,
				Scope:     "global",
			},
		},
		sectionsByTemplateID: map[int64][]contract.BuiltinPromptSection{
			-31: {{ID: -301, TemplateID: -31, SectionKey: "daily", TriggerType: "always", Body: "global daily rule", Enabled: true}},
		},
	}
	store := &fakePromptStore{
		defaultRuleSections: []promptstore.PromptTemplateSection{
			{
				ID:                13,
				TemplateID:        12,
				TemplatePromptKey: "main/rules/project",
				TemplateTitle:     "Daily Rule",
				TemplateTags:      mustJSONTags("intent:default_rule", "scope.cwd:/repo/a"),
				SectionKey:        "daily",
				TriggerType:       "always",
				Body:              "project daily rule",
				Enabled:           true,
			},
		},
	}

	catalog := newRuntimeCatalogForStore(store, builtin)
	sections, err := catalog.ListDefaultRuleSections(context.Background(), "/repo/a")
	if err != nil {
		t.Fatalf("ListDefaultRuleSections() error = %v", err)
	}
	if len(sections) != 1 || sections[0].Body != "project daily rule" {
		t.Fatalf("ListDefaultRuleSections() = %#v, want scoped project rule only", sections)
	}
}

func TestRuntimeCatalogInsertVersionNilStoreReturnsErrorAndStoreDelegates(t *testing.T) {
	t.Parallel()

	nilCatalog := newRuntimeCatalogForStore(nil, nil)
	if nilCatalog != nil {
		t.Fatalf("newRuntimeCatalogForStore(nil, nil) = %#v, want nil", nilCatalog)
	}
	nilCatalog = newRuntimeCatalogForStore(nil, builtinReviewRegistryForTest())
	if _, err := nilCatalog.InsertVersion(context.Background(), PromptTemplateVersion{PromptKey: "main/test"}); err == nil {
		t.Fatal("InsertVersion() with nil store error = nil, want error")
	}

	store := &fakePromptStore{insertVersionID: 91}
	catalog := newRuntimeCatalogForStore(store, nil)
	id, err := catalog.InsertVersion(context.Background(), PromptTemplateVersion{PromptKey: "main/test"})
	if err != nil {
		t.Fatalf("InsertVersion() error = %v", err)
	}
	if id != 91 || len(store.insertVersions) != 1 || store.insertVersions[0].PromptKey != "main/test" {
		t.Fatalf("InsertVersion() = (%d, %#v), want delegated insert", id, store.insertVersions)
	}
}

type fakeBuiltinPromptRegistry struct {
	templates            []contract.BuiltinPromptTemplate
	sectionsByTemplateID map[int64][]contract.BuiltinPromptSection
}

func (r *fakeBuiltinPromptRegistry) ListTemplates() []contract.BuiltinPromptTemplate {
	out := make([]contract.BuiltinPromptTemplate, len(r.templates))
	copy(out, r.templates)
	return out
}

func (r *fakeBuiltinPromptRegistry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	for _, template := range r.templates {
		if template.PromptKey == promptKey {
			return template, true
		}
	}
	return contract.BuiltinPromptTemplate{}, false
}

func (r *fakeBuiltinPromptRegistry) SectionsByTemplateID(templateID int64) []contract.BuiltinPromptSection {
	sections := r.sectionsByTemplateID[templateID]
	out := make([]contract.BuiltinPromptSection, len(sections))
	copy(out, sections)
	return out
}

func runtimeCatalogHasPromptKey(templates []PromptTemplate, promptKey string) bool {
	return runtimeCatalogTemplateByKey(templates, promptKey) != nil
}

func requireRuntimeCatalogList(t *testing.T, catalog RuntimePromptCatalog) []PromptTemplate {
	t.Helper()
	templates, err := catalog.ListTemplates(context.Background(), RuntimeListFilter{AgentKey: "main", CWD: "/repo/a"})
	if err != nil {
		t.Fatalf("ListTemplates() error = %v", err)
	}
	return templates
}

func requireRuntimeCatalogGet(t *testing.T, catalog RuntimePromptCatalog, promptKey string) *PromptTemplate {
	t.Helper()
	template, err := catalog.GetTemplate(context.Background(), promptKey, "/repo/a")
	if err != nil {
		t.Fatalf("GetTemplate() error = %v", err)
	}
	return template
}

func requireRuntimePromptKeyCount(t *testing.T, templates []PromptTemplate, promptKey string, want int) {
	t.Helper()
	if got := runtimeCatalogPromptKeyCount(templates, promptKey); got != want {
		t.Fatalf("ListTemplates() prompt key count = %d, want %d", got, want)
	}
}

func requireRuntimeTemplateText(t *testing.T, templates []PromptTemplate, promptKey string, id int64, text string) {
	t.Helper()
	got := runtimeCatalogTemplateByKey(templates, promptKey)
	if got == nil || got.ID != id || got.PromptText != text {
		t.Fatalf("ListTemplates() winner = %#v, want id=%d text=%q", got, id, text)
	}
}

func requireRuntimeStoreTemplate(t *testing.T, got *PromptTemplate, id int64, title string) {
	t.Helper()
	if got.ID != id || got.Title != title {
		t.Fatalf("GetTemplate() = %#v, want id=%d title=%q", *got, id, title)
	}
}

func requireBuiltinReviewSection(t *testing.T, catalog RuntimePromptCatalog, templateID int64) {
	t.Helper()
	sections, err := catalog.ListSectionsByTemplateID(context.Background(), templateID)
	if err != nil {
		t.Fatalf("ListSectionsByTemplateID() error = %v", err)
	}
	requireBuiltinReviewSectionMetadata(t, sections)
}

func requireBuiltinReviewSectionMetadata(t *testing.T, sections []PromptTemplateSection) {
	t.Helper()
	if len(sections) != 1 || sections[0].Body != "builtin section" ||
		sections[0].TemplatePromptKey != runtimeCatalogReviewPromptKey ||
		sections[0].TemplateTitle != "Builtin Review" || sections[0].TemplateDescription != "Builtin review description." {
		t.Fatalf("builtin sections = %#v, want builtin section with template metadata", sections)
	}
}

func runtimeCatalogTemplateByKey(templates []PromptTemplate, promptKey string) *PromptTemplate {
	for i := range templates {
		if templates[i].PromptKey == promptKey {
			return &templates[i]
		}
	}
	return nil
}

func runtimeCatalogPromptKeyCount(templates []PromptTemplate, promptKey string) int {
	count := 0
	for _, template := range templates {
		if template.PromptKey == promptKey {
			count++
		}
	}
	return count
}

func runtimeCatalogRecallTopicCount(sections []PromptTemplateSection, topic string) int {
	count := 0
	for _, section := range sections {
		if section.RecallTopic == topic {
			count++
		}
	}
	return count
}

func runtimeCatalogStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
