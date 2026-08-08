package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func remoteCISQLSchemaSourceEntries(entries []os.DirEntry) []os.DirEntry {
	sources := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		isSchemaSource := strings.HasPrefix(name, "ledger_store_sqlite_schema") || name == "ledger_store_sqlite_observability.go"
		if entry.IsDir() || !isSchemaSource || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		sources = append(sources, entry)
	}
	return sources
}

// TestRemoteCIContractConsumerGuardCoversECI keeps Alibaba ECI consumers under
// the same canonical contract-value ownership guard as the gate packages.
func TestRemoteCIContractConsumerGuardCoversECI(t *testing.T) {
	root := findRepoRoot(t)
	const eciRoot = "internal/devtools/alicloud/eci/"
	covered := false
	for _, file := range remoteCIContractConsumerFiles(t, root) {
		if strings.Contains(filepath.ToSlash(file), eciRoot) {
			covered = true
			break
		}
	}
	if !covered {
		t.Fatalf("remote CI contract consumers do not include %s", eciRoot)
	}

	unsafe := remoteCIParseGuardFixture(t, `package fixture
const platform = "linux/amd64"
`)
	if !remoteCIRepeatsContractValue(unsafe) || remoteCIImportsContractOwner(unsafe) {
		t.Fatal("consumer owner guard fixture did not model an unowned repeated contract value")
	}
	safe := remoteCIParseGuardFixture(t, `package fixture
import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
const platform = cicontract.TargetPlatform
`)
	if remoteCIRepeatsContractValue(safe) && !remoteCIImportsContractOwner(safe) {
		t.Fatal("consumer owner guard rejected an owner-backed contract value")
	}
}

// TestRemoteCIBaselineStateSchemaVersionHasOneCanonicalOwner prevents a
// generation-specific copy from becoming a second baseline schema owner.
func TestRemoteCIBaselineStateSchemaVersionHasOneCanonicalOwner(t *testing.T) {
	root := findRepoRoot(t)
	contractPath := filepath.Join(root, "internal", "devtools", "cicontract", "contract.go")
	contractSource := readRemoteCIContractGuardFile(t, contractPath)
	owner := fmt.Sprintf("BaselineStateSchemaVersion uint32 = %d", cicontract.BaselineStateSchemaVersion)
	if strings.Count(contractSource, owner) != 1 {
		t.Fatalf("canonical baseline schema owner declarations = %d, want 1", strings.Count(contractSource, owner))
	}
	for _, relative := range []string{
		"internal/devtools/cicontract/generation_one.go",
		"internal/devtools/gate/remote_baseline_generation_one.go",
		"internal/devtools/gate/remote_baseline_generation_one_test.go",
	} {
		source := readRemoteCIContractGuardFile(t, filepath.Join(root, filepath.FromSlash(relative)))
		if strings.Contains(source, "GenerationOneBaselineStateSchemaVersion") {
			t.Errorf("%s retains the retired duplicate generation-one baseline schema owner", relative)
		}
		if strings.HasPrefix(relative, "internal/devtools/gate/") && !strings.Contains(source, "cicontract.BaselineStateSchemaVersion") {
			t.Errorf("%s does not consume cicontract.BaselineStateSchemaVersion", relative)
		}
	}
}

// TestRemoteCISQLAuthoritySchemaRejectsUnregisteredExtraTable proves the
// reverse schema guard rejects a second physical SQLite authority.
func TestRemoteCISQLAuthoritySchemaRejectsUnregisteredExtraTable(t *testing.T) {
	safe := `const schema = "CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT PRIMARY KEY)"`
	if extras := remoteCIUnregisteredSQLSchemaTables(safe); len(extras) != 0 {
		t.Fatalf("registered SQLite schema fixture was rejected: %v", extras)
	}
	legacy := `const schema = "CREATE TABLE IF NOT EXISTS ci_runs (job_id TEXT PRIMARY KEY) CREATE TABLE IF NOT EXISTS ci_unregistered_second_authority (id TEXT)"`
	extras := remoteCIUnregisteredSQLSchemaTables(legacy)
	if len(extras) != 1 || extras[0] != "ci_unregistered_second_authority" {
		t.Fatalf("unregistered SQLite schema fixture violations = %v, want the extra table", extras)
	}
}
