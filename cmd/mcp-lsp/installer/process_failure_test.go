package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestProcessFailureErrorSummarizesAndUnwrapsWithoutSecrets(t *testing.T) {
	secretOutput := "secret-output-token"
	commandName, commandArgs := processFailureTestCommand(secretOutput)
	command := exec.Command(commandName, commandArgs...)
	output, commandErr := command.CombinedOutput()
	if commandErr == nil {
		t.Fatal("failing process returned nil error")
	}
	failure := newProcessFailureError(
		"primary-health-check",
		filepath.Join(t.TempDir(), "user-private", "gopls.exe"),
		commandErr,
		output,
		2,
		3,
	)

	var summary *ProcessFailureError
	if !errors.As(failure, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError", failure)
	}
	var exitErr *exec.ExitError
	if !errors.As(failure, &exitErr) {
		t.Fatalf("failure does not unwrap *exec.ExitError: %v", failure)
	}
	if !errors.Is(failure, commandErr) {
		t.Fatalf("failure does not preserve the original command error: %v", failure)
	}
	if !summary.ExitCodePresent || summary.ExitCode != 23 {
		t.Fatalf("exit summary = present:%t code:%d, want present:true code:23", summary.ExitCodePresent, summary.ExitCode)
	}
	wantDigest := sha256.Sum256(output)
	if summary.OutputBytes != len(output) || summary.OutputSHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("output summary = bytes:%d sha:%q, want bytes:%d sha:%q", summary.OutputBytes, summary.OutputSHA256, len(output), hex.EncodeToString(wantDigest[:]))
	}
	if got := failure.Error(); strings.Contains(got, secretOutput) || strings.Contains(got, "user-private") || strings.Contains(got, "gopls.exe") {
		t.Fatalf("safe error leaked output or path: %q", got)
	}
	receipt, err := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: failure.Error()})
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	if got := string(receipt); strings.Contains(got, secretOutput) || strings.Contains(got, "user-private") {
		t.Fatalf("receipt leaked process data: %q", got)
	}
}

func TestInstallerAndHealthFailuresUseSafeProcessSummary(t *testing.T) {
	secretOutput := "secret-output-token"
	secretArg := "--secret-arg=user-private-token"
	failingProcess := writeFailingProcess(t, secretOutput)
	userBinary := filepath.Join(t.TempDir(), "user-private", "gopls.exe")

	p := NewProvider()
	p.Register("secret", InstallerConfig{
		BinaryName:          userBinary,
		InstallCmd:          failingProcess,
		InstallArgs:         []string{secretArg},
		AllowInstallCommand: true,
	})
	_, installErr := p.EnsureInstalledDetailed(WithInstallCommandCapability(context.Background()), "secret")
	assertSafeFailure(t, installErr, secretOutput, secretArg, userBinary)

	checkArgs := []string{secretArg}
	primaryErr := validatePrimaryBinary(context.Background(), failingProcess, InstallerConfig{
		BinaryName:      userBinary,
		BinaryCheckArgs: checkArgs,
	})
	assertSafeFailure(t, primaryErr, secretOutput, secretArg, userBinary)

	requiredErr := validateRequiredBinaries(context.Background(), InstallerConfig{
		RequiredBinaries: []RequiredBinary{{
			Name:      userBinary,
			CheckArgs: checkArgs,
			PathResolver: func(context.Context) (string, error) {
				return failingProcess, nil
			},
		}},
	})
	assertSafeFailure(t, requiredErr, secretOutput, secretArg, userBinary)
}

func TestProcessFailureErrorPreservesDeadline(t *testing.T) {
	secretOutput := []byte("secret-output-token")
	failure := newProcessFailureError(
		"auto-install",
		"gopls",
		context.DeadlineExceeded,
		secretOutput,
		1,
		0,
	)
	if !errors.Is(failure, context.DeadlineExceeded) {
		t.Fatalf("failure = %v, want errors.Is(context.DeadlineExceeded)", failure)
	}
	if strings.Contains(failure.Error(), string(secretOutput)) {
		t.Fatalf("deadline failure leaked output: %q", failure.Error())
	}
}

func TestProcessFailureErrorClassifiesDotnetOutputWithoutLeakingIt(t *testing.T) {
	failure := newProcessFailureError("runtime-dependency-command", "dotnet-csharp-ls", errors.New("exit 1"), []byte("error NU1101: Unable to find package csharp-ls at C:\\private\\source"), 11, 0)
	var summary *ProcessFailureError
	if !errors.As(failure, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError", failure)
	}
	if summary.OutputClass != "NU1101" {
		t.Fatalf("output class=%q, want NU1101", summary.OutputClass)
	}
	if strings.Contains(failure.Error(), "private\\source") || strings.Contains(failure.Error(), "Unable to find") {
		t.Fatalf("classified failure leaked raw output: %q", failure.Error())
	}
}

func TestProcessFailureErrorDoesNotClassifyNPMOutputAsNuGet(t *testing.T) {
	failure := newProcessFailureError(
		"windows-node-npm-install",
		"npm",
		errors.New("exit 1"),
		[]byte("npm error package lifecycle command failed"),
		6,
		1,
	)
	var summary *ProcessFailureError
	if !errors.As(failure, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError", failure)
	}
	if summary.OutputClass != "npm_install_failed" {
		t.Fatalf("npm output class=%q, want npm_install_failed", summary.OutputClass)
	}
}

// TestProcessFailureErrorFieldCoverageGuard 动态枚举安全进程摘要的公共字段，并验证每个字段都进入
// 低敏 Error 消费端；新增或陈旧字段会直接阻断，避免收据只更新一半。
func TestProcessFailureErrorFieldCoverageGuard(t *testing.T) {
	summary := &ProcessFailureError{
		Operation:       "fieldguard-operation",
		LogicalName:     "fieldguard-process",
		ArgsCount:       17,
		PackageCount:    19,
		ExitCodePresent: true,
		ExitCode:        23,
		OutputBytes:     29,
		OutputSHA256:    "fieldguard-sha256",
		OutputClass:     "tool_error",
		OutputSummary:   "line=1;bytes=1;sha256=fieldguard-line;signals=tool_error",
	}
	consumerTokens := map[string]string{
		"Operation":       "fieldguard-operation failed",
		"LogicalName":     "logical_name=fieldguard-process",
		"ArgsCount":       "args_count=17",
		"PackageCount":    "package_count=19",
		"ExitCodePresent": "exit_code_present=true",
		"ExitCode":        "exit_code=23",
		"OutputBytes":     "output_bytes=29",
		"OutputSHA256":    "output_sha256=fieldguard-sha256",
		"OutputClass":     "output_class=tool_error",
		"OutputSummary":   "output_summary=line=1;bytes=1;sha256=fieldguard-line;signals=tool_error",
	}

	typeOfSummary := reflect.TypeOf(ProcessFailureError{})
	producerFields := make(map[string]struct{}, typeOfSummary.NumField())
	for index := 0; index < typeOfSummary.NumField(); index++ {
		field := typeOfSummary.Field(index)
		if field.IsExported() {
			producerFields[field.Name] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for field := range producerFields {
		if _, ok := consumerTokens[field]; !ok {
			missing = append(missing, field)
		}
	}
	stale := make([]string, 0)
	encoded := summary.Error()
	for field, token := range consumerTokens {
		if _, ok := producerFields[field]; !ok {
			stale = append(stale, field)
			continue
		}
		if !strings.Contains(encoded, token) {
			t.Errorf("ProcessFailureError field %s is not represented by safe token %q in %q", field, token, encoded)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("ProcessFailureError field coverage drift: missing=%v stale=%v", missing, stale)
	}
}

func TestInstallerProcessFailurePreservesDeadline(t *testing.T) {
	secretOutput := "secret-timeout-output-token"
	failingProcess := writeFailingProcess(t, secretOutput)
	userBinary := filepath.Join(t.TempDir(), "user-private", "timeout-lsp.exe")
	p := NewProvider()
	p.Register("timeout", InstallerConfig{
		BinaryName:          userBinary,
		InstallCmd:          failingProcess,
		InstallArgs:         []string{"--secret-timeout-arg"},
		AllowInstallCommand: true,
	})
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := p.EnsureInstalledDetailed(WithInstallCommandCapability(ctx), "timeout")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("installer timeout error = %v, want errors.Is(context.DeadlineExceeded)", err)
	}
	if strings.Contains(err.Error(), secretOutput) || strings.Contains(err.Error(), userBinary) {
		t.Fatalf("installer timeout error leaked process data: %q", err)
	}
}

func assertSafeFailure(t *testing.T, err error, secretOutput, secretArg, userBinary string) {
	t.Helper()
	if err == nil {
		t.Fatal("failure = nil")
	}
	var summary *ProcessFailureError
	if !errors.As(err, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError: %v", err, err)
	}
	if !summary.ExitCodePresent || summary.ExitCode != 23 || summary.OutputBytes == 0 || summary.OutputSHA256 == "" {
		t.Fatalf("unsafe or incomplete process summary: %+v", summary)
	}
	serialized := err.Error()
	if strings.Contains(serialized, secretOutput) || strings.Contains(serialized, secretArg) || strings.Contains(serialized, userBinary) {
		t.Fatalf("error leaked process data: %q", serialized)
	}
	receipt, marshalErr := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: serialized})
	if marshalErr != nil {
		t.Fatalf("marshal receipt: %v", marshalErr)
	}
	if got := string(receipt); strings.Contains(got, secretOutput) || strings.Contains(got, secretArg) || strings.Contains(got, userBinary) {
		t.Fatalf("receipt leaked process data: %q", got)
	}
}
