package heteroci

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const PhysicalHostAttestationSchemaVersion = "heteroci-physical-host-attestation/v2"

// PhysicalHostAttestation is the live, caller-independent identity proof for the one Ubuntu host.
type PhysicalHostAttestation struct {
	SchemaVersion      string `json:"schema_version"`
	OSID               string `json:"os_id"`
	Machine            string `json:"machine"`
	MachineIDSHA256    string `json:"machine_id_sha256"`
	OSReleaseIdentity  string `json:"os_release_identity"`
	UnameIdentity      string `json:"uname_identity"`
	DockerOSType       string `json:"docker_os_type"`
	DockerArchitecture string `json:"docker_architecture"`
	Digest             string `json:"digest"`
}

// Validate enforces the exact Ubuntu/linux/amd64 physical host facts and self-digest.
func (attestation PhysicalHostAttestation) Validate() error {
	if attestation.SchemaVersion != PhysicalHostAttestationSchemaVersion {
		return errors.New("physical host attestation schema version is invalid")
	}
	if attestation.OSID != "ubuntu" {
		return errors.New("physical host attestation OS is not exact Ubuntu")
	}
	if attestation.Machine != "x86_64" {
		return errors.New("physical host attestation machine is not exact x86_64")
	}
	if !isCanonicalSHA256(attestation.MachineIDSHA256) || !isCanonicalSHA256(attestation.OSReleaseIdentity) || !isCanonicalSHA256(attestation.UnameIdentity) {
		return errors.New("physical host attestation file identities are invalid")
	}
	if attestation.DockerOSType != "linux" {
		return errors.New("physical host attestation Docker OS is not linux")
	}
	if attestation.DockerArchitecture != "x86_64" {
		return errors.New("physical host attestation Docker architecture is not x86_64")
	}
	if !isCanonicalSHA256(attestation.Digest) || attestation.Digest != attestation.CanonicalDigest() {
		return errors.New("physical host attestation digest is invalid")
	}
	return nil
}

// CanonicalDigest returns the domain-separated digest of the attestation facts without Digest itself.
func (attestation PhysicalHostAttestation) CanonicalDigest() string {
	material := strings.Join([]string{
		"heteroci-physical-host-attestation/v2",
		attestation.SchemaVersion,
		attestation.OSID,
		attestation.Machine,
		attestation.MachineIDSHA256,
		attestation.OSReleaseIdentity,
		attestation.UnameIdentity,
		attestation.DockerOSType,
		attestation.DockerArchitecture,
	}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func isCanonicalSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
