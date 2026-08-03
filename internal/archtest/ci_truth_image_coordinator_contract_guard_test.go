package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteHooksDoNotRetainShardCapOverrides(t *testing.T) {
	root := coordinatorContractRepoRoot(t)
	for _, hook := range []string{"pre-commit", "pre-push"} {
		source := readCoordinatorContractFile(t, filepath.Join(root, ".githooks", hook))
		for _, forbidden := range []string{"super-dolphin.remote.maxShards", "--max-shards"} {
			if strings.Contains(source, forbidden) {
				t.Errorf("%s must not retain removed shard-cap override %q", hook, forbidden)
			}
		}
	}
}

// TestCIWorkflowBootstrapsOnlyTheImmutableCoordinator keeps the exceptional
// Docker boundary narrow: the protected workflow may start its immutable
// bootstrap coordinator, but candidate checkout scripts may not run Docker.
func TestCIWorkflowBootstrapsOnlyTheImmutableCoordinator(t *testing.T) {
	root := coordinatorContractRepoRoot(t)
	workflow := readCoordinatorContractFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	if strings.Count(workflow, "docker run") != 1 || !strings.Contains(workflow, "workflow-host") {
		t.Fatalf("protected workflow must have exactly one immutable coordinator bootstrap: %q", workflow)
	}
	candidate := readCoordinatorContractFile(t, filepath.Join(root, "scripts", "ci_truth_image_gate.sh"))
	if strings.Contains(candidate, "docker run") || strings.Contains(candidate, "go test") || strings.Contains(candidate, "make guard") {
		t.Fatal("candidate CI script must delegate to the coordinator instead of running canonical gates on the host")
	}
}

func TestMakefileCIEntrypointsDelegateToCoordinator(t *testing.T) {
	makefile := readCoordinatorContractFile(t, filepath.Join(coordinatorContractRepoRoot(t), "Makefile"))
	recipes := makefileCITargetRecipes(makefile)
	wantProfiles := map[string]string{
		"ci-l0":         "local-fast",
		"ci-l1":         "push",
		"ci-l2-claude":  "remote-required",
		"ci-l3-release": "release",
	}
	if len(recipes) != len(wantProfiles) {
		t.Fatalf("Makefile CI targets = %#v, want exact L0-L3 coordinator entrypoints", recipes)
	}
	for target, profile := range wantProfiles {
		recipe, ok := recipes[target]
		if !ok {
			t.Errorf("Makefile is missing coordinator target %q", target)
			continue
		}
		joined := strings.Join(recipe, "\n")
		if strings.Contains(joined, "go test") || strings.Contains(joined, "docker run") || strings.Contains(joined, "make guard") || strings.Contains(joined, "$(TEST_WITH_GUARD)") {
			t.Errorf("Makefile target %q runs a canonical gate outside the coordinator: %q", target, joined)
		}
		want := "./scripts/ci_truth_image_gate.sh " + profile
		if len(recipe) != 1 || !strings.Contains(joined, want) {
			t.Errorf("Makefile target %q recipe = %q, want one %q delegation", target, joined, want)
		}
	}
}

func coordinatorContractRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(directory, "..", ".."))
}

func readCoordinatorContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func makefileCITargetRecipes(makefile string) map[string][]string {
	recipes := make(map[string][]string)
	current := ""
	for line := range strings.SplitSeq(makefile, "\n") {
		if strings.HasPrefix(line, "\t") && current != "" {
			recipes[current] = append(recipes[current], strings.TrimSpace(line))
			continue
		}
		current = ""
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") {
			continue
		}
		target, _, found := strings.Cut(line, ":")
		if !found || (target != "ci" && !strings.HasPrefix(target, "ci-")) {
			continue
		}
		current = target
		recipes[current] = nil
	}
	return recipes
}
