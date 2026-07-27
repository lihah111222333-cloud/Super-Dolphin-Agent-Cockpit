package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var makefileRepositoryPathRe = regexp.MustCompile(`\./(?:cmd|internal|pkg|scripts|frontend-app|sql|test|tests)(?:/[A-Za-z0-9_.-]+)*(?:/\.\.\.)?`)

// TestMakefileDoesNotExposeDeletedEntrypoints 锁定 Makefile 只引用当前仓库仍存在的入口。
func TestMakefileDoesNotExposeDeletedEntrypoints(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	for _, deleted := range []string{
		"cmd/frida-bootstrap",
		"cmd/mcp-server",
		"internal/logaudit",
		"cmd/log-audit",
		"cmd/rpc-test",
		"cmd/ida-test-orchestrator",
		"github.com/multi-agent/go-agent-v2/pkg/idamcp",
		"FRIDA_",
	} {
		assertScriptDoesNotContain(t, makefile, deleted)
	}
}

// TestMakefileLiteralRepositoryPathsExist 拒绝新增未列入 denylist 的悬空命令路径。
func TestMakefileLiteralRepositoryPathsExist(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	checked := make(map[string]struct{})
	for _, match := range makefileRepositoryPathRe.FindAllString(makefile, -1) {
		relative := strings.TrimSuffix(strings.TrimPrefix(match, "./"), "/...")
		if _, ok := checked[relative]; ok {
			continue
		}
		checked[relative] = struct{}{}
		path := filepath.Join("..", filepath.FromSlash(relative))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Makefile literal repository path %q is not reachable: %v", match, err)
		}
	}
	if len(checked) == 0 {
		t.Fatal("Makefile guard did not discover any literal repository paths")
	}
}
