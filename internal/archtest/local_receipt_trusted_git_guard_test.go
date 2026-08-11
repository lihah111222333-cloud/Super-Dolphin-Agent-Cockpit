package archtest

import (
	"go/ast"
	"strings"
	"testing"
)

// TestLocalReceiptExactTreeTrustedGitCallGuard keeps every local receipt
// exact-object read on the one gateprivate policy while retaining the
// receipt-bound binary revalidation.
func TestLocalReceiptExactTreeTrustedGitCallGuard(t *testing.T) {
	root := findRepoRoot(t)
	path := root + "/internal/devtools/gate/local_executor_receipt_git.go"
	source := readRemoteCIContractGuardFile(t, path)
	if strings.Contains(source, "exec.CommandContext") || strings.Contains(source, "command.Dir = repositoryRoot") || strings.Contains(source, "gateprivate.TrustedGitCommand(") {
		t.Fatal("local receipt exact-tree reads must not construct raw Git commands, inherit repository routing, or delegate to the unbound Git owner")
	}
	file := parseRemoteCIContractGuardFile(t, path)
	assertLocalReceiptTrustedGitWrapper(t, file)
	assertLocalReceiptExactTreeHelperCalls(t, file)
}

func assertLocalReceiptTrustedGitWrapper(t *testing.T, file *ast.File) {
	t.Helper()
	wrapper := remoteCIFunctionByName(file, "localReceiptTrustedGitCommand")
	if wrapper == nil || !localPassFunctionHasIdentifier(wrapper, []string{"VerifiedPath"}) || !localReceiptWrapperUsesCandidateObjectAuthority(wrapper) {
		t.Fatal("local receipt Git wrapper must retain receipt binary verification and delegate to gateprivate typed Git owner with receipt-bound candidate object authority")
	}
}

func localReceiptWrapperUsesCandidateObjectAuthority(wrapper *ast.FuncDecl) bool {
	if wrapper == nil {
		return false
	}
	usesTypedOwner := false
	ast.Inspect(wrapper.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if !localReceiptUsesTypedGitOwner(call) || !localReceiptUsesReceiptBoundAuthority(call) {
			return true
		}
		usesTypedOwner = true
		return false
	})
	return usesTypedOwner
}

func localReceiptUsesTypedGitOwner(call *ast.CallExpr) bool {
	owner, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if owner.Sel.Name != "TrustedGitCommandWithCandidateObjectAuthority" {
		return false
	}
	packageName, ok := owner.X.(*ast.Ident)
	if !ok {
		return false
	}
	return packageName.Name == "gateprivate"
}

func localReceiptUsesReceiptBoundAuthority(call *ast.CallExpr) bool {
	if len(call.Args) < 3 {
		return false
	}
	authority, ok := call.Args[2].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if authority.Sel.Name != "candidateObjectAuthority" {
		return false
	}
	receipt, ok := authority.X.(*ast.Ident)
	if !ok {
		return false
	}
	return receipt.Name == "trustedGit"
}

func assertLocalReceiptExactTreeHelperCalls(t *testing.T, file *ast.File) {
	t.Helper()
	for _, name := range []string{"verifyGitTreeObject", "gitTreeBlob", "gitTreeRegularFiles", "gitTreeLocalRunnerPackageEntries"} {
		function := remoteCIFunctionByName(file, name)
		if function == nil || !localPassFunctionHasIdentifier(function, []string{"localReceiptTrustedGitCommand"}) {
			t.Errorf("local receipt exact-tree helper %s must use the trusted Git wrapper", name)
		}
	}
}
