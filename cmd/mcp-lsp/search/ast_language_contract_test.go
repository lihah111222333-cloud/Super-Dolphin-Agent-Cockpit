package search

import (
	"strings"
	"testing"
)

func TestNormalizeASTLanguageExplicitValidationContract(t *testing.T) {
	t.Run("unknown explicit value does not infer", func(t *testing.T) {
		_, err := normalizeASTLanguage("brainfuck", "/tmp/query.go", false, "*.go")
		if err == nil || !strings.Contains(err.Error(), `unsupported ast_language "brainfuck"`) {
			t.Fatalf("unknown explicit ast_language error = %v, want unsupported value", err)
		}
	})
	t.Run("explicit value conflicts with clear glob", func(t *testing.T) {
		_, err := normalizeASTLanguage("go", "/tmp/project", true, "**/*.py")
		if err == nil || !strings.Contains(err.Error(), "ast_language") || !strings.Contains(err.Error(), "glob") {
			t.Fatalf("explicit ast_language/glob conflict error = %v", err)
		}
	})
	t.Run("unclear glob does not create conflict", func(t *testing.T) {
		got, err := normalizeASTLanguage("golang", "/tmp/project", true, "**/*")
		if err != nil {
			t.Fatalf("explicit ast_language with unclear glob error = %v", err)
		}
		if got != "go" {
			t.Fatalf("explicit ast_language with unclear glob = %q, want go", got)
		}
	})
}
