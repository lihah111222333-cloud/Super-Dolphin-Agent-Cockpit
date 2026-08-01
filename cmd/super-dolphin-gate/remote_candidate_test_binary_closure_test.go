package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSemanticGoTestCompileClosureHashesOnlyCandidateFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "external.go"), []byte("package external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := fmt.Appendf(nil, `{"ImportPath":"example/candidate","Dir":%q,"GoFiles":["pkg.go"]}
{"ImportPath":"example/external","Dir":%q,"GoFiles":["external.go"]}
`, root, external)
	first, err := semanticGoTestCompileClosure(root, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "external.go"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := semanticGoTestCompileClosure(root, data)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("external module input changed closure: %s != %s", first, second)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg.go"), []byte("package candidate // changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := semanticGoTestCompileClosure(root, data)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("candidate source change did not change closure")
	}
}
