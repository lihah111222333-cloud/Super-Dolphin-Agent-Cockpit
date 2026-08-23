package heteroci

import (
	"strings"
	"testing"
)

func testPhysicalHostAttestation() PhysicalHostAttestation {
	attestation := PhysicalHostAttestation{
		SchemaVersion: PhysicalHostAttestationSchemaVersion,
		OSID:          "ubuntu", Machine: "x86_64",
		MachineIDSHA256:   "sha256:" + strings.Repeat("1", 64),
		OSReleaseIdentity: "sha256:" + strings.Repeat("2", 64),
		UnameIdentity:     "sha256:" + strings.Repeat("3", 64),
		DockerOSType:      "linux", DockerArchitecture: "x86_64",
	}
	attestation.Digest = attestation.CanonicalDigest()
	return attestation
}

func TestPhysicalHostAttestationRejectsNonUbuntuMachineDockerOrDigestDrift(t *testing.T) {
	base := testPhysicalHostAttestation()
	mutations := []struct {
		name   string
		mutate func(*PhysicalHostAttestation)
	}{
		{name: "non Ubuntu", mutate: func(value *PhysicalHostAttestation) { value.OSID = "debian" }},
		{name: "non x86_64 machine", mutate: func(value *PhysicalHostAttestation) { value.Machine = "aarch64" }},
		{name: "non linux Docker", mutate: func(value *PhysicalHostAttestation) { value.DockerOSType = "windows" }},
		{name: "non x86_64 Docker", mutate: func(value *PhysicalHostAttestation) { value.DockerArchitecture = "arm64" }},
		{name: "digest drift", mutate: func(value *PhysicalHostAttestation) { value.Digest = "sha256:" + "f" + value.Digest[len("sha256:")+1:] }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := base
			mutation.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("PhysicalHostAttestation.Validate unexpectedly accepted drift")
			}
		})
	}
	changed := base
	changed.Machine = "x86_64"
	changed.DockerArchitecture = "x86_64"
	changed.OSID = "ubuntu"
	changed.Digest = changed.CanonicalDigest()
	if changed.CanonicalDigest() != base.CanonicalDigest() {
		t.Fatal("equivalent physical attestation facts changed digest")
	}
}
