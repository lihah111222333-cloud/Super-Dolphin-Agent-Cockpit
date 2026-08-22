package archtest

import "testing"

// TestLocalReceiptLookupReverifyBoundaryGuard keeps local receipt verification
// on the typed candidate-object authority after retiring remote PASS lookup.
func TestLocalReceiptLookupReverifyBoundaryGuard(t *testing.T) {
	root := findRepoRoot(t)
	receiptFile := parseRemoteCIContractGuardFile(t, root+"/internal/devtools/gate/local_executor_receipt.go")
	prelookup := remoteCIFunctionByName(receiptFile, "reverifyForLookup")
	if prelookup == nil || !localPassFunctionHasIdentifier(prelookup, []string{"prelookupTrustedGit", "reverifyLookupBoundMaterial"}) {
		t.Fatal("local receipt pre-lookup reverify must retain the typed authority and exact-tree proof boundary")
	}
}
