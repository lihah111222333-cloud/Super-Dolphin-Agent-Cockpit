//go:build windows

package multilsp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// TestWindowsFileURIFromPathMatchesVSCodeDriveCanonicalization 锁定 Windows 盘符的
// vscode-uri 兼容形式，防止 Prisma schema URI 与请求 URI 因大小写或冒号转义漂移。
func TestWindowsFileURIFromPathMatchesVSCodeDriveCanonicalization(t *testing.T) {
	path := filepath.Join(`C:\Workspace With Space`, "schema.prisma")
	got := fileURIFromPath(path)
	const want = "file:///c%3A/Workspace%20With%20Space/schema.prisma"
	if got != want {
		t.Fatalf("fileURIFromPath(%q) = %q, want %q", path, got, want)
	}

	roundTrip, err := absolutePathFromURI(got)
	if err != nil {
		t.Fatalf("absolutePathFromURI(%q): %v", got, err)
	}
	if !filepath.IsAbs(roundTrip) || filepath.Clean(roundTrip) != filepath.Clean(path) {
		t.Fatalf("file URI round trip = %q, want %q", roundTrip, path)
	}

	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            filepath.Dir(path),
		WorkspaceRoots: []string{filepath.Dir(path)},
	})
	fromPath, err := (&manager{}).resolveDocumentRef(ctx, path, "prisma")
	if err != nil {
		t.Fatalf("resolve Windows path document ref: %v", err)
	}
	fromURI, err := (&manager{}).resolveDocumentRef(ctx, got, "prisma")
	if err != nil {
		t.Fatalf("resolve Windows file URI document ref: %v", err)
	}
	if fromPath.absPath != fromURI.absPath || fromPath.uri != fromURI.uri {
		t.Fatalf("Windows path/URI identities diverged: path=%+v uri=%+v", fromPath, fromURI)
	}
}
