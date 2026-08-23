package archtest

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

// TestRemoteRuntimeDockerHostFieldChainGuard keeps the caller-supplied Docker
// endpoint on the runtime producer, policy digest, transport and daemon edges.
func TestRemoteRuntimeDockerHostFieldChainGuard(t *testing.T) {
	typeOfBinding := reflect.TypeFor[remoteruntime.LinuxContainerBinding]()
	field, ok := typeOfBinding.FieldByName("DockerHost")
	if !ok {
		t.Fatal("LinuxContainerBinding.DockerHost producer field is missing")
	}
	jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if jsonName != "docker_host" {
		t.Fatalf("LinuxContainerBinding.DockerHost JSON field = %q, want docker_host", jsonName)
	}
	serverField, ok := typeOfBinding.FieldByName("DockerServerID")
	if !ok {
		t.Fatal("LinuxContainerBinding.DockerServerID producer field is missing")
	}
	serverJSONName, _, _ := strings.Cut(serverField.Tag.Get("json"), ",")
	if serverJSONName != "docker_server_id" {
		t.Fatalf("LinuxContainerBinding.DockerServerID JSON field = %q, want docker_server_id", serverJSONName)
	}
	encoded, err := json.Marshal(remoteruntime.LinuxContainerBinding{DockerHost: "unix:///run/docker.sock", DockerServerID: "docker-server-fixture"})
	if err != nil {
		t.Fatalf("marshal DockerHost producer: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode DockerHost producer: %v", err)
	}
	if _, ok := wire["docker_host"]; !ok {
		t.Fatalf("DockerHost producer omitted docker_host: %s", encoded)
	}
	if _, ok := wire["docker_server_id"]; !ok {
		t.Fatalf("DockerServerID producer omitted docker_server_id: %s", encoded)
	}
	for _, fieldName := range []string{"docker_host", "docker_server_id"} {
		if !slices.Contains(remoteruntime.LinuxContainerFields(), fieldName) {
			t.Fatalf("LinuxContainerFields consumer registry omitted %s", fieldName)
		}
	}
	for name, typ := range map[string]reflect.Type{
		"ProbeResponse": reflect.TypeFor[heteroci.ProbeResponse](),
		"NodeSnapshot":  reflect.TypeFor[heteroci.NodeSnapshot](),
		"Facts":         reflect.TypeFor[heterocinode.Facts](),
	} {
		field, ok := typ.FieldByName("PhysicalAttestation")
		if !ok {
			t.Fatalf("%s producer field PhysicalAttestation is missing", name)
		}
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name != "Facts" && jsonName != "physical_attestation" {
			t.Fatalf("%s PhysicalAttestation JSON field = %q", name, jsonName)
		}
	}
	for _, fieldName := range []string{"MachineIDSHA256", "OSReleaseIdentity", "UnameIdentity"} {
		if _, ok := reflect.TypeFor[heteroci.PhysicalHostAttestation]().FieldByName(fieldName); !ok {
			t.Fatalf("PhysicalHostAttestation producer field %s is missing", fieldName)
		}
	}

	root := findRepoRoot(t)
	markers := map[string][]string{
		"internal/devtools/remoteruntime/config.go": {
			"DockerHost          string `json:\"docker_host\"`",
			"DockerServerID      string `json:\"docker_server_id\"`",
			"ConfigSchemaVersion = \"remote-ci-runtime-config/v3\"",
			"RuntimePolicyDigestDomain = \"remote-ci-runtime-policy/v3\"",
			"validateDockerHost(binding.DockerHost)",
			"validateSHA256Identity(value)",
			"validateImmutableDockerImage(binding.DockerImage)",
		},
		"internal/devtools/heteroci/probe_types.go": {
			"ProbeResponseSchemaVersion = \"heteroci-probe-response/v3\"",
			"PhysicalAttestation PhysicalHostAttestation `json:\"physical_attestation\"`",
		},
		"internal/devtools/heteroci/physical_attestation.go": {
			"PhysicalHostAttestationSchemaVersion = \"heteroci-physical-host-attestation/v2\"",
			"MachineIDSHA256",
			"OSReleaseIdentity",
			"UnameIdentity",
		},
		"internal/devtools/heterocinode/probe.go": {
			"ConfigSchemaVersion = \"heteroci-node-probe-config/v4\"",
		},
		"internal/devtools/heteroci/snapshot.go": {
			"SnapshotSchemaVersion = \"heteroci-snapshot/v5\"",
			"PhysicalAttestation PhysicalHostAttestation    `json:\"physical_attestation\"`",
		},
		"internal/devtools/remoteruntime/transport.go": {
			"\"--docker-host\", node.Linux.DockerHost",
			"\"--docker-server-id\", node.Linux.DockerServerID",
			"\"--runner-identity\", node.RunnerIdentity",
		},
		"internal/devtools/heterocinode/linux_facts.go": {
			"DockerHost",
			"DockerServerID",
			"OSReleasePath",
			"UnameExecutable",
			"MachineIDPath",
			"MachineIDSHA256",
			"OSReleaseIdentity",
			"UnameIdentity",
			"ValidateSHA256Identity",
			"provider.attestor.Attest(ctx)",
			"info", "--format", "{{.ID}}",
			"DOCKER_HOST=",
		},
		"internal/devtools/heterocinode/linux_attestation.go": {
			"readUbuntuOSRelease",
			"readMachineID",
			"readLinuxMachine",
			"readStableRegularFile",
			"machine-id content or identity does not match caller receipt",
			"linux x86_64",
			"os.Lstat",
		},
		"cmd/super-dolphin-heteroci-node/cli.go": {
			"DockerHost",
			"DockerServerID",
			"OSReleasePath",
			"UnameExecutable",
			"MachineIDPath",
			"MachineIDSHA256",
			"OSReleaseIdentity",
			"UnameIdentity",
			"config.Linux.DockerHost",
			"config.Linux.DockerServerID",
			"attestProbeExecutable",
			"io.LimitReader",
			"os.SameFile",
		},
		"scripts/deploy/linux/linux_runner_daemon.sh": {
			"docker_host_seen=0",
			"--docker-host must be provided once",
			"validate_docker_host \"$docker_host\"",
			"unset DOCKER_CONTEXT",
			"export DOCKER_HOST=\"$docker_host\"",
			"--docker-server-id must be provided once",
			"--runner-identity must be provided once",
			"attest_runner_identity",
			"info --format '{{.ID}}'",
			"-S \"$socket_path\" && ! -L \"$socket_path\"",
		},
	}
	for relative, required := range markers {
		source := readRemoteCIContractGuardFile(t, root+"/"+relative)
		for _, marker := range required {
			if !strings.Contains(source, marker) {
				t.Errorf("%s is missing DockerHost field-chain marker %q", relative, marker)
			}
		}
	}
}
