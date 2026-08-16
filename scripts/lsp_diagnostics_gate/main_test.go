package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

type fakeGoInvocation struct {
	Args       []string `json:"args"`
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	CGOEnabled string   `json:"cgo_enabled"`
	GOFlags    string   `json:"go_flags"`
}

func TestMain(m *testing.M) {
	if capture := os.Getenv("LSP_DIAGNOSTICS_GATE_FAKE_GO_CAPTURE"); capture != "" {
		invocation := fakeGoInvocation{
			Args: os.Args[1:], GOOS: os.Getenv("GOOS"), GOARCH: os.Getenv("GOARCH"),
			CGOEnabled: os.Getenv("CGO_ENABLED"), GOFlags: os.Getenv("GOFLAGS"),
		}
		data, err := json.Marshal(invocation)
		if err == nil {
			err = os.WriteFile(capture, append(data, '\n'), 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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

// TestDiagnosticsGatePlainTextProtocolContract 锁定 diagnostics gate 只消费严格的 mcp-lsp 纯文本行协议。
func TestDiagnosticsGatePlainTextProtocolContract(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		wantErrPart string
	}{
		{name: "producer consumer round trip", mode: "plain-success"},
		{name: "malformed escape fails fast", mode: "plain-malformed-escape", wantErrPart: "malformed"},
		{name: "raw carriage return fails fast", mode: "plain-raw-cr", wantErrPart: "malformed"},
		{name: "raw nul fails fast", mode: "plain-raw-nul", wantErrPart: "malformed"},
		{name: "unknown header field fails fast", mode: "plain-unknown-header", wantErrPart: "unknown"},
		{name: "missing header field fails fast", mode: "plain-missing-unit", wantErrPart: "missing"},
		{name: "missing diagnostic row field fails fast", mode: "plain-missing-row-field", wantErrPart: "missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, "a.go"), "package a\n")
			output := filepath.Join(root, "coverage.json")
			err := run(context.Background(), options{
				root: root, files: []string{"a.go"}, output: output,
				peer:    []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", tc.mode},
				timeout: 2 * time.Second,
			})
			if tc.wantErrPart == "" {
				if err != nil {
					t.Fatalf("plain-text producer/consumer round trip: %v", err)
				}
				if info, statErr := os.Stat(output); statErr != nil || info.Size() == 0 {
					t.Fatalf("coverage artifact after plain-text round trip: info=%v err=%v", info, statErr)
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantErrPart) {
				t.Fatalf("run() error = %v, want fail-fast error containing %q", err, tc.wantErrPart)
			}
			if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
				t.Fatalf("coverage artifact exists after malformed protocol: %v", statErr)
			}
		})
	}
}

func TestDiagnosticShardsKeepSmallSetsSingleAndSplitLargeSetsCompletely(t *testing.T) {
	small := []string{"a.go", "b.go"}
	if shards := diagnosticShards(small); len(shards) != 1 || strings.Join(shards[0], ",") != "a.go,b.go" {
		t.Fatalf("small shards = %#v, want one unchanged shard", shards)
	}
	large := make([]string, diagnosticsShardFileThreshold+1)
	for i := range large {
		large[i] = fmt.Sprintf("%04d.go", i)
	}
	shards := diagnosticShards(large)
	if len(shards) != 2 {
		t.Fatalf("large shards = %d, want 2", len(shards))
	}
	joined := append(append([]string(nil), shards[0]...), shards[1]...)
	if strings.Join(joined, ",") != strings.Join(large, ",") {
		t.Fatal("large shards lost or reordered diagnostics targets")
	}
}

func TestDiagnosticShardsSeparateLanguageServerFamilies(t *testing.T) {
	files := []string{"a.go", "b.ts", "c.json", "d.go", "e.jsx", "f.yaml"}
	shards := diagnosticShards(files)
	if len(shards) != 3 {
		t.Fatalf("language shards = %#v, want 3", shards)
	}
	want := []string{"a.go,d.go", "b.ts,e.jsx", "c.json,f.yaml"}
	for index := range want {
		if got := strings.Join(shards[index], ","); got != want[index] {
			t.Fatalf("language shard %d = %q, want %q", index, got, want[index])
		}
	}
}

func TestPeerEnvironmentPinsLanguageServerMemoryBudgets(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "99GiB")
	t.Setenv("GOMAXPROCS", "99")
	t.Setenv("NODE_OPTIONS", "--max-old-space-size=99999")
	env := map[string]string{}
	for _, entry := range peerEnvironment(t.TempDir()) {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	if env["GOMEMLIMIT"] != "3GiB" || env["GOMAXPROCS"] != "4" ||
		env["NODE_OPTIONS"] != "--max-old-space-size=1024" {
		t.Fatalf("peer resource environment = %#v", env)
	}
}

func TestDiagnosticShardErrorPreservesRealFailureAndNormalizesSiblingExit(t *testing.T) {
	realFailure := fmt.Errorf("diagnostics broken.go: invalid syntax")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := diagnosticShardError(ctx, fmt.Errorf("peer exited: signal killed")); got != context.Canceled {
		t.Fatalf("sibling exit = %v, want context canceled", got)
	}
	if got := diagnosticShardError(context.Background(), realFailure); got != realFailure {
		t.Fatalf("real failure = %v, want %v", got, realFailure)
	}
}

func TestDiagnosticsWithRetryRetriesOnlyExplicitRetryableErrors(t *testing.T) {
	t.Run("retryable then success", func(t *testing.T) {
		calls := 0
		err := diagnosticsWithRetry(context.Background(), 2, 0, func() error {
			calls++
			if calls == 1 {
				return &retryableDiagnosticsError{err: errors.New("temporary timeout")}
			}
			return nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("diagnosticsWithRetry() = (%v, calls=%d), want success after 2 calls", err, calls)
		}
	})

	t.Run("permanent error", func(t *testing.T) {
		calls := 0
		want := errors.New("diagnostic failure")
		err := diagnosticsWithRetry(context.Background(), 2, 0, func() error {
			calls++
			return want
		})
		if !errors.Is(err, want) || calls != 1 {
			t.Fatalf("diagnosticsWithRetry() = (%v, calls=%d), want permanent failure after 1 call", err, calls)
		}
	})
}

func TestDiagnosticsToolErrorRecognizesExplicitRetryableMarker(t *testing.T) {
	var result toolResult
	err := json.Unmarshal([]byte(`{"content":[{"text":"ERROR code=lsp_timeout retryable=1\nMESSAGE\ttimed out"}],"isError":true}`), &result)
	if err != nil {
		t.Fatal(err)
	}
	var retryable *retryableDiagnosticsError
	if err := diagnosticsToolError(result); !errors.As(err, &retryable) {
		t.Fatalf("diagnosticsToolError() = %v, want retryable diagnostics error", err)
	}
}

func TestDiagnosticTargetsAllUsesTrackedFilesActualAdaptersAndNoisePolicy(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.go"), "package a\n")
	writeTestFile(t, filepath.Join(root, "config.json"), "{}\n")
	writeTestFile(t, filepath.Join(root, "asset.bin"), "not source\n")
	writeTestFile(t, filepath.Join(root, "node_modules", "ignored.go"), "package ignored\n")
	writeTestFile(t, filepath.Join(root, "only_windows.go"), "//go:build windows\n\npackage a\n")
	writeTestFile(t, filepath.Join(root, "standalone_ignore.go"), "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-f", "a.go", "config.json", "asset.bin", "node_modules/ignored.go", "only_windows.go", "standalone_ignore.go")

	selection, err := diagnosticTargets(root, options{all: true})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := "a.go,config.json,standalone_ignore.go"
	wantSkipped := "only_windows.go"
	if runtime.GOOS == "windows" {
		wantFiles = "a.go,config.json,only_windows.go,standalone_ignore.go"
		wantSkipped = ""
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
	assertWindowsTargetCompileSelection(t, selection)
}

func assertWindowsTargetCompileSelection(t *testing.T, selection targetSelection) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if got := selection.targetCompiles; len(got) != 1 || got[0].File != "only_windows.go" || got[0].GOOS != "windows" {
		t.Fatalf("target compile selection = %#v", got)
	}
	if got := selection.targetCompiles[0]; len(got.BuildTags) != 0 || got.BuildTagRegistryVersion != "" {
		t.Fatalf("Windows target unexpectedly changed to tagged evidence = %#v", got)
	}
}

func TestDiagnosticTargetsIncludesOnlyCanonicalGoplsStandaloneIgnoreMain(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "standalone.go"), "//go:build ignore\n\npackage main\n\nfunc main() {}\n")

	selection, err := diagnosticTargets(root, options{files: []string{"standalone.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(selection.files, ","), "standalone.go"; got != want {
		t.Fatalf("diagnostics targets = %q, want %q", got, want)
	}
	if len(selection.skipped) != 0 {
		t.Fatalf("standalone diagnostics unexpectedly skipped targets = %#v", selection.skipped)
	}
}

func TestCollectCanonicalGoplsStandaloneIgnoreMainRunsDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "standalone.go"), "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	output := filepath.Join(root, "coverage.json")

	err := run(context.Background(), options{
		root: root, files: []string{"standalone.go"}, output: output,
		peer: []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", "polluted"},
	})
	if err == nil || !strings.Contains(err.Error(), "found 1 Error/Warning/Information/Hint diagnostics") {
		t.Fatalf("standalone diagnostics error = %v, want propagated diagnostics failure", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("coverage artifact exists after standalone diagnostics failure: %v", err)
	}
}

func TestCollectUnknownBuildConstraintFailsClosed(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tag_excluded.go"), "//go:build never_enabled_lsp_gate\n\npackage a\n")
	output := filepath.Join(root, "coverage.json")
	old := []byte("old-coverage\n")
	if err := os.WriteFile(output, old, 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), options{
		root: root, files: []string{"tag_excluded.go"}, output: output,
		peer: []string{os.Args[0], "-test.run=TestDiagnosticsFakePeer", "--", "success"},
	})
	if err == nil || !strings.Contains(err.Error(), "has no supported target platform") {
		t.Fatalf("run error = %v, want fail-closed target-platform error", err)
	}
	got, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(old) {
		t.Fatalf("artifact changed after failed target resolution: %q", got)
	}
}

func TestRegisteredBuildTagRegistryHasExactLegalTags(t *testing.T) {
	want := []string{"codex_smoketest", "e2e", "e2e_claude", "e2e_vision", "lsp_integration", "manual", "sqlite_stress"}
	if len(registeredBuildTagTargets) != len(want) {
		t.Fatalf("registered build tags = %#v, want %v", registeredBuildTagTargets, want)
	}
	for index, tag := range want {
		registered := registeredBuildTagTargets[index]
		if registered.version != buildTagTargetRegistryVersion || !reflect.DeepEqual(registered.tags, []string{tag}) {
			t.Fatalf("registered build tag at index %d = %#v, want version %q tag %q", index, registered, buildTagTargetRegistryVersion, tag)
		}
	}
}

func TestRegisteredBuildTagsMatchTrackedPositiveTagInventory(t *testing.T) {
	tests := []struct {
		tag       string
		fileCount int
	}{
		{tag: "codex_smoketest", fileCount: 1},
		{tag: "e2e", fileCount: 20},
		{tag: "e2e_claude", fileCount: 1},
		{tag: "e2e_vision", fileCount: 1},
		{tag: "lsp_integration", fileCount: 1},
		{tag: "manual", fileCount: 6},
		{tag: "sqlite_stress", fileCount: 1},
	}
	root := lspDiagnosticsGateRepoRoot(t)
	for _, tc := range tests {
		t.Run(tc.tag, func(t *testing.T) {
			command := exec.Command("git", "grep", "-l", "^//go:build "+tc.tag+"$", "--", "*.go")
			command.Dir = root
			output, err := command.Output()
			if err != nil {
				t.Fatalf("read tracked positive build-tag inventory for %q: %v", tc.tag, err)
			}
			files := strings.Fields(string(output))
			if len(files) != tc.fileCount {
				t.Fatalf("tracked positive build-tag inventory for %q = %v, want %d files", tc.tag, files, tc.fileCount)
			}
		})
	}
}

func TestRegisteredBuildTagsCompileWithTagsAndWriteVersionedEvidence(t *testing.T) {
	tags := []string{"codex_smoketest", "e2e", "e2e_claude", "e2e_vision", "lsp_integration", "manual", "sqlite_stress"}
	for _, tag := range tags {
		t.Run(tag, func(t *testing.T) {
			root := t.TempDir()
			file := "tagged_" + tag + "_test.go"
			writeTestFile(t, filepath.Join(root, file), "//go:build "+tag+"\n\npackage a\n")
			assertRegisteredBuildTagTarget(t, root, file, tag)

			capture := installFakeGo(t)
			output := filepath.Join(root, "coverage.json")
			if err := run(context.Background(), options{root: root, files: []string{file}, output: output, timeout: 2 * time.Second}); err != nil {
				t.Fatal(err)
			}
			assertBuildTagCompileInvocation(t, readFakeGoInvocation(t, capture), tag)
			assertBuildTagCoverage(t, readCoverageArtifact(t, output), file, tag)
		})
	}
}

func TestTargetCompileEnvironmentDisablesCGOForCrossTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows source is host-visible on Windows")
	}
	root := t.TempDir()
	t.Setenv("GOFLAGS", "-race")
	file := "only_windows.go"
	writeTestFile(t, filepath.Join(root, file), "//go:build windows\n\npackage a\n")
	capture := installFakeGo(t)
	output := filepath.Join(root, "coverage.json")
	if err := run(context.Background(), options{root: root, files: []string{file}, output: output, timeout: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	invocation := readFakeGoInvocation(t, capture)
	if invocation.GOOS != "windows" || invocation.GOARCH != "amd64" {
		t.Fatalf("cross-target compiler environment = GOOS=%s GOARCH=%s", invocation.GOOS, invocation.GOARCH)
	}
	if invocation.CGOEnabled != "0" {
		t.Fatalf("cross-target compiler CGO_ENABLED = %q, want 0", invocation.CGOEnabled)
	}
	if invocation.GOFlags != "" {
		t.Fatalf("cross-target compiler GOFLAGS = %q, want empty", invocation.GOFlags)
	}
}

func TestCgoOnlyHostExcludedTargetFailsFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows source is host-visible on Windows")
	}
	root := t.TempDir()
	file := "only_windows_cgo.go"
	writeTestFile(t, filepath.Join(root, file), "//go:build windows && cgo\n\npackage a\n")
	if _, err := diagnosticTargets(root, options{files: []string{file}}); err == nil {
		t.Fatal("cgo-only host-excluded target unexpectedly produced compile evidence")
	}
}

func assertRegisteredBuildTagTarget(t *testing.T, root string, file string, tag string) {
	t.Helper()
	selection, err := diagnosticTargets(root, options{files: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.files) != 0 || len(selection.targetCompiles) != 1 {
		t.Fatalf("tagged target selection = %#v", selection)
	}
	target := selection.targetCompiles[0]
	if target.GOOS != runtime.GOOS || target.GOARCH != runtime.GOARCH || strings.Join(target.BuildTags, ",") != tag ||
		target.BuildTagRegistryVersion != buildTagTargetRegistryVersion {
		t.Fatalf("tagged target = %#v, want tag %q", target, tag)
	}
}

func assertBuildTagCompileInvocation(t *testing.T, invocation fakeGoInvocation, tag string) {
	t.Helper()
	if got := strings.Join(invocation.Args, " "); !strings.Contains(got, "test -c -tags "+tag+" -o ") || !strings.HasSuffix(got, " .") {
		t.Fatalf("go compiler args = %q", got)
	}
	if invocation.GOOS != runtime.GOOS || invocation.GOARCH != runtime.GOARCH {
		t.Fatalf("go compiler environment = GOOS=%s GOARCH=%s", invocation.GOOS, invocation.GOARCH)
	}
}

func assertBuildTagCoverage(t *testing.T, coverage coverageArtifact, file string, tag string) {
	t.Helper()
	if coverage.Inspected != 1 || coverage.TrackedCandidates != 1 || coverage.SkippedCount != 1 || len(coverage.TargetCompiles) != 1 {
		t.Fatalf("tagged coverage counts = %#v", coverage)
	}
	evidence := coverage.TargetCompiles[0]
	if evidence.File != file || strings.Join(evidence.BuildTags, ",") != tag || evidence.BuildTagRegistryVersion != buildTagTargetRegistryVersion ||
		evidence.GOOS != runtime.GOOS || evidence.GOARCH != runtime.GOARCH {
		t.Fatalf("tagged coverage evidence = %#v, want file %q tag %q", evidence, file, tag)
	}
}

func installFakeGo(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	if err := os.Link(os.Args[0], filepath.Join(binDir, name)); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "go-invocation.json")
	t.Setenv("LSP_DIAGNOSTICS_GATE_FAKE_GO_CAPTURE", capture)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return capture
}

func readFakeGoInvocation(t *testing.T, path string) fakeGoInvocation {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var invocation fakeGoInvocation
	if err := json.Unmarshal(data, &invocation); err != nil {
		t.Fatal(err)
	}
	return invocation
}

func readCoverageArtifact(t *testing.T, path string) coverageArtifact {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var coverage coverageArtifact
	if err := json.Unmarshal(data, &coverage); err != nil {
		t.Fatal(err)
	}
	return coverage
}

func TestMatchingTargetBuildContextSupportsOtherUnix(t *testing.T) {
	root := t.TempDir()
	const name = "other_unix.go"
	writeTestFile(t, filepath.Join(root, name), "//go:build unix && !darwin && !linux\n\npackage a\n")

	target, err := matchingTargetBuildContext(root, name)
	if err != nil {
		t.Fatal(err)
	}
	if target == nil || target.GOOS != "freebsd" || target.GOARCH != "amd64" {
		t.Fatalf("other Unix target = %#v, want freebsd/amd64", target)
	}
}

func TestTargetCompileKeySeparatesBuildTagsAndRegistryVersion(t *testing.T) {
	plain := targetCompileTarget{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	tagged := plain
	tagged.BuildTags = []string{"e2e"}
	tagged.BuildTagRegistryVersion = buildTagTargetRegistryVersion
	if targetCompileKey("pkg", plain) == targetCompileKey("pkg", tagged) {
		t.Fatal("tagged and untagged target compile keys were deduplicated")
	}
	otherVersion := tagged
	otherVersion.BuildTagRegistryVersion = buildTagTargetRegistryVersion + "-other"
	if targetCompileKey("pkg", tagged) == targetCompileKey("pkg", otherVersion) {
		t.Fatal("different build-tag registry versions were deduplicated")
	}
}

func TestActualStagedWindowsGateFileGetsTargetCompileEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows source is host-visible on Windows")
	}
	root := lspDiagnosticsGateRepoRoot(t)
	target := stagedWindowsTargetCompile(t, root)
	coverage := runStagedWindowsTargetCompile(t, root, target)
	assertStagedWindowsTargetCompileCoverage(t, coverage, target)
}

func lspDiagnosticsGateRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func stagedWindowsTargetCompile(t *testing.T, root string) targetCompileTarget {
	t.Helper()
	selection, err := diagnosticTargets(root, options{files: []string{"internal/devtools/gate/executor_signal_windows.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.files) != 0 || len(selection.targetCompiles) != 1 {
		t.Fatalf("Windows target selection = %#v", selection)
	}
	target := selection.targetCompiles[0]
	if target.File != "internal/devtools/gate/executor_signal_windows.go" || target.GOOS != "windows" {
		t.Fatalf("Windows target compile = %#v", target)
	}
	if len(target.BuildTags) != 0 || target.BuildTagRegistryVersion != "" {
		t.Fatalf("Windows target unexpectedly carries build-tag evidence = %#v", target)
	}
	return target
}

func runStagedWindowsTargetCompile(t *testing.T, root string, target targetCompileTarget) coverageArtifact {
	t.Helper()
	output := filepath.Join(t.TempDir(), "coverage.json")
	if err := run(context.Background(), options{
		root: root, files: []string{target.File}, output: output, timeout: 2 * time.Minute,
	}); err != nil {
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
	return coverage
}

func assertStagedWindowsTargetCompileCoverage(t *testing.T, coverage coverageArtifact, target targetCompileTarget) {
	t.Helper()
	if coverage.Inspected != 1 || len(coverage.Files) != 0 || len(coverage.TargetCompiles) != 1 ||
		coverage.TargetCompiles[0].File != target.File || coverage.TrackedCandidates != 1 ||
		coverage.SkippedCount != 1 {
		t.Fatalf("Windows target coverage = %#v", coverage)
	}
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
		text, isError := diagnosticsFakeTextResult(mode, request, calls)
		result := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
}

func diagnosticsPlainTextFixture(mode string) (string, bool) {
	switch mode {
	case "plain-success":
		return "OK total=0 showing=0 truncated=0 unit=diagnostic\nMESSAGE\tno+diagnostics", true
	case "plain-malformed-escape":
		return "OK total=0 showing=0 truncated=0 unit=diagnostic\nMESSAGE\tbad\\q", true
	case "plain-raw-cr":
		return "OK total=0 showing=0 truncated=0 unit=diagnostic\nMESSAGE\tbad\rvalue", true
	case "plain-raw-nul":
		return "OK total=0 showing=0 truncated=0 unit=diagnostic\nMESSAGE\tbad\x00value", true
	case "plain-unknown-header":
		return "OK total=0 showing=0 truncated=0 unit=diagnostic extra=1\nMESSAGE\tno+diagnostics", true
	case "plain-missing-unit":
		return "OK total=0 showing=0 truncated=0\nMESSAGE\tno+diagnostics", true
	case "plain-missing-row-field":
		return "OK total=1 showing=1 truncated=0 unit=diagnostic\nROW\tfile=a.go\tseverity=Error", true
	default:
		return "", false
	}
}

func diagnosticsFakeTextResult(mode string, request map[string]any, calls int) (string, bool) {
	if text, ok := diagnosticsPlainTextFixture(mode); ok {
		return text, false
	}
	message := "no diagnostics"
	switch mode {
	case "no-package":
		message = "no package metadata for file"
	case "partial":
		message = "partial diagnostics response"
	case "polluted", "hint":
		severity, detail := "Error", "build constraints exclude all Go files in [aix,ppc64]"
		if mode == "hint" {
			severity, detail = "Hint", "modernize"
		}
		return diagnosticFakeRowText(message, severity, detail), false
	case "tool-error":
		return "ERROR code=tool_error retryable=0\n" + lineprotocol.TextRecord("MESSAGE", "fake diagnostics failure"), true
	}
	if mode == "single-peer" {
		params, _ := request["params"].(map[string]any)
		arguments, _ := params["arguments"].(map[string]any)
		if arguments["file_path"] == "b.go" && calls != 2 {
			message = "no package metadata: second target did not reuse peer"
		}
	}
	return lineprotocol.HeaderLine(0, 0, false, "diagnostic") + "\n" + lineprotocol.TextRecord("MESSAGE", message), false
}

func diagnosticFakeRowText(message, severity, detail string) string {
	row := lineprotocol.FieldsRecord("ROW",
		lineprotocol.Field{Key: "file", Value: "a.go"}, lineprotocol.Field{Key: "line", Value: "1"},
		lineprotocol.Field{Key: "col", Value: "1"}, lineprotocol.Field{Key: "severity", Value: severity},
		lineprotocol.Field{Key: "message", Value: detail})
	return lineprotocol.HeaderLine(1, 1, false, "diagnostic") + "\n" + lineprotocol.TextRecord("MESSAGE", message) + "\n" + row
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
