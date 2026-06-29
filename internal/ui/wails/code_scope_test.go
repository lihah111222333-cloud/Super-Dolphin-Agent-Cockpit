package wails

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type stubProjectStateReader struct {
	state *contract.ProjectsSnapshot
	err   error
}

func (s stubProjectStateReader) GetProjects(context.Context) (*contract.ProjectsSnapshot, error) {
	return s.state, s.err
}

func TestRequestScopeRootsRejectsUnknownProject(t *testing.T) {
	root := t.TempDir()
	unknown := t.TempDir()

	_, err := requestScopeRoots(context.Background(), &config.Config{ProjectRoot: root}, nil, unknown, []string{unknown})
	if err == nil {
		t.Fatal("requestScopeRoots() error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "project root") {
		t.Fatalf("requestScopeRoots() error = %q", err)
	}
}

func TestRequestScopeRootsUsesRegisteredProjects(t *testing.T) {
	root := t.TempDir()
	known := t.TempDir()

	roots, err := requestScopeRoots(
		context.Background(),
		&config.Config{ProjectRoot: root},
		stubProjectStateReader{state: &contract.ProjectsSnapshot{Projects: []string{known}, Active: "."}},
		known,
		[]string{known},
	)
	if err != nil {
		t.Fatalf("requestScopeRoots() error = %v", err)
	}
	want, err := realPathForCheck(known)
	if err != nil {
		t.Fatalf("realPathForCheck() error = %v", err)
	}
	if len(roots) != 1 || roots[0] != want {
		t.Fatalf("requestScopeRoots() roots = %#v, want %#v", roots, []string{want})
	}
}

func TestResolveScopeRootsReturnsErrorWhenCatalogEmpty(t *testing.T) {
	_, err := resolveScopeRoots(".", []string{"."}, scopeCatalog{knownRoots: map[string]struct{}{}})
	if err == nil {
		t.Fatal("resolveScopeRoots() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "project root") {
		t.Fatalf("resolveScopeRoots() error = %q", err)
	}
}

func TestResolveScopeRootsRejectsInvalidExplicitProjectInsteadOfSkipping(t *testing.T) {
	known := t.TempDir()
	unknown := t.TempDir()
	knownReal, err := realPathForCheck(known)
	if err != nil {
		t.Fatalf("realPathForCheck() error = %v", err)
	}

	_, err = resolveScopeRoots("", []string{unknown, known}, scopeCatalog{knownRoots: map[string]struct{}{knownReal: {}}})
	if err == nil {
		t.Fatal("resolveScopeRoots() error = nil, want invalid explicit project rejection")
	}
	if !strings.Contains(err.Error(), "project root") {
		t.Fatalf("resolveScopeRoots() error = %q, want project root validation failure", err)
	}
}
