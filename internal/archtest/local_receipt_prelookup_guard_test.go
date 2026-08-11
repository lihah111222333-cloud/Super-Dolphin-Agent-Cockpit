package archtest

import "testing"

// TestLocalReceiptLookupReverifyBoundaryGuard prevents PASS lookup from using
// the materialized-root reverify path. A hit must verify exact Git objects
// through the sealed candidate authority without touching the worktree.
func TestLocalReceiptLookupReverifyBoundaryGuard(t *testing.T) {
	root := findRepoRoot(t)
	planFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/remoteci/local_workload_plan.go")
	lookup := remoteCIFunctionByName(planFile, "validateLocalWorkloadPlanReceipt")
	if lookup == nil || !localPassFunctionHasIdentifier(lookup, []string{"ReverifyLocalExecutorSessionReceiptForLookup"}) || localPassFunctionHasIdentifier(lookup, []string{"Reverify"}) {
		t.Fatal("local PASS lookup must use the exact-tree pre-lookup receipt reverify and must not call materialized-root Reverify")
	}
	receiptFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt.go")
	prelookup := remoteCIFunctionByName(receiptFile, "reverifyForLookup")
	if prelookup == nil || !localPassFunctionHasIdentifier(prelookup, []string{"prelookupTrustedGit", "reverifyLookupBoundMaterial"}) {
		t.Fatal("local receipt pre-lookup reverify must retain the typed authority and exact-tree proof boundary")
	}
	fingerprintFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/remoteci/workload_fingerprint_git.go")
	for _, name := range []string{"loadRemoteGitTreeSnapshotWithCandidateObjectAuthority", "readGitBlobs"} {
		function := remoteCIFunctionByName(fingerprintFile, name)
		if function == nil || !localPassFunctionHasIdentifier(function, []string{"TrustedGitCommandWithCandidateObjectAuthority"}) {
			t.Fatalf("local private candidate fingerprint %s must use the typed candidate object authority", name)
		}
	}
}
