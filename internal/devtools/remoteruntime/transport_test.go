package remoteruntime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	if result.NodeID != Ubuntu01NodeID || result.Identity != "Ubuntu-01-probe" {
		t.Fatalf("unexpected result identity: %+v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("command calls = %d, want 1", calls.Load())
	}
	assertProbeCommand(t, config, gotSpec)
	if !bytes.Equal(result.Bytes(), []byte(`{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok"}`)) {
		t.Fatalf("result bytes changed: %s", result.Bytes())
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
		mustWriteTestOutput(t, spec.Stdout, `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok"}`)
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

func TestTransportRejectsMultipleOrNonCanonicalResults(t *testing.T) {
	config := runtimeFixture()
	cases := map[string]string{
		"duplicate objects": `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok"}{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok"}`,
		"trailing newline": `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok"}
`,
		"identity drift": `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"wrong","status":"ok"}`,
		"node drift":     `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-02","probe_identity":"Ubuntu-01-probe","status":"ok"}`,
		"missing status": `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe"}`,
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
	_, err = transport.Probe(ctx, targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"probe"}`))
	if !errors.Is(err, ErrCommandTimedOut) {
		t.Fatalf("Probe() error = %v, want timeout", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("command calls = %d, want exactly one", calls.Load())
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
	spec, err := transport.Command(OperationRun, targetFixture("target"), Windows01NodeID, []byte(`{"request":"run"}`))
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

func TestTransportLinuxRunnerUsesDaemonContract(t *testing.T) {
	transport, err := New(runtimeFixture(), CommandRunnerFunc(func(_ context.Context, _ CommandSpec) (CommandResult, error) {
		return CommandResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := transport.Command(OperationRun, targetFixture("target"), Ubuntu01NodeID, []byte(`{"request":"run"}`))
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
	if _, err := DecodeNodeResult([]byte(`{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"wrong","status":"ok"}`), node, OperationProbe); !errors.Is(err, ErrNodeResultInvalid) {
		t.Fatalf("DecodeNodeResult() error = %v, want identity error", err)
	}
}

func TestDecodeNodeResultUsesOperationSpecificAllowlist(t *testing.T) {
	node := runtimeFixture().Nodes[0]
	cases := []struct {
		name  string
		data  string
		op    Operation
		valid bool
	}{
		{name: "probe valid", data: `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok"}`, op: OperationProbe, valid: true},
		{name: "run valid", data: `{"schema_version":"remote-ci-node-run-result/v1","node_id":"Ubuntu-01","runner_identity":"Ubuntu-01-runner","status":"ok"}`, op: OperationRun, valid: true},
		{name: "worker valid", data: `{"schema_version":"remote-ci-node-worker-result/v1","node_id":"Ubuntu-01","worker_identity":"Ubuntu-01-worker","status":"ok"}`, op: OperationWorker, valid: true},
		{name: "endpoint unknown", data: `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","probe_identity":"Ubuntu-01-probe","status":"ok","endpoint":"/private"}`, op: OperationProbe},
		{name: "generic identity rejected", data: `{"schema_version":"remote-ci-node-probe-result/v1","node_id":"Ubuntu-01","identity":"Ubuntu-01-probe","status":"ok"}`, op: OperationProbe},
		{name: "run identity drift", data: `{"schema_version":"remote-ci-node-run-result/v1","node_id":"Ubuntu-01","runner_identity":"Ubuntu-01-worker","status":"ok"}`, op: OperationRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeNodeResult([]byte(tc.data), node, tc.op)
			if (err == nil) != tc.valid {
				t.Fatalf("DecodeNodeResult() error = %v, valid=%v", err, tc.valid)
			}
		})
	}
}
