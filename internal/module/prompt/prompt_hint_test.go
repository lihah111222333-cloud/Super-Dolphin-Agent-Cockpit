package prompt

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/uipreference"
)

type stubPromptHintPrefs struct {
	values map[string]json.RawMessage
}

func (s *stubPromptHintPrefs) GetValue(_ context.Context, cwd, key string) (json.RawMessage, error) {
	if s == nil || s.values == nil {
		return nil, platformdb.ErrNotFound
	}
	raw, ok := s.values[cwd+"\x1f"+key]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return raw, nil
}

func (s *stubPromptHintPrefs) Upsert(context.Context, uipreference.UpsertParams) error {
	return nil
}

func (s *stubPromptHintPrefs) List(context.Context, string) ([]uipreference.UIPreference, error) {
	return nil, nil
}

type stubPromptHintSharedFiles struct {
	files map[string]sharedfilestore.SharedFile
}

func (s *stubPromptHintSharedFiles) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	if s == nil || s.files == nil {
		return nil, platformdb.ErrNotFound
	}
	file, ok := s.files[path]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return &file, nil
}

func (s *stubPromptHintSharedFiles) GetContent(ctx context.Context, path string) (string, error) {
	file, err := s.Get(ctx, path)
	if err != nil || file == nil {
		return "", err
	}
	return file.Content, nil
}

func (s *stubPromptHintSharedFiles) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

func TestAssembleStartIncludesPromptHintOverride(t *testing.T) {
	t.Parallel()

	const override = "OVERRIDE-HINT-MUST-REACH-LLM"
	raw, err := json.Marshal(override)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	prefs := &stubPromptHintPrefs{values: map[string]json.RawMessage{
		"/repo\x1f" + promptHintOverridePreferenceKey: raw,
	}}
	shared := &stubPromptHintSharedFiles{files: map[string]sharedfilestore.SharedFile{
		promptHintDefaultSharedFilePath: {Path: promptHintDefaultSharedFilePath, Content: "DEFAULT-HINT"},
	}}

	svc := NewService(&Config{}, nil, WithPromptHintSources(prefs, shared))
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Prompt:           "display",
		BaseInstructions: "legacy base",
		Provider:         "codex",
		CWD:              "/repo",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(assembly.BaseInstructions, override) {
		t.Fatalf("BaseInstructions missing override hint: %q", assembly.BaseInstructions)
	}
	if strings.Contains(assembly.BaseInstructions, "DEFAULT-HINT") {
		t.Fatalf("BaseInstructions unexpectedly used default when override present: %q", assembly.BaseInstructions)
	}
}

func TestAssembleStartFallsBackToDefaultPromptHint(t *testing.T) {
	t.Parallel()

	prefs := &stubPromptHintPrefs{values: map[string]json.RawMessage{}}
	shared := &stubPromptHintSharedFiles{files: map[string]sharedfilestore.SharedFile{
		promptHintDefaultSharedFilePath: {Path: promptHintDefaultSharedFilePath, Content: "DEFAULT-HINT-FALLBACK"},
	}}

	svc := NewService(&Config{}, nil, WithPromptHintSources(prefs, shared))
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		BaseInstructions: "legacy base",
		Provider:         "codex",
		CWD:              "/repo",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(assembly.BaseInstructions, "DEFAULT-HINT-FALLBACK") {
		t.Fatalf("BaseInstructions missing default fallback: %q", assembly.BaseInstructions)
	}
}

func TestAssembleStartWithoutHintSourcesDoesNotPanic(t *testing.T) {
	t.Parallel()

	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		BaseInstructions: "legacy base",
		Provider:         "codex",
		CWD:              "/repo",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if assembly.BaseInstructions == "" {
		t.Fatalf("BaseInstructions empty")
	}
}
