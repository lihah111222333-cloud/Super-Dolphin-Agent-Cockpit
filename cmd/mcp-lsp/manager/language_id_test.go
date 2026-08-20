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

func TestDetectLanguageIDProtoExtension(t *testing.T) {
	for _, file := range []string{"schema.proto", "PROTO/messages.PROTO"} {
		if got := DetectLanguageID(file); got != "proto" {
			t.Fatalf("DetectLanguageID(%q) = %q, want proto", file, got)
		}
	}
}

func TestDetectLanguageIDMQLUsesClangdCpp(t *testing.T) {
	cases := map[string]string{
		"Experts/legacy.mq4": "cpp",
		"Experts/robot.mq5":  "cpp",
		"Include/common.MQH": "cpp",
		"Experts/legacy.mql": "cpp",
		"Experts/robot.mql4": "cpp",
		"Experts/robot.mql5": "cpp",
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

func TestLanguageIDForExtensionResolvesAllCategoriesWithoutRegistryState(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		".go":        "go",
		".jsx":       "javascriptreact",
		".html":      "html",
		".scss":      "css",
		".cpp":       "cpp",
		".mq4":       "cpp",
		".mq5":       "cpp",
		".mqh":       "cpp",
		".mql":       "cpp",
		".mql4":      "cpp",
		".mql5":      "cpp",
		".swift":     "swift",
		".py":        "python",
		".php":       "php",
		".rb":        "ruby",
		".rs":        "rust",
		".java":      "java",
		".sh":        "shellscript",
		".terraform": "",
		".tf":        "terraform",
		".graphql":   "graphql",
		".proto":     "proto",
		".md":        "markdown",
		".json":      "json",
		".yaml":      "yaml",
	}
	for ext, want := range cases {
		got, ok := languageIDForExtension(ext)
		if want == "" {
			if ok {
				t.Fatalf("languageIDForExtension(%q) = %q, true; want unknown", ext, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("languageIDForExtension(%q) = %q, %t; want %q, true", ext, got, ok, want)
		}
	}
}
