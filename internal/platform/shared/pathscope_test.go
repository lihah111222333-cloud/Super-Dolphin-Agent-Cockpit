package shared

import (
	"path/filepath"
	"testing"
)

func TestNormalizeRelativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trim and clean", input: "  nested/../file.txt  ", want: "file.txt"},
		{name: "empty becomes dot", input: "   ", want: "."},
		{name: "current directory stays dot", input: "./", want: "."},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeRelativePath(tc.input); got != tc.want {
				t.Fatalf("NormalizeRelativePath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestContainsPath(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "tmp", "root")
	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{name: "same root", target: root, want: true},
		{name: "nested path", target: filepath.Join(root, "dir", "file.txt"), want: true},
		{name: "sibling prefix does not match", target: root + "-other", want: false},
		{name: "outside path", target: filepath.Join(string(filepath.Separator), "tmp", "other"), want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ContainsPath(root, tc.target); got != tc.want {
				t.Fatalf("ContainsPath(%q, %q) = %v, want %v", root, tc.target, got, tc.want)
			}
		})
	}
}
