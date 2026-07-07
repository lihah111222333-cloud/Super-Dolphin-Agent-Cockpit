package contracttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// LoadExpectedPromptSnapshot 从 git 索引中的独立 prompt golden 加载期望证据。
func LoadExpectedPromptSnapshot(t testing.TB, snapshotID string) ExpectedPromptSnapshot {
	t.Helper()
	fields, err := loadExpectedPromptSnapshotFields(snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return ExpectedPromptSnapshot{snapshotID: strings.TrimSpace(snapshotID), fields: fields, loadedFromSnapshot: true}
}

// LoadExpectedEventSnapshot 从 git 索引中的独立 event golden 加载期望证据。
func LoadExpectedEventSnapshot(t testing.TB, snapshotID string) ExpectedEventSnapshot {
	t.Helper()
	canonical, err := loadExpectedEventSnapshot(snapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return ExpectedEventSnapshot{snapshotID: strings.TrimSpace(snapshotID), canonicalJSON: canonical, loadedFromSnapshot: true}
}

func loadExpectedPromptSnapshotFields(snapshotID string) (PromptParityFields, error) {
	raw, err := loadCheckedSnapshot("prompt", snapshotID)
	if err != nil {
		return PromptParityFields{}, err
	}
	var fields PromptParityFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return PromptParityFields{}, fmt.Errorf("decode prompt snapshot %s: %w", snapshotPath("prompt", snapshotID), err)
	}
	if promptSnapshotFieldsIncomplete(fields) {
		return PromptParityFields{}, fmt.Errorf("prompt snapshot %s is missing required fields", snapshotPath("prompt", snapshotID))
	}
	return fields, nil
}

func loadExpectedEventSnapshot(snapshotID string) ([]byte, error) {
	raw, err := loadCheckedSnapshot("event", snapshotID)
	if err != nil {
		return nil, err
	}
	return canonicalEventJSONBytes(raw)
}

func loadCheckedSnapshot(kind, snapshotID string) ([]byte, error) {
	path, err := cleanSnapshotPath(kind, snapshotID)
	if err != nil {
		return nil, err
	}
	raw, repoPath, err := readTrackedSnapshot(kind, path)
	if err != nil {
		return nil, err
	}
	return raw, validateSnapshotIndex(kind, path, repoPath, raw)
}

// readTrackedSnapshot 读取 snapshot 文件并确认它不是测试中生成的临时内容。
func readTrackedSnapshot(kind, path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("%s snapshot %s is required: %w", kind, path, err)
	}
	if info.Size() == 0 {
		return nil, "", fmt.Errorf("%s snapshot %s is empty", kind, path)
	}
	repoPath, err := trackedSnapshotPath(kind, path)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s snapshot %s: %w", kind, path, err)
	}
	if strings.Contains(string(raw), "GENERATED_DURING_TEST") {
		return nil, "", fmt.Errorf("%s snapshot %s must be checked-in golden data, not generated during the test", kind, path)
	}
	return raw, repoPath, nil
}

func trackedSnapshotPath(kind, path string) (string, error) {
	out, err := exec.Command("git", "ls-files", "--cached", "--full-name", "--error-unmatch", path).Output()
	if err != nil {
		return "", fmt.Errorf("%s snapshot %s must be tracked golden data", kind, path)
	}
	return strings.TrimSpace(string(out)), nil
}

func validateSnapshotIndex(kind, path, repoPath string, raw []byte) error {
	indexRaw, err := exec.Command("git", "show", ":"+filepath.ToSlash(repoPath)).Output()
	if err != nil {
		return fmt.Errorf("%s snapshot %s must exist in the git index: %w", kind, path, err)
	}
	if !bytes.Equal(raw, indexRaw) {
		return fmt.Errorf("%s snapshot %s has unstaged working-tree changes", kind, path)
	}
	return nil
}

func cleanSnapshotPath(kind, snapshotID string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(snapshotID))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) || strings.Contains(clean, string(filepath.Separator)) {
		return "", fmt.Errorf("%s snapshot id %q is invalid", kind, snapshotID)
	}
	return snapshotPath(kind, clean), nil
}

func snapshotPath(kind, snapshotID string) string {
	return filepath.Join("testdata", kind+"_snapshots", strings.TrimSpace(snapshotID)+".json")
}

func promptSnapshotFieldsIncomplete(fields PromptParityFields) bool {
	return fields.BaseInstructions == "" ||
		fields.DeveloperInstructions == "" ||
		fields.PrefixHash == "" ||
		fields.Boundary == "" ||
		fields.SectionSnapshot == ""
}

func canonicalEventJSON(event any) ([]byte, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal provider event evidence: %w", err)
	}
	return canonicalEventJSONBytes(raw)
}

func canonicalEventJSONBytes(raw []byte) ([]byte, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode event evidence JSON: %w", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("canonicalize event evidence JSON: %w", err)
	}
	if len(canonical) == 0 || bytes.Equal(canonical, []byte("null")) {
		return nil, errors.New("event evidence must not be empty or null")
	}
	return canonical, nil
}

// isTautologicalEvidence 判断 supplemental evidence 是否只是恒真断言。
func isTautologicalEvidence(got, want any) bool {
	if got == nil || want == nil || reflect.TypeOf(got) != reflect.TypeOf(want) {
		return false
	}
	switch typed := got.(type) {
	case bool:
		return true
	case string:
		wantString, _ := want.(string)
		return strings.TrimSpace(typed) == "" && strings.TrimSpace(wantString) == ""
	default:
		return false
	}
}
