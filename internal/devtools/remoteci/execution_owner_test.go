package remoteci

import (
	"context"
	"errors"
	"testing"
)

type executionOwnerRunner struct {
	calls  int
	result RunResult
	err    error
}

func (runner *executionOwnerRunner) RunPrepared(context.Context, *PreparedRun) (RunResult, error) {
	runner.calls++
	return runner.result, runner.err
}

func TestExecutionOwnerContract(t *testing.T) {
	t.Run("runs and finalizes exactly once", testExecutionOwnerRunsAndFinalizesExactlyOnce)
	t.Run("finalizer failure overrides run return", testExecutionOwnerFinalizerFailureOverridesRunReturn)
	t.Run("rejects missing dependencies", testExecutionOwnerRejectsMissingDependencies)
}

func testExecutionOwnerRunsAndFinalizesExactlyOnce(t *testing.T) {
	runErr := errors.New("run failed")
	runner := &executionOwnerRunner{result: RunResult{JobID: "job-owner"}, err: runErr}
	finalizeCalls := 0
	owner, err := newExecutionOwner(runner, func(input RunInput, result *RunResult, observed error) error {
		finalizeCalls++
		if input.Tree != "tree" || result.JobID != "job-owner" || !errors.Is(observed, runErr) {
			t.Fatalf("finalizer input=%#v result=%#v error=%v", input, result, observed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("newExecutionOwner() error = %v", err)
	}
	result, err := owner.ExecutePrepared(t.Context(), RunInput{Tree: "tree"}, &PreparedRun{})
	if !errors.Is(err, runErr) || result.JobID != "job-owner" {
		t.Fatalf("ExecutePrepared() result=%#v error=%v", result, err)
	}
	if runner.calls != 1 || finalizeCalls != 1 {
		t.Fatalf("runner calls=%d finalizer calls=%d, want one each", runner.calls, finalizeCalls)
	}
}

func testExecutionOwnerFinalizerFailureOverridesRunReturn(t *testing.T) {
	runErr := errors.New("run failed")
	finalizeErr := errors.New("finalize failed")
	owner, err := newExecutionOwner(&executionOwnerRunner{err: runErr}, func(RunInput, *RunResult, error) error { return finalizeErr })
	if err != nil {
		t.Fatalf("newExecutionOwner() error = %v", err)
	}
	if _, err := owner.ExecutePrepared(t.Context(), RunInput{}, &PreparedRun{}); !errors.Is(err, finalizeErr) || errors.Is(err, runErr) {
		t.Fatalf("ExecutePrepared() error = %v, want only finalizer failure", err)
	}
}

func testExecutionOwnerRejectsMissingDependencies(t *testing.T) {
	if _, err := NewExecutionOwner(nil, func(RunInput, *RunResult, error) error { return nil }); err == nil {
		t.Fatal("NewExecutionOwner() accepted nil coordinator")
	}
	if _, err := newExecutionOwner(&executionOwnerRunner{}, nil); err == nil {
		t.Fatal("newExecutionOwner() accepted nil finalizer")
	}
	var owner *ExecutionOwner
	if _, err := owner.ExecutePrepared(context.Background(), RunInput{}, &PreparedRun{}); err == nil {
		t.Fatal("nil ExecutionOwner accepted execution")
	}
}
