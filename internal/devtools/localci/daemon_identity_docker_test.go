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
	"time"
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
		{"--context", "desktop-linux", "info", "--format", "{{json .}}"},
		{"context", "inspect", "--format", "{{json .}}", "desktop-linux"},
		{"context", "inspect", "--format", "{{json .}}"},
		{"--context", "desktop-linux", "info", "--format", "{{json .}}"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("docker args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestDockerDaemonIdentityCheckpointFieldRegistry(t *testing.T) {
	assertSchedulerStructFields(t, reflect.TypeFor[DockerDaemonIdentityCheckpoint](), []string{
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

func TestProbeDockerSchedulerAuthorityContextAliasesShareIdentity(t *testing.T) {
	clearDockerAuthorityEnvironment(t)
	first, err := probeDockerSchedulerAuthority(context.Background(), 3, authorityProbeRunner(
		dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil),
		testDaemonID, testDaemonID, 12, 24*bytesPerGiB,
	))
	if err != nil {
		t.Fatalf("first probeDockerSchedulerAuthority() error = %v", err)
	}
	alias, err := probeDockerSchedulerAuthority(context.Background(), 3, authorityProbeRunner(
		dockerContextOutput(t, "desktop-alias", "unix:///var/run/../run/docker.sock", false, "", nil),
		testDaemonID, testDaemonID, 12, 24*bytesPerGiB,
	))
	if err != nil {
		t.Fatalf("alias probeDockerSchedulerAuthority() error = %v", err)
	}
	if first.ContextName == alias.ContextName || first.IdentityKey != alias.IdentityKey {
		t.Fatalf("authority aliases do not converge: first=%#v alias=%#v", first, alias)
	}
	if first.SchedulerConfig.MaxActiveWorkloads != 3 || alias.SchedulerConfig.MaxActiveWorkloads != 3 {
		t.Fatalf("authority aliases lost configured capacity: first=%#v alias=%#v", first, alias)
	}
}

func TestProbeDockerSchedulerAuthorityRejectsContextABA(t *testing.T) {
	clearDockerAuthorityEnvironment(t)
	firstContext := dockerContextOutput(t, "context-a", "unix:///var/run/docker-a.sock", false, "", nil)
	runner := &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{
		{output: firstContext},
		{output: dockerInfoOutput(testDaemonID, 12, 24*bytesPerGiB)},
		{output: firstContext},
		{output: dockerContextOutput(t, "context-b", "unix:///var/run/docker-b.sock", false, "", nil)},
	}}
	if _, err := probeDockerSchedulerAuthority(context.Background(), 3, runner); err == nil || !strings.Contains(err.Error(), "context drift") {
		t.Fatalf("probeDockerSchedulerAuthority() error = %v, want context drift", err)
	}
	if len(runner.args) != 4 {
		t.Fatalf("Docker calls = %d, want fail-fast before capacity inspection", len(runner.args))
	}
}

func TestProbeDockerSchedulerAuthorityRejectsInsufficientCapacity(t *testing.T) {
	clearDockerAuthorityEnvironment(t)
	contextOutput := dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil)
	tests := []struct {
		name          string
		logicalCPUs   int64
		memoryBytes   int64
		wantSubstring string
	}{
		{name: "CPU", logicalCPUs: 11, memoryBytes: 24 * bytesPerGiB, wantSubstring: "logical CPU capacity insufficient"},
		{name: "memory", logicalCPUs: 12, memoryBytes: 23 * bytesPerGiB, wantSubstring: "memory capacity insufficient"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := authorityProbeRunner(contextOutput, testDaemonID, testDaemonID, test.logicalCPUs, test.memoryBytes)
			if _, err := probeDockerSchedulerAuthority(context.Background(), 3, runner); err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("probeDockerSchedulerAuthority() error = %v, want %q", err, test.wantSubstring)
			}
		})
	}
}

func TestProbeDockerSchedulerAuthorityRejectsCapacityDaemonMismatch(t *testing.T) {
	clearDockerAuthorityEnvironment(t)
	runner := authorityProbeRunner(
		dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil),
		testDaemonID, "different-daemon", 12, 24*bytesPerGiB,
	)
	if _, err := probeDockerSchedulerAuthority(context.Background(), 3, runner); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("probeDockerSchedulerAuthority() error = %v, want capacity identity mismatch", err)
	}
}

func TestProbeDockerSchedulerAuthorityLive(t *testing.T) {
	if os.Getenv(freshContainerSmokeSwitch) != "1" {
		t.Skipf("set %s=1 to run the current Docker authority smoke", freshContainerSmokeSwitch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	checkpoint, err := ProbeDockerSchedulerAuthority(ctx)
	if err != nil {
		t.Fatalf("ProbeDockerSchedulerAuthority() live Docker error = %v", err)
	}
	if checkpoint.IdentityKey == "" || checkpoint.SchedulerConfig.DaemonID == "" {
		t.Fatalf("live Docker authority checkpoint is incomplete: %#v", checkpoint)
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
			wantCalls:     1,
		},
		{
			name: "context drift",
			runner: &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{
				{output: dockerContextOutput(t, "first", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerInfoOutput(testDaemonID, 18, 25*bytesPerGiB)},
				{output: dockerContextOutput(t, "first", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerContextOutput(t, "second", "unix:///var/run/docker.sock", false, "", nil)},
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
				{output: dockerContextOutput(t, "desktop-linux", "unix:///var/run/docker.sock", false, "", nil)},
				{output: dockerInfoOutput("different-daemon", 18, 25*bytesPerGiB)},
			}},
			wantSubstring: "identity mismatch",
			wantCalls:     5,
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

func TestDockerDaemonIdentityProbePinsInfoToFirstContextBeforeRejectingActiveDrift(t *testing.T) {
	firstContext := dockerContextOutput(t, "context-a", "unix:///var/run/docker-a.sock", false, "", nil)
	driftedActiveContext := dockerContextOutput(t, "context-b", "unix:///var/run/docker-b.sock", false, "", nil)
	runner := &daemonIdentityDockerRunnerStub{results: []daemonIdentityDockerRunnerResult{
		{output: firstContext},
		{output: dockerInfoOutput("daemon-a", 18, 25*bytesPerGiB)},
		{output: firstContext},
		{output: driftedActiveContext},
	}}
	probe := newTestDockerDaemonIdentityProbe(t, runner)

	if _, err := probe.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "context drift") {
		t.Fatalf("Probe() error = %v, want active context drift", err)
	}
	wantArgs := [][]string{
		{"context", "inspect", "--format", "{{json .}}"},
		{"--context", "context-a", "info", "--format", "{{json .}}"},
		{"context", "inspect", "--format", "{{json .}}", "context-a"},
		{"context", "inspect", "--format", "{{json .}}"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("docker args = %#v, want %#v", runner.args, wantArgs)
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

func TestDockerDaemonIdentityProbeLiveDocker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runner := execDockerRunner{}
	probe, err := newDockerDaemonIdentityProbe(runner)
	if err != nil {
		t.Fatalf("newDockerDaemonIdentityProbe() error = %v", err)
	}
	observation := requireLiveDockerContext(t, ctx, runner, probe)
	info := requireLiveDockerInfo(t, ctx, runner, observation.name)
	checkpoint, err := probe.Probe(ctx)
	if err != nil {
		t.Fatalf("Probe() live Docker error = %v", err)
	}
	if checkpoint.ContextName != observation.name || checkpoint.SchedulerConfig.DaemonID != info.ID {
		t.Fatalf("live checkpoint = %#v, context=%q daemon=%q", checkpoint, observation.name, info.ID)
	}
}

func requireLiveDockerContext(
	t *testing.T,
	ctx context.Context,
	runner execDockerRunner,
	probe *dockerDaemonIdentityProbe,
) dockerContextObservation {
	t.Helper()
	contextOutput, err := runner.Run(ctx, "context", "inspect", "--format", dockerContextJSONFormat)
	if err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}
	payload, err := decodeDockerContext(contextOutput)
	if err != nil {
		t.Fatalf("decode live Docker context: %v", err)
	}
	observation, err := probe.normalizeContext(payload)
	if err != nil {
		t.Fatalf("normalize live Docker context: %v", err)
	}
	return observation
}

func requireLiveDockerInfo(
	t *testing.T,
	ctx context.Context,
	runner execDockerRunner,
	contextName string,
) dockerInfoPayload {
	t.Helper()
	infoOutput, err := runner.Run(ctx, "--context", contextName, "info", "--format", dockerInfoJSONFormat)
	if err != nil {
		t.Skipf("Docker daemon is unavailable: %v", err)
	}
	info, err := decodeDockerInfo(infoOutput)
	if err != nil {
		t.Fatalf("decode live Docker info: %v", err)
	}
	if err := validateDaemonID(info.ID); err != nil {
		t.Fatalf("validate live Docker daemon ID: %v", err)
	}
	return info
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
		{output: contextOutput},
		{output: dockerInfoOutput(daemonID, 18, 25*bytesPerGiB)},
	}}
}

func authorityProbeRunner(
	contextOutput string,
	identityDaemonID string,
	capacityDaemonID string,
	logicalCPUs int64,
	memoryBytes int64,
) *daemonIdentityDockerRunnerStub {
	runner := identityProbeRunner(contextOutput, identityDaemonID)
	runner.results = append(runner.results, daemonIdentityDockerRunnerResult{
		output: dockerInfoOutput(capacityDaemonID, logicalCPUs, memoryBytes),
	})
	return runner
}

func clearDockerAuthorityEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		value, exists := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
		t.Cleanup(func() {
			var err error
			if exists {
				err = os.Setenv(name, value)
			} else {
				err = os.Unsetenv(name)
			}
			if err != nil {
				t.Errorf("restore %s: %v", name, err)
			}
		})
	}
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
