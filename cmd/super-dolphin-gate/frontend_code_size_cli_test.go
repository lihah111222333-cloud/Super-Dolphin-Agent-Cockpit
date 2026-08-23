package main

import (
	"bytes"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

// TestFrontendCodeSizeCLIRejectsMissingTree 验证命令不接受隐式工作区候选源。
func TestFrontendCodeSizeCLIRejectsMissingTree(t *testing.T) {
	code := runCLI([]string{"frontend-code-size", "check"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != int(gatecontract.ExitProtocol) {
		t.Fatalf("exit=%d, want protocol", code)
	}
}

func TestParseFrontendCodeSizeArgsCanonicalizesSingleDashAndRejectsMixedDuplicates(t *testing.T) {
	operation, tree, accepted, err := parseFrontendCodeSizeArgs([]string{"check", "-tree", "tree", "-accepted-tree", "accepted"})
	if err != nil || operation != "check" || tree != "tree" || accepted != "accepted" {
		t.Fatalf("single-dash frontend authority = %q %q %q, %v", operation, tree, accepted, err)
	}
	_, _, _, err = parseFrontendCodeSizeArgs([]string{"check", "-tree", "first", "--tree=second", "--accepted-tree", "accepted"})
	if err == nil || !strings.Contains(err.Error(), "duplicate authority flag --tree") {
		t.Fatalf("mixed duplicate frontend tree error = %v", err)
	}
}
