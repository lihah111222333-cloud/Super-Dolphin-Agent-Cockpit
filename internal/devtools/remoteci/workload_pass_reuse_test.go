package remoteci

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRemoteWorkerSemanticEnvironmentIsCanonicalAndOneHot(t *testing.T) {
	assignments := cicontract.CanonicalWorkerExecutionEnvironment()
	values, err := remoteWorkerSemanticEnvironmentValues(assignments)
	if err != nil {
		t.Fatalf("remoteWorkerSemanticEnvironmentValues() error = %v", err)
	}
	for key, want := range map[string]string{
		"CGO_ENABLED": "1",
		"GOOS":        "linux",
		"GOARCH":      "amd64",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
	} {
		if values[key] != want {
			t.Fatalf("canonical semantic env %s=%q, want %q", key, values[key], want)
		}
	}
	for _, assignment := range assignments {
		key, _, _ := strings.Cut(assignment, "=")
		forbidden := []string{"GOCACHE", "GOMODCACHE", "GOTMPDIR", "TMPDIR", "HOME", "XDG_CACHE_HOME", "PLAYWRIGHT_BROWSERS_PATH", ".CACHE/", "JOB", "AGENT", "TOKEN"}
		for _, fragment := range forbidden {
			if strings.Contains(strings.ToUpper(key), fragment) {
				t.Fatalf("semantic env contains non-correctness identity key %q", key)
			}
		}
	}
}

func TestRemoteWorkerSemanticEnvironmentRejectsMalformedOrDuplicate(t *testing.T) {
	for name, assignments := range map[string][]string{
		"missing value": {"GOOS="},
		"duplicate":     {"GOOS=linux", "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=1"},
		"missing key":   {"linux"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := remoteWorkerSemanticEnvironmentValues(assignments); err == nil {
				t.Fatalf("remoteWorkerSemanticEnvironmentValues(%v) unexpectedly succeeded", assignments)
			}
		})
	}
}

func TestRemoteWorkloadEnvironmentDigestBindsCanonicalMaterialAndWorkerProvenance(t *testing.T) {
	environment := cicontract.CanonicalWorkerExecutionEnvironment()
	base := remoteWorkloadEnvironment{
		SchemaVersion:             "remote-workload-pass-environment/v8",
		Platform:                  "linux/amd64",
		PolicyDigest:              "sha256:policy",
		ToolchainDigest:           "sha256:toolchain",
		RuntimeSeedSHA256:         "sha256:seed",
		CGOEnabled:                "1",
		GOOS:                      "linux",
		GOARCH:                    "amd64",
		SemanticEnvironmentSchema: cicontract.WorkerExecutionEnvironmentSchemaVersion,
		SemanticEnvironment:       environment,
		WorkerExecutionProvenance: cicontract.WorkerExecutionProvenanceID,
	}
	marshal := func(material remoteWorkloadEnvironment) string {
		encoded, err := json.Marshal(material)
		if err != nil {
			t.Fatalf("marshal environment material: %v", err)
		}
		return remoteWorkloadEnvironmentSHA256(encoded)
	}
	baseDigest := marshal(base)
	mutations := map[string]func(*remoteWorkloadEnvironment){
		"cgo":    func(value *remoteWorkloadEnvironment) { value.CGOEnabled = "0" },
		"goos":   func(value *remoteWorkloadEnvironment) { value.GOOS = "darwin" },
		"goarch": func(value *remoteWorkloadEnvironment) { value.GOARCH = "arm64" },
		"env member": func(value *remoteWorkloadEnvironment) {
			value.SemanticEnvironment = append(append([]string(nil), value.SemanticEnvironment...), "LANG=C")
		},
		"provenance": func(value *remoteWorkloadEnvironment) { value.WorkerExecutionProvenance = "other-provenance/v1" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if got := marshal(changed); got == baseDigest {
				t.Fatalf("environment mutation did not change digest: %q", got)
			}
		})
	}
}
