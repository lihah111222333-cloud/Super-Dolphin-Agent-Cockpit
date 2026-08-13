package multilsp

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestManagerResolveDocumentRefAcceptsCaseInsensitiveFileURI(t *testing.T) {
	repo := normalizedTempDir(t)
	target := writeGoFile(t, repo, "中转.go")
	uri := fileURIFromPath(target)
	mixedCaseURI := "FiLe:" + strings.TrimPrefix(uri, "file:")
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            repo,
		WorkspaceRoots: []string{repo},
	})

	ref, err := (&manager{}).resolveDocumentRef(ctx, mixedCaseURI, "go")
	if err != nil {
		t.Fatalf("resolveDocumentRef(%q): %v", mixedCaseURI, err)
	}
	if ref.absPath != target {
		t.Fatalf("resolveDocumentRef(%q).absPath = %q, want %q", mixedCaseURI, ref.absPath, target)
	}
}
