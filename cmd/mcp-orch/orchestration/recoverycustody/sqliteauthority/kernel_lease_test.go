package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

// rejectingWithLease 证明 Bootstrap 不得在进入租约 mutation 临界区前打开或写数据库。
type rejectingWithLease struct{}

func (rejectingWithLease) Validate(context.Context, AuthorityScope) error { return ErrLeaseRejected }

func (lease rejectingWithLease) WithValidatedScope(ctx context.Context, scope AuthorityScope, callback func() error) error {
	if err := lease.Validate(ctx, scope); err != nil {
		return err
	}
	return callback()
}

func TestKernelAuthorityLeaseExactScope(t *testing.T) {
	config := authorityTestConfig(t, "kernel-exact-scope")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	physicalPath, err := validateIndependentPaths(config)
	if err != nil {
		t.Fatalf("validateIndependentPaths() error = %v", err)
	}
	statePathDigest, err := digestValue(StatePathDigestDomain, physicalPath)
	if err != nil {
		t.Fatalf("digestValue() error = %v", err)
	}
	want := AuthorityScope{
		StoreID:                    config.StoreID,
		StatePathDigest:            statePathDigest,
		AuthorityID:                config.AuthorityID,
		AuthorityClusterID:         config.AuthorityClusterID,
		AuthorityGeneration:        config.AuthorityGeneration,
		OwnerID:                    config.OwnerID,
		OwnerGeneration:            config.OwnerGeneration,
		OwnerFenceDigest:           config.OwnerFenceDigest,
		AuthorityProfile:           config.AuthorityProfile,
		AuthorityProfileGeneration: config.AuthorityProfileGeneration,
		AuthorityProfileDigest:     config.AuthorityProfileDigest,
		SecurityDomainID:           config.SecurityDomainID,
		RetentionPolicyDigest:      config.RetentionPolicyDigest,
	}
	if lease.scope != want {
		t.Fatalf("lease scope = %#v, want %#v", lease.scope, want)
	}
	if err := lease.Validate(t.Context(), want); err != nil {
		t.Fatalf("Validate(exact) error = %v", err)
	}
	assertEveryAuthorityScopeFieldIsFenced(t, lease, want)
}

// assertEveryAuthorityScopeFieldIsFenced 逐字段改变完整 scope 并要求租约拒绝。
func assertEveryAuthorityScopeFieldIsFenced(t *testing.T, lease *KernelAuthorityLease, scope AuthorityScope) {
	t.Helper()
	scopeType := reflect.TypeOf(scope)
	for index := 0; index < scopeType.NumField(); index++ {
		field := scopeType.Field(index)
		t.Run(field.Name, func(t *testing.T) {
			changed := scope
			value := reflect.ValueOf(&changed).Elem().Field(index)
			switch value.Kind() {
			case reflect.String:
				value.SetString(value.String() + "-changed")
			case reflect.Int64:
				value.SetInt(value.Int() + 1)
			default:
				t.Fatalf("unsupported AuthorityScope field kind %s", value.Kind())
			}
			if err := lease.Validate(t.Context(), changed); !errors.Is(err, ErrLeaseRejected) {
				t.Fatalf("Validate(changed %s) error = %v, want ErrLeaseRejected", field.Name, err)
			}
		})
	}
}

func TestKernelAuthorityLeaseRejectsSecondOwner(t *testing.T) {
	config := authorityTestConfig(t, "kernel-second-owner")
	first, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease(first) error = %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("Close(first) error = %v", err)
		}
	})
	second, err := AcquireKernelAuthorityLease(t.Context(), config)
	if second != nil || !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("AcquireKernelAuthorityLease(second) = (%v, %v), want nil ErrLeaseRejected", second, err)
	}
}

func TestKernelAuthorityLeaseRejectsProductDirectoryAliases(t *testing.T) {
	t.Run("same_directory", func(t *testing.T) {
		config := authorityTestConfig(t, "kernel-same-directory")
		config.ProductSQLitePath = filepath.Join(filepath.Dir(config.Path), "product.db")
		assertKernelLeaseInvalidConfig(t, config)
	})
	t.Run("symlink_alias", func(t *testing.T) {
		config := authorityTestConfig(t, "kernel-directory-alias")
		alias := filepath.Join(filepath.Dir(filepath.Dir(config.Path)), "authority-alias")
		if err := os.Symlink(filepath.Dir(config.Path), alias); err != nil {
			t.Fatalf("create directory alias: %v", err)
		}
		config.ProductSQLitePath = filepath.Join(alias, "product.db")
		assertKernelLeaseInvalidConfig(t, config)
	})
}

func assertKernelLeaseInvalidConfig(t *testing.T, config Config) {
	t.Helper()
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if lease != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("AcquireKernelAuthorityLease() = (%v, %v), want nil ErrInvalidConfig", lease, err)
	}
}

func TestKernelAuthorityLeaseCloseWaitsForTransaction(t *testing.T) {
	config := authorityTestConfig(t, "kernel-close-waits")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	transactionDone := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "sqliteauthority.test.kernel-lease-transaction", func(ctx context.Context) {
		transactionDone <- lease.WithValidatedScope(ctx, lease.scope, func() error {
			close(started)
			<-release
			return nil
		})
	})
	<-started
	closeDone := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "sqliteauthority.test.kernel-lease-close", func(context.Context) {
		closeDone <- lease.Close()
	})
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before transaction completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-transactionDone; err != nil {
		t.Fatalf("WithValidatedScope() error = %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := lease.Validate(t.Context(), lease.scope); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Validate(after close) error = %v, want ErrLeaseRejected", err)
	}
}

func TestBootstrapLeaseRejectionPerformsZeroDatabaseWrites(t *testing.T) {
	config := authorityTestConfig(t, "kernel-bootstrap-zero-write")
	if err := Bootstrap(t.Context(), config, rejectingWithLease{}); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Bootstrap() error = %v, want ErrLeaseRejected", err)
	}
	if _, err := os.Lstat(config.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("authority database exists after rejected Bootstrap: %v", err)
	}
}

// criticalSectionLease 记录真实 mutation transaction 是否完整位于租约 callback 内。
type criticalSectionLease struct {
	mu        sync.Mutex
	scope     AuthorityScope
	holding   bool
	withCalls int
}

func (l *criticalSectionLease) Validate(_ context.Context, scope AuthorityScope) error {
	if l.scope != scope {
		return ErrLeaseRejected
	}
	return nil
}

func (l *criticalSectionLease) WithValidatedScope(
	ctx context.Context,
	scope AuthorityScope,
	callback func() error,
) error {
	if err := l.Validate(ctx, scope); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holding = true
	l.withCalls++
	defer func() { l.holding = false }()
	return callback()
}

func TestEveryAuthorityMutationTransactionHoldsLease(t *testing.T) {
	config := authorityTestConfig(t, "kernel-every-mutation")
	authority, lease := openCriticalSectionAuthority(t, config)
	request := schemaTestRequest(t)
	pending, err := authority.Submit(t.Context(), request)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	resolution := ResolutionRequest{SchemaVersion: 1, ResolutionID: "resolution-kernel-hold",
		Handle: pending.Handle, Kind: "availability", FinalizationOperationID: "finalization-kernel-hold"}
	resolution, err = FinalizeResolutionRequestDigest(resolution)
	if err != nil {
		t.Fatalf("FinalizeResolutionRequestDigest() error = %v", err)
	}
	if _, err := authority.Resolve(t.Context(), resolution); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if lease.withCalls != 4 {
		t.Fatalf("lease mutation callbacks = %d, want 4 for submit, prepare, claim reconfirmation and evidence commit", lease.withCalls)
	}
}

// openCriticalSectionAuthority 构造会拒绝租约 callback 外 transaction 的完整 authority 夹具。
func openCriticalSectionAuthority(t *testing.T, config Config) (*Authority, *criticalSectionLease) {
	t.Helper()
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	physicalPath, err := validateIndependentPaths(config)
	if err != nil {
		t.Fatalf("validateIndependentPaths() error = %v", err)
	}
	scope, err := authorityScopeFromConfig(config, physicalPath)
	if err != nil {
		t.Fatalf("authorityScopeFromConfig() error = %v", err)
	}
	lease := &criticalSectionLease{scope: scope}
	finalizer := &evidenceFinalizerFixture{config: config}
	authority, err := OpenAuthority(t.Context(), config, lease, finalizer, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	rawRunner := authority.transactionRunner
	authority.transactionRunner = func(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
		if !lease.holding {
			return errors.New("mutation transaction ran outside lease callback")
		}
		return rawRunner(ctx, db, fence, callback)
	}
	return authority, lease
}
