package mcpcontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	_ "modernc.org/sqlite"
)

func TestManagedRegisterWrongClaimsDoNotConsumeToken(t *testing.T) {
	store := NewMemoryGenerationStore()
	registry := newStrictManagedTestRegistry(store)
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{
		BinaryName: "mcp-orch",
	})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	wrong := managedRegisterRequest(bootstrap, "request-wrong")
	wrong.AgentID = "forged-agent"
	if _, err := callManagedRegister(t, registry, wrong); err == nil {
		t.Fatal("Register(wrong claims) error = nil")
	}

	valid := managedRegisterRequest(bootstrap, "request-valid")
	resp, err := callManagedRegister(t, registry, valid)
	if err != nil {
		t.Fatalf("Register(valid after wrong claims) error = %v", err)
	}
	if resp.Generation == 0 || resp.ManagedAuthority == nil || resp.ManagedAuthority.NextToken == "" {
		t.Fatalf("Register() response = %#v, want generation and rotated token", resp)
	}
}

func TestManagedRegisterCapabilityMismatchDoesNotConsumeToken(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{
		BinaryName: "mcp-orch",
	})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	invalid := managedRegisterRequest(bootstrap, "request-invalid-capability")
	invalid.CapabilitiesOffered = []string{" tools/task ", "tools/task"}
	invalid.CapabilitiesRequired = []string{"tools/task", "tools/missing"}
	if _, err := callManagedRegister(t, registry, invalid); err == nil ||
		!strings.Contains(err.Error(), "required capabilities are not offered") {
		t.Fatalf("Register(required not offered) error = %v, want capability mismatch", err)
	}
	rejectedRequired := managedRegisterRequest(bootstrap, "request-rejected-required")
	rejectedRequired.CapabilitiesOffered = []string{"tools/task", "tools/unknown"}
	rejectedRequired.CapabilitiesRequired = []string{"tools/unknown"}
	if _, err := callManagedRegister(t, registry, rejectedRequired); err == nil ||
		!strings.Contains(err.Error(), "rejected by managed profile") {
		t.Fatalf("Register(required rejected by profile) error = %v, want profile mismatch", err)
	}

	_ = requireManagedRegister(
		t,
		registry,
		managedRegisterRequest(bootstrap, "request-valid-after-mismatch"),
		"valid after capability mismatch",
	)
}

func TestManagedRegisterDedupeAndCapabilityNegotiationAreReal(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{
		BinaryName: "mcp-orch",
	})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	valid := managedRegisterRequest(bootstrap, "request-valid-capability")
	valid.CapabilitiesOffered = []string{" tools/task ", "tools/task", "tools/workspace", "tools/unknown"}
	valid.CapabilitiesRequired = []string{"tools/task", " tools/task "}
	valid.Subscriptions = []string{" config/agent ", "config/agent", "config/thread"}
	response := requireManagedRegister(t, registry, valid, "valid after capability mismatch")
	if got := strings.Join(response.CapabilitiesNegotiated, ","); got != "tools/task,tools/workspace" {
		t.Fatalf("CapabilitiesNegotiated = %q, want deduped offered capabilities", got)
	}
	if got := strings.Join(response.CapabilitiesRejected, ","); got != "tools/unknown" {
		t.Fatalf("CapabilitiesRejected = %q, want unsupported optional capability", got)
	}
	instance, ok := registry.GetInstance(response.LeaseKey())
	if !ok {
		t.Fatal("managed lease missing")
	}
	if got := strings.Join(instance.Subscriptions, ","); got != "config/agent,config/thread" {
		t.Fatalf("Subscriptions = %q, want deduped subscriptions", got)
	}
	if got := strings.Join(instance.Capabilities, ","); got != "tools/task,tools/workspace" {
		t.Fatalf("instance Capabilities = %q, want negotiated capabilities only", got)
	}
}

func TestManagedOrchAllowedCapabilitiesAreRequestLocal(t *testing.T) {
	first := managedOrchAllowedCapabilities()
	delete(first, "tools/task")
	second := managedOrchAllowedCapabilities()
	if _, ok := second["tools/task"]; !ok {
		t.Fatal("managed capability profile leaked mutation across requests")
	}
	request := dto.RegisterRequest{
		ClientKind:          dto.ClientKindOrch,
		CapabilitiesOffered: []string{"tools/workspace", "tools/unknown", "tools/task"},
		ManagedAuthority:    &dto.ManagedAuthorityProof{},
	}
	negotiated, rejected := negotiateRegisterCapabilities(request)
	if got := strings.Join(negotiated, ","); got != "tools/workspace,tools/task" {
		t.Fatalf("negotiated capabilities = %q, want request order preserved", got)
	}
	if got := strings.Join(rejected, ","); got != "tools/unknown" {
		t.Fatalf("rejected capabilities = %q, want unsupported capability only", got)
	}
}

func TestManagedRegisterSameRequestRecoversLostAckAndRejectsReplay(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	req := managedRegisterRequest(bootstrap, "request-1")
	first := requireManagedRegister(t, registry, req, "first")
	retry := requireManagedRegister(t, registry, req, "same-request")
	if retry.Generation != first.Generation || retry.ManagedAuthority == nil ||
		first.ManagedAuthority == nil || retry.ManagedAuthority.NextToken != first.ManagedAuthority.NextToken {
		t.Fatalf("same-request response = %#v, want exact receipt %#v", retry, first)
	}

	next := req
	next.ManagedAuthority = &dto.ManagedAuthorityProof{
		ProtocolVersion: dto.ManagedAuthorityProtocolVersion,
		RequestID:       "request-2",
		Token:           first.ManagedAuthority.NextToken,
	}
	second := requireManagedRegister(t, registry, next, "next-token")
	if second.Generation <= first.Generation {
		t.Fatalf("next generation = %d, want > %d", second.Generation, first.Generation)
	}
	if _, err := callManagedRegister(t, registry, req); err == nil {
		t.Fatal("replay old request/token error = nil")
	}
}

func TestManagedGenerationSurvivesEvictAndRegistryRestart(t *testing.T) {
	store := NewMemoryGenerationStore()
	registry := newStrictManagedTestRegistry(store)
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	first, err := callManagedRegister(t, registry, managedRegisterRequest(bootstrap, "request-1"))
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	registry.OnDisconnect(first.LeaseKey())

	restarted := newStrictManagedTestRegistry(store)
	nextBootstrap, err := restarted.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("restarted IssueManagedAuthority() error = %v", err)
	}
	resume := first.Generation
	req := managedRegisterRequest(nextBootstrap, "request-2")
	req.ResumeFromGeneration = &resume
	second, err := callManagedRegister(t, restarted, req)
	if err != nil {
		t.Fatalf("restarted Register() error = %v", err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("restarted generation = %d, want > %d", second.Generation, first.Generation)
	}
}

func TestSQLiteGenerationStoreSerializesTwoProcesses(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	type childResult struct {
		output string
		err    error
	}
	outputs := make(chan childResult, 2)
	var children sync.WaitGroup
	for range 2 {
		children.Go(func() {
			output, err := runGenerationChildCommand(dbPath, markerPath, "next")
			select {
			case outputs <- childResult{output: output, err: err}:
			case <-ctx.Done():
			}
		})
	}
	results := make([]childResult, 0, 2)
	for range 2 {
		select {
		case result := <-outputs:
			results = append(results, result)
		case <-ctx.Done():
			t.Fatalf("concurrent generation children did not finish: %v", ctx.Err())
		}
	}
	children.Wait()
	first, second := results[0], results[1]
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent generation children errors = %v / %v, output = %q / %q",
			first.err, second.err, first.output, second.output)
	}
	got := []int{parseChildGeneration(t, first.output), parseChildGeneration(t, second.output)}
	sort.Ints(got)
	if fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("concurrent child generations = %v, want [1 2]", got)
	}
}

func TestSQLiteGenerationStoreSurvivesRestartAndRejectsDeletedHistory(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	first := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	second := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if second <= first {
		t.Fatalf("restarted generation = %d, want > %d", second, first)
	}

	db := openGenerationTestDB(t, dbPath)
	if _, err := db.Exec("DELETE FROM mcp_managed_generations WHERE instance_id = ?", managedOrchInstanceID); err != nil {
		t.Fatalf("delete generation history: %v", err)
	}
	store := newSQLiteGenerationTestStore(t, db, markerPath)
	if _, err := store.Next(managedOrchInstanceID, nil); err == nil ||
		!strings.Contains(err.Error(), "history is incomplete") {
		t.Fatalf("Next(after deleted history) error = %v, want incomplete history", err)
	}
}

func TestSQLiteGenerationStoreRejectsDeletedOwnerState(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	_ = parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if err := os.Remove(markerPath); err != nil {
		t.Fatalf("remove generation owner marker: %v", err)
	}
	db := openGenerationTestDB(t, dbPath)
	store := newSQLiteGenerationTestStore(t, db, markerPath)
	if _, err := store.Next(managedOrchInstanceID, nil); err == nil ||
		!strings.Contains(err.Error(), "marker is missing") {
		t.Fatalf("Next(after deleted owner marker) error = %v, want missing marker", err)
	}
}

func TestSQLiteGenerationStoreRejectsDeletedExternalLedger(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	_ = parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if err := os.RemoveAll(markerPath + ".ledger"); err != nil {
		t.Fatalf("remove generation ledger: %v", err)
	}
	db := openGenerationTestDB(t, dbPath)
	store := newSQLiteGenerationTestStore(t, db, markerPath)
	if _, err := store.Next(managedOrchInstanceID, nil); err == nil ||
		!strings.Contains(err.Error(), "ledger history is missing") {
		t.Fatalf("Next(after deleted ledger) error = %v, want missing ledger history", err)
	}
}

func TestSQLiteGenerationStoreRejectsDeletedDatabaseHistory(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	_ = parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove generation database state %s: %v", filepath.Base(path), err)
		}
	}
	db := openGenerationTestDB(t, dbPath)
	migration, err := os.ReadFile(filepath.Join("..", "db", "sqlite", "migrations", "122_mcp_managed_generations.sql"))
	if err != nil {
		t.Fatalf("read generation migration: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("recreate generation schema: %v", err)
	}
	store := newSQLiteGenerationTestStore(t, db, markerPath)
	if _, err := store.Next(managedOrchInstanceID, nil); err == nil ||
		!strings.Contains(err.Error(), "conflicts with uninitialized durable epoch") {
		t.Fatalf("Next(after deleted database history) error = %v, want historical marker conflict", err)
	}
}

func TestSQLiteGenerationStoreRejectsOnlineBackupRollback(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	first := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	backupPath := filepath.Join(t.TempDir(), "generation-backup.sqlite")
	db := openGenerationTestDB(t, dbPath)
	if _, err := db.Exec("VACUUM INTO ?", backupPath); err != nil {
		t.Fatalf("online backup generation database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close generation database before restore: %v", err)
	}
	second := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if second <= first {
		t.Fatalf("second generation = %d, want > %d", second, first)
	}
	if err := os.Rename(backupPath, dbPath); err != nil {
		t.Fatalf("restore online generation backup: %v", err)
	}
	restored := openGenerationTestDB(t, dbPath)
	store := newSQLiteGenerationTestStore(t, restored, markerPath)
	if _, err := store.Next(managedOrchInstanceID, nil); err == nil ||
		!strings.Contains(err.Error(), "rollback") {
		t.Fatalf("Next(after online backup rollback) error = %v, want rollback detection", err)
	}
}

func TestSQLiteGenerationStoreRecoversInterruptedOwnerMarkerCommit(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	db := openGenerationTestDB(t, dbPath)
	var epoch string
	if err := db.QueryRow(
		"SELECT owner_epoch FROM mcp_managed_generation_owner WHERE singleton_id = 1",
	).Scan(&epoch); err != nil {
		t.Fatalf("load generation owner epoch: %v", err)
	}
	if err := os.WriteFile(markerPath, []byte(epoch+"\n"), 0o600); err != nil {
		t.Fatalf("simulate interrupted generation owner marker commit: %v", err)
	}
	store := newSQLiteGenerationTestStore(t, db, markerPath)
	generation, err := store.Next(managedOrchInstanceID, nil)
	if err != nil {
		t.Fatalf("Next(after interrupted marker commit) error = %v", err)
	}
	if generation != 1 {
		t.Fatalf("generation after interrupted marker commit = %d, want 1", generation)
	}
}

func TestSQLiteGenerationStoreCrashBeforeAndAfterCommit(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	first := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	runGenerationChild(t, dbPath, markerPath, "crash-before-commit")
	second := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if second != first+1 {
		t.Fatalf("generation after pre-commit crash = %d, want %d", second, first+1)
	}
	runGenerationChild(t, dbPath, markerPath, "crash-after-commit")
	fourth := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if fourth != second+2 {
		t.Fatalf("generation after post-commit crash = %d, want %d", fourth, second+2)
	}
}

func TestSQLiteGenerationStoreRecoversLedgerProtocolCrashWindows(t *testing.T) {
	dbPath, markerPath := prepareSQLiteGenerationStore(t)
	runGenerationChild(t, dbPath, markerPath, "crash-after-intent")
	first := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if first != 1 {
		t.Fatalf("generation after intent crash = %d, want 1", first)
	}
	runGenerationChild(t, dbPath, markerPath, "crash-after-sqlite")
	third := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if third != 3 {
		t.Fatalf("generation after SQLite crash = %d, want 3", third)
	}
	runGenerationChild(t, dbPath, markerPath, "crash-after-ledger-commit")
	fifth := parseChildGeneration(t, runGenerationChild(t, dbPath, markerPath, "next"))
	if fifth != 5 {
		t.Fatalf("generation after ledger commit crash = %d, want 5", fifth)
	}
}

func TestSQLiteGenerationStoreChildProcess(t *testing.T) {
	if os.Getenv("MCP_GENERATION_CHILD") != "1" {
		return
	}
	dbPath := os.Getenv("MCP_GENERATION_DB")
	markerPath := os.Getenv("MCP_GENERATION_MARKER")
	mode := os.Getenv("MCP_GENERATION_MODE")
	db, err := sql.Open("sqlite", generationTestDSN(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSQLiteGenerationStore(db, markerPath)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "next", "crash-after-commit":
		runGenerationNextChild(t, store, mode)
	case "crash-before-commit":
		runGenerationPreCommitCrashChild(t, db)
	case "crash-after-intent", "crash-after-sqlite", "crash-after-ledger-commit":
		runGenerationLedgerCrashChild(t, store, mode)
	default:
		t.Fatalf("unknown child mode %q", mode)
	}
}

func runGenerationNextChild(t *testing.T, store *SQLiteGenerationStore, mode string) {
	t.Helper()
	generation, err := store.Next(managedOrchInstanceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("GEN=%d\n", generation)
	if mode == "crash-after-commit" {
		os.Exit(0)
	}
}

func runGenerationPreCommitCrashChild(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(
		"UPDATE mcp_managed_generations SET generation = generation + 1 WHERE instance_id = ?",
		managedOrchInstanceID,
	); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func runGenerationLedgerCrashChild(t *testing.T, store *SQLiteGenerationStore, mode string) {
	t.Helper()
	err := withGenerationOwnerLock(store.markerPath+".lock", func() error {
		if err := store.ensureOwnerMarker(); err != nil {
			return err
		}
		epoch, err := readGenerationOwnerEpoch(store.markerPath)
		if err != nil {
			return err
		}
		if err := store.ensureGenerationLedger(epoch); err != nil {
			return err
		}
		state, snapshot, err := store.reconcileGenerationLedger(epoch, managedOrchInstanceID)
		if err != nil {
			return err
		}
		next, claimID, err := store.reserveGenerationClaim(
			epoch,
			managedOrchInstanceID,
			state,
			snapshot,
		)
		if err != nil || mode == "crash-after-intent" {
			return err
		}
		if err := store.advanceSQLiteGenerationClaim(
			managedOrchInstanceID,
			state,
			next,
			claimID,
		); err != nil || mode == "crash-after-sqlite" {
			return err
		}
		return store.writeGenerationLedgerRecord(
			epoch,
			managedOrchInstanceID,
			next,
			claimID,
			"commit",
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func prepareSQLiteGenerationStore(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "generation.sqlite")
	db := openGenerationTestDB(t, dbPath)
	migration, err := os.ReadFile(filepath.Join("..", "db", "sqlite", "migrations", "122_mcp_managed_generations.sql"))
	if err != nil {
		t.Fatalf("read generation migration: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("apply generation migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close prepared generation DB: %v", err)
	}
	return dbPath, filepath.Join(dir, "generation.owner")
}

func openGenerationTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", generationTestDSN(path))
	if err != nil {
		t.Fatalf("open generation DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func generationTestDSN(path string) string {
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)"
}

func newSQLiteGenerationTestStore(t *testing.T, db *sql.DB, markerPath string) *SQLiteGenerationStore {
	t.Helper()
	store, err := NewSQLiteGenerationStore(db, markerPath)
	if err != nil {
		t.Fatalf("NewSQLiteGenerationStore() error = %v", err)
	}
	return store
}

func runGenerationChild(t *testing.T, dbPath, markerPath, mode string) string {
	t.Helper()
	output, err := runGenerationChildCommand(dbPath, markerPath, mode)
	if err != nil {
		t.Fatalf("generation child mode=%s error=%v output=%s", mode, err, output)
	}
	return output
}

func runGenerationChildCommand(dbPath, markerPath, mode string) (string, error) {
	command := exec.Command(os.Args[0], "-test.run=^TestSQLiteGenerationStoreChildProcess$")
	command.Env = append(os.Environ(),
		"MCP_GENERATION_CHILD=1",
		"MCP_GENERATION_DB="+dbPath,
		"MCP_GENERATION_MARKER="+markerPath,
		"MCP_GENERATION_MODE="+mode,
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func parseChildGeneration(t *testing.T, output string) int {
	t.Helper()
	start := strings.Index(output, "GEN=")
	if start < 0 {
		t.Fatalf("child output missing generation: %q", output)
	}
	value := strings.Fields(output[start+len("GEN="):])[0]
	generation, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse child generation %q: %v", value, err)
	}
	return generation
}

func TestManagedReservedRoleIsExactAndOrchOnly(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	base := managedRegisterRequest(bootstrap, "request-1")
	mutations := []struct {
		name string
		edit func(*dto.RegisterRequest)
	}{
		{name: "agent scoped", edit: func(req *dto.RegisterRequest) { req.AgentID = "agent-1" }},
		{name: "thread scoped", edit: func(req *dto.RegisterRequest) { req.ThreadID = "thread-1" }},
		{name: "not shared", edit: func(req *dto.RegisterRequest) { req.Shared = false }},
		{name: "tool role", edit: func(req *dto.RegisterRequest) { req.PeerKind = dto.PeerKindTool }},
		{name: "lsp", edit: func(req *dto.RegisterRequest) { req.ClientKind = dto.ClientKindLSP }},
		{name: "custom binary", edit: func(req *dto.RegisterRequest) { req.BinaryName = "custom" }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			tt.edit(&req)
			if _, err := callManagedRegister(t, registry, req); err == nil {
				t.Fatal("Register(forged reserved role) error = nil")
			}
		})
	}
}

func TestStrictOrchRejectsLegacyClientWithoutManagedProof(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	for _, clientKind := range []string{
		dto.ClientKindOrch,
		"ORCH",
		" OrCh ",
		"\torch\n",
		"",
	} {
		_, err := callManagedRegister(t, registry, dto.RegisterRequest{
			InstanceID: "legacy-orch",
			BinaryName: "mcp-orch",
			ClientKind: clientKind,
			PeerKind:   dto.PeerKindSharedService,
			Shared:     true,
			PID:        100,
		})
		if err == nil || !strings.Contains(err.Error(), "requires managed authority") {
			t.Fatalf("Register(legacy orch client_kind=%q) error = %v, want explicit managed authority rejection", clientKind, err)
		}
	}
}

func TestStrictOrchActivationLeavesLSPAndIDARegistrationUnchanged(t *testing.T) {
	for _, clientKind := range []string{dto.ClientKindLSP, dto.ClientKindIDA} {
		t.Run(clientKind, func(t *testing.T) {
			registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
			resp, err := callManagedRegister(t, registry, dto.RegisterRequest{
				InstanceID: "legacy-" + clientKind,
				BinaryName: "mcp-" + clientKind,
				ClientKind: clientKind,
				PeerKind:   dto.PeerKindTool,
				PID:        101,
			})
			if err != nil {
				t.Fatalf("legacy %s Register() error = %v", clientKind, err)
			}
			if resp.ManagedAuthority != nil {
				t.Fatalf("legacy %s response unexpectedly negotiated managed authority", clientKind)
			}
			got, ok := registry.GetInstance(resp.LeaseKey())
			if !ok {
				t.Fatalf("legacy %s lease missing", clientKind)
			}
			if got.ClientKind != clientKind || got.PeerKind != dto.PeerKindSharedService || !got.Shared {
				t.Fatalf("legacy %s shape = client:%q peer:%q shared:%v", clientKind, got.ClientKind, got.PeerKind, got.Shared)
			}
		})
	}
}

func managedRegisterRequest(bootstrap dto.ManagedAuthorityBootstrap, requestID string) dto.RegisterRequest {
	return dto.RegisterRequest{
		InstanceID: bootstrap.InstanceID,
		BootID:     bootstrap.BootID,
		BinaryName: "mcp-orch",
		ClientKind: dto.ClientKindOrch,
		PeerKind:   dto.PeerKindSharedService,
		Shared:     true,
		AgentID:    "",
		ThreadID:   "",
		PID:        100,
		ManagedAuthority: &dto.ManagedAuthorityProof{
			ProtocolVersion: dto.ManagedAuthorityProtocolVersion,
			RequestID:       requestID,
			Token:           bootstrap.Token,
		},
	}
}

func newStrictManagedTestRegistry(store GenerationStore) *ToolRegistry {
	return NewToolRegistry(RegistryOptions{
		GenerationStore:    store,
		StrictManagedKinds: []string{dto.ClientKindOrch},
	})
}

func callManagedRegister(t *testing.T, registry *ToolRegistry, req dto.RegisterRequest) (dto.RegisterResponse, error) {
	t.Helper()
	local := jrpcserver.NewLocal(handler.Map{
		dto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, request dto.RegisterRequest) (dto.RegisterResponse, error) {
			return registry.Register(ctx, request)
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()
	var resp dto.RegisterResponse
	err := local.Client.CallResult(context.Background(), dto.MethodRegister, req, &resp)
	return resp, err
}

func requireManagedRegister(
	t *testing.T,
	registry *ToolRegistry,
	req dto.RegisterRequest,
	operation string,
) dto.RegisterResponse {
	t.Helper()
	response, err := callManagedRegister(t, registry, req)
	if err != nil {
		t.Fatalf("%s Register() error = %v", operation, err)
	}
	return response
}
