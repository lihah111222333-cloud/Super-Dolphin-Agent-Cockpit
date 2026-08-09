package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFailedCompiledSelectorBatchPropagatesBatchErrorToObservedPass(t *testing.T) {
	workload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	group := CompileGroup{PackageTarget: "./sample", WorkloadIDs: []GateID{GateID(workload.ID)}}
	base := time.UnixMilli(8_750_000).UTC()
	observation := compiledSelectorBatchObservation{
		started: base, bodyStarted: base.Add(time.Millisecond), completed: base.Add(20 * time.Millisecond),
		log: newBoundedPlanLog(executorPlanMaxLogBytes), selectorLogs: map[string][]byte{},
		selectorTimings: map[string][]GoTestTiming{
			"TestCompileGroup": {{Name: "TestCompileGroup", Status: GoTestStatusPass, DurationMS: 18}},
		},
		selectorIntervals: map[string]compiledSelectorBatchInterval{
			"TestCompileGroup": {runAt: base.Add(2 * time.Millisecond), completedAt: base.Add(20 * time.Millisecond)},
		},
	}
	batchErr := errors.New("compiled selector batch process cleanup failed")
	results, err := failedCompiledSelectorBatchResults(group, []string{"go", "tool", "test2json"}, &observation, batchErr, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	result := results[group.WorkloadIDs[0]]
	if result.Status == ResultStatusPassed || result.ExitCode == 0 {
		t.Fatalf("batch error was projected as passing selector: %#v", result)
	}
	if !strings.Contains(string(result.Log), "batch-error kind=batch error-digest=") || strings.Contains(string(result.Log), batchErr.Error()) {
		t.Fatalf("batch error observability is not safely classified: %q", result.Log)
	}
}

func TestCompiledSelectorBatchErrorSummaryClassifiesControlPlaneSources(t *testing.T) {
	tests := []struct {
		name string
		set  func(*compiledSelectorBatchObservation) error
		want string
	}{
		{name: "run", set: func(observation *compiledSelectorBatchObservation) error {
			err := errors.New("command failed at /private/secret/path")
			observation.runErr = err
			return err
		}, want: "run"},
		{name: "close", set: func(observation *compiledSelectorBatchObservation) error {
			err := errors.New("stream close failed")
			observation.closeErr = err
			return err
		}, want: "close"},
		{name: "parse", set: func(observation *compiledSelectorBatchObservation) error {
			err := errors.New("malformed test2json event")
			observation.parseErr = err
			return err
		}, want: "parse"},
		{name: "context", set: func(observation *compiledSelectorBatchObservation) error {
			observation.contextErr = context.Canceled
			return context.Canceled
		}, want: "context"},
		{name: "cleanup", set: func(observation *compiledSelectorBatchObservation) error {
			err := errors.Join(errCompiledSelectorBatchCleanup, errors.New("remove runtime roots: permission denied"))
			observation.runErr = err
			return err
		}, want: "cleanup"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := compiledSelectorBatchObservation{}
			batchErr := test.set(&observation)
			summary := string(compiledSelectorBatchErrorSummary(observation, batchErr))
			if !strings.Contains(summary, "batch-error kind="+test.want+" error-digest=") {
				t.Fatalf("summary = %q", summary)
			}
			if strings.Contains(summary, batchErr.Error()) {
				t.Fatalf("summary leaked raw batch error: %q", summary)
			}
		})
	}
}
