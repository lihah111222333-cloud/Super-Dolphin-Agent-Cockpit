package similarity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ---------------------------------------------------------------------------
// IgnoreKey + ignored set IO
// ---------------------------------------------------------------------------

func TestIgnoreKeyOrderInvariant(t *testing.T) {
	a := IgnoreKey("private", "feedback/a.md", "team", "feedback/b.md")
	b := IgnoreKey("team", "feedback/b.md", "private", "feedback/a.md")
	if a != b {
		t.Fatalf("IgnoreKey order should be invariant: %q vs %q", a, b)
	}
}

func TestIgnoreKeyFormat(t *testing.T) {
	got := IgnoreKey("private", "a.md", "team", "b.md")
	if got != "private:a.md|team:b.md" {
		t.Fatalf("IgnoreKey = %q, want private:a.md|team:b.md", got)
	}
}

func TestIgnoreKeyTrimsWhitespace(t *testing.T) {
	got := IgnoreKey("  private  ", "  a.md  ", "team", "b.md")
	if got != "private:a.md|team:b.md" {
		t.Fatalf("IgnoreKey = %q, want trimmed", got)
	}
}

func TestLoadIgnoredMissingFile(t *testing.T) {
	if _, err := LoadIgnored(t.TempDir()); err == nil {
		t.Fatal("LoadIgnored() error = nil, want missing file error")
	}
}

func TestLoadIgnoredEmptyRoot(t *testing.T) {
	if _, err := LoadIgnored(""); err == nil {
		t.Fatal("LoadIgnored(empty) error = nil, want private root error")
	}
}

func TestLoadIgnoredCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ignoredFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := LoadIgnored(dir); err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestAppendThenLoadIgnored(t *testing.T) {
	dir := t.TempDir()
	key := IgnoreKey("private", "a.md", "team", "b.md")
	if err := AppendIgnored(dir, key); err != nil {
		t.Fatalf("AppendIgnored: %v", err)
	}
	set, _ := LoadIgnored(dir)
	if _, ok := set[key]; !ok {
		t.Fatalf("expected key %q present, got %v", key, set)
	}
}

func TestAppendIgnoredIdempotent(t *testing.T) {
	dir := t.TempDir()
	key := IgnoreKey("private", "a.md", "team", "b.md")
	for i := range 3 {
		if err := AppendIgnored(dir, key); err != nil {
			t.Fatalf("append iter %d: %v", i, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ignoredFileName))
	if cnt := bytes.Count(raw, []byte(key)); cnt != 1 {
		t.Fatalf("expected key once, got %d times: %s", cnt, raw)
	}
}

func TestAppendIgnoredSorted(t *testing.T) {
	dir := t.TempDir()
	keys := []string{
		IgnoreKey("private", "z.md", "team", "a.md"),
		IgnoreKey("private", "a.md", "team", "b.md"),
		IgnoreKey("private", "m.md", "team", "n.md"),
	}
	for _, k := range keys {
		if err := AppendIgnored(dir, k); err != nil {
			t.Fatalf("append %q: %v", k, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ignoredFileName))
	var file ignoredFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(file.Pairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d: %v", len(file.Pairs), file.Pairs)
	}
	for i := 1; i < len(file.Pairs); i++ {
		if file.Pairs[i-1] >= file.Pairs[i] {
			t.Fatalf("pairs not sorted: %v", file.Pairs)
		}
	}
}

func TestAppendIgnoredEmptyKey(t *testing.T) {
	if err := AppendIgnored(t.TempDir(), "  "); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestAppendIgnoredEmptyRoot(t *testing.T) {
	if err := AppendIgnored("", "k"); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestAppendIgnoredCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep")
	if err := AppendIgnored(dir, IgnoreKey("private", "a.md", "team", "b.md")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ignoredFileName)); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestAppendIgnoredConcurrent(t *testing.T) {
	dir := t.TempDir()
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	workersDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-workersDone:
		case <-time.After(time.Second):
			t.Fatal("similarity append goroutines did not stop")
		}
	})
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			key := IgnoreKey("private", fmt.Sprintf("a%02d.md", i), "team", fmt.Sprintf("b%02d.md", i))
			if err := AppendIgnored(dir, key); err != nil {
				t.Errorf("AppendIgnored: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(workersDone)
	set, _ := LoadIgnored(dir)
	if len(set) != goroutines {
		t.Fatalf("expected %d keys, got %d: %v", goroutines, len(set), set)
	}
}

// ---------------------------------------------------------------------------
// Prompt + parse
// ---------------------------------------------------------------------------

func TestBuildPromptIncludesHeaderAndPayload(t *testing.T) {
	input := AllInput{Groups: []PairInput{
		{ID: 0, Type: "feedback",
			A: PairInputEntry{Scope: "private", Name: "rule a", Description: "desc a", Content: "body a"},
			B: PairInputEntry{Scope: "team", Name: "rule b", Description: "desc b", Content: "body b"}},
	}}
	got, err := BuildPrompt(input)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	if !strings.Contains(got, "memory 整合助手") {
		t.Fatalf("prompt missing header")
	}
	for _, must := range []string{"rule a", "rule b", "body a", "body b", "\"id\": 0"} {
		if !strings.Contains(got, must) {
			t.Fatalf("prompt missing %q", must)
		}
	}
	// M4 / B3 regression guard: prompt 必须给 LLM 列出 scope 取值范围 + payload
	// 实际填入 "private"/"team"，否则 LLM 可能误把 scope 当 type 解读。
	if !strings.Contains(got, "scope 字段取值范围") {
		t.Fatalf("prompt missing scope enumeration directive")
	}
	for _, scopeValue := range []string{"\"scope\": \"private\"", "\"scope\": \"team\""} {
		if !strings.Contains(got, scopeValue) {
			t.Fatalf("prompt payload missing %q (scope JSON value)", scopeValue)
		}
	}
}

func TestParseDecisionsValid(t *testing.T) {
	raw := `{"decisions":[{"id":0,"merge":true,"keep":"B","merged_description":"d","merged_content":"c"},{"id":1,"merge":false,"reason":"diff"}]}`
	out, err := ParseDecisions(raw)
	if err != nil {
		t.Fatalf("ParseDecisions: %v", err)
	}
	if len(out.Decisions) != 2 || !out.Decisions[0].Merge || out.Decisions[0].Keep != "B" || out.Decisions[1].Merge {
		t.Fatalf("decisions = %+v", out)
	}
}

func TestParseDecisionsEmptyAndInvalid(t *testing.T) {
	if _, err := ParseDecisions(""); err == nil {
		t.Fatal("expected error for empty raw")
	}
	if _, err := ParseDecisions("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// IgnorePair
// ---------------------------------------------------------------------------

func TestIgnorePairValidatesPaths(t *testing.T) {
	deps := newFakeDeps(t)
	if err := IgnorePair(context.Background(), deps, "/cwd", "private", "", "private", "b.md"); err == nil {
		t.Fatal("expected error for empty pathA")
	}
	if err := IgnorePair(context.Background(), deps, "/cwd", "private", "a.md", "private", " "); err == nil {
		t.Fatal("expected error for empty pathB")
	}
}

func TestIgnorePairWritesKey(t *testing.T) {
	deps := newFakeDeps(t)
	if err := IgnorePair(context.Background(), deps, "/cwd", "private", "a.md", "team", "b.md"); err != nil {
		t.Fatalf("IgnorePair: %v", err)
	}
	set, _ := LoadIgnored(deps.privateRoot)
	if _, ok := set[IgnoreKey("private", "a.md", "team", "b.md")]; !ok {
		t.Fatalf("ignored set missing key: %v", set)
	}
}

// ---------------------------------------------------------------------------
// ConsolidateAll
// ---------------------------------------------------------------------------

func TestConsolidateAllZeroWhenNoSimilarPairs(t *testing.T) {
	deps := newFakeDeps(t)
	res, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if err != nil {
		t.Fatalf("ConsolidateAll: %v", err)
	}
	if res.Merged != 0 || res.Ignored != 0 || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf("expected zero result, got %+v", res)
	}
}

func TestConsolidateAllAppliesMergeDecision(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":true,"keep":"A","merged_description":"合后","merged_content":"c\nWhy: r\nHow to apply: a"}]}`)
	res, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if err != nil {
		t.Fatalf("ConsolidateAll: %v", err)
	}
	if res.Merged != 1 || res.Failed != 0 {
		t.Fatalf("result = %+v, want merged=1", res)
	}
	if len(deps.mergeCalls) != 1 {
		t.Fatalf("expected 1 merge call, got %d", len(deps.mergeCalls))
	}
	call := deps.mergeCalls[0]
	if call.TargetA != "private" || call.PathA != "a.md" {
		t.Fatalf("keep side wrong: %+v", call)
	}
	if call.MergedDescription != "合后" {
		t.Fatalf("merged description override missing: %+v", call)
	}
}

func TestConsolidateAllIgnoresWhenDecisionFalse(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":false,"reason":"diff"}]}`)
	res, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if err != nil {
		t.Fatalf("ConsolidateAll: %v", err)
	}
	if res.Ignored != 1 || res.Merged != 0 {
		t.Fatalf("result = %+v, want ignored=1", res)
	}
	set, _ := LoadIgnored(deps.privateRoot)
	if len(set) != 1 {
		t.Fatalf("expected 1 ignored key, got %d", len(set))
	}
}

func TestConsolidateAllRejectsInvalidKeep(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":true,"keep":"C","merged_description":"d","merged_content":"c"}]}`)
	res, _ := ConsolidateAll(context.Background(), deps, "/cwd")
	if res.Failed != 1 {
		t.Fatalf("result = %+v, want failed=1", res)
	}
}

func TestConsolidateAllAcceptsLowercaseKeep(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":true,"keep":"a","merged_description":"d","merged_content":"c\nWhy: r\nHow to apply: a"}]}`)
	res, _ := ConsolidateAll(context.Background(), deps, "/cwd")
	if res.Merged != 1 {
		t.Fatalf("result = %+v, want merged=1 (lowercase keep)", res)
	}
}

func TestConsolidateAllRejectsMissingMergedFields(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":true,"keep":"A"}]}`)
	res, _ := ConsolidateAll(context.Background(), deps, "/cwd")
	if res.Failed != 1 {
		t.Fatalf("result = %+v, want failed=1", res)
	}
}

func TestConsolidateAllSkipsMissingDecision(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[]}`)
	res, _ := ConsolidateAll(context.Background(), deps, "/cwd")
	if res.Skipped != 1 {
		t.Fatalf("result = %+v, want skipped=1", res)
	}
}

func TestConsolidateAllRejectsDuplicateID(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":true,"keep":"A","merged_description":"d","merged_content":"c"},{"id":0,"merge":false}]}`)
	res, _ := ConsolidateAll(context.Background(), deps, "/cwd")
	if res.Failed != 1 || res.Merged != 0 {
		t.Fatalf("result = %+v, want failed=1 (duplicate id)", res)
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "duplicate") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("errors %v missing 'duplicate' mention", res.Errors)
	}
}

func TestConsolidateAllDescriptionTruncated(t *testing.T) {
	longDesc := strings.Repeat("超长描述", 100) // 400 runes
	payload := AllOutput{Decisions: []Decision{{
		ID: 0, Merge: true, Keep: "A",
		MergedDescription: longDesc,
		MergedContent:     "c\nWhy: r\nHow to apply: a",
	}}}
	raw, _ := json.Marshal(payload)
	deps := seedConsolidatePair(t, string(raw))
	res, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if err != nil {
		t.Fatalf("ConsolidateAll: %v", err)
	}
	if res.Merged != 1 {
		t.Fatalf("result = %+v, want merged=1", res)
	}
	if len(deps.mergeCalls) != 1 {
		t.Fatalf("expected 1 merge call")
	}
	mergedDesc := deps.mergeCalls[0].MergedDescription
	if r := []rune(mergedDesc); len(r) > maxDescriptionRunes {
		t.Fatalf("description not truncated: %d runes", len(r))
	}
	// M4 / B1 regression guard: truncateRunesHead 必须保留开头（截尾部）。
	// 之前用 dedup.TruncateOldestParagraphs 会反过来删开头保末段，对 LLM
	// 单次重写产物错位，关键总述会被砍掉。断言开头几个 rune 仍是输入开头。
	if !strings.HasPrefix(mergedDesc, "超长描述") {
		t.Fatalf("description truncation should keep head, got prefix: %q", mergedDesc[:min(30, len(mergedDesc))])
	}
}

func TestConsolidateAllShortCircuitsAfterIgnore(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":false,"reason":"diff"}]}`)
	if _, err := ConsolidateAll(context.Background(), deps, "/cwd"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if deps.dreamCalls != 1 {
		t.Fatalf("first run dreamCalls = %d, want 1", deps.dreamCalls)
	}
	// After ignore, the pair is filtered by the caller (main package populate),
	// so simulate the second run by emptying the pairs list and re-running.
	deps.pairs = nil
	if _, err := ConsolidateAll(context.Background(), deps, "/cwd"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if deps.dreamCalls != 1 {
		t.Fatalf("second run should short-circuit (no LLM call), dreamCalls = %d", deps.dreamCalls)
	}
}

func TestConsolidateAllReturnsLLMError(t *testing.T) {
	deps := seedConsolidatePair(t, "")
	deps.dreamErr = errors.New("rate limited /Users/secret/path")
	_, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if err == nil {
		t.Fatal("expected error from LLM")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error should propagate LLM message: %v", err)
	}
	// Subpkg wraps but does NOT redact; main pkg handler is responsible for redaction.
}

func TestConsolidateAllPropagatesDreamNotConfiguredSentinel(t *testing.T) {
	deps := seedConsolidatePair(t, "")
	deps.dreamErr = contract.ErrDreamExecutorNotConfigured
	_, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if !errors.Is(err, contract.ErrDreamExecutorNotConfigured) {
		t.Fatalf("err = %v, want ErrDreamExecutorNotConfigured propagated", err)
	}
}

func TestConsolidateAllReadFailureCountedAsFailed(t *testing.T) {
	deps := seedConsolidatePair(t, `{"decisions":[{"id":0,"merge":true,"keep":"A","merged_description":"d","merged_content":"c\nWhy: r\nHow to apply: a"}]}`)
	deps.entries = map[string]EntrySnapshot{} // empty → ReadEntry fails
	res, err := ConsolidateAll(context.Background(), deps, "/cwd")
	if err != nil {
		t.Fatalf("ConsolidateAll: %v", err)
	}
	if res.Failed != 1 {
		t.Fatalf("result = %+v, want failed=1 (read failed)", res)
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "read entry failed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("errors %v missing 'read entry failed'", res.Errors)
	}
}

// ---------------------------------------------------------------------------
// fakeDeps
// ---------------------------------------------------------------------------

type fakeDeps struct {
	privateRoot string
	pairs       []SimilarPair
	entries     map[string]EntrySnapshot // key: target:path
	mergeCalls  []MergeRequest
	mergeErr    error
	dreamRaw    string
	dreamErr    error
	dreamCalls  int
}

func newFakeDeps(t *testing.T) *fakeDeps {
	t.Helper()
	return &fakeDeps{
		privateRoot: t.TempDir(),
		entries:     map[string]EntrySnapshot{},
	}
}

func seedConsolidatePair(t *testing.T, dreamRaw string) *fakeDeps {
	t.Helper()
	deps := newFakeDeps(t)
	deps.dreamRaw = dreamRaw
	deps.pairs = []SimilarPair{{
		NameA: "A", NameB: "B",
		PathA: "a.md", PathB: "b.md",
		TargetA: "private", TargetB: "private",
		Score: 0.9,
	}}
	deps.entries["private:a.md"] = EntrySnapshot{Name: "A", Description: "desc a", Content: "body a", Type: "feedback"}
	deps.entries["private:b.md"] = EntrySnapshot{Name: "B", Description: "desc b", Content: "body b", Type: "feedback"}
	return deps
}

func (f *fakeDeps) PrivateRoot(_ context.Context, _ string) (string, error) {
	return f.privateRoot, nil
}

func (f *fakeDeps) SimilarPairs(_ context.Context, _ string) ([]SimilarPair, error) {
	return f.pairs, nil
}

func (f *fakeDeps) ReadEntry(_ context.Context, _, target, path string) (EntrySnapshot, error) {
	e, ok := f.entries[target+":"+path]
	if !ok {
		return EntrySnapshot{}, fmt.Errorf("not found %s:%s", target, path)
	}
	return e, nil
}

func (f *fakeDeps) Merge(_ context.Context, req MergeRequest) error {
	if f.mergeErr != nil {
		return f.mergeErr
	}
	f.mergeCalls = append(f.mergeCalls, req)
	return nil
}

func (f *fakeDeps) DreamExecute(_ context.Context, _ string) (string, error) {
	f.dreamCalls++
	return f.dreamRaw, f.dreamErr
}

func (f *fakeDeps) Logger() *slog.Logger { return nil }
