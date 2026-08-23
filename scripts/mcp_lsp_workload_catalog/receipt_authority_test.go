package catalog

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateReceiptFailsWhenRepositoryAuthorityCannotBeResolved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	err := ValidateReceipt(Catalog{}, "missing", path)
	if err == nil || !strings.Contains(err.Error(), "repository root not found") {
		t.Fatalf("ValidateReceipt() error = %v, want repository authority failure", err)
	}
}
