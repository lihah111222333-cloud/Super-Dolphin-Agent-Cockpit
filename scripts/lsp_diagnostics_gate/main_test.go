package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCollectDiagnosticsRejectsIncompleteAndKeepsArtifact(t *testing.T) {
	modes := []string{"timeout", "no-package", "partial", "polluted", "hint", "tool-error", "missing-target"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "a.go"), "package a\n")
			writeTestFile(t, filepath.Join(root, "b.go"), "package a\n")
			output := filepath.Join(root, "coverage.json")
			old := []byte("old-coverage\n")
			if err := os.WriteFile(output, old, 0o600); err != nil {
				t.Fatal(err)
			}

			err := run(context.Background(), options{
				root: root, files: []string{"a.go", "b.go"}, output: output,
				peer:    []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", mode},
				timeout: 150 * time.Millisecond,
			})
			if err == nil {
				t.Fatal("run() error = nil")
			}
			got, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != string(old) {
				t.Fatalf("artifact changed on failure: %q", got)
			}
		})
	}
}

func TestCollectDiagnosticsSuccessWritesNonEmptyCoverageWithOnePeer(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), "package a\n")
	writeTestFile(t, filepath.Join(root, "b.go"), "package a\n")
	output := filepath.Join(root, "coverage.json")
	err := run(context.Background(), options{
		root: root, files: []string{"b.go", "a.go"}, output: output,
		peer:    []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", "single-peer"},
		timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var coverage coverageArtifact
	if err := json.Unmarshal(data, &coverage); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(coverage.Files, ","), "a.go,b.go"; got != want {
		t.Fatalf("files = %q, want %q", got, want)
	}
	if coverage.Inspected != 2 || coverage.Diagnostics != 0 {
		t.Fatalf("coverage = %#v", coverage)
	}
	if coverage.TrackedCandidates != 2 || coverage.SkippedCount != 0 {
		t.Fatalf("coverage counts = %#v", coverage)
	}
}

func TestDiagnosticTargetsAllUsesTrackedFilesActualAdaptersAndNoisePolicy(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), "package a\n")
	writeTestFile(t, filepath.Join(root, "config.json"), "{}\n")
	writeTestFile(t, filepath.Join(root, "asset.bin"), "not source\n")
	writeTestFile(t, filepath.Join(root, "node_modules", "ignored.go"), "package ignored\n")
	writeTestFile(t, filepath.Join(root, "only_windows.go"), "//go:build windows\n\npackage a\n")
	writeTestFile(t, filepath.Join(root, "tag_excluded.go"), "//go:build never_enabled_lsp_gate\n\npackage a\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-f", "a.go", "config.json", "asset.bin", "node_modules/ignored.go", "only_windows.go", "tag_excluded.go")

	selection, err := diagnosticTargets(root, options{all: true})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := "a.go,config.json"
	wantSkipped := "only_windows.go,tag_excluded.go"
	if runtime.GOOS == "windows" {
		wantFiles = "a.go,config.json,only_windows.go"
		wantSkipped = "tag_excluded.go"
	}
	if got := strings.Join(selection.files, ","); got != wantFiles {
		t.Fatalf("targets = %q, want %q", got, wantFiles)
	}
	var skipped []string
	for _, item := range selection.skipped {
		if item.Reason != "host-build-constraints" {
			t.Fatalf("skip reason = %q", item.Reason)
		}
		skipped = append(skipped, item.File)
	}
	if got := strings.Join(skipped, ","); got != wantSkipped {
		t.Fatalf("skipped = %q, want %q", got, wantSkipped)
	}
	if selection.candidates != len(selection.files)+len(selection.skipped) {
		t.Fatalf("candidate accounting = %#v", selection)
	}
}

func TestCollectHostExcludedOnlyAuditsSkipWithoutArtifact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tag_excluded.go"), "//go:build never_enabled_lsp_gate\n\npackage a\n")
	output := filepath.Join(root, "coverage.json")
	stderr := captureCollectorStderr(t, func() error {
		return run(context.Background(), options{
			root: root, files: []string{"tag_excluded.go"}, output: output,
			peer: []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", "success"},
		})
	})
	if want := "lsp diagnostics skip: candidates=1 inspected=0 skipped=1 reason=host-build-constraints\n"; stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("coverage artifact exists after skip: %v", err)
	}
}

func captureCollectorStderr(t *testing.T, run func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()
	runErr := run()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatalf("run error = %v", runErr)
	}
	return string(data)
}

func TestCollectUnsupportedOnlyFailsAndKeepsArtifact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "asset.bin"), "not source\n")
	output := filepath.Join(root, "coverage.json")
	old := []byte("old-coverage\n")
	if err := os.WriteFile(output, old, 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), options{
		root: root, files: []string{"asset.bin"}, output: output,
		peer: []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", "success"},
	})
	if err == nil || !strings.Contains(err.Error(), "no diagnostic targets are supported") {
		t.Fatalf("unsupported-only error = %v", err)
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(old) {
		t.Fatalf("artifact changed on unsupported-only failure: %q", got)
	}
}

func TestDiagnosticTargetsRejectsRepositoryEscapeAndSymlink(t *testing.T) {
	root := t.TempDir()
	if _, err := diagnosticTargets(root, options{files: []string{"../outside.go"}}); err == nil || !strings.Contains(err.Error(), "escapes repository root") {
		t.Fatalf("repository escape error = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	writeTestFile(t, outside, "package outside\n")
	link := filepath.Join(root, "linked.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	selection, err := diagnosticTargets(root, options{files: []string{"linked.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if selection.candidates != 0 || len(selection.files) != 0 {
		t.Fatalf("symlink escaped diagnostics selection: %#v", selection)
	}
}

func TestDiagnosticsFakePeer(t *testing.T) {
	mode, ok := diagnosticsFakePeerMode()
	if !ok {
		return
	}
	serveDiagnosticsFakePeer(mode)
}

func diagnosticsFakePeerMode() (string, bool) {
	if len(os.Args) < 2 {
		return "", false
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return "", false
	}
	return os.Args[separator+1], true
}

func serveDiagnosticsFakePeer(mode string) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	calls := 0
	for {
		var request map[string]any
		if err := decoder.Decode(&request); err != nil {
			return
		}
		id, hasID := request["id"]
		if !hasID {
			continue
		}
		if request["method"] == "initialize" {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
			continue
		}
		calls++
		if mode == "timeout" {
			time.Sleep(time.Second)
			continue
		}
		if mode == "missing-target" && calls == 2 {
			return
		}
		payload := diagnosticsFakePayload(mode, request, calls)
		result := map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprint(payload["meta"])}}, "structuredContent": payload, "isError": mode == "tool-error"}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
}

func diagnosticsFakePayload(mode string, request map[string]any, calls int) map[string]any {
	payload := map[string]any{"success": true, "data": []any{}, "total": 0, "showing": 0, "meta": map[string]any{"message": "no diagnostics"}}
	switch mode {
	case "no-package":
		payload["meta"] = map[string]any{"message": "no package metadata for file"}
	case "partial":
		payload["meta"] = map[string]any{"message": "partial diagnostics response"}
	case "polluted":
		payload["data"] = []any{map[string]any{"file": "a.go", "cols": []string{"severity", "message"}, "rows": [][]any{{"Error", "build constraints exclude all Go files in [aix,ppc64]"}}}}
		payload["total"] = 1
	case "hint":
		payload["data"] = []any{map[string]any{"file": "a.go", "cols": []string{"severity", "message"}, "rows": [][]any{{"Hint", "modernize"}}}}
		payload["total"] = 1
	}
	if mode == "single-peer" {
		params, _ := request["params"].(map[string]any)
		arguments, _ := params["arguments"].(map[string]any)
		if arguments["file_path"] == "b.go" && calls != 2 {
			payload["meta"] = map[string]any{"message": "no package metadata: second target did not reuse peer"}
		}
	}
	return payload
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
