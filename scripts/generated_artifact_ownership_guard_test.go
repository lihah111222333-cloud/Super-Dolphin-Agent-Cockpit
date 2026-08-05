package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedArtifactRefreshListsOwnedOutputsByMode(t *testing.T) {
	script := filepath.Join(scriptRepoRoot(t), "scripts", "refresh_generated_artifacts.sh")
	tests := []struct {
		mode string
		want []string
	}{
		{
			mode: "codemap",
			want: []string{
				"file\tREADME.md",
				"file\tdocs/doc/codemap/13-archtest-boundaries.md",
				"file\tdocs/doc/codemap/README.md",
				"file\tdocs/doc/codemap/ai-index.json",
				"file\tdocs/doc/codemap/anchor-identities.json",
			},
		},
		{
			mode: "capcontract",
			want: []string{
				"file\tdocs/doc/codemap/capability-contract/capability_manifest.json",
			},
		},
		{
			mode: "project-map",
			want: []string{
				"tree\tdocs/doc/codemap/project-map",
			},
		},
		{
			mode: "all",
			want: []string{
				"file\tREADME.md",
				"file\tdocs/doc/codemap/13-archtest-boundaries.md",
				"file\tdocs/doc/codemap/README.md",
				"file\tdocs/doc/codemap/ai-index.json",
				"file\tdocs/doc/codemap/anchor-identities.json",
				"file\tdocs/doc/codemap/capability-contract/capability_manifest.json",
				"tree\tdocs/doc/codemap/project-map",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			cmd := exec.Command("bash", script, tt.mode, "--list-outputs")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("list generated outputs for %s: %v\n%s", tt.mode, err, output)
			}
			got := strings.Split(strings.TrimSpace(string(output)), "\n")
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("owned output contract for %s mismatch\nwant:\n%s\ngot:\n%s", tt.mode, strings.Join(tt.want, "\n"), strings.Join(got, "\n"))
			}
		})
	}
}
