package codemapindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnchorManifestDetectsContentMutationWithoutLineMovement(t *testing.T) {
	root := t.TempDir()
	writeAnchorTestFile(t, root, "internal/demo/demo.go", "package demo\n\nfunc Run() string {\n\treturn \"before\"\n}\n")
	docs := []SemanticMarkdown{{
		File:  "01-demo.md",
		Lines: []string{"# Demo", "- implementation: `internal/demo/demo.go:3-5`"},
	}}

	before, err := BuildAnchorManifest(root, docs)
	if err != nil {
		t.Fatalf("BuildAnchorManifest(before) error = %v", err)
	}
	if len(before.Anchors) != 1 || before.Anchors[0].Symbol != "Run" {
		t.Fatalf("anchors = %#v, want one Run identity", before.Anchors)
	}
	data, err := MarshalAnchorManifest(before)
	if err != nil {
		t.Fatalf("MarshalAnchorManifest() error = %v", err)
	}

	writeAnchorTestFile(t, root, "internal/demo/demo.go", "package demo\n\nfunc Run() string {\n\treturn \"after!\"\n}\n")
	after, err := BuildAnchorManifest(root, docs)
	if err != nil {
		t.Fatalf("BuildAnchorManifest(after) error = %v", err)
	}
	if before.Anchors[0].ContentSHA256 == after.Anchors[0].ContentSHA256 {
		t.Fatal("content mutation did not change content_sha256")
	}
	if err := ValidateAnchorManifest(data, after); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("ValidateAnchorManifest() error = %v, want stale mutation failure", err)
	}
}

func TestAnchorManifestRejectsUnknownSchemaAndUnknownField(t *testing.T) {
	expected := AnchorManifest{
		SchemaVersion: AnchorManifestSchemaVersion,
		Generator:     AnchorManifestGenerator,
		Anchors:       []AnchorIdentity{},
	}
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "future schema",
			data: `{"schema_version":2,"generator":"` + AnchorManifestGenerator + `","anchors":[]}`,
			want: "unsupported",
		},
		{
			name: "unknown field",
			data: `{"schema_version":1,"generator":"` + AnchorManifestGenerator + `","anchors":[],"fallback":true}`,
			want: "unknown field",
		},
		{
			name: "trailing value",
			data: `{"schema_version":1,"generator":"` + AnchorManifestGenerator + `","anchors":[]} {}`,
			want: "trailing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAnchorManifest([]byte(test.data), expected)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateAnchorManifest() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAnchorManifestRoundTripIsCanonical(t *testing.T) {
	expected := AnchorManifest{
		SchemaVersion: AnchorManifestSchemaVersion,
		Generator:     AnchorManifestGenerator,
		Anchors: []AnchorIdentity{{
			CodemapFile:   "01-demo.md",
			CodemapLine:   2,
			TargetPath:    "internal/demo/demo.go",
			LineSpec:      "3",
			ContentSHA256: strings.Repeat("a", 64),
			Symbol:        "Run",
		}},
	}
	data, err := MarshalAnchorManifest(expected)
	if err != nil {
		t.Fatalf("MarshalAnchorManifest() error = %v", err)
	}
	if err := ValidateAnchorManifest(data, expected); err != nil {
		t.Fatalf("ValidateAnchorManifest() error = %v", err)
	}
	var decoded AnchorManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Anchors[0].Symbol != "Run" {
		t.Fatalf("decoded symbol = %q, want Run", decoded.Anchors[0].Symbol)
	}
}

func TestWriteAnchorManifestMigratesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchor-identities.json")
	expected := AnchorManifest{
		SchemaVersion: AnchorManifestSchemaVersion,
		Generator:     AnchorManifestGenerator,
		Anchors:       []AnchorIdentity{},
	}
	if err := WriteAnchorManifest(path, expected); err != nil {
		t.Fatalf("WriteAnchorManifest() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := ValidateAnchorManifest(data, expected); err != nil {
		t.Fatalf("ValidateAnchorManifest() error = %v", err)
	}
}

func writeAnchorTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
