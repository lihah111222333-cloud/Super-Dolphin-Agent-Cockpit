package multilsp

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestPoolEnvironmentDefaultsOnlyWhenUnset 证明池容量默认值只适用于未设置的可选覆盖项。
func TestPoolEnvironmentDefaultsOnlyWhenUnset(t *testing.T) {
	unsetPoolEnvironmentForTest(t, lspPoolSizeEnv)
	unsetPoolEnvironmentForTest(t, lspPoolShardCapEnv)

	if got, err := PoolSizeFromEnv(); err != nil || got != defaultPoolSize {
		t.Fatalf("PoolSizeFromEnv() = (%d, %v), want unset default %d", got, err, defaultPoolSize)
	}
	if got, err := PoolShardCapFromEnv(); err != nil || got != defaultShardCap {
		t.Fatalf("PoolShardCapFromEnv() = (%d, %v), want unset default %d", got, err, defaultShardCap)
	}
}

// TestPoolEnvironmentExplicitInvalidValuesFailFast 证明空值、非数字、非正数和超上限配置不会静默裁剪。
func TestPoolEnvironmentExplicitInvalidValuesFailFast(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		read  func() (int, error)
	}{
		{name: "pool empty", key: lspPoolSizeEnv, value: "", read: PoolSizeFromEnv},
		{name: "pool non-number", key: lspPoolSizeEnv, value: "many", read: PoolSizeFromEnv},
		{name: "pool zero", key: lspPoolSizeEnv, value: "0", read: PoolSizeFromEnv},
		{name: "pool over maximum", key: lspPoolSizeEnv, value: "21", read: PoolSizeFromEnv},
		{name: "shard empty", key: lspPoolShardCapEnv, value: "", read: PoolShardCapFromEnv},
		{name: "shard non-number", key: lspPoolShardCapEnv, value: "many", read: PoolShardCapFromEnv},
		{name: "shard zero", key: lspPoolShardCapEnv, value: "0", read: PoolShardCapFromEnv},
		{name: "shard over maximum", key: lspPoolShardCapEnv, value: "1025", read: PoolShardCapFromEnv},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if value, err := tc.read(); err == nil {
				t.Fatalf("%s=%q resolved to %d without an error", tc.key, tc.value, value)
			}
		})
	}
}

// TestNewManagerWithErrorPropagatesPoolEnvironmentFailure 证明生产构造函数在创建 pool 前传播显式错误配置。
func TestNewManagerWithErrorPropagatesPoolEnvironmentFailure(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "invalid")
	_, err := NewManagerWithError(Config{
		WorkspaceRoot: t.TempDir(),
		IdleTimeout:   time.Minute,
	})
	if err == nil || !strings.Contains(err.Error(), lspPoolSizeEnv) {
		t.Fatalf("NewManagerWithError() error = %v, want %s configuration failure", err, lspPoolSizeEnv)
	}
}

func unsetPoolEnvironmentForTest(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	})
}
