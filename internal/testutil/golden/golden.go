package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmezard/go-difflib/difflib"
)

var updateFiles = flag.Bool("update", false, "refresh golden JSON fixtures")

type Domain string

const (
	DomainTurnAgent   Domain = "turn-agent"
	DomainTransport   Domain = "transport"
	DomainIntegration Domain = "integration"
)

type Case struct {
	BaseDir string
	Domain  Domain
	Name    string
}

// AssertJSON 处理assertJSON。
func AssertJSON(t *testing.T, tc Case, actual any) {
	t.Helper()

	path, err := tc.path()
	if err != nil {
		t.Fatalf("resolve golden path: %v", err)
	}
	got, err := canonicalJSON(actual)
	if err != nil {
		t.Fatalf("canonicalize actual JSON: %v", err)
	}
	if *updateFiles {
		writeGolden(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file %s: %v", path, err)
	}
	want, err = canonicalBytes(want)
	if err != nil {
		t.Fatalf("canonicalize golden JSON %s: %v", path, err)
	}
	if bytes.Equal(want, got) {
		return
	}
	t.Fatalf("golden mismatch for %s:\n%s", path, unifiedDiff(want, got))
}

// path 处理路径。
func (tc Case) path() (string, error) {
	baseDir := strings.TrimSpace(tc.BaseDir)
	name := strings.TrimSpace(tc.Name)
	if baseDir == "" {
		return "", fmt.Errorf("base dir must not be empty")
	}
	if tc.Domain == "" {
		return "", fmt.Errorf("domain must not be empty")
	}
	if name == "" {
		return "", fmt.Errorf("case name must not be empty")
	}
	cleanName := filepath.Clean(name)
	if cleanName == "." || strings.HasPrefix(cleanName, "..") {
		return "", fmt.Errorf("invalid case name %q", name)
	}
	return filepath.Join(baseDir, string(tc.Domain), cleanName+".golden.json"), nil
}

func writeGolden(t *testing.T, path string, content []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create golden dir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write golden file %s: %v", path, err)
	}
}

func canonicalJSON(actual any) ([]byte, error) {
	raw, err := json.Marshal(actual)
	if err != nil {
		return nil, err
	}
	return canonicalBytes(raw)
}

func canonicalBytes(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var normalized any
	if err := dec.Decode(&normalized); err != nil {
		return nil, err
	}
	err := dec.Decode(new(any))
	if err == nil {
		return nil, fmt.Errorf("unexpected trailing JSON content")
	}
	if !isEOF(err) {
		return nil, err
	}
	pretty, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(pretty, '\n'), nil
}

func isEOF(err error) bool {
	return err == io.EOF
}

func unifiedDiff(want, got []byte) string {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(want)),
		B:        difflib.SplitLines(string(got)),
		FromFile: "want",
		ToFile:   "got",
		Context:  2,
	})
	if err != nil {
		return fmt.Sprintf("want:\n%s\ngot:\n%s", want, got)
	}
	return diff
}
