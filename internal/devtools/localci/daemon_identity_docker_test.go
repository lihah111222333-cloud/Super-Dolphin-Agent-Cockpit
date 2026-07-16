package localci

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testDockerOwnerUID = 501

type daemonIdentityDockerRunnerResult struct {
	output string
	err    error
}

type daemonIdentityDockerRunnerStub struct {
	results   []daemonIdentityDockerRunnerResult
	args      [][]string
	afterCall func(int)
}

func (stub *daemonIdentityDockerRunnerStub) Run(_ context.Context, args ...string) (string, error) {
	callIndex := len(stub.args)
	stub.args = append(stub.args, append([]string(nil), args...))
	if stub.afterCall != nil {
		stub.afterCall(callIndex)
	}
	if callIndex >= len(stub.results) {
		return "", errors.New("unexpected docker runner call")
	}
	result := stub.results[callIndex]
	return result.output, result.err
}

func TestDockerDaemonIdentityProbeBuildsCanonicalCheckpoint(t *testing.T) {
	contextOutput := dockerContextOutput(t, "desktop-linux", "unix:///Users/test/.docker/run/../run/docker.sock", false, "", nil)
	runner := identityProbeRunner(contextOutput, testDaemonID)
	probe := newTestDockerDaemonIdentityProbe(t, runner)

	checkpoint, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if checkpoint.ContextName != "desktop-linux" {
		t.Fatalf("context name = %q", checkpoint.ContextName)
	}
	wantConfig := SchedulerConfig{
		Endpoint:       "unix:///Users/test/.docker/run/docker.sock",
		TLSFingerprint: "",
		DaemonID:       testDaemonID,
		OwnerUID:       testDockerOwnerUID,
	}
	if !reflect.DeepEqual(checkpoint.SchedulerConfig, wantConfig) {
		t.Fatalf("scheduler config = %#v, want %#v", checkpoint.SchedulerConfig, wantConfig)
	}
	wantIdentity, err := newDaemonIdentity(wantConfig.Endpoint, wantConfig.TLSFingerprint, wantConfig.DaemonID, wantConfig.OwnerUID)
	if err != nil {
		t.Fatalf("newDaemonIdentity() error = %v", err)
	}
	if checkpoint.IdentityKey != wantIdentity.key {
		t.Fatalf("identity key = %q, want %q", checkpoint.IdentityKey, wantIdentity.key)
	}
	wantArgs := [][]string{
		{"context", "inspect", "--format", "{{json .}}"},
		{"info", "--format", "{{json .}}"},
		{"context", "inspect", "--format", "{{json .}}"},
		{"info", "--format", "{{json .}}"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("docker args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestDockerDaemonIdentityCheckpointFieldRegistry(t *testing.T) {
	assertSchedulerStructFields(t, reflect.TypeOf(DockerDaemonIdentityCheckpoint{}), []string{
		"ContextName", "SchedulerConfig", "IdentityKey",
	})
}

func TestDockerDaemonIdentityProbeContextAliasesShareIdentityKey(t *testing.T) {
	first := newTestDockerDaemonIdentityProbe(t, identityProbeRunner(
		dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil),
		testDaemonID,
	))
	alias := newTestDockerDaemonIdentityProbe(t, identityProbeRunner(
		dockerContextOutput(t, "desktop-alias", "unix:///var/run/../run/docker.sock", false, "", nil),
		testDaemonID,
	))

	firstCheckpoint, err := first.Probe(context.Background())
	if err != nil {
		t.Fatalf("first Probe() error = %v", err)
	}
	aliasCheckpoint, err := alias.Probe(context.Background())
	if err != nil {
		t.Fatalf("alias Probe() error = %v", err)
	}
	if firstCheckpoint.ContextName == aliasCheckpoint.ContextName {
		t.Fatal("test contexts are not aliases")
	}
	if firstCheckpoint.IdentityKey != aliasCheckpoint.IdentityKey {
		t.Fatalf("alias identity keys differ: %q != %q", firstCheckpoint.IdentityKey, aliasCheckpoint.IdentityKey)
	}
}

func TestDockerDaemonIdentityProbeFingerprintsTrustedTCPMaterial(t *testing.T) {
	tlsRoot := t.TempDir()
	tlsDockerRoot := filepath.Join(tlsRoot, "docker")
	if err := os.Mkdir(tlsDockerRoot, 0o700); err != nil {
		t.Fatalf("mkdir TLS root: %v", err)
	}
	materials := map[string]string{
		"ca.pem":   "test-ca-material",
		"cert.pem": "test-cert-material",
		"key.pem":  "test-key-material",
	}
	for name, contents := range materials {
		if err := os.WriteFile(filepath.Join(tlsDockerRoot, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write TLS material %s: %v", name, err)
		}
	}
	contextOutput := dockerContextOutput(t, "remote", "tcp://DOCKER.EXAMPLE:2376", false, tlsRoot, []string{"key.pem", "ca.pem", "cert.pem"})
	probe := newTestDockerDaemonIdentityProbe(t, identityProbeRunner(contextOutput, testDaemonID))

	checkpoint, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if checkpoint.SchedulerConfig.Endpoint != "tcp://docker.example:2376" {
		t.Fatalf("endpoint = %q", checkpoint.SchedulerConfig.Endpoint)
	}
	if !strings.HasPrefix(checkpoint.SchedulerConfig.TLSFingerprint, "sha256:") {
		t.Fatalf("TLS fingerprint = %q", checkpoint.SchedulerConfig.TLSFingerprint)
	}
	for _, contents := range materials {
		if strings.Contains(checkpoint.SchedulerConfig.TLSFingerprint, contents) {
			t.Fatal("TLS fingerprint leaked material")
		}
	}
}

func TestDockerDaemonIdentityProbeRejectsDriftAndOverrides(t *testing.T) {
	tests := []struct {
		name          string
		runner        *daemonIdentityDockerRunnerStub
		lookupEnv     func(string) (string, bool)
		wantSubstring string
		wantCalls     int
	}{
		{
			name: "DOCKER_HOST override",
			runner: identityProbeRunner(
				dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil),
				testDaemonID,
			),
			lookupEnv: func(name string) (string, bool) {
				return "tcp://attacker.example:2375", name == "DOCKER_HOST"
			},
			wantSubstring: "DOCKER_HOST",
		},
		{
			name: "DOCKER_CONTEXT mismatch",
			runner: identityProbeRunner(
				dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil),
				testDaemonID,
			),
			lookupEnv: func(name string) (string, bool) {
				return "different-context", name == "DOCKER_CONTEXT"
			},
			wantSubstring: "DOCKER_CONTEXT",
			wantCalls:     4,
		},
		{
			name: "context drift",
			runner: &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{
				{output: dockerContextOutput(t, "first", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB)},
				{output: dockerContextOutput(t, "second", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB)},
			}},
			wantSubstring: "context drift",
			wantCalls:     4,
		},
		{
			name: "daemon identity mismatch",
			runner: &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{
				{output: dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB)},
				{output: dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerInfoOutput("different-daemon", 18, 25*bytesPerGiB)},
			}},
			wantSubstring: "identity mismatch",
			wantCalls:     4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := newTestDockerDaemonIdentityProbe(t, test.runner)
			if test.lookupEnv != nil {
				probe.lookupEnv = test.lookupEnv
			}
			if _, err := probe.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("Probe() error = %v, want substring %q", err, test.wantSubstring)
			}
			if len(test.runner.args) != test.wantCalls {
				t.Fatalf("docker calls = %d, want %d", len(test.runner.args), test.wantCalls)
			}
		})
	}
}

func TestDockerDaemonIdentityProbeRejectsMalformedContext(t *testing.T) {
	valid := dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil)
	tests := []struct {
		name          string
		contextOutput string
		wantSubstring string
	}{
		{name: "unknown field", contextOutput: strings.TrimSuffix(valid, "}") + `,"Unexpected":true}`, wantSubstring: "unknown field"},
		{name: "trailing JSON", contextOutput: valid + `{}`, wantSubstring: "trailing"},
		{name: "unknown endpoint field", contextOutput: `{"Name":"desktop-linux","Metadata":{},"Endpoints":{"docker":{"Host":"unix:///var/run/docker.sock","SkipTLSVerify":false,"Unexpected":true}},"TLSMaterial":{},"Storage":{"MetadataPath":"","TLSPath":""}}`, wantSubstring: "unknown field"},
		{name: "unsupported endpoint", contextOutput: dockerContextOutput(t, "remote", "ssh://docker.example", false, "", nil), wantSubstring: "unsupported"},
		{name: "unix TLS material", contextOutput: dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, t.TempDir(), []string{"ca.pem"}), wantSubstring: "must not declare TLS"},
		{name: "TCP without TLS material", contextOutput: dockerContextOutput(t, "remote", "tcp://docker.example:2376", false, t.TempDir(), nil), wantSubstring: "TLS material"},
		{name: "TCP skips verification", contextOutput: dockerContextOutput(t, "remote", "tcp://docker.example:2376", true, t.TempDir(), []string{"ca.pem"}), wantSubstring: "verification"},
		{name: "TCP material traversal", contextOutput: dockerContextOutput(t, "remote", "tcp://docker.example:2376", false, t.TempDir(), []string{"../ca.pem"}), wantSubstring: "filename"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{{output: test.contextOutput}}}
			probe := newTestDockerDaemonIdentityProbe(t, runner)
			if _, err := probe.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("Probe() error = %v, want substring %q", err, test.wantSubstring)
			}
		})
	}
}

func TestDockerDaemonIdentityProbeFailsFast(t *testing.T) {
	wantErr := errors.New("docker context unavailable")
	runner := &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{{err: wantErr}}}
	probe := newTestDockerDaemonIdentityProbe(t, runner)
	if _, err := probe.Probe(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Probe() error = %v, want %v", err, wantErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner = identityProbeRunner(dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil), testDaemonID)
	probe = newTestDockerDaemonIdentityProbe(t, runner)
	if _, err := probe.Probe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() error = %v, want context canceled", err)
	}
	if len(runner.args) != 0 {
		t.Fatalf("docker calls = %d, want 0", len(runner.args))
	}

	ctx, cancel = context.WithCancel(context.Background())
	runner = identityProbeRunner(dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil), testDaemonID)
	runner.afterCall = func(call int) {
		if call == 0 {
			cancel()
		}
	}
	probe = newTestDockerDaemonIdentityProbe(t, runner)
	if _, err := probe.Probe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe() error = %v, want context canceled after command", err)
	}
	if len(runner.args) != 1 {
		t.Fatalf("docker calls = %d, want 1", len(runner.args))
	}
}

func TestNewDockerDaemonIdentityProbeRejectsTypedNil(t *testing.T) {
	var runner *daemonIdentityDockerRunnerStub
	if _, err := newDockerDaemonIdentityProbe(runner); err == nil {
		t.Fatal("newDockerDaemonIdentityProbe() accepted typed-nil runner")
	}

	var probe *dockerDaemonIdentityProbe
	if _, err := probe.Probe(context.Background()); err == nil {
		t.Fatal("Probe() accepted typed-nil probe")
	}
}

func newTestDockerDaemonIdentityProbe(t *testing.T, runner dockerRunner) *dockerDaemonIdentityProbe {
	t.Helper()
	probe, err := newDockerDaemonIdentityProbe(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonIdentityProbe() error = %v", err)
	}
	probe.currentUID = func() (int, error) { return testDockerOwnerUID, nil }
	probe.lookupEnv = func(string) (string, bool) { return "", false }
	return probe
}

func identityProbeRunner(contextOutput, daemonID string) *daemonIdentityDockerRunnerStub {
	return &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{
		{output: contextOutput},
		{output: dockerInfoOutput(daemonID, 18, 25*bytesPerGiB)},
		{output: contextOutput},
		{output: dockerInfoOutput(daemonID, 18, 25*bytesPerGiB)},
	}}
}

func dockerContextOutput(
	t *testing.T,
	name string,
	host string,
	skipTLSVerify bool,
	tlsPath string,
	tlsFiles []string,
) string {
	t.Helper()
	payload := map[string]any{
		"Name":     name,
		"Metadata": map[string]any{},
		"Endpoints": map[string]any{
			"docker": map[string]any{"Host": host, "SkipTLSVerify": skipTLSVerify},
		},
		"TLSMaterial": map[string]any{},
		"Storage": map[string]any{
			"MetadataPath": "",
			"TLSPath":      tlsPath,
		},
	}
	if tlsFiles != nil {
		payload["TLSMaterial"] = map[string]any{"docker": tlsFiles}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal Docker context: %v", err)
	}
	return string(encoded)
}
