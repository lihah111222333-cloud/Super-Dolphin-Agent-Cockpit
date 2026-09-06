//go:build darwin || linux

package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKernelAuthorityLeaseRejectsPermissionDrift(t *testing.T) {
	config := authorityTestConfig(t, "kernel-chmod")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	directory := filepath.Dir(config.Path)
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Errorf("restore authority directory mode: %v", err)
		}
		if err := lease.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("chmod authority directory: %v", err)
	}
	if err := lease.Validate(t.Context(), lease.scope); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Validate(after chmod) error = %v, want ErrLeaseRejected", err)
	}
}

func TestKernelAuthorityLeaseRejectsDirectoryReplacement(t *testing.T) {
	config := authorityTestConfig(t, "kernel-rename")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	directory := filepath.Dir(config.Path)
	moved := directory + "-moved"
	if err := os.Rename(directory, moved); err != nil {
		t.Fatalf("rename authority directory: %v", err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create replacement authority directory: %v", err)
	}
	if err := lease.Validate(t.Context(), lease.scope); !errors.Is(err, ErrLeaseRejected) {
		t.Fatalf("Validate(after directory replacement) error = %v, want ErrLeaseRejected", err)
	}
}

func TestKernelAuthorityLeasePostCallbackDriftIsOutcomeUnknown(t *testing.T) {
	config := authorityTestConfig(t, "kernel-post-callback")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	directory := filepath.Dir(config.Path)
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Errorf("restore authority directory mode: %v", err)
		}
		if err := lease.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	err = lease.WithValidatedScope(t.Context(), lease.scope, func() error {
		return os.Chmod(directory, 0o755)
	})
	if !errors.Is(err, ErrLeaseRejected) || !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("WithValidatedScope(post-callback drift) error = %v, want lease rejected and outcome unknown", err)
	}
}

func TestPostTransactionLeaseDriftMakesAuthorityQueryOnly(t *testing.T) {
	config := authorityTestConfig(t, "kernel-query-only")
	lease, err := AcquireKernelAuthorityLease(t.Context(), config)
	if err != nil {
		t.Fatalf("AcquireKernelAuthorityLease() error = %v", err)
	}
	directory := filepath.Dir(config.Path)
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Errorf("restore authority directory mode: %v", err)
		}
		if err := lease.Close(); err != nil {
			t.Errorf("lease Close() error = %v", err)
		}
	})
	authority := openPostDriftAuthority(t, config, lease, directory)
	_, err = authority.Submit(t.Context(), schemaTestRequest(t))
	if !errors.Is(err, ErrLeaseRejected) || !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("Submit(post-transaction drift) error = %v, want lease rejected and outcome unknown", err)
	}
	if !authority.queryOnly.Load() {
		t.Fatal("authority did not become query-only after post-transaction lease drift")
	}
}

// openPostDriftAuthority 在一次真实 transaction commit 后注入父目录权限漂移。
func openPostDriftAuthority(
	t *testing.T,
	config Config,
	lease *KernelAuthorityLease,
	directory string,
) *Authority {
	t.Helper()
	if err := Bootstrap(t.Context(), config, lease); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	authority, err := OpenAuthority(t.Context(), config, lease, inertFinalizer{}, acceptingReceiptVerifier{})
	if err != nil {
		t.Fatalf("OpenAuthority() error = %v", err)
	}
	t.Cleanup(func() {
		if err := authority.Close(); err != nil {
			t.Errorf("authority Close() error = %v", err)
		}
	})
	rawRunner := authority.transactionRunner
	authority.transactionRunner = func(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
		if err := rawRunner(ctx, db, fence, callback); err != nil {
			return err
		}
		return os.Chmod(directory, 0o755)
	}
	return authority
}
