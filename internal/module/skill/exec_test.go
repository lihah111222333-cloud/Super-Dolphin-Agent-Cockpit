package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
)

func TestExecCommandRejectsShellMetacharacters(t *testing.T) {
	t.Parallel()

	svc := &service{}
	if _, err := svc.ExecCommand(context.Background(), "printf", []string{"a|b"}, ""); err == nil {
		t.Fatal("ExecCommand expected shell metacharacter validation error")
	}
}

func TestExecCommandFallsBackToProjectRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	svc := &service{projectRoot: root}
	out, err := svc.ExecCommand(context.Background(), "pwd", nil, "")
	if err != nil {
		t.Fatalf("ExecCommand returned error: %v", err)
	}
	if out.CWD != root {
		t.Fatalf("ExecCommand cwd mismatch: got %q want %q", out.CWD, root)
	}
	if got := strings.TrimSpace(out.Stdout); got != root {
		t.Fatalf("ExecCommand stdout mismatch: got %q want %q", got, root)
	}
}

func TestExecCommandInjectsWhitelistedEnv(t *testing.T) {
	t.Setenv("TEST_E2E_SKILL_ENV", "allowed")
	t.Setenv("UNRELATED_SKILL_ENV", "blocked")

	svc := &service{}
	allowed, err := svc.ExecCommand(context.Background(), "printenv", []string{"TEST_E2E_SKILL_ENV"}, "")
	if err != nil {
		t.Fatalf("ExecCommand allowed env returned error: %v", err)
	}
	if got := strings.TrimSpace(allowed.Stdout); got != "allowed" {
		t.Fatalf("allowed env mismatch: got %q", got)
	}
	blocked, err := svc.ExecCommand(context.Background(), "printenv", []string{"UNRELATED_SKILL_ENV"}, "")
	if err != nil {
		t.Fatalf("ExecCommand blocked env returned error: %v", err)
	}
	if blocked.ExitCode == 0 || strings.TrimSpace(blocked.Stdout) != "" {
		t.Fatalf("blocked env leaked: exit=%d stdout=%q", blocked.ExitCode, blocked.Stdout)
	}
}

func TestNewServiceConfiguresProjectRootAndHTTPTimeout(t *testing.T) {
	t.Parallel()

	impl, ok := NewService(nil, " /tmp/project ").(*service)
	if !ok {
		t.Fatal("NewService type assertion failed")
	}
	if impl.projectRoot != "/tmp/project" {
		t.Fatalf("projectRoot mismatch: got %q", impl.projectRoot)
	}
	if impl.http == nil || impl.http.Timeout != 15*time.Second {
		t.Fatalf("http timeout mismatch: %#v", impl.http)
	}
}

func TestRunCardAllowsShellSyntaxViaInternalShellPath(t *testing.T) {
	t.Parallel()

	card := commandcardstore.CommandCard{
		CardKey:         "demo",
		Title:           "demo",
		CommandTemplate: "printf foo | tr o a",
		ArgsSchema:      json.RawMessage("{}"),
		Enabled:         true,
	}
	svc := &service{cards: stubCardStore{card: card}}

	out, err := svc.RunCard(context.Background(), "demo", map[string]any{})
	if err != nil {
		t.Fatalf("RunCard returned error: %v", err)
	}
	if got := strings.TrimSpace(out.Exec.Stdout); got != "faa" {
		t.Fatalf("RunCard stdout mismatch: got %q", got)
	}
}

type stubCardStore struct {
	card commandcardstore.CommandCard
}

func (s stubCardStore) Get(context.Context, string) (*commandcardstore.CommandCard, error) {
	card := s.card
	return &card, nil
}

func (stubCardStore) Delete(context.Context, string) error { return nil }

func (stubCardStore) InsertVersion(context.Context, commandcardstore.CommandCardVersion) error {
	return nil
}

func (stubCardStore) ListVersions(context.Context, string) ([]commandcardstore.CommandCardVersion, error) {
	return nil, nil
}

func (s stubCardStore) Upsert(context.Context, commandcardstore.CommandCard) (*commandcardstore.CommandCard, error) {
	card := s.card
	return &card, nil
}

func (s stubCardStore) List(context.Context, commandcardstore.ListFilter) ([]commandcardstore.CommandCard, error) {
	return []commandcardstore.CommandCard{s.card}, nil
}
