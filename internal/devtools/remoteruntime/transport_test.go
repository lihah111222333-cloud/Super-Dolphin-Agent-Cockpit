package remoteruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
)

func TestTransportProbeUsesBoundedContextAndSeparateChannels(t *testing.T) {
	config := runtimeFixture()
	var calls atomic.Int32
	var gotSpec CommandSpec
	transport := probeTransportFixture(t, &gotSpec, &calls)
	result, err := transport.Probe(context.Background(), targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"probe"}`))
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if result.NodeID != Ubuntu01NodeID || result.Identity != runtimeFixture().Nodes[0].ProbeIdentity {
		t.Fatalf("unexpected result identity: %+v", result)
	}
	if result.CleanupVerified {
		t.Fatal("probe result incorrectly claimed cleanup verification")
	}
	if calls.Load() != 1 {
		t.Fatalf("command calls = %d, want 1", calls.Load())
	}
	assertProbeCommand(t, config, gotSpec)
	if !bytes.Equal(result.Bytes(), mustProbeResultJSON(t, config.Nodes[0])) {
		t.Fatalf("result bytes changed: %s", result.Bytes())
	}
}

func TestProbeCommandDoesNotRequireExecutionFacts(t *testing.T) {
	transport, err := New(runtimeFixture(), CommandRunnerFunc(func(_ context.Context, _ CommandSpec) (CommandResult, error) {
		return CommandResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := transport.ProbeCommand(targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"probe"}`))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if strings.Contains(joined, "--goos") || strings.Contains(joined, "--goarch") || strings.Contains(joined, "--module-identity") || strings.Contains(joined, "--workload-identity") {
		t.Fatalf("probe argv contains batch execution facts: %v", spec.Args)
	}
}

func TestProbeBytesDoesNotRequireExecutionFacts(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	transport, err := New(runtimeFixture(), CommandRunnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
		_, writeErr := spec.Stdout.Write(mustProbeResultJSON(t, node))
		return CommandResult{}, writeErr
	}))
	if err != nil {
		t.Fatal(err)
	}
	data, err := transport.ProbeBytes(context.Background(), targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"probe"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, mustProbeResultJSON(t, node)) {
		t.Fatalf("ProbeBytes() changed canonical result: %s", data)
	}
}

func TestBatchSuccessConfirmsCleanupAfterStrictResult(t *testing.T) {
	for _, operation := range []Operation{OperationRun, OperationWorker} {
		t.Run(string(operation), func(t *testing.T) {
			transport, err := New(runtimeFixture(), CommandRunnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
				_, writeErr := spec.Stdout.Write(mustBatchResultJSON(t))
				return CommandResult{}, writeErr
			}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := transport.Execute(context.Background(), targetFixture("target"), executionFixture(UbuntuPlatform), operation, Ubuntu01NodeID, []byte(`{"request":"batch"}`))
			if err != nil {
				t.Fatal(err)
			}
			if !result.CleanupVerified {
				t.Fatalf("successful %s result did not confirm cleanup: %+v", operation, result)
			}
		})
	}
}

func probeTransportFixture(t *testing.T, specOut *CommandSpec, calls *atomic.Int32) *Transport {
	t.Helper()
	transport, err := New(runtimeFixture(), CommandRunnerFunc(func(ctx context.Context, spec CommandSpec) (CommandResult, error) {
		calls.Add(1)
		*specOut = spec
		if _, ok := ctx.Deadline(); !ok {
			t.Error("command context has no deadline")
		}
		mustWriteTestOutput(t, spec.Stdout, string(mustProbeResultJSON(t, runtimeFixture().Nodes[0])))
		mustWriteTestOutput(t, spec.Stderr, "raw remote log must not be projected")
		return CommandResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func assertProbeCommand(t *testing.T, config RuntimeConfig, spec CommandSpec) {
	t.Helper()
	if spec.Executable != config.SSHExecutable || len(spec.Args) < 6 {
		t.Fatalf("unexpected SSH command spec: %+v", spec)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "ConnectTimeout=2") || !strings.Contains(joined, "'/opt/super-dolphin/probe' 'probe' '--config' '/etc/super-dolphin/probe.json' '--probe-request-base64'") || strings.Contains(joined, "120") {
		t.Fatalf("SSH argv is not derived from timeout/binding: %v", spec.Args)
	}
}

func mustWriteTestOutput(t *testing.T, writer io.Writer, value string) {
	t.Helper()
	if _, err := io.WriteString(writer, value); err != nil {
		t.Fatal(err)
	}
}

func mustProbeResultJSON(t *testing.T, node NodeBinding) []byte {
	t.Helper()
	response := heteroci.ProbeResponse{
		SchemaVersion: heteroci.ProbeResponseSchemaVersion, OwnerEpoch: 1,
		CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		NodeID:        node.NodeID, Endpoint: node.Endpoint, Platform: heteroci.Platform(node.Platform),
		ProbeIdentity: node.ProbeIdentity, ProbeBootID: "boot-01", ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{},
		Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: 1,
		Total:     heteroci.ResourceCapacity{CPUMilli: 1000, MemoryBytes: 1 << 20, MaxConcurrency: 1},
		Available: heteroci.ResourceCapacity{CPUMilli: 1000, MemoryBytes: 1 << 20, MaxConcurrency: 1},
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustBatchResultJSON(t *testing.T) []byte {
	t.Helper()
	result := heterocinode.BatchResult{
		SchemaVersion: heterocinode.BatchResultSchemaVersion, Authority: heterocinode.BatchAuthorityLocalNonAuthoritative,
		Status: heterocinode.BatchStatusNotVerified, Outcome: heterocinode.BatchOutcomeSuccess,
		Outcomes: []heterocinode.BatchOutcome{{UnitID: "unit-01", Status: heterocinode.BatchOutcomeSuccess, ExitCode: 0}},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mutateProbeJSON(t *testing.T, node NodeBinding, field string, value any) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(mustProbeResultJSON(t, node), &fields); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	fields[field] = encoded
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestTransportRejectsMultipleOrNonCanonicalResults(t *testing.T) {
	config := runtimeFixture()
	node := config.Nodes[0]
	valid := string(mustProbeResultJSON(t, node))
	cases := map[string]string{
		"duplicate objects": valid + valid,
		"trailing newline":  valid + "\n",
		"endpoint drift":    string(mutateProbeJSON(t, node, "endpoint", "ssh://other-node")),
		"identity drift":    string(mutateProbeJSON(t, node, "probe_identity", "wrong")),
		"unknown root":      string(mutateProbeJSON(t, node, "repository_root", "/private")),
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			transport, err := New(config, CommandRunnerFunc(func(_ context.Context, spec CommandSpec) (CommandResult, error) {
				_, writeErr := io.WriteString(spec.Stdout, output)
				return CommandResult{}, writeErr
			}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := transport.Probe(context.Background(), targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"probe"}`)); !errors.Is(err, ErrNodeResultInvalid) {
				t.Fatalf("Probe() error = %v, want ErrNodeResultInvalid", err)
			}
		})
	}
}

func TestTransportTimeoutDoesNotRetry(t *testing.T) {
	config := runtimeFixture()
	var calls atomic.Int32
	transport, err := New(config, CommandRunnerFunc(func(ctx context.Context, _ CommandSpec) (CommandResult, error) {
		calls.Add(1)
		<-ctx.Done()
		return CommandResult{}, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := transport.Probe(ctx, targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"probe"}`))
	if !errors.Is(err, ErrCommandTimedOut) {
		t.Fatalf("Probe() error = %v, want timeout", err)
	}
	if result.CleanupVerified {
		t.Fatal("timed-out probe result claimed cleanup verification")
	}
	if calls.Load() != 1 {
		t.Fatalf("command calls = %d, want exactly one", calls.Load())
	}
}

func TestExecuteUsesExplicitRequestTimeout(t *testing.T) {
	config := runtimeFixture()
	config.ProbeTimeoutMS = 10
	config.RequestTimeoutMS = 25
	config.RunTimeoutMS = 20
	config.CleanupTimeoutMS = 15
	config.WorkerTimeoutMS = 22
	transport, err := New(config, CommandRunnerFunc(func(ctx context.Context, _ CommandSpec) (CommandResult, error) {
		<-ctx.Done()
		return CommandResult{}, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := transport.Execute(context.Background(), targetFixture("target"), executionFixture(UbuntuPlatform), OperationRun, Ubuntu01NodeID, []byte(`{"request":"batch"}`))
	if !errors.Is(err, ErrCommandTimedOut) {
		t.Fatalf("Execute() error = %v, want request timeout", err)
	}
	if result.CleanupVerified {
		t.Fatal("request-timeout result claimed cleanup verification")
	}
}

func TestBatchFailuresDoNotClaimCleanup(t *testing.T) {
	cases := []struct {
		name   string
		runner CommandRunnerFunc
	}{
		{name: "nonzero", runner: func(_ context.Context, _ CommandSpec) (CommandResult, error) {
			return CommandResult{ExitCode: 7}, nil
		}},
		{name: "strict result decode failure", runner: func(_ context.Context, spec CommandSpec) (CommandResult, error) {
			_, err := spec.Stdout.Write(mustProbeResultJSON(t, runtimeFixture().Nodes[0]))
			return CommandResult{}, err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport, err := New(runtimeFixture(), tc.runner)
			if err != nil {
				t.Fatal(err)
			}
			result, err := transport.Run(context.Background(), targetFixture("target"), executionFixture(UbuntuPlatform), Ubuntu01NodeID, []byte(`{"request":"batch"}`))
			if err == nil {
				t.Fatal("Run unexpectedly succeeded")
			}
			if result.CleanupVerified {
				t.Fatalf("failed run claimed cleanup verification: %+v", result)
			}
		})
	}
}

func TestTransportWindowsArgvHasNoUnixPlatformFields(t *testing.T) {
	config := runtimeFixture()
	transport, err := New(config, CommandRunnerFunc(func(_ context.Context, _ CommandSpec) (CommandResult, error) {
		return CommandResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := transport.RunCommand(targetFixture("target"), executionFixture(WindowsPlatform), Windows01NodeID, []byte(`{"request":"run"}`))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	if !strings.Contains(joined, "C:/super-dolphin/windows_runner.ps1") || !strings.Contains(joined, "-CommandBase64") || !strings.Contains(joined, "-RepoPath") {
		t.Fatalf("Windows argv does not use binding paths: %v", spec.Args)
	}
	if strings.Contains(joined, "docker") || strings.Contains(joined, "systemd") || strings.Contains(joined, "120") {
		t.Fatalf("Windows argv contains mixed/default fields: %v", spec.Args)
	}
}

func TestBatchPathsRequireExecutionFacts(t *testing.T) {
	transport, err := New(runtimeFixture(), CommandRunnerFunc(func(_ context.Context, _ CommandSpec) (CommandResult, error) {
		return CommandResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	target := targetFixture("target")
	request := []byte(`{"request":"batch"}`)
	if _, err := transport.RunCommand(target, ExecutionFacts{}, Ubuntu01NodeID, request); err == nil {
		t.Fatal("RunCommand accepted missing execution facts")
	}
	if _, err := transport.WorkerCommand(target, executionFixture(WindowsPlatform), Ubuntu01NodeID, request); err == nil {
		t.Fatal("WorkerCommand accepted cross-platform execution facts")
	}
	if _, err := transport.Run(context.Background(), target, ExecutionFacts{}, Ubuntu01NodeID, request); err == nil {
		t.Fatal("Run accepted missing execution facts")
	}
	if _, err := transport.Worker(context.Background(), target, executionFixture(WindowsPlatform), Ubuntu01NodeID, request); err == nil {
		t.Fatal("Worker accepted cross-platform execution facts")
	}
	if _, err := transport.Execute(context.Background(), target, ExecutionFacts{}, OperationRun, Ubuntu01NodeID, request); err == nil {
		t.Fatal("Execute accepted missing execution facts")
	}
}

func TestTransportLinuxRunnerUsesDaemonContract(t *testing.T) {
	transport, err := New(runtimeFixture(), CommandRunnerFunc(func(_ context.Context, _ CommandSpec) (CommandResult, error) {
		return CommandResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := transport.RunCommand(targetFixture("target"), executionFixture(UbuntuPlatform), Ubuntu01NodeID, []byte(`{"request":"run"}`))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(spec.Args, " ")
	for _, required := range []string{"linux_runner_daemon.sh", "'--repo'", "'--mode' 'docker'", "'--runtime' 'ubuntu'", "'--docker-image'", "'--worker-binary'", "'--worker-identity'", "'--shared-root'", "'--cache-root'", "'--repository-id' 'target'", "'--object-format' 'sha1'", "'--source-commit'", "'--source-tree'", "'--toolchain-identity'", "'--module-identity'", "'--workload-identity'", "'--timeout-seconds' '45'", "'--' '/opt/super-dolphin/worker' 'batch'"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("Linux daemon argv missing %q: %v", required, spec.Args)
		}
	}
}

func TestDecodeNodeResultRequiresBindingIdentity(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	if _, err := DecodeNodeResult(mutateProbeJSON(t, node, "probe_identity", "wrong"), node, OperationProbe); !errors.Is(err, ErrNodeResultInvalid) {
		t.Fatalf("DecodeNodeResult() error = %v, want identity error", err)
	}
}

func TestDecodeNodeResultUsesExistingNodeProtocols(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	probe := mustProbeResultJSON(t, node)
	batch := mustBatchResultJSON(t)
	cases := []struct {
		name  string
		data  []byte
		op    Operation
		valid bool
		want  string
	}{
		{name: "probe", data: probe, op: OperationProbe, valid: true, want: node.ProbeIdentity},
		{name: "run batch", data: batch, op: OperationRun, valid: true, want: node.RunnerIdentity},
		{name: "worker batch", data: batch, op: OperationWorker, valid: true, want: node.WorkerIdentity},
		{name: "probe endpoint drift", data: mutateProbeJSON(t, node, "endpoint", "ssh://other-node"), op: OperationProbe},
		{name: "probe unknown field", data: mutateProbeJSON(t, node, "repository_root", "/private"), op: OperationProbe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := DecodeNodeResult(tc.data, node, tc.op)
			if (err == nil) != tc.valid {
				t.Fatalf("DecodeNodeResult() error = %v, valid=%v", err, tc.valid)
			}
			if tc.valid && result.Identity != tc.want {
				t.Fatalf("identity = %q, want %q", result.Identity, tc.want)
			}
		})
	}
}
