package sqliteauthority

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestConfigRejectsAuthorityPathWithWhitespace 确保 authority 数据库叶子名首尾空格在配置边界 fail-fast。
func TestConfigRejectsAuthorityPathWithWhitespace(t *testing.T) {
	config := authorityTestConfig(t, "authority.db")
	config.ExternalService = true
	config.ProductSQLitePath = ""
	config.WorkspaceRoot = ""
	config.Path = filepath.Join(filepath.Dir(config.Path), " authority.db ")

	err := config.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Config.Validate() error = %v, want ErrInvalidConfig", err)
	}
}
