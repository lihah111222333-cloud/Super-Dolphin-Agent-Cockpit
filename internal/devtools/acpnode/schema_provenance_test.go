package acpnode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type ancestryMetadata struct {
	Branch           string `json:"branch"`
	FrozenBase       string `json:"frozen_base"`
	FrozenBaseParent string `json:"frozen_base_parent"`
	RejectedCommit   string `json:"rejected_commit"`
	RejectedBundle   struct {
		SizeBytes int    `json:"size_bytes"`
		SHA256    string `json:"sha256"`
	} `json:"rejected_bundle"`
	SchemaProvenance struct {
		OfficialSchemaSizeBytes int    `json:"official_schema_size_bytes"`
		OfficialSchemaSHA256    string `json:"official_schema_sha256"`
	} `json:"schema_provenance"`
}

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

func TestFrozenAncestryProvenanceIsPinned(t *testing.T) {
	metadata := readAncestryMetadata(t)
	assertFrozenBase(t, metadata)
	assertRejectedArtifact(t, metadata)
	assertSchemaInAncestry(t, metadata)
}

// readAncestryMetadata 离线读取固定祖先收据。
func readAncestryMetadata(t *testing.T) ancestryMetadata {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "ancestry_provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata ancestryMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

// assertFrozenBase 验证新分支的固定祖先身份。
func assertFrozenBase(t *testing.T, metadata ancestryMetadata) {
	if metadata.Branch != "codex/acp-node-v2-repair" || metadata.FrozenBase != "33f438a0d3bcc545e86fc7538fa85d8cdb99ded0" || metadata.FrozenBaseParent != "ffa879e9c2fdf14f5591a12ff25cbf87bc00c512" {
		t.Fatalf("frozen ancestry drifted: %+v", metadata)
	}
}

// assertRejectedArtifact 验证被拒绝产物仍以固定摘要保留。
func assertRejectedArtifact(t *testing.T, metadata ancestryMetadata) {
	if metadata.RejectedCommit != "55af2c6bb6ad9c9d55be2c164621be1853c9911e" || metadata.RejectedBundle.SizeBytes != 27180499 || metadata.RejectedBundle.SHA256 != "9f73ec710dce54740bd7ed6b985959d175bd5a84b287f129f2c0b26e5a2b9950" {
		t.Fatalf("rejected artifact provenance drifted: %+v", metadata)
	}
}

// assertSchemaInAncestry 验证祖先收据引用同一官方 schema 摘要。
func assertSchemaInAncestry(t *testing.T, metadata ancestryMetadata) {
	if metadata.SchemaProvenance.OfficialSchemaSizeBytes != 198609 || metadata.SchemaProvenance.OfficialSchemaSHA256 != "92c1dfcda10dd47e99127500a3763da2b471f9ac61e12b9bf0430c32cf953796" {
		t.Fatalf("schema provenance in ancestry receipt drifted: %+v", metadata)
	}
}
