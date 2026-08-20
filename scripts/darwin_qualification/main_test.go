package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestValidateSourceRevisionStrictCanonical(t *testing.T) {
	validCommit := "https://github.com/darlinghq/darling@fedcba9876543210fedcba9876543210fedcba98"
	validTag := "https://github.com/darlinghq/darling@refs/tags/v0.1.0"
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "full pinned commit", value: validCommit},
		{name: "immutable tag", value: validTag},
		{name: "internal space", value: "https://github.com/darlinghq/darling@refs/tags/v0 .1.0", wantErr: true},
		{name: "control character", value: "https://github.com/darlinghq/darling@refs/tags/v0.1.0\n", wantErr: true},
		{name: "path traversal", value: "https://github.com/darlinghq/darling@refs/tags/../main", wantErr: true},
		{name: "empty segment", value: "https://github.com/darlinghq/darling@refs/tags//v0.1.0", wantErr: true},
		{name: "latest alias", value: "https://github.com/darlinghq/darling@refs/tags/latest", wantErr: true},
		{name: "main alias", value: "https://github.com/darlinghq/darling@refs/tags/main", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSourceRevision(test.value)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateSourceRevision(%q) error = %v, wantErr=%v", test.value, err, test.wantErr)
			}
		})
	}
}

func TestParseOptionsRejectsRepeatedStringAliases(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "repository repo path and repo", args: []string{"--repo-path=/tmp/repo-a", "--repo=/tmp/repo-b"}},
		{name: "repository repo path and repository", args: []string{"--repo-path=/tmp/repo-a", "--repository=/tmp/repo-b"}},
		{name: "repository repo and repository", args: []string{"--repo=/tmp/repo-a", "--repository=/tmp/repo-b"}},
		{name: "node id", args: []string{"--node-id=node-a", "--node-id=node-b"}},
		{name: "probe identity", args: []string{"--probe-identity=probe-a", "--probe-identity=probe-b"}},
		{name: "probe boot id", args: []string{"--probe-boot-id=boot-a", "--probe-boot-id=boot-b"}},
		{name: "image digest", args: []string{"--image-digest=sha256:a", "--image-digest=sha256:b"}},
		{name: "source revision", args: []string{"--source-revision=revision-a", "--source-revision=revision-b"}},
		{name: "qualification suite aliases", args: []string{"--qualification-suite=suite-a", "--suite=suite-b"}},
		{name: "receipt path aliases", args: []string{"--receipt-path=/tmp/receipt-a", "--receipt=/tmp/receipt-b"}},
		{name: "go path aliases", args: []string{"--go-path=/tmp/go-a", "--go=/tmp/go-b"}},
		{name: "Darling path aliases", args: []string{"--darling-path=/tmp/darling-a", "--darling=/tmp/darling-b"}},
		{name: "file path aliases", args: []string{"--file-path=/tmp/file-a", "--file=/tmp/file-b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseOptions(test.args); err == nil {
				t.Fatalf("parseOptions(%v) accepted repeated string aliases", test.args)
			}
		})
	}
}

func TestParseOptionsRejectsRepeatedTimeout(t *testing.T) {
	receipt := filepath.Join(t.TempDir(), "receipt.json")
	args := validQualificationArgs(t.TempDir(), receipt, testExecutablePath(t), testExecutablePath(t), testExecutablePath(t))
	args = append(args, "--timeout-seconds=1", "--timeout-seconds=2")
	if _, err := parseOptions(args); err == nil {
		t.Fatalf("parseOptions accepted repeated --timeout-seconds")
	}
}

func TestVerifyMachOCanonicalOutputs(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		stderr  string
		wantErr bool
	}{
		{name: "GNU canonical", stdout: "Mach-O 64-bit x86_64 executable"},
		{name: "Apple canonical", stdout: "Mach-O 64-bit executable x86_64"},
		{name: "universal", stdout: "Mach-O 64-bit universal executable x86_64", wantErr: true},
		{name: "arm64", stdout: "Mach-O 64-bit executable arm64", wantErr: true},
		{name: "ELF", stdout: "ELF 64-bit LSB pie executable", wantErr: true},
		{name: "empty", wantErr: true},
		{name: "stderr", stdout: "Mach-O 64-bit x86_64 executable", stderr: "warning", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := writeFakeExecutable(t, fakeFileOutputScript(test.stdout, test.stderr))
			options := cliOptions{
				repository:     explicitString{value: t.TempDir(), seen: true},
				filePath:       explicitString{value: tool, seen: true},
				timeoutSeconds: 1,
			}
			err := verifyMachO(options, filepath.Join(options.repository.value, "hiddenexec.test"))
			if (err != nil) != test.wantErr {
				t.Fatalf("verifyMachO(%q) error = %v, wantErr=%v", test.stdout, err, test.wantErr)
			}
		})
	}
}

func TestVerifyTestListRequiresExactUniqueNames(t *testing.T) {
	requiredTests := []string{
		"TestDarwinIdentityUsesHighResolutionNativeStartToken",
		"TestDarwinProcessTreeRSSUsesPsClosure",
		"TestDarwinStartupPIDReuseDoesNotSignal",
		"TestDarwinStartupCaptureActionRaceRemainsZeroSignal",
		"TestProcessStartIdentityPredatesCurrentBoot",
		"TestProcessStartIdentityPredatesCurrentBootRejectsMalformedIdentity",
	}
	canonical := strings.Join(requiredTests, "\n") + "\n"
	missing := strings.Join(requiredTests[:len(requiredTests)-1], "\n") + "\n"
	duplicate := strings.Join(append([]string{requiredTests[0]}, requiredTests...), "\n") + "\n"
	cases := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{name: "canonical", output: canonical},
		{name: "missing", output: missing, wantErr: true},
		{name: "extra", output: canonical + "TestUnexpected\n", wantErr: true},
		{name: "duplicate", output: duplicate, wantErr: true},
		{name: "carriage return", output: strings.ReplaceAll(canonical, "\n", "\r\n"), wantErr: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := verifyTestList(test.output)
			if (err != nil) != test.wantErr {
				t.Fatalf("verifyTestList(%q) error = %v, wantErr=%v", test.output, err, test.wantErr)
			}
		})
	}
}

func TestWriteReceiptCanonicalCreateOnlyModeAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	receipt := testQualificationReceipt()
	if err := writeReceipt(path, receipt); err != nil {
		t.Fatalf("writeReceipt() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat receipt: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("receipt mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var decoded heteroci.CapabilityQualificationReceipt
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if !reflect.DeepEqual(decoded, receipt) || decoded.Digest != decoded.CanonicalDigest() {
		t.Fatalf("receipt round trip = %#v, want %#v with canonical digest", decoded, receipt)
	}
	if err := writeReceipt(path, receipt); err == nil {
		t.Fatalf("writeReceipt overwrote existing create-only receipt")
	}
	invalid := receipt
	invalid.Digest = "sha256:invalid"
	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := writeReceipt(invalidPath, invalid); err == nil {
		t.Fatalf("writeReceipt accepted invalid canonical digest")
	}
}

func TestRunQualificationFakeToolFailureDoesNotWriteReceipt(t *testing.T) {
	root := t.TempDir()
	receipt := filepath.Join(root, "qualification.json")
	goTool := writeFakeExecutable(t, "printf '%s' 'compile failure' >&2\nexit 7")
	otherTool := writeFakeExecutable(t, "exit 0")
	args := validQualificationArgs(filepath.Join(root, "repository"), receipt, goTool, otherTool, otherTool)
	if err := os.Mkdir(argsRepository(args), 0o700); err != nil {
		t.Fatalf("create fake repository: %v", err)
	}
	if err := runQualification(append(args, "--timeout-seconds=1")); err == nil {
		t.Fatalf("runQualification unexpectedly accepted fake compiler failure")
	}
	if _, err := os.Lstat(receipt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runQualification receipt state = %v, want absent", err)
	}
}

func testQualificationReceipt() heteroci.CapabilityQualificationReceipt {
	receipt := heteroci.CapabilityQualificationReceipt{
		SchemaVersion:        heteroci.CapabilityQualificationSchemaVersion,
		NodeID:               "node-a",
		Runtime:              heteroci.RuntimeDarlingCompatRuntime,
		ProbeIdentity:        "probe-a",
		ProbeBootID:          "boot-a",
		ImageDigest:          "sha256:" + strings.Repeat("a", 64),
		SourceRevision:       "https://github.com/darlinghq/darling@refs/tags/v0.1.0",
		QualificationSuite:   qualificationSuite,
		ImmutableIdentity:    "sha256:" + strings.Repeat("b", 64),
		CompletedAtUnixMilli: 1_700_000_000_000,
	}
	receipt.Digest = receipt.CanonicalDigest()
	return receipt
}

func validQualificationArgs(repository, receipt, goPath, darlingPath, filePath string) []string {
	return []string{
		"--repo-path=" + repository,
		"--node-id=node-a",
		"--probe-identity=probe-a",
		"--probe-boot-id=boot-a",
		"--image-digest=sha256:" + strings.Repeat("a", 64),
		"--source-revision=https://github.com/darlinghq/darling@refs/tags/v0.1.0",
		"--qualification-suite=" + qualificationSuite,
		"--receipt-path=" + receipt,
		"--go-path=" + goPath,
		"--darling-path=" + darlingPath,
		"--file-path=" + filePath,
	}
}

func testExecutablePath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	return path
}

func argsRepository(args []string) string {
	for _, arg := range args {
		if repository, ok := strings.CutPrefix(arg, "--repo-path="); ok {
			return repository
		}
	}
	return ""
}

func fakeFileOutputScript(stdout, stderr string) string {
	script := ""
	if stdout != "" {
		script += "printf '%s' '" + stdout + "'\n"
	}
	if stderr != "" {
		script += "printf '%s' '" + stderr + "' >&2\n"
	}
	return script
}

func writeFakeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	return path
}
