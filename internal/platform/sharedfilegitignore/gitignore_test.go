package sharedfilegitignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTempCWD(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestEnsure_CreatesGitIgnoreWhenMissing(t *testing.T) {
	cwd := setupTempCWD(t)
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if err != nil {
		t.Fatalf("ReadFile err = %v", err)
	}
	if !strings.Contains(string(got), ".agnet/shared/_internal/") {
		t.Fatalf(".gitignore = %q, want entry .agnet/shared/_internal/", got)
	}
	if !strings.Contains(string(got), gitignoreHeader) {
		t.Fatalf(".gitignore missing header line: %q", got)
	}
}

func TestEnsure_AppendsToExisting(t *testing.T) {
	cwd := setupTempCWD(t)
	pre := "node_modules/\nbuild/\n"
	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatalf("seed err = %v", err)
	}
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if !strings.Contains(string(got), "node_modules/") {
		t.Fatalf("original entry lost: %q", got)
	}
	if !strings.Contains(string(got), ".agnet/shared/_internal/") {
		t.Fatalf("new entry not appended: %q", got)
	}
}

func TestEnsure_IsIdempotent_OnRepeatedCalls(t *testing.T) {
	cwd := setupTempCWD(t)
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("first err = %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("second err = %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if string(first) != string(second) {
		t.Fatalf("second Ensure modified file:\n--first--\n%s\n--second--\n%s", first, second)
	}
}

func TestEnsure_IsIdempotent_SkipsAppendOnFreshProcessIfRulePresent(t *testing.T) {
	cwd := setupTempCWD(t)
	pre := "node_modules/\n.agnet/shared/_internal/\n"
	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatalf("seed err = %v", err)
	}
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if string(got) != pre {
		t.Fatalf("file mutated:\nwant=%q\ngot =%q", pre, got)
	}
	// Critically: header line must NOT be added when rule already present.
	if strings.Contains(string(got), gitignoreHeader) {
		t.Fatalf("header inserted despite existing rule: %q", got)
	}
}

func TestEnsure_RecognizesParentMatch(t *testing.T) {
	cwd := setupTempCWD(t)
	pre := ".agnet/shared/\n"
	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatalf("seed err = %v", err)
	}
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if string(got) != pre {
		t.Fatalf("parent-dir rule not recognized; got = %q", got)
	}
}

func TestEnsure_RecognizesLeadingSlash(t *testing.T) {
	cwd := setupTempCWD(t)
	pre := "/.agnet/shared/_internal/\n"
	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatalf("seed err = %v", err)
	}
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if string(got) != pre {
		t.Fatalf("leading-slash variant not recognized; got = %q", got)
	}
}

func TestEnsure_RecognizesNoTrailingSlash(t *testing.T) {
	cwd := setupTempCWD(t)
	pre := ".agnet/shared/_internal\n"
	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatalf("seed err = %v", err)
	}
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if string(got) != pre {
		t.Fatalf("no-trailing-slash variant not recognized; got = %q", got)
	}
}

func TestEnsure_HandlesEmptyCWD(t *testing.T) {
	if err := Ensure("", nil); err != nil {
		t.Fatalf("Ensure(empty) err = %v, want nil", err)
	}
}

func TestEnsure_AppendsNewlineWhenExistingFileHasNoTrailingNewline(t *testing.T) {
	cwd := setupTempCWD(t)
	pre := "node_modules/" // no trailing newline
	if err := os.WriteFile(filepath.Join(cwd, ".gitignore"), []byte(pre), 0o644); err != nil {
		t.Fatalf("seed err = %v", err)
	}
	if err := Ensure(cwd, nil); err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".gitignore"))
	if !strings.Contains(string(got), "node_modules/\n"+gitignoreHeader) {
		t.Fatalf("missing newline before header; got = %q", got)
	}
}
