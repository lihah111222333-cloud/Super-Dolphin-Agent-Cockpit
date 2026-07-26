package localci

import (
	"strings"
	"testing"
)

type imageIdentityStub struct {
	indexDigest    string
	manifestDigest string
	configDigest   string
	diffIDs        []string
	os             string
	architecture   string
	variant        string
}

func (stub imageIdentityStub) OCIIndexDigest() string         { return stub.indexDigest }
func (stub imageIdentityStub) PlatformManifestDigest() string { return stub.manifestDigest }
func (stub imageIdentityStub) ConfigDigest() string           { return stub.configDigest }
func (stub imageIdentityStub) RootFSDiffIDs() []string        { return stub.diffIDs }
func (stub imageIdentityStub) OS() string                     { return stub.os }
func (stub imageIdentityStub) Architecture() string           { return stub.architecture }
func (stub imageIdentityStub) Variant() string                { return stub.variant }

func TestValidateImageIdentityRequiresCompleteImmutableIdentity(t *testing.T) {
	identity := validImageIdentityStub()
	expected := expectedImageMetadata{
		PolicyDigest:    digest("8"),
		SourceTreeSHA:   strings.Repeat("b", 40),
		InputDigest:     digest("6"),
		ToolchainDigest: digest("7"),
		SchemaVersion:   "1",
		OS:              "linux",
		Architecture:    "amd64",
	}
	labels := expected.labels()

	if err := validateImageIdentity(identity, labels, expected); err != nil {
		t.Fatalf("validateImageIdentity() error = %v", err)
	}

	identity.configDigest = "sha256:not-a-digest"
	if err := validateImageIdentity(identity, labels, expected); err == nil {
		t.Fatal("validateImageIdentity() accepted malformed config digest")
	}
}

func TestValidateImageIdentityRejectsLabelAndPlatformDrift(t *testing.T) {
	identity := validImageIdentityStub()
	expected := expectedImageMetadata{
		PolicyDigest:    digest("8"),
		SourceTreeSHA:   strings.Repeat("b", 40),
		InputDigest:     digest("6"),
		ToolchainDigest: digest("7"),
		SchemaVersion:   "1",
		OS:              "linux",
		Architecture:    "amd64",
	}
	labels := expected.labels()
	labels[labelPolicySHA] = digest("9")
	if err := validateImageIdentity(identity, labels, expected); err == nil {
		t.Fatal("validateImageIdentity() accepted policy label drift")
	}

	labels = expected.labels()
	identity.architecture = "arm64"
	if err := validateImageIdentity(identity, labels, expected); err == nil {
		t.Fatal("validateImageIdentity() accepted platform drift")
	}
}

func validImageIdentityStub() imageIdentityStub {
	return imageIdentityStub{
		indexDigest:    digest("1"),
		manifestDigest: digest("2"),
		configDigest:   digest("3"),
		diffIDs:        []string{digest("4"), digest("5")},
		os:             "linux",
		architecture:   "amd64",
	}
}

func TestValidateContainerCapabilityIsolationRejectsStorageOptions(t *testing.T) {
	host := &containerHostConfig{
		CapDrop:     []string{"ALL"},
		NetworkMode: noContainerNetwork,
		StorageOpt:  map[string]string{},
	}
	if err := validateContainerCapabilityIsolation(host); err != nil {
		t.Fatalf("empty storage options were rejected: %v", err)
	}
	host.StorageOpt["size"] = "10G"
	if err := validateContainerCapabilityIsolation(host); err == nil {
		t.Fatal("backend-specific writable layer storage option was accepted")
	}
}

func digest(character string) string { return "sha256:" + strings.Repeat(character, 64) }
