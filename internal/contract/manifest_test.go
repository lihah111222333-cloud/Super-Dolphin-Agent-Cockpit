package contract

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildManifestPassesModelRegistryEnvFromProcessToStdioBinaries(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_MODEL_REGISTRY", "/bundle/models.yaml")

	manifest := BuildManifest(dto.ManifestContext{
		BinaryDir:     "/bundle/bin",
		TransportMode: dto.ManifestTransportStdioOnly,
	})

	if len(manifest.Binaries) == 0 {
		t.Fatal("BuildManifest() returned no binaries")
	}
	for _, bin := range manifest.Binaries {
		if got := bin.Env["SUPER_DOLPHIN_MODEL_REGISTRY"]; got != "/bundle/models.yaml" {
			t.Fatalf("binary %s SUPER_DOLPHIN_MODEL_REGISTRY = %q, want /bundle/models.yaml", bin.Name, got)
		}
	}
}
