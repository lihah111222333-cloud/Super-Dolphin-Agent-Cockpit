package datasource

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestPromptProviderRendersUploadedDatasourceFiles(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	writeDatasourceUploads(t, uploadDir, "zeta.txt", "alpha.pdf")

	provider := NewPromptProvider(svc)
	got, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: project},
		Start:    &contract.StartInput{CWD: project},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got == nil {
		t.Fatal("Resolve() = nil, want datasource prompt text")
	}
	assertDatasourcePrompt(t, *got, project)
}

func TestPromptProviderReturnsNilWithoutDatasourceFiles(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)

	provider := NewPromptProvider(svc)
	got, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: project},
		Start:    &contract.StartInput{CWD: project},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != nil {
		t.Fatalf("Resolve() = %q, want nil for empty datasource list", *got)
	}
}

func TestPromptProviderRendersPersistedDatasourceText(t *testing.T) {
	project := t.TempDir()
	store := &recordingDatasourceStore{
		documents: []DatasourceDocument{
			{
				WorkspaceRoot: project,
				Name:          "notes.txt",
				Extension:     ".txt",
				Content:       "first line\nsecond line",
			},
		},
	}
	provider := NewPromptProvider(NewServiceWithStore(store))

	got, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: project},
		Start:    &contract.StartInput{CWD: project},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got == nil {
		t.Fatal("Resolve() = nil, want datasource prompt text")
	}
	if !strings.Contains(*got, "### notes.txt") || !strings.Contains(*got, "first line\nsecond line") {
		t.Fatalf("Resolve() missing persisted datasource content:\n%s", *got)
	}
	if store.listPromptCalls != 1 {
		t.Fatalf("ListPromptDocuments() calls = %d, want 1", store.listPromptCalls)
	}
	if strings.Contains(*got, project) {
		t.Fatalf("Resolve() leaked absolute project path:\n%s", *got)
	}
}

// TestDatasourcePromptRejectsTooManyWorkspaceDocuments 固定 workspace 文档数量超限时阻断 prompt。
func TestDatasourcePromptRejectsTooManyWorkspaceDocuments(t *testing.T) {
	project := t.TempDir()
	store := &recordingDatasourceStore{
		documents: make([]DatasourceDocument, 0, 21),
	}
	for i := range 21 {
		store.documents = append(store.documents, DatasourceDocument{
			WorkspaceRoot: project,
			Name:          "doc-" + string(rune('a'+i)) + ".txt",
			Extension:     ".txt",
			Content:       strings.Repeat("x", 4096),
		})
	}
	provider := NewPromptProvider(NewServiceWithStore(store))

	got, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: project},
		Start:    &contract.StartInput{CWD: project},
	})

	if err == nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() error = %v, want critical prompt section error", err)
	}
	if got != nil {
		t.Fatalf("Resolve() text = %q, want nil on too many datasource documents", *got)
	}
	if store.listPromptCalls != 1 {
		t.Fatalf("ListPromptDocuments() calls = %d, want 1", store.listPromptCalls)
	}
}

// TestDatasourcePromptRejectsOversizedWorkspaceDocuments 固定 workspace 文档总字节超限时阻断 prompt。
func TestDatasourcePromptRejectsOversizedWorkspaceDocuments(t *testing.T) {
	project := t.TempDir()
	store := &recordingDatasourceStore{
		documents: make([]DatasourceDocument, 0, 17),
	}
	for i := range 17 {
		store.documents = append(store.documents, DatasourceDocument{
			WorkspaceRoot: project,
			Name:          "doc-" + string(rune('a'+i)) + ".txt",
			Extension:     ".txt",
			Content:       strings.Repeat("x", datasourcePromptMaxDocumentBytes),
		})
	}
	provider := NewPromptProvider(NewServiceWithStore(store))

	got, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: project},
		Start:    &contract.StartInput{CWD: project},
	})

	if err == nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() error = %v, want critical prompt section error", err)
	}
	if got != nil {
		t.Fatalf("Resolve() text = %q, want nil on oversized datasource workspace bytes", *got)
	}
	if store.listPromptCalls != 1 {
		t.Fatalf("ListPromptDocuments() calls = %d, want 1", store.listPromptCalls)
	}
}

// TestDatasourcePromptRejectsOversizedSingleDocument 固定单文档超限时阻断 prompt，禁止静默截断。
func TestDatasourcePromptRejectsOversizedSingleDocument(t *testing.T) {
	project := t.TempDir()
	store := &recordingDatasourceStore{
		documents: []DatasourceDocument{
			{
				WorkspaceRoot: project,
				Name:          "large.txt",
				Extension:     ".txt",
				Content:       strings.Repeat("a", datasourcePromptMaxDocumentBytes) + "TAIL_MARKER",
			},
		},
	}
	provider := NewPromptProvider(NewServiceWithStore(store))

	got, err := provider.Resolve(context.Background(), contract.SectionContext{
		BuildCtx: contract.BuildCtx{CWD: project},
		Start:    &contract.StartInput{CWD: project},
	})

	if err == nil || !contract.IsCriticalPromptSectionError(err) {
		t.Fatalf("Resolve() error = %v, want critical prompt section error", err)
	}
	if got != nil {
		t.Fatalf("Resolve() text = %q, want nil on oversized single datasource document", *got)
	}
	if store.listPromptCalls != 1 {
		t.Fatalf("ListPromptDocuments() calls = %d, want 1", store.listPromptCalls)
	}
}

func firstDatasourcePromptLine(text string) string {
	if line, _, ok := strings.Cut(text, "\n"); ok {
		return line
	}
	return text
}

func writeDatasourceUploads(t *testing.T, uploadDir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(uploadDir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func assertDatasourcePrompt(t *testing.T, prompt, project string) {
	t.Helper()
	if !strings.HasPrefix(prompt, "## "+contract.DynamicSectionDatasource) {
		t.Fatalf("Resolve() prefix = %q, want datasource section header", firstDatasourcePromptLine(prompt))
	}
	alphaIndex := strings.Index(prompt, "- alpha.pdf")
	zetaIndex := strings.Index(prompt, "- zeta.txt")
	if alphaIndex == -1 || zetaIndex == -1 {
		t.Fatalf("Resolve() missing datasource file names:\n%s", prompt)
	}
	if alphaIndex > zetaIndex {
		t.Fatalf("Resolve() did not preserve sorted datasource names:\n%s", prompt)
	}
	if strings.Contains(prompt, project) {
		t.Fatalf("Resolve() leaked absolute project path:\n%s", prompt)
	}
}
