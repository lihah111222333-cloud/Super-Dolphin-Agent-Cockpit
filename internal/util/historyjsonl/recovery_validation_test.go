package historyjsonl

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRecoveryConstructorsRejectInvalidConfiguration(t *testing.T) {
	t.Parallel()

	for _, limit := range []int{0, -1} {
		if cache, err := newRecoveryArtifactCache(limit); err == nil || cache != nil {
			t.Fatalf("newRecoveryArtifactCache(%d) = (%v, %v), want (nil, error)", limit, cache, err)
		}
	}
	if validator, err := newRecoveryValidator(newDefaultRecoveryFS(), 0); err == nil || validator != nil {
		t.Fatalf("newRecoveryValidator(limit=0) = (%v, %v), want (nil, error)", validator, err)
	}
	if validator, err := newRecoveryValidator(recoveryFS{}, 8); err == nil || validator != nil {
		t.Fatalf("newRecoveryValidator(incomplete) = (%v, %v), want (nil, error)", validator, err)
	}
}

func TestDefaultRecoveryFSReturnsIndependentDescriptors(t *testing.T) {
	t.Parallel()
	first := newDefaultRecoveryFS()
	second := newDefaultRecoveryFS()
	first.lstat = nil
	if second.lstat == nil || second.stat == nil || second.open == nil || second.evalSymlinks == nil || second.walkDir == nil {
		t.Fatal("default recovery filesystem descriptors unexpectedly share mutable state")
	}
}

func TestDefaultRecoveryValidatorReusesSingleInstanceConcurrently(t *testing.T) {
	const workers = 64
	results := make(chan *recoveryValidator, workers)
	errResults := make(chan error, workers)
	ready := make(chan struct{}, workers)
	start := make(chan struct{})
	original := defaultRecoveryValidator
	var initialized atomic.Int32
	defaultRecoveryValidator = sync.OnceValue(func() struct {
		validator *recoveryValidator
		err       error
	} {
		initialized.Add(1)
		validator, err := newRecoveryValidator(newDefaultRecoveryFS(), defaultRecoveryArtifactCacheLimit)
		return struct {
			validator *recoveryValidator
			err       error
		}{validator: validator, err: err}
	})
	t.Cleanup(func() { defaultRecoveryValidator = original })
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			ready <- struct{}{}
			<-start
			result := defaultRecoveryValidator()
			results <- result.validator
			errResults <- result.err
		})
	}
	for range workers {
		<-ready
	}
	close(start)
	group.Wait()
	close(results)
	close(errResults)

	for err := range errResults {
		if err != nil {
			t.Fatalf("defaultRecoveryValidator() error = %v", err)
		}
	}
	var first *recoveryValidator
	for validator := range results {
		if validator == nil {
			t.Fatal("defaultRecoveryValidator() validator = nil")
		}
		if first == nil {
			first = validator
			continue
		}
		if validator != first {
			t.Fatalf("defaultRecoveryValidator() validator = %p, want single instance %p", validator, first)
		}
	}
	if got := initialized.Load(); got != 1 {
		t.Fatalf("defaultRecoveryValidator() initialization count = %d, want 1", got)
	}
}

func TestRecoveryArtifactCacheEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	cache, err := newRecoveryArtifactCache(2)
	if err != nil {
		t.Fatalf("newRecoveryArtifactCache() error = %v", err)
	}
	first := recoveryCacheEntry{key: recoveryCacheKey{identity: "first"}}
	second := recoveryCacheEntry{key: recoveryCacheKey{identity: "second"}}
	third := recoveryCacheEntry{key: recoveryCacheKey{identity: "third"}}
	cache.put(first)
	cache.put(second)
	if _, ok := cache.get(first.key); !ok {
		t.Fatal("cache get(first) missed before eviction")
	}
	cache.put(third)
	if _, ok := cache.get(second.key); ok {
		t.Fatal("cache retained least recently used entry")
	}
	if _, ok := cache.get(first.key); !ok {
		t.Fatal("cache evicted refreshed entry")
	}
	if _, ok := cache.get(third.key); !ok {
		t.Fatal("cache evicted newest entry")
	}
}

func TestRecoveryValidatorClassifiesDiscoveryDeletionAsRace(t *testing.T) {
	t.Parallel()

	const identity = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	home := t.TempDir()
	path := writeRecoveryCodexArtifact(t, home, identity, 0)
	ops := newDefaultRecoveryFS()
	ops.walkDir = func(root string, walkFn fs.WalkDirFunc) error {
		if err := filepath.WalkDir(root, walkFn); err != nil {
			return err
		}
		return os.Remove(path)
	}
	validator := mustNewRecoveryValidator(t, ops, 8)

	_, err := validator.validate(ReadRequest{
		Provider:         "codex",
		ProviderThreadID: identity,
		CodexHome:        home,
	})
	if !IsRecoveryArtifactRaceError(err) {
		t.Fatalf("validate() error = %v, want RecoveryArtifactRaceError", err)
	}
	if IsMissingProviderHistory(err) {
		t.Fatalf("validate() error = %v, must not downgrade discovery deletion to missing", err)
	}
}

func TestRecoveryValidatorCachesStableLargeArtifactAndInvalidatesRevision(t *testing.T) {
	t.Parallel()

	const identity = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	home := t.TempDir()
	path := writeRecoveryCodexArtifact(t, home, identity, 16_384)
	var walkCalls atomic.Int64
	var openCalls atomic.Int64
	ops := newDefaultRecoveryFS()
	ops.walkDir = func(root string, walkFn fs.WalkDirFunc) error {
		walkCalls.Add(1)
		return filepath.WalkDir(root, walkFn)
	}
	ops.open = func(path string) (*os.File, error) {
		openCalls.Add(1)
		return os.Open(path)
	}
	validator := mustNewRecoveryValidator(t, ops, 8)
	req := ReadRequest{Provider: "codex", ProviderThreadID: identity, CodexHome: home}

	if _, err := validator.validate(req); err != nil {
		t.Fatalf("first validate() error = %v", err)
	}
	if _, err := validator.validate(req); err != nil {
		t.Fatalf("cached validate() error = %v", err)
	}
	if got := walkCalls.Load(); got != 1 {
		t.Fatalf("stable cache WalkDir calls = %d, want 1", got)
	}
	if got := openCalls.Load(); got != 2 {
		t.Fatalf("stable cache identity revalidation open calls = %d, want 2", got)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open artifact append: %v", err)
	}
	if _, err := file.WriteString(recoveryCodexMessageLine("revision-change")); err != nil {
		_ = file.Close()
		t.Fatalf("append artifact: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close artifact append: %v", err)
	}
	if _, err := validator.validate(req); err != nil {
		t.Fatalf("revision validate() error = %v", err)
	}
	if got := walkCalls.Load(); got != 1 {
		t.Fatalf("revision invalidation WalkDir calls = %d, want cached path without re-walk", got)
	}
	if got := openCalls.Load(); got != 3 {
		t.Fatalf("revision invalidation open calls = %d, want 3", got)
	}
}

func TestRecoveryValidatorRejectsSameMetadataIdentityRewrite(t *testing.T) {
	t.Parallel()

	const identity = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	const otherIdentity = "019e218f-b514-7733-be85-b3ee7f6a78a7"
	home := t.TempDir()
	path := writeRecoveryCodexArtifact(t, home, identity, 0)
	validator := mustNewRecoveryValidator(t, newDefaultRecoveryFS(), 8)
	req := ReadRequest{Provider: "codex", ProviderThreadID: identity, CodexHome: home}
	if _, err := validator.validate(req); err != nil {
		t.Fatalf("prime validate() error = %v", err)
	}
	rewriteRecoveryCodexArtifactIdentityPreservingMetadata(t, path, otherIdentity)
	_, err := validator.validate(req)
	if !IsRecoveryArtifactIdentityError(err) {
		t.Fatalf("validate() error = %v, want identity mismatch", err)
	}
}

func TestRecoveryValidatorRejectsSameMetadataTailCorruption(t *testing.T) {
	t.Parallel()

	const identity = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	home := t.TempDir()
	path := writeRecoveryCodexArtifact(t, home, identity, 1)
	validator := mustNewRecoveryValidator(t, newDefaultRecoveryFS(), 8)
	req := ReadRequest{Provider: "codex", ProviderThreadID: identity, CodexHome: home}
	if _, err := validator.validate(req); err != nil {
		t.Fatalf("prime validate() error = %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	last := len(body) - 2
	if last <= 0 || body[last] != '}' {
		t.Fatalf("unexpected artifact tail %q", body)
	}
	body[last] = ']'
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("rewrite artifact tail: %v", err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatalf("restore artifact timestamps: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat rewritten artifact: %v", err)
	}
	if !sameRecoveryFile(before, after) {
		t.Fatalf("test rewrite did not preserve inode/size/mtime/mode")
	}
	if _, err := validator.validate(req); err == nil {
		t.Fatal("validate() error = nil, want same-metadata content revision rejection")
	}
}

func TestRecoveryValidatorDoesNotInferOwnerRootFromExplicitArtifact(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")

	const identity = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	untrustedHome := t.TempDir()
	path := writeRecoveryCodexArtifact(t, untrustedHome, identity, 0)
	validator := mustNewRecoveryValidator(t, newDefaultRecoveryFS(), 8)
	_, err := validator.validate(ReadRequest{
		Provider:         "codex",
		ProviderThreadID: identity,
		RolloutPath:      path,
	})
	if !IsRecoveryArtifactIdentityError(err) || IsMissingProviderHistory(err) {
		t.Fatalf("validate() error = %v, want authoritative owner identity rejection", err)
	}
}

func TestRecoveryValidatorCacheIsBounded(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	validator := mustNewRecoveryValidator(t, newDefaultRecoveryFS(), 4)
	for i := range 12 {
		identity := fmt.Sprintf("019e218f-b514-7733-be85-%012x", i+1)
		writeRecoveryCodexArtifact(t, home, identity, 0)
		if _, err := validator.validate(ReadRequest{
			Provider:         "codex",
			ProviderThreadID: identity,
			CodexHome:        home,
		}); err != nil {
			t.Fatalf("validate(%s) error = %v", identity, err)
		}
	}
	if got := validator.cache.len(); got != 4 {
		t.Fatalf("cache len = %d, want cap 4", got)
	}
}

func BenchmarkRecoveryValidatorCachedLargeArtifact(b *testing.B) {
	const identity = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	home := b.TempDir()
	writeRecoveryCodexArtifact(b, home, identity, 16_384)
	validator := mustNewRecoveryValidator(b, newDefaultRecoveryFS(), 8)
	req := ReadRequest{Provider: "codex", ProviderThreadID: identity, CodexHome: home}
	if _, err := validator.validate(req); err != nil {
		b.Fatalf("prime validate() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := validator.validate(req); err != nil {
			b.Fatalf("cached validate() error = %v", err)
		}
	}
}

type recoveryTestTB interface {
	Helper()
	Fatalf(string, ...any)
}

func mustNewRecoveryValidator(tb recoveryTestTB, ops recoveryFS, cacheLimit int) *recoveryValidator {
	tb.Helper()
	validator, err := newRecoveryValidator(ops, cacheLimit)
	if err != nil {
		tb.Fatalf("newRecoveryValidator() error = %v", err)
	}
	return validator
}

func writeRecoveryCodexArtifact(tb recoveryTestTB, home, identity string, messages int) string {
	tb.Helper()
	path := filepath.Join(home, "sessions", "2026", "07", "29", "rollout-test-"+identity+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tb.Fatalf("mkdir recovery artifact: %v", err)
	}
	var body strings.Builder
	body.WriteString(fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q}}`+"\n", identity))
	for i := range messages {
		body.WriteString(recoveryCodexMessageLine(fmt.Sprintf("message-%05d-%s", i, strings.Repeat("x", 128))))
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		tb.Fatalf("write recovery artifact: %v", err)
	}
	return path
}

func rewriteRecoveryCodexArtifactIdentityPreservingMetadata(tb recoveryTestTB, path, identity string) {
	tb.Helper()
	before, err := os.Stat(path)
	if err != nil {
		tb.Fatalf("stat artifact: %v", err)
	}
	body := []byte(fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q}}`+"\n", identity))
	if int64(len(body)) != before.Size() {
		tb.Fatalf("rewrite artifact length = %d, want original size %d", len(body), before.Size())
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		tb.Fatalf("rewrite artifact: %v", err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		tb.Fatalf("restore artifact timestamps: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		tb.Fatalf("stat rewritten artifact: %v", err)
	}
	if !sameRecoveryFile(before, after) {
		tb.Fatalf("test rewrite did not preserve inode/size/mtime/mode")
	}
}

func recoveryCodexMessageLine(content string) string {
	return fmt.Sprintf(
		`{"timestamp":"2026-07-29T00:00:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`+"\n",
		content,
	)
}
