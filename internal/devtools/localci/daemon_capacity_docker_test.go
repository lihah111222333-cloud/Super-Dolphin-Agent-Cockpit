package localci

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type daemonCapacityDockerRunnerStub struct {
	output string
	err    error
	args   [][]string
	after  func()
}

func (stub *daemonCapacityDockerRunnerStub) Run(_ context.Context, args ...string) (string, error) {
	stub.args = append(stub.args, append([]string(nil), args...))
	if stub.after != nil {
		stub.after()
	}
	return stub.output, stub.err
}

func TestDockerDaemonCapacityInspectorReadsCurrentClassCapacity(t *testing.T) {
	runner := &daemonCapacityDockerRunnerStub{
		output: dockerInfoOutput(testDaemonID, 18, 8215146496),
	}
	inspector, err := newDockerDaemonCapacityInspector(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonCapacityInspector() error = %v", err)
	}
	inspector.now = testObservedAt

	capacity, err := inspector.InspectDaemonCapacity(context.Background(), testDaemonID)
	if err != nil {
		t.Fatalf("InspectDaemonCapacity() error = %v", err)
	}
	if capacity.DaemonID != testDaemonID || capacity.LogicalCPUs != 18 || capacity.MemoryBytes != 8215146496 || capacity.ObservedAt != testObservedAt() {
		t.Fatalf("capacity = %#v", capacity)
	}
	wantArgs := [][]string{{"info", "--format", "{{json .}}"}}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("docker args = %#v, want %#v", runner.args, wantArgs)
	}
	if _, err := ValidateDaemonCapacity(context.Background(), testDaemonID, 3, inspector); err == nil || !strings.Contains(err.Error(), "memory capacity insufficient") {
		t.Fatalf("ValidateDaemonCapacity() error = %v, want memory capacity insufficient", err)
	}
}

func TestDockerDaemonCapacityInspectorMapsPassingCapacity(t *testing.T) {
	runner := &daemonCapacityDockerRunnerStub{
		output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB),
	}
	inspector, err := newDockerDaemonCapacityInspector(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonCapacityInspector() error = %v", err)
	}
	inspector.now = testObservedAt

	evidence, err := ValidateDaemonCapacity(context.Background(), testDaemonID, 3, inspector)
	if err != nil {
		t.Fatalf("ValidateDaemonCapacity() error = %v", err)
	}
	if evidence.Available.LogicalCPUs != 18 || evidence.Available.MemoryBytes != 25*bytesPerGiB {
		t.Fatalf("available capacity = %#v", evidence.Available)
	}
}

func TestDockerDaemonCapacityInspectorRejectsInvalidDockerInfo(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		requestedID   string
		wantSubstring string
	}{
		{name: "unknown field", output: `{"ID":"` + testDaemonID + `","NCPU":18,"MemTotal":26843545600,"Unexpected":true}`, requestedID: testDaemonID, wantSubstring: "unknown field"},
		{name: "trailing JSON", output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB) + `{}`, requestedID: testDaemonID, wantSubstring: "trailing"},
		{name: "identity drift", output: dockerInfoOutput("different-daemon", 18, 25*bytesPerGiB), requestedID: testDaemonID, wantSubstring: "identity mismatch"},
		{name: "zero CPUs", output: dockerInfoOutput(testDaemonID, 0, 25*bytesPerGiB), requestedID: testDaemonID, wantSubstring: "positive"},
		{name: "zero memory", output: dockerInfoOutput(testDaemonID, 18, 0), requestedID: testDaemonID, wantSubstring: "positive"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &daemonCapacityDockerRunnerStub{output: test.output}
			inspector, err := newDockerDaemonCapacityInspector(runner)
			if err != nil {
				t.Fatalf("newDockerDaemonCapacityInspector() error = %v", err)
			}
			if _, err := inspector.InspectDaemonCapacity(context.Background(), test.requestedID); err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("InspectDaemonCapacity() error = %v, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestDockerDaemonCapacityInspectorFailsFast(t *testing.T) {
	commandErr := errors.New("docker info failed")
	runner := &daemonCapacityDockerRunnerStub{err: commandErr}
	inspector, err := newDockerDaemonCapacityInspector(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonCapacityInspector() error = %v", err)
	}
	if _, err := inspector.InspectDaemonCapacity(context.Background(), testDaemonID); !errors.Is(err, commandErr) {
		t.Fatalf("InspectDaemonCapacity() error = %v, want %v", err, commandErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner = &daemonCapacityDockerRunnerStub{output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB)}
	inspector, err = newDockerDaemonCapacityInspector(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonCapacityInspector() error = %v", err)
	}
	if _, err := inspector.InspectDaemonCapacity(ctx, testDaemonID); !errors.Is(err, context.Canceled) {
		t.Fatalf("InspectDaemonCapacity() error = %v, want context canceled", err)
	}
	if len(runner.args) != 0 {
		t.Fatalf("docker runner calls = %d, want 0", len(runner.args))
	}

	ctx, cancel = context.WithCancel(context.Background())
	runner = &daemonCapacityDockerRunnerStub{
		output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB),
		after:  cancel,
	}
	inspector, err = newDockerDaemonCapacityInspector(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonCapacityInspector() error = %v", err)
	}
	if _, err := inspector.InspectDaemonCapacity(ctx, testDaemonID); !errors.Is(err, context.Canceled) {
		t.Fatalf("InspectDaemonCapacity() error = %v, want context canceled after command", err)
	}
}

func TestNewDockerDaemonCapacityInspectorRejectsTypedNilRunner(t *testing.T) {
	var runner *daemonCapacityDockerRunnerStub
	if _, err := newDockerDaemonCapacityInspector(runner); err == nil {
		t.Fatal("newDockerDaemonCapacityInspector() accepted typed-nil runner")
	}

	var inspector *dockerDaemonCapacityInspector
	if _, err := inspector.InspectDaemonCapacity(context.Background(), testDaemonID); err == nil {
		t.Fatal("InspectDaemonCapacity() accepted typed-nil inspector")
	}
}

func dockerInfoOutput(daemonID string, logicalCPUs, memoryBytes int64) string {
	return `{"ID":"` + daemonID + `","NCPU":` + strconv.FormatInt(logicalCPUs, 10) + `,"MemTotal":` + strconv.FormatInt(memoryBytes, 10) + `}`
}
