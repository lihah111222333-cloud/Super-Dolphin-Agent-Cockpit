package codexapp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRolloutFixture 在指定 codex home 下写入单个 rollout jsonl。
// findRolloutPath 依赖 glob 查找历史文件，测试用这份 fixture 锁定目录形状。
func writeRolloutFixture(t *testing.T, root, threadID string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", "2026", "04", "23")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "rollout-abc-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestFindRolloutPathHonoursCodexHome 验证非空 codexHome 会把查找限定到指定目录树。
// 非默认 Codex 实例写出的 rollout 文件必须可被发现，避免历史读取串回用户默认 home。
func TestFindRolloutPathHonoursCodexHome(t *testing.T) {
	t.Parallel()

	codexHome := t.TempDir()
	want := writeRolloutFixture(t, codexHome, "thread-multi")

	got, err := findRolloutPath("thread-multi", codexHome)
	if err != nil {
		t.Fatalf("findRolloutPath err = %v", err)
	}
	if got != want {
		t.Fatalf("findRolloutPath = %q, want %q", got, want)
	}
}

// TestFindRolloutPathFallsBackToLegacyHome 验证显式允许旧路径时，空 codexHome 仍查找 ~/.codex。
// 该兼容只服务未配置多 provider home 的部署，调用方必须通过环境变量选择启用。
func TestFindRolloutPathFallsBackToLegacyHome(t *testing.T) {
	// t.Setenv 会串行化 HOME 修改，因此本测试不能并行。
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)
	t.Setenv("CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME", "1")

	want := writeRolloutFixture(t, filepath.Join(fakeHome, ".codex"), "thread-legacy")

	got, err := findRolloutPath("thread-legacy", "")
	if err != nil {
		t.Fatalf("findRolloutPath err = %v", err)
	}
	if got != want {
		t.Fatalf("findRolloutPath = %q, want %q", got, want)
	}
}

// TestFindRolloutPathRequiresExplicitLegacyOptIn 验证旧 ~/.codex fallback 必须显式开启。
// 缺少 codexHome 且未 opt-in 时直接返回错误，避免无意读取默认 home。
func TestFindRolloutPathRequiresExplicitLegacyOptIn(t *testing.T) {
	t.Setenv("CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME", "")
	if _, err := findRolloutPath("thread-legacy", ""); err == nil {
		t.Fatal("expected codex home required error without legacy opt-in")
	}
}

func TestFindRolloutPathNotFound(t *testing.T) {
	t.Parallel()

	codexHome := t.TempDir()
	if _, err := findRolloutPath("missing-thread", codexHome); err == nil {
		t.Fatal("expected error for missing rollout")
	}
}

// TestResolveRolloutRootTrimsWhitespace 验证带空白的 codexHome 仍优先于旧 fallback。
// session runtimeConfigString 读取时会 trim，这里同时保护手写输入不会因空白静默落回默认 home。
func TestResolveRolloutRootTrimsWhitespace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := resolveRolloutRoot("  " + dir + "  ")
	if err != nil {
		t.Fatalf("resolveRolloutRoot err = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveRolloutRoot = %q, want %q", got, dir)
	}
}
