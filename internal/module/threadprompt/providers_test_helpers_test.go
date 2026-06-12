package threadprompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func expertTemplate(promptKey string, priority int, whenToUse string) promptstore.PromptTemplate {
	return promptstore.PromptTemplate{
		PromptKey: promptKey,
		WhenToUse: whenToUse,
		Priority:  priority,
		Enabled:   true,
	}
}

type fakePromptStore struct {
	promptstore.Store
	templates                []promptstore.PromptTemplate
	getTemplates             map[string]promptstore.PromptTemplate
	getErr                   error
	listErr                  error
	listFilters              []promptstore.ListFilter
	sectionsByTemplateID     map[int64][]promptstore.PromptTemplateSection
	recallSections           []promptstore.PromptTemplateSection
	recallSectionsByCWD      map[string][]promptstore.PromptTemplateSection
	recallCWDs               []string
	recallErr                error
	defaultRuleSections      []promptstore.PromptTemplateSection
	defaultRuleSectionsByCWD map[string][]promptstore.PromptTemplateSection
	defaultRuleCWDs          []string
	defaultRuleErr           error
	insertVersions           []promptstore.PromptTemplateVersion
	insertVersionID          int64
	insertVersionErr         error
}

func (f *fakePromptStore) List(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]promptstore.PromptTemplate(nil), f.templates...), nil
}

func (f *fakePromptStore) Get(_ context.Context, promptKey string) (*promptstore.PromptTemplate, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getTemplates != nil {
		if template, ok := f.getTemplates[promptKey]; ok {
			copy := template
			return &copy, nil
		}
	}
	for i := len(f.templates) - 1; i >= 0; i-- {
		if f.templates[i].PromptKey == promptKey {
			copy := f.templates[i]
			return &copy, nil
		}
	}
	return nil, platformdb.ErrNotFound
}

func (f *fakePromptStore) InsertVersion(_ context.Context, version promptstore.PromptTemplateVersion) (int64, error) {
	if f.insertVersionErr != nil {
		return 0, f.insertVersionErr
	}
	f.insertVersions = append(f.insertVersions, version)
	if f.insertVersionID != 0 {
		return f.insertVersionID, nil
	}
	return int64(len(f.insertVersions)), nil
}

func (f *fakePromptStore) ListSectionsByTemplateID(_ context.Context, templateID int64) ([]promptstore.PromptTemplateSection, error) {
	return append([]promptstore.PromptTemplateSection(nil), f.sectionsByTemplateID[templateID]...), nil
}

func (f *fakePromptStore) ListSectionsByTemplateIDs(_ context.Context, templateIDs []int64) ([]promptstore.PromptTemplateSection, error) {
	return fakePromptSectionsByTemplateIDs(f.sectionsByTemplateID, templateIDs), nil
}

func (f *fakePromptStore) ListRecallSections(_ context.Context, cwd string) ([]promptstore.PromptTemplateSection, error) {
	f.recallCWDs = append(f.recallCWDs, cwd)
	if f.recallErr != nil {
		return nil, f.recallErr
	}
	if f.recallSectionsByCWD != nil {
		return append([]promptstore.PromptTemplateSection(nil), f.recallSectionsByCWD[cwd]...), nil
	}
	return append([]promptstore.PromptTemplateSection(nil), f.recallSections...), nil
}

func (f *fakePromptStore) ListDefaultRuleSections(_ context.Context, cwd string) ([]promptstore.PromptTemplateSection, error) {
	f.defaultRuleCWDs = append(f.defaultRuleCWDs, cwd)
	if f.defaultRuleErr != nil {
		return nil, f.defaultRuleErr
	}
	if f.defaultRuleSectionsByCWD != nil {
		return append([]promptstore.PromptTemplateSection(nil), f.defaultRuleSectionsByCWD[cwd]...), nil
	}
	return append([]promptstore.PromptTemplateSection(nil), f.defaultRuleSections...), nil
}

type capturingDynamicRegistrar struct {
	names []string
}

func (r *capturingDynamicRegistrar) RegisterDynamicProvider(provider contract.DynamicSectionProvider) error {
	r.names = append(r.names, provider.SectionName())
	return nil
}

func (r *capturingDynamicRegistrar) UnregisterDynamicProvider(name string) bool {
	return false
}

func requireContainsInOrder(t *testing.T, text string, values ...string) {
	t.Helper()
	lastIdx := -1
	for _, value := range values {
		idx := strings.Index(text, value)
		if idx < 0 || idx < lastIdx {
			t.Fatalf("text = %q, want values in order: %v", text, values)
		}
		lastIdx = idx
	}
}

func mustJSONTags(tags ...string) json.RawMessage {
	encoded, err := json.Marshal(tags)
	if err != nil {
		// archguard:ignore panic_count -- static string-slice test fixture must always marshal.
		panic(err)
	}
	return encoded
}
