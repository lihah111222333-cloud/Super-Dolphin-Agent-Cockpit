package unified_test

import (
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildManifest_DefaultFamilies(t *testing.T) {
	binaryDir := "/tmp/default-bin"
	got := dto.BuildManifest(dto.ManifestContext{BinaryDir: binaryDir})
	if len(got.Binaries) != 2 || got.Binaries[0].Name != "go-agent-mcp-lsp" || got.Binaries[1].Name != "go-agent-mcp-orch" {
		t.Fatalf("unexpected default manifest: %+v", got.Binaries)
	}
	for _, bin := range got.Binaries {
		if len(bin.Command) == 0 {
			t.Errorf("binary %q has empty Command", bin.Name)
		}
		if len(bin.Command) > 0 && !strings.Contains(bin.Command[0], binaryDir) {
			t.Errorf("binary %q Command should contain BinaryDir", bin.Name)
		}
	}
}

func TestBuildManifest_WithIDA(t *testing.T) {
	got := dto.BuildManifest(dto.ManifestContext{ThreadCaps: dto.CapabilitySet{"ida": true}})
	if len(got.Binaries) != 3 || got.Binaries[2].Name != "go-agent-mcp-ida" {
		t.Fatalf("unexpected ida manifest: %+v", got.Binaries)
	}
}

func TestBuildManifest_BinaryPaths(t *testing.T) {
	got := dto.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	want := []string{
		filepath.Join("/tmp/bin", "go-agent-mcp-lsp"),
		filepath.Join("/tmp/bin", "go-agent-mcp-orch"),
	}
	for i, binary := range got.Binaries {
		if len(binary.Command) != 1 || binary.Command[0] != want[i] {
			t.Fatalf("unexpected binary command: %+v", got.Binaries)
		}
	}
}

func TestBuildManifest_EmptyBinaryDirUsesRelativeCommands(t *testing.T) {
	got := dto.BuildManifest(dto.ManifestContext{})
	want := []string{
		filepath.Join("", "go-agent-mcp-lsp"),
		filepath.Join("", "go-agent-mcp-orch"),
	}
	for i, binary := range got.Binaries {
		if len(binary.Command) != 1 || binary.Command[0] != want[i] {
			t.Fatalf("unexpected binary command: %+v", got.Binaries)
		}
	}
}
