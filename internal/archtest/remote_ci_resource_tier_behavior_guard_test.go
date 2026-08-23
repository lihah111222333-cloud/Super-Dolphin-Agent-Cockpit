package archtest

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestHeterociCapacityGuardUsesTypedResources keeps capacity decisions inside
// the retained generic heteroci planner rather than a retired cloud tier table.
func TestHeterociCapacityGuardUsesTypedResources(t *testing.T) {
	manifest := newHeterogeneousCIManifest(1)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("heteroci manifest validation failed: %v", err)
	}
	if len(manifest.Nodes[0].Capabilities) != 1 {
		t.Fatal("heteroci node capacity must retain one typed capability")
	}
	if manifest.Nodes[0].Capabilities[0].Runtime != heteroci.RuntimeUbuntuContainer {
		t.Fatalf("heteroci runtime = %q, want %q", manifest.Nodes[0].Capabilities[0].Runtime, heteroci.RuntimeUbuntuContainer)
	}
}
