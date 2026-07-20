package gateclosure

import "testing"

func TestRuntimeDepsOCIIndexRejectsMalformedInputs(t *testing.T) {
	indexDocument := registryManifest{Digest: testDigest, MediaType: ociIndexMediaType}
	index := ociIndex{SchemaVersion: 2, MediaType: ociIndexMediaType}
	descriptor := testRuntimeDepsOCIDescriptor()
	tests := []struct {
		name  string
		check func() error
	}{
		{
			name: "index media type",
			check: func() error {
				invalid := index
				invalid.MediaType = ociManifestMediaType
				return validateRuntimeDepsIndexDocument(indexDocument, invalid)
			},
		},
		{
			name: "duplicate platform descriptor",
			check: func() error {
				_, err := runtimeDepsDescriptorsByPlatform([]ociDescriptor{descriptor, descriptor}, runtimeDepsPlatforms)
				return err
			},
		},
		{
			name: "missing platform descriptor",
			check: func() error {
				_, err := runtimeDepsDescriptorsByPlatform([]ociDescriptor{descriptor}, runtimeDepsPlatforms)
				return err
			},
		},
		{
			name: "invalid descriptor identity",
			check: func() error {
				invalid := descriptor
				invalid.Digest = "latest"
				_, err := runtimeDepsDescriptorsByPlatform([]ociDescriptor{invalid}, runtimeDepsPlatforms)
				return err
			},
		},
	}
	assertRuntimeDepsOCIFailures(t, tests)
}

func TestRuntimeDepsOCIManifestAndConfigRejectMalformedInputs(t *testing.T) {
	descriptor := testRuntimeDepsOCIDescriptor()
	manifest := testRuntimeDepsOCIManifest()
	config := testRuntimeDepsOCIConfig()
	tests := []struct {
		name  string
		check func() error
	}{
		{
			name: "platform manifest digest mismatch",
			check: func() error {
				document := registryManifest{Digest: testDigest, MediaType: ociManifestMediaType}
				return validateRuntimeDepsPlatformManifest(document, descriptor, manifest)
			},
		},
		{
			name: "invalid config digest",
			check: func() error {
				invalid := manifest
				invalid.Config.Digest = "latest"
				return validateRuntimeDepsPlatformManifest(registryManifest{Digest: descriptor.Digest, MediaType: ociManifestMediaType}, descriptor, invalid)
			},
		},
		{
			name: "rootfs layer mismatch",
			check: func() error {
				invalid := config
				invalid.RootFS.DiffIDs = nil
				return validateRuntimeDepsConfig(invalid, manifest, "linux/amd64")
			},
		},
	}
	assertRuntimeDepsOCIFailures(t, tests)
}

func TestRuntimeDepsOCIImageSizeRejectsInvalidLayers(t *testing.T) {
	manifest := testRuntimeDepsOCIManifest()
	tests := []struct {
		name  string
		check func() error
	}{
		{
			name: "layer size overflow",
			check: func() error {
				invalid := manifest
				invalid.Config.Size = int64(^uint64(0) >> 1)
				_, err := runtimeDepsImageSize(invalid)
				return err
			},
		},
		{
			name: "nonpositive layer size",
			check: func() error {
				invalid := manifest
				invalid.Layers = append([]struct {
					Digest string `json:"digest"`
					Size   int64  `json:"size"`
				}(nil), manifest.Layers...)
				invalid.Layers[0].Size = 0
				_, err := runtimeDepsImageSize(invalid)
				return err
			},
		},
	}
	assertRuntimeDepsOCIFailures(t, tests)
}

func assertRuntimeDepsOCIFailures(t *testing.T, tests []struct {
	name  string
	check func() error
}) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.check(); err == nil {
				t.Fatal("malformed OCI input unexpectedly passed")
			}
		})
	}
}

func testRuntimeDepsOCIDescriptor() ociDescriptor {
	return ociDescriptor{
		MediaType: ociManifestMediaType,
		Digest:    testDigestB,
		Size:      1,
		Platform:  ociPlatform{OS: "linux", Architecture: "amd64"},
	}
}

func testRuntimeDepsOCIManifest() ociManifest {
	manifest := ociManifest{SchemaVersion: 2}
	manifest.Config.Digest = testDigestC
	manifest.Config.Size = 1
	manifest.Layers = append(manifest.Layers, struct {
		Digest string `json:"digest"`
		Size   int64  `json:"size"`
	}{Digest: testDigestD, Size: 1})
	return manifest
}

func testRuntimeDepsOCIConfig() ociImageConfig {
	config := ociImageConfig{Architecture: "amd64", OS: "linux"}
	config.RootFS.Type = "layers"
	config.RootFS.DiffIDs = []string{testDigestD}
	return config
}
