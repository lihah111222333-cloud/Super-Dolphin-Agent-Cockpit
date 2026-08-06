package golden

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pmezard/go-difflib/difflib"
)

// TestOwner 保存调用测试包对 golden 更新开关的显式所有权。
type TestOwner struct {
	update *bool
}

// NewTestOwner 绑定调用测试包注册的 -update flag。
func NewTestOwner(update *bool) TestOwner {
	if update == nil {
		panic("golden test owner update flag must not be nil")
	}
	return TestOwner{update: update}
}

func (owner TestOwner) shouldUpdate() bool {
	if owner.update == nil {
		panic("golden test owner update flag must not be nil")
	}
	return *owner.update
}

// Domain 标识 golden 用例所属目录，是磁盘路径的一部分。
// 新增值时需要保证目录名稳定，否则历史 fixture 会被重新定位。
type Domain string

const (
	// golden 用例域名常量。
	DomainTurnAgent   Domain = "turn-agent"
	DomainTransport   Domain = "transport"
	DomainIntegration Domain = "integration"
)

// Case 描述一个 golden JSON 用例的目录、域和名称。
// Name 会参与路径拼接并拒绝目录穿越，避免测试更新越过 fixture 根目录。
type Case struct {
	BaseDir string
	Domain  Domain
	Name    string
}

// AssertJSON 对比 actual 的规范化 JSON 与 golden 文件。
// 只有 owner 显式传入 -update 时才写回 fixture，普通测试失败会输出稳定 diff。
func AssertJSON(t *testing.T, owner TestOwner, tc Case, actual any) {
	t.Helper()

	path, err := tc.path()
	if err != nil {
		t.Fatalf("resolve golden path: %v", err)
	}
	got, err := canonicalJSON(actual)
	if err != nil {
		t.Fatalf("canonicalize actual JSON: %v", err)
	}
	if owner.shouldUpdate() {
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

// path 生成 golden 文件路径，并拒绝空名称或目录穿越。
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

// writeGolden 刷新 golden 文件内容，测试失败由调用方 t.Fatalf 接管。
func writeGolden(t *testing.T, path string, content []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create golden dir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write golden file %s: %v", path, err)
	}
}

// canonicalJSON 将任意值序列化为稳定格式 JSON。
// 序列化错误直接返回给测试，避免 golden 文件吸收不可编码值。
func canonicalJSON(actual any) ([]byte, error) {
	raw, err := json.Marshal(actual)
	if err != nil {
		return nil, err
	}
	return canonicalBytes(raw)
}

// canonicalBytes 将 JSON 字节规范化为带末尾换行的缩进格式。
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

// isEOF 只接受 decoder 的标准 EOF，供 trailing JSON 检查区分正常结束和解析错误。
func isEOF(err error) bool {
	return err == io.EOF
}

// unifiedDiff 生成 golden 期望值和实际值之间的 unified diff。
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
