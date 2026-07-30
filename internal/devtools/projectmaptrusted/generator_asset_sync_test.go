package projectmaptrusted

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestTrustedGeneratorAssetHasDeterministicGzipHeader(t *testing.T) {
	gzipData, err := trustedGeneratorGzip()
	if err != nil {
		t.Fatalf("read trusted generator gzip: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(gzipData))
	if err != nil {
		t.Fatalf("open trusted generator gzip: %v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close trusted generator gzip: %v", err)
		}
	}()
	if !reader.ModTime.IsZero() {
		t.Fatalf("trusted generator gzip has timestamp %v; regenerate with gzip -n", reader.ModTime)
	}
	if reader.Name != "" {
		t.Fatalf("trusted generator gzip has embedded filename %q; regenerate with gzip -n", reader.Name)
	}
	if reader.Comment != "" || len(reader.Extra) != 0 {
		t.Fatal("trusted generator gzip has non-deterministic optional header fields")
	}
}

func TestTrustedGeneratorAssetMatchesCanonicalSource(t *testing.T) {
	source, err := trustedGeneratorSource()
	if err != nil {
		t.Fatalf("decode trusted generator: %v", err)
	}
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the test file")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	canonical, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(canonicalGeneratorPath)))
	if err != nil {
		t.Fatalf("read canonical generator: %v", err)
	}
	if !bytes.Equal(source, canonical) {
		t.Fatal("compiled project-map generator asset drifted from scripts/generate_ai_project_map.mjs")
	}
}

func TestTrustedGeneratorAssetUsesImmutableEmbedFSMetrics(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the test file")
	}
	metrics := archtest.MeasureBaselineFileMetrics(filepath.Join(filepath.Dir(testFile), "generator_asset.go"))
	if metrics.GlobalVars != 0 {
		t.Fatalf("generator asset global vars = %d, want 0", metrics.GlobalVars)
	}
}
