package search

import "testing"

func TestInferLanguage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "go source", input: "main.go", want: "go"},
		{name: "javascript alias", input: "component.JSX", want: "javascript"},
		{name: "typescript alias", input: "handler.mts", want: "typescript"},
		{name: "markdown alias", input: "README.markdown", want: "markdown"},
		{name: "yaml alias", input: "config.yml", want: "yaml"},
		{name: "no extension", input: "Makefile", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := inferLanguage(tc.input); got != tc.want {
				t.Fatalf("inferLanguage(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeASTLanguage(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		target  string
		isDir   bool
		glob    string
		want    string
		wantErr bool
	}{
		{name: "canonical alias", raw: "golang", want: "go"},
		{name: "infer from target file", target: "/tmp/query.tsx", want: "typescript"},
		{name: "infer from glob", target: "/tmp/project", isDir: true, glob: "**/*.py", want: "python"},
		{name: "missing language", target: "/tmp/project", isDir: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeASTLanguage(tc.raw, tc.target, tc.isDir, tc.glob)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeASTLanguage(%q, %q, %t, %q) expected error", tc.raw, tc.target, tc.isDir, tc.glob)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeASTLanguage(%q, %q, %t, %q) returned error: %v", tc.raw, tc.target, tc.isDir, tc.glob, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeASTLanguage(%q, %q, %t, %q) = %q, want %q", tc.raw, tc.target, tc.isDir, tc.glob, got, tc.want)
			}
		})
	}
}
