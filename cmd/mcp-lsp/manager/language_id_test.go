package manager

import "testing"

func TestDetectLanguageIDReactExtensions(t *testing.T) {
	cases := map[string]string{
		"component.jsx": "javascriptreact",
		"component.tsx": "typescriptreact",
	}
	for file, want := range cases {
		if got := DetectLanguageID(file); got != want {
			t.Fatalf("DetectLanguageID(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestDetectLanguageIDDockerBaseNames(t *testing.T) {
	cases := map[string]string{
		"Containerfile":        "dockerfile",
		"Dockerfile":           "dockerfile",
		"deploy/Containerfile": "dockerfile",
		"deploy/Dockerfile":    "dockerfile",
	}
	for file, want := range cases {
		if got := DetectLanguageID(file); got != want {
			t.Fatalf("DetectLanguageID(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestDetectLanguageIDShellExtensionsUseShellscript(t *testing.T) {
	cases := map[string]string{
		"script.sh":     "shellscript",
		"script.bash":   "shellscript",
		"script.zsh":    "shellscript",
		"script.ksh":    "shellscript",
		"script.bats":   "shellscript",
		"SCRIPT.BASH":   "shellscript",
		"nested/run.sh": "shellscript",
	}
	for file, want := range cases {
		if got := DetectLanguageID(file); got != want {
			t.Fatalf("DetectLanguageID(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestDetectLanguageIDGitHooksUseShellscript(t *testing.T) {
	cases := map[string]string{
		".githooks/pre-commit":                "shellscript",
		".githooks/pre-push":                  "shellscript",
		".githooks/custom-check":              "shellscript",
		".githooks/README.md":                 "markdown",
		"/repo/.githooks/commit-msg":          "shellscript",
		"/repo/.git/hooks/prepare-commit-msg": "shellscript",
		"scripts/pre-commit":                  "",
	}
	for file, want := range cases {
		if got := DetectLanguageID(file); got != want {
			t.Fatalf("DetectLanguageID(%q) = %q, want %q", file, got, want)
		}
	}
}
