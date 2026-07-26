package localci

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDockerBuildxRunnerKeepsEmptySharedCachePool(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	cachePath := filepath.Join(root, "cache", buildxSharedCacheDirectory)
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
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("shared cache pool was removed: %v", err)
	}
}

func TestDockerBuildxRunnerKeepsNonIndexedSharedCachePool(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	cachePath := filepath.Join(root, "cache", buildxSharedCacheDirectory)
	if err := os.Mkdir(cachePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "partial"), []byte("incomplete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Build(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	buildCall := recordedBuildxBuildCall(t, executor.calls)
	if cacheFrom := prefixedArguments(buildCall.args, "--cache-from="); len(cacheFrom) != 0 {
		t.Fatalf("non-indexed shared cache was imported: %v", cacheFrom)
	}
}
