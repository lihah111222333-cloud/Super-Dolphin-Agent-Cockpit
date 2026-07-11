package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
)

func TestReplaceRangeUnsupportedTextFilesE2EAppliesPatchForms(t *testing.T) {
	for _, tt := range unsupportedTextReplaceRangeCases() {
		t.Run(tt.name, func(t *testing.T) {
			runUnsupportedTextReplaceRangeCase(t, tt)
		})
	}
}

type unsupportedTextReplaceRangeCase struct {
	name    string
	file    string
	content string
	req     func(path string) EditRequest
	want    string
}

func unsupportedTextReplaceRangeCases() []unsupportedTextReplaceRangeCase {
	return []unsupportedTextReplaceRangeCase{
		markdownContextPatchCase(),
		plaintextMultiHunkPatchCase(),
		plaintextBareHeaderPatchCase(),
		jsonPatchCase(),
		yamlConfigPatchCase(),
	}
}

func markdownContextPatchCase() unsupportedTextReplaceRangeCase {
	return unsupportedTextReplaceRangeCase{
		name: "markdown context patch",
		file: "plan.md",
		content: strings.Join([]string{
			"# Plan",
			"",
			"## Other",
			"- status: pending",
			"",
			"## Target",
			"- status: pending",
			"",
		}, "\n"),
		req: func(path string) EditRequest {
			return EditRequest{
				Action:   "replace_range",
				FilePath: path,
				Patch: strings.Join([]string{
					"@@ target status",
					" ## Target",
					"-- status: pending",
					"+- status: done",
					"",
				}, "\n"),
			}
		},
		want: strings.Join([]string{
			"# Plan",
			"",
			"## Other",
			"- status: pending",
			"",
			"## Target",
			"- status: done",
			"",
		}, "\n"),
	}
}

func plaintextMultiHunkPatchCase() unsupportedTextReplaceRangeCase {
	return unsupportedTextReplaceRangeCase{
		name:    "plaintext multi-hunk patch",
		file:    "notes.txt",
		content: "alpha\nbeta\nomega\n",
		req: func(path string) EditRequest {
			return EditRequest{
				Action:   "replace_range",
				FilePath: path,
				Patch: strings.Join([]string{
					"@@ alpha",
					"-alpha",
					"+ALPHA",
					"@@ omega",
					"-omega",
					"+OMEGA",
					"",
				}, "\n"),
			}
		},
		want: "ALPHA\nbeta\nOMEGA\n",
	}
}

func plaintextBareHeaderPatchCase() unsupportedTextReplaceRangeCase {
	return unsupportedTextReplaceRangeCase{
		name:    "plaintext lenient bare header patch",
		file:    "bare-header.txt",
		content: "alpha\nbeta\nomega\n",
		req: func(path string) EditRequest {
			return EditRequest{
				Action:   "replace_range",
				FilePath: path,
				Patch: strings.Join([]string{
					"@@",
					"-beta",
					"+BETA",
					"",
				}, "\n"),
			}
		},
		want: "alpha\nBETA\nomega\n",
	}
}

func jsonPatchCase() unsupportedTextReplaceRangeCase {
	return unsupportedTextReplaceRangeCase{
		name:    "json patch",
		file:    "config.json",
		content: "{\n  \"enabled\": false,\n  \"name\": \"demo\"\n}\n",
		req: func(path string) EditRequest {
			return EditRequest{
				Action:   "replace_range",
				FilePath: path,
				Patch: strings.Join([]string{
					"@@ enabled",
					"-  \"enabled\": false,",
					"+  \"enabled\": true,",
					"",
				}, "\n"),
			}
		},
		want: "{\n  \"enabled\": true,\n  \"name\": \"demo\"\n}\n",
	}
}

func yamlConfigPatchCase() unsupportedTextReplaceRangeCase {
	return unsupportedTextReplaceRangeCase{
		name: "yaml config patch",
		file: "app.yaml",
		content: strings.Join([]string{
			"server:",
			"  host: localhost",
			"  port: 8080",
			"features:",
			"  dag: false",
			"",
		}, "\n"),
		req: func(path string) EditRequest {
			return EditRequest{
				Action:   "replace_range",
				FilePath: path,
				Patch: strings.Join([]string{
					"@@ yaml feature",
					" features:",
					"-  dag: false",
					"+  dag: true",
					"",
				}, "\n"),
			}
		},
		want: strings.Join([]string{
			"server:",
			"  host: localhost",
			"  port: 8080",
			"features:",
			"  dag: true",
			"",
		}, "\n"),
	}
}

func runUnsupportedTextReplaceRangeCase(t *testing.T, tt unsupportedTextReplaceRangeCase) {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, tt.file)
	if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	payload, err := json.Marshal(tt.req(path))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	handler := NewEditHandler(lspmanager.NewRegistry(nil))
	got, err := handler(testToolContext(root), payload)
	if err != nil {
		t.Fatalf("replace_range returned error: %v", err)
	}
	result, ok := got.(replaceRangeResult)
	if !ok {
		t.Fatalf("result type = %T, want replaceRangeResult", got)
	}
	assertUnsupportedTextReplaceRangeResult(t, result)
	assertUpdatedFileContent(t, path, tt.want)
}

func assertUnsupportedTextReplaceRangeResult(t *testing.T, result replaceRangeResult) {
	t.Helper()

	if result.Status != "applied" || !result.Persisted {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.LSPSync {
		t.Fatalf("LSPSync = true, want false for unsupported text file")
	}
	if !strings.Contains(result.Warning, lspmanager.ErrUnsupportedLanguage.Error()) {
		t.Fatalf("warning = %q, want unsupported language context", result.Warning)
	}
	if result.DiagnosticGeneration != 0 {
		t.Fatalf("DiagnosticGeneration = %d, want 0 without manager", result.DiagnosticGeneration)
	}
}

func assertUpdatedFileContent(t *testing.T, path, want string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if string(raw) != want {
		t.Fatalf("updated content mismatch:\nwant:\n%s\ngot:\n%s", want, string(raw))
	}
}
