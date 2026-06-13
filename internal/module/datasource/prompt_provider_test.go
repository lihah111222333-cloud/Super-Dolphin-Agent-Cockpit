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
	for _, name := range []string{"zeta.txt", "alpha.pdf"} {
		if err := os.WriteFile(filepath.Join(uploadDir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

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
	if !strings.HasPrefix(*got, "## "+contract.DynamicSectionDatasource) {
		t.Fatalf("Resolve() prefix = %q, want datasource section header", firstDatasourcePromptLine(*got))
	}
	alphaIndex := strings.Index(*got, "- alpha.pdf")
	zetaIndex := strings.Index(*got, "- zeta.txt")
	if alphaIndex == -1 || zetaIndex == -1 {
		t.Fatalf("Resolve() missing datasource file names:\n%s", *got)
	}
	if alphaIndex > zetaIndex {
		t.Fatalf("Resolve() did not preserve sorted datasource names:\n%s", *got)
	}
	if strings.Contains(*got, project) {
		t.Fatalf("Resolve() leaked absolute project path:\n%s", *got)
	}
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
	if strings.Contains(*got, project) {
		t.Fatalf("Resolve() leaked absolute project path:\n%s", *got)
	}
}

func firstDatasourcePromptLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}
