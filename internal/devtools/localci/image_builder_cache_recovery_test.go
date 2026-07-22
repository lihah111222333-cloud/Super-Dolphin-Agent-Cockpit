package localci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerBuildxRunnerRecoversEmptyInterruptedCacheNamespace(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	cachePath := filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:"))
	if err := os.Mkdir(cachePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Build(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	buildCall := recordedBuildxBuildCall(t, executor.calls)
	if cacheFrom := prefixedArguments(buildCall.args, "--cache-from="); len(cacheFrom) != 0 {
		t.Fatalf("empty interrupted cache was imported: %v", cacheFrom)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty interrupted cache namespace was not removed: %v", err)
	}
}

func TestDockerBuildxRunnerRejectsNonEmptyIncompleteCacheNamespace(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	cachePath := filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:"))
	if err := os.Mkdir(cachePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "partial"), []byte("incomplete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Build(context.Background(), request); err == nil {
		t.Fatal("non-empty incomplete cache namespace was accepted")
	}
	if len(executor.calls) != 0 {
		t.Fatal("non-empty incomplete cache namespace reached the command executor")
	}
}
