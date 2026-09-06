package sqliteauthority

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// localNonAuthoritativeLifecycleLease 只模拟本地测试中的校验调用与拒绝；它不代表生产租约、所有权或 fence 证据。
type localNonAuthoritativeLifecycleLease struct {
	calls        int
	rejectOnCall int
}

func (l *localNonAuthoritativeLifecycleLease) Validate(context.Context, AuthorityScope) error {
	l.calls++
	if l.calls == l.rejectOnCall {
		return errors.New("local non-authoritative lease rejection")
	}
	return nil
}

func (l *localNonAuthoritativeLifecycleLease) WithValidatedScope(
	ctx context.Context,
	scope AuthorityScope,
	callback func() error,
) error {
	if err := l.Validate(ctx, scope); err != nil {
		return ErrLeaseRejected
	}
	return callback()
}

// localNonAuthoritativeLifecycleFinalizer 只生成本地夹具或注入错误；其结果不是生产 quorum、签名或 evidence。
type localNonAuthoritativeLifecycleFinalizer struct {
	config        Config
	finalizeErr   error
	queryErr      error
	finalizeCalls int
	queryCalls    int
}

func (f *localNonAuthoritativeLifecycleFinalizer) Finalize(
	_ context.Context,
	request FinalizationRequest,
) (FinalizedEvidence, error) {
	f.finalizeCalls++
	if f.finalizeErr != nil {
		return FinalizedEvidence{}, f.finalizeErr
	}
	return finalizedEvidenceFixture(f.config, request.ExpectedResolution)
}

func (f *localNonAuthoritativeLifecycleFinalizer) Query(
	_ context.Context,
	request FinalizationRequest,
) (FinalizationObservation, error) {
	f.queryCalls++
	if f.queryErr != nil {
		return FinalizationObservation{}, f.queryErr
	}
	return finalizationObservationFixture(request, FinalizationQueryAbsent, nil), nil
}

// localNonAuthoritativeLifecycleVerifier 仅允许本地生命周期夹具继续执行，不证明生产 receipt 的真实性。
type localNonAuthoritativeLifecycleVerifier struct{}

func (localNonAuthoritativeLifecycleVerifier) VerifyReceipt(
	context.Context,
	recoverycustody.SubmissionHandle,
	recoverycustody.ExternalReceipt,
) error {
	return nil
}

func TestSQLiteAuthorityLifecycle(t *testing.T) {
	t.Run("open_contract", testLifecycleOpenContract)
	t.Run("lease_validation", testLifecycleLeaseValidation)
	t.Run("path_isolation", testLifecyclePathIsolation)
	t.Run("external_service_private_only", testExternalServicePrivateOnly)
	t.Run("private_filesystem", testLifecyclePrivateFilesystem)
	t.Run("replay_and_recovery", testLifecycleReplayAndRecovery)
}

func testExternalServicePrivateOnly(t *testing.T) {
	config := authorityTestConfig(t, "external-private-only")
	generic := config
	invalidPrivate := config
	invalidPrivate.ExternalService = true
	if err := invalidPrivate.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() accepted external mode with generic paths: %v", err)
	}
	config.ExternalService = true
	config.ProductSQLitePath = ""
	config.WorkspaceRoot = ""
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() external private-only error = %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() external private-only error = %v", err)
	}
	if err := authority.Close(); err != nil {
		t.Fatalf("Close() external private-only error = %v", err)
	}
	generic.ExternalService = false
	if _, err := OpenAuthority(t.Context(), generic, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{}); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("OpenAuthority() accepted a mode-drifted generic config: %v", err)
	}
	genericDB := authorityTestConfig(t, "generic-to-private")
	if err := Bootstrap(t.Context(), genericDB, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() generic mode error = %v", err)
	}
	private := genericDB
	private.ExternalService = true
	private.ProductSQLitePath = ""
	private.WorkspaceRoot = ""
	if _, err := OpenAuthority(t.Context(), private, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{}); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("OpenAuthority() accepted a generic DB in private-only mode: %v", err)
	}
}

func testLifecyclePrivateFilesystem(t *testing.T) {
	t.Run("world_readable_authority_file", func(t *testing.T) {
		config := authorityTestConfig(t, "world-readable")
		if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
			t.Fatalf("Bootstrap() error = %v", err)
		}
		if err := os.Chmod(config.Path, 0o644); err != nil {
			t.Fatalf("chmod authority file: %v", err)
		}
		authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
		if authority != nil {
			_ = authority.Close()
			t.Fatal("OpenAuthority() accepted a world-readable authority file")
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("OpenAuthority() error = %v, want ErrInvalidConfig", err)
		}
	})

	t.Run("sidecar_symlink", func(t *testing.T) {
		config := authorityTestConfig(t, "sidecar-symlink")
		if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
			t.Fatalf("Bootstrap() error = %v", err)
		}
		target := filepath.Join(filepath.Dir(config.Path), "wal-target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatalf("write sidecar target: %v", err)
		}
		if err := os.Symlink(target, config.Path+"-wal"); err != nil {
			t.Fatalf("symlink WAL sidecar: %v", err)
		}
		authority, err := OpenAuthority(t.Context(), config, acceptingLease{}, inertFinalizer{}, acceptingReceiptVerifier{})
		if authority != nil {
			_ = authority.Close()
			t.Fatal("OpenAuthority() accepted a symlinked WAL sidecar")
		}
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("OpenAuthority() error = %v, want ErrInvalidConfig", err)
		}
	})
}

func testLifecycleOpenContract(t *testing.T) {
	config := authorityTestConfig(t, "lifecycle-open")
	lease := &localNonAuthoritativeLifecycleLease{}
	if err := Bootstrap(t.Context(), config, lease); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if lease.calls != 1 {
		t.Fatalf("lease calls after Bootstrap = %d, want 1", lease.calls)
	}
	assertAuthorityFilePrivate(t, config.Path)

	authority, err := OpenAuthority(t.Context(), config, lease,
		&localNonAuthoritativeLifecycleFinalizer{config: config}, localNonAuthoritativeLifecycleVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if lease.calls != 2 {
		t.Fatalf("lease calls after OpenAuthority = %d, want 2", lease.calls)
	}
	if authority.db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", authority.db.Stats().MaxOpenConnections)
	}
	for _, check := range []struct {
		name string
		want string
	}{
		{name: "journal_mode", want: "wal"},
		{name: "synchronous", want: "2"},
		{name: "foreign_keys", want: "1"},
		{name: "busy_timeout", want: "5000"},
	} {
		var got string
		if err := authority.db.QueryRowContext(t.Context(), "PRAGMA "+check.name).Scan(&got); err != nil {
			t.Fatalf("read PRAGMA %s: %v", check.name, err)
		}
		if !strings.EqualFold(got, check.want) {
			t.Fatalf("PRAGMA %s = %q, want %q", check.name, got, check.want)
		}
	}
	assertAuthorityFilePrivate(t, config.Path)
}

func testLifecycleLeaseValidation(t *testing.T) {
	t.Run("bootstrap_rejection", testLifecycleBootstrapLeaseRejection)
	t.Run("open_rejection", testLifecycleOpenLeaseRejection)
	t.Run("each_mutation_revalidates_and_rejection_is_sticky", testLifecycleMutationLeaseRejection)
}

func testLifecycleBootstrapLeaseRejection(t *testing.T) {
	config := authorityTestConfig(t, "lease-bootstrap-rejected")
	lease := &localNonAuthoritativeLifecycleLease{rejectOnCall: 1}
	if err := Bootstrap(t.Context(), config, lease); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Bootstrap() error = %v, want ErrLeaseRejected", err)
	}
	if lease.calls != 1 {
		t.Fatalf("lease calls = %d, want 1", lease.calls)
	}
}

func testLifecycleOpenLeaseRejection(t *testing.T) {
	config := authorityTestConfig(t, "lease-open-rejected")
	if err := Bootstrap(t.Context(), config, &localNonAuthoritativeLifecycleLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	lease := &localNonAuthoritativeLifecycleLease{rejectOnCall: 1}
	authority, err := OpenAuthority(t.Context(), config, lease,
		&localNonAuthoritativeLifecycleFinalizer{config: config}, localNonAuthoritativeLifecycleVerifier{})
	if authority != nil {
		closeUnexpectedLifecycleAuthority(t, authority)
		t.Fatal("OpenAuthority() returned an authority after lease rejection")
	}
	if !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("OpenAuthority() error = %v, want ErrLeaseRejected", err)
	}
	if lease.calls != 1 {
		t.Fatalf("lease calls = %d, want 1", lease.calls)
	}
}

func testLifecycleMutationLeaseRejection(t *testing.T) {
	authority, lease, request, pending := openLifecycleMutationAuthority(t)
	resolution := lifecycleResolutionRequest(t, pending.Handle, "lease-resolution")
	if _, err := authority.Resolve(t.Context(), resolution); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Resolve() error = %v, want ErrLeaseRejected", err)
	}
	if got := [2]any{lease.calls, authority.queryOnly.Load()}; got != [2]any{3, true} {
		t.Fatalf("post-rejection state = %v, want [3 true]", got)
	}
	assertLifecycleQueryAfterLeaseRejection(t, authority, lease, request, pending.Handle)
}

func openLifecycleMutationAuthority(
	t *testing.T,
) (*Authority, *localNonAuthoritativeLifecycleLease, recoverycustody.SubmissionRequest, recoverycustody.Outcome) {
	t.Helper()
	config := authorityTestConfig(t, "lease-mutation-rejected")
	if err := Bootstrap(t.Context(), config, &localNonAuthoritativeLifecycleLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	lease := &localNonAuthoritativeLifecycleLease{rejectOnCall: 3}
	authority, err := OpenAuthority(t.Context(), config, lease,
		&localNonAuthoritativeLifecycleFinalizer{config: config}, localNonAuthoritativeLifecycleVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	request := schemaTestRequest(t)
	pending, err := authority.Submit(t.Context(), request)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got := [2]any{pending.Status, lease.calls}; got != [2]any{recoverycustody.StatusPending, 2} {
		t.Fatalf("Submit() state = %v, want [pending 2]", got)
	}
	return authority, lease, request, pending
}

func assertLifecycleQueryAfterLeaseRejection(
	t *testing.T,
	authority *Authority,
	lease *localNonAuthoritativeLifecycleLease,
	request recoverycustody.SubmissionRequest,
	handle recoverycustody.SubmissionHandle,
) {
	t.Helper()
	queried, err := authority.Query(t.Context(), handle)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if got := [2]any{queried.Status, lease.calls}; got != [2]any{recoverycustody.StatusPending, 3} {
		t.Fatalf("Query() state = %v, want [pending 3]", got)
	}
	refused, err := authority.Submit(t.Context(), request)
	if !errors.Is(err, ErrAuthorityReadOnly) {
		t.Fatalf("sticky Submit() error = %v, want ErrAuthorityReadOnly", err)
	}
	want := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
		ReasonCode: "recovery_custody_authority_query_only"}
	if !reflect.DeepEqual(refused, want) {
		t.Fatalf("sticky Submit() = %+v, want %+v", refused, want)
	}
	if lease.calls != 3 {
		t.Fatalf("sticky query-only revalidated lease: calls = %d, want 3", lease.calls)
	}
}

func testLifecyclePathIsolation(t *testing.T) {
	tests := []struct {
		name      string
		useOpen   bool
		configure func(*testing.T, Config) Config
	}{
		{
			name:    "authority_leaf_symlink",
			useOpen: true,
			configure: func(t *testing.T, config Config) Config {
				t.Helper()
				target := filepath.Join(filepath.Dir(config.Path), "symlink-target.db")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(target, config.Path); err != nil {
					t.Fatalf("symlink authority path: %v", err)
				}
				return config
			},
		},
		{
			name: "authority_inside_workspace",
			configure: func(_ *testing.T, config Config) Config {
				config.Path = filepath.Join(config.WorkspaceRoot, "authority.db")
				return config
			},
		},
		{
			name:    "authority_equals_product_path",
			useOpen: true,
			configure: func(_ *testing.T, config Config) Config {
				config.ProductSQLitePath = config.Path
				return config
			},
		},
		{
			name: "authority_shares_product_inode",
			configure: func(t *testing.T, config Config) Config {
				t.Helper()
				if err := os.WriteFile(config.Path, nil, 0o600); err != nil {
					t.Fatalf("write authority inode fixture: %v", err)
				}
				if err := os.Link(config.Path, config.ProductSQLitePath); err != nil {
					t.Fatalf("link product inode fixture: %v", err)
				}
				return config
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.configure(t, authorityTestConfig(t, test.name))
			lease := &localNonAuthoritativeLifecycleLease{}
			var err error
			if test.useOpen {
				var authority *Authority
				authority, err = OpenAuthority(t.Context(), config, lease,
					&localNonAuthoritativeLifecycleFinalizer{config: config}, localNonAuthoritativeLifecycleVerifier{})
				if authority != nil {
					closeUnexpectedLifecycleAuthority(t, authority)
					t.Fatalf("OpenAuthority() returned non-nil authority for invalid path identity")
				}
			} else {
				err = Bootstrap(t.Context(), config, lease)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("path validation error = %v, want ErrInvalidConfig", err)
			}
			if lease.calls != 0 {
				t.Fatalf("invalid path reached lease validation: calls = %d, want 0", lease.calls)
			}
		})
	}
}

func closeUnexpectedLifecycleAuthority(t *testing.T, authority *Authority) {
	t.Helper()
	if err := authority.Close(); err != nil {
		t.Errorf("close unexpected authority: %v", err)
	}
}

func testLifecycleReplayAndRecovery(t *testing.T) {
	config := authorityTestConfig(t, "lifecycle-recovery")
	if err := Bootstrap(t.Context(), config, &localNonAuthoritativeLifecycleLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	finalizeFailure := errors.New("local non-authoritative finalizer failure")
	firstFinalizer := &localNonAuthoritativeLifecycleFinalizer{config: config, finalizeErr: finalizeFailure}
	first, err := OpenAuthority(t.Context(), config, &localNonAuthoritativeLifecycleLease{},
		firstFinalizer, localNonAuthoritativeLifecycleVerifier{})
	if err != nil {
		t.Fatalf("first OpenAuthority() error = %v", err)
	}
	request, pending := submitLifecyclePendingAndConflict(t, first)
	resolution := lifecycleResolutionRequest(t, pending.Handle, "lifecycle-resolution")
	active := failLifecycleResolution(t, first, firstFinalizer, resolution, pending.Handle)
	if err := first.Close(); err != nil {
		t.Fatalf("close first authority: %v", err)
	}
	resumeFinalizer := &localNonAuthoritativeLifecycleFinalizer{config: config}
	resumedAuthority, terminal := resumeLifecycleResolution(t, config, resumeFinalizer, pending.Handle, active)
	if err := resumedAuthority.Close(); err != nil {
		t.Fatalf("close resumed authority: %v", err)
	}
	assertLifecycleTerminalReopen(t, config, request, pending.Handle, terminal, finalizeFailure)
}

func failLifecycleResolution(
	t *testing.T,
	authority *Authority,
	finalizer *localNonAuthoritativeLifecycleFinalizer,
	resolution ResolutionRequest,
	handle recoverycustody.SubmissionHandle,
) recoverycustody.Outcome {
	t.Helper()
	failed, err := authority.Resolve(t.Context(), resolution)
	if !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("Resolve(finalizer failure) error = %v, want ErrInvalidEvidence", err)
	}
	if failed.Status != recoverycustody.StatusIndeterminate {
		t.Fatalf("Resolve(finalizer failure) status = %q, want indeterminate", failed.Status)
	}
	if got := [2]int{finalizer.finalizeCalls, finalizer.queryCalls}; got != [2]int{1, 1} {
		t.Fatalf("first finalizer calls = %v, want [1 1]", got)
	}
	active, err := authority.Query(t.Context(), handle)
	if err != nil {
		t.Fatalf("Query(active same handle) error = %v", err)
	}
	want := recoverycustody.Outcome{Handle: handle, Status: recoverycustody.StatusIndeterminate,
		ReasonCode: "recovery_custody_resolution_active"}
	if !reflect.DeepEqual(active, want) {
		t.Fatalf("Query(active same handle) = %+v, want %+v", active, want)
	}
	return active
}

func resumeLifecycleResolution(
	t *testing.T,
	config Config,
	finalizer *localNonAuthoritativeLifecycleFinalizer,
	handle recoverycustody.SubmissionHandle,
	wantActive recoverycustody.Outcome,
) (*Authority, recoverycustody.Outcome) {
	t.Helper()
	authority, err := OpenAuthority(t.Context(), config, &localNonAuthoritativeLifecycleLease{},
		finalizer, localNonAuthoritativeLifecycleVerifier{})
	if err != nil {
		t.Fatalf("reopen active authority: %v", err)
	}
	reopenedActive, err := authority.Query(t.Context(), handle)
	if err != nil {
		t.Fatalf("reopened active Query() error = %v", err)
	}
	if !reflect.DeepEqual(reopenedActive, wantActive) {
		t.Fatalf("reopened active Query() = %+v, want %+v", reopenedActive, wantActive)
	}
	terminal, err := authority.ResumeResolution(t.Context(), handle)
	if err != nil {
		t.Fatalf("ResumeResolution() error = %v", err)
	}
	if terminal.Status != recoverycustody.StatusAvailable || terminal.Receipt == nil {
		t.Fatalf("ResumeResolution() = %+v, want available terminal", terminal)
	}
	if got := [2]int{finalizer.finalizeCalls, finalizer.queryCalls}; got != [2]int{1, 1} {
		t.Fatalf("resume finalizer calls = %v, want [1 1]", got)
	}
	return authority, terminal
}

func submitLifecyclePendingAndConflict(
	t *testing.T,
	authority *Authority,
) (recoverycustody.SubmissionRequest, recoverycustody.Outcome) {
	t.Helper()
	request := schemaTestRequest(t)
	pending, err := authority.Submit(t.Context(), request)
	if err != nil || pending.Status != recoverycustody.StatusPending {
		t.Fatalf("Submit() = (%+v, %v), want pending", pending, err)
	}
	replayed, err := authority.Submit(t.Context(), request)
	if err != nil || !reflect.DeepEqual(replayed, pending) {
		t.Fatalf("exact Submit replay = (%+v, %v), want %+v", replayed, err, pending)
	}
	conflictingRequest := request
	conflictingRequest.CommandID = "different-command"
	conflictingRequest, err = recoverycustody.FinalizeRequestDigest(conflictingRequest)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest(conflict) error = %v", err)
	}
	if _, err := authority.Submit(t.Context(), conflictingRequest); !errors.Is(err, recoverycustody.ErrCustodyConflict) {
		t.Fatalf("same-operation conflicting Submit() error = %v, want ErrCustodyConflict", err)
	}
	return request, pending
}

func assertLifecycleTerminalReopen(
	t *testing.T,
	config Config,
	request recoverycustody.SubmissionRequest,
	handle recoverycustody.SubmissionHandle,
	terminal recoverycustody.Outcome,
	finalizerErr error,
) {
	t.Helper()
	terminalFinalizer := &localNonAuthoritativeLifecycleFinalizer{
		config: config, finalizeErr: finalizerErr, queryErr: finalizerErr,
	}
	reopenedTerminal, err := OpenAuthority(t.Context(), config, &localNonAuthoritativeLifecycleLease{},
		terminalFinalizer, localNonAuthoritativeLifecycleVerifier{})
	if err != nil {
		t.Fatalf("reopen terminal authority: %v", err)
	}
	t.Cleanup(func() {
		if err := reopenedTerminal.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	tests := []struct {
		name string
		call func() (recoverycustody.Outcome, error)
	}{
		{name: "submit_exact_replay", call: func() (recoverycustody.Outcome, error) {
			return reopenedTerminal.Submit(t.Context(), request)
		}},
		{name: "query", call: func() (recoverycustody.Outcome, error) {
			return reopenedTerminal.Query(t.Context(), handle)
		}},
		{name: "resume_terminal", call: func() (recoverycustody.Outcome, error) {
			return reopenedTerminal.ResumeResolution(t.Context(), handle)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.call()
			if err != nil || !reflect.DeepEqual(got, terminal) {
				t.Fatalf("terminal outcome = (%+v, %v), want exact %+v", got, err, terminal)
			}
		})
	}
	if terminalFinalizer.finalizeCalls != 0 || terminalFinalizer.queryCalls != 0 {
		t.Fatalf("terminal reopen called finalizer: finalize:%d query:%d, want 0/0",
			terminalFinalizer.finalizeCalls, terminalFinalizer.queryCalls)
	}
}

func lifecycleResolutionRequest(
	t *testing.T,
	handle recoverycustody.SubmissionHandle,
	resolutionID string,
) ResolutionRequest {
	t.Helper()
	request := ResolutionRequest{
		SchemaVersion:           AuthoritySchemaVersion,
		ResolutionID:            resolutionID,
		Handle:                  handle,
		Kind:                    recoverycustody.ReceiptAvailability,
		FinalizationOperationID: resolutionID + "-finalization",
	}
	finalized, err := FinalizeResolutionRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	return finalized
}
