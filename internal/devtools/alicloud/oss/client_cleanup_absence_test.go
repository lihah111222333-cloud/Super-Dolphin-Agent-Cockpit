package oss

import (
	"context"
	"strings"
	"testing"
)

func TestConfirmPrefixEmptyReturnsTrueForEmptyCount(t *testing.T) {
	assertConfirmPrefixEmpty(t, "Object Number is: 0\nTotal Object Size is: 0\n", true, "")
}

func TestConfirmPrefixEmptyReturnsFalseForResidualCount(t *testing.T) {
	assertConfirmPrefixEmpty(t, "Object Number is: 1\nTotal Object Size is: 12\n", false, "")
}

func TestConfirmPrefixEmptyRejectsMissingCount(t *testing.T) {
	assertConfirmPrefixEmpty(t, "Total Object Size is: 0\n", false, "missing a unique Object Number count")
}

func assertConfirmPrefixEmpty(t *testing.T, stdout string, want bool, wantError string) {
	t.Helper()
	runner := &fakeRunner{stdout: []byte(stdout)}
	client := newTestClient(t, runner)
	empty, err := client.ConfirmPrefixEmpty(context.Background(), "source-bundles/job-1/")
	if empty != want {
		t.Fatalf("ConfirmPrefixEmpty() empty=%t, want %t", empty, want)
	}
	if wantError == "" && err != nil {
		t.Fatalf("ConfirmPrefixEmpty() error = %v", err)
	}
	if wantError != "" && (err == nil || !strings.Contains(err.Error(), wantError)) {
		t.Fatalf("ConfirmPrefixEmpty() error = %v, want %q", err, wantError)
	}
	assertOSSListAbsenceCall(t, runner.calls)
}

func assertOSSListAbsenceCall(t *testing.T, calls []runCall) {
	t.Helper()
	if len(calls) != 1 || calls[0].args[0] != "oss" || calls[0].args[1] != "ls" {
		t.Fatalf("OSS list call = %#v", calls)
	}
	args := calls[0].args
	if strings.Contains(strings.Join(args, " "), "--recursive") {
		t.Fatalf("OSS list call must not use unsupported --recursive: %#v", args)
	}
	if len(args) < 5 || args[3] != "--limited-num" || args[4] != "1" {
		t.Fatalf("OSS list call must use a one-object bounded query: %#v", args)
	}
}
