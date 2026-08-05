package acpnode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOfficialACP20SchemaProvenanceIsPinnedOffline(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "schema_provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Schema       string `json:"schema"`
		TagObject    string `json:"tag_object"`
		PeeledCommit string `json:"peeled_commit"`
		SizeBytes    int    `json:"size_bytes"`
		SHA256       string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Schema != "official ACP schema-v1.20.0" || metadata.TagObject != "4908af80fe0285fc765cddec8aeb54627a81e9ec" || metadata.PeeledCommit != "5e89c71497fe07dd4ae633c181a17224f4a8956d" {
		t.Fatalf("schema identity drifted: %+v", metadata)
	}
	if metadata.SizeBytes != 198609 || metadata.SHA256 != "92c1dfcda10dd47e99127500a3763da2b471f9ac61e12b9bf0430c32cf953796" {
		t.Fatalf("schema digest drifted: %+v", metadata)
	}
}
