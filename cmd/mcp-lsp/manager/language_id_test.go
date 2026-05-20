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
