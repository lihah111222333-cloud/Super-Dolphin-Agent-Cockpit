package managedruntime

// 本文件的本地单包测试仅验证候选校验逻辑，不构成生产进程或摘要权威证据。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

type executableDigestVerifierFixture struct {
	err   error
	calls []pidregistry.StableProcessIdentity
}

func (f *executableDigestVerifierFixture) VerifyExecutableDigest(
	_ context.Context,
	identity pidregistry.StableProcessIdentity,
	_ string,
) error {
	f.calls = append(f.calls, identity)
	return f.err
}

func TestProcessReceiptVerifierVerifiesStableIdentityAndDigest(t *testing.T) {
	request := validPrepareRequest(t, "verifier")
	receipt := validPreparedReceipt(t, request, "verifier")
	identity := pidregistry.StableProcessIdentity{
		PID: int(receipt.Process.PID), ProcessStartToken: receipt.Process.ProcessStartToken,
		ExecutableIdentity: receipt.Process.ExecutableIdentity,
	}
	digest := &executableDigestVerifierFixture{}
	captures := 0
	verifier := &ProcessReceiptVerifier{
		Capture: func(pid int) (pidregistry.StableProcessIdentity, error) {
			captures++
			if pid != int(receipt.Process.PID) {
				t.Fatalf("capture PID = %d, want %d", pid, receipt.Process.PID)
			}
			return identity, nil
		},
		ExecutableDigestVerifier: digest,
	}
	if err := verifier.VerifyPrepared(context.Background(), request, receipt); err != nil {
		t.Fatalf("VerifyPrepared() error = %v", err)
	}
	if captures != 2 || len(digest.calls) != 2 {
		t.Fatalf("captures=%d digest calls=%d, want two each", captures, len(digest.calls))
	}
}

func TestProcessReceiptVerifierRejectsDriftNotFoundOverflowAndDigestMismatch(t *testing.T) {
	request := validPrepareRequest(t, "verifier-errors")
	base := validPreparedReceipt(t, request, "verifier-errors")
	identity := pidregistry.StableProcessIdentity{
		PID: int(base.Process.PID), ProcessStartToken: base.Process.ProcessStartToken,
		ExecutableIdentity: base.Process.ExecutableIdentity,
	}
	cases := []struct {
		name          string
		mutateReceipt func(*PreparedReceipt)
		capture       func(int) (pidregistry.StableProcessIdentity, error)
		digestErr     error
		want          error
	}{
		{name: "generation drift", capture: func(int) (pidregistry.StableProcessIdentity, error) {
			identity.ProcessStartToken = "different"
			return identity, nil
		}, want: ErrProcessReceiptIdentityMismatch},
		{name: "not found", capture: func(int) (pidregistry.StableProcessIdentity, error) {
			return pidregistry.StableProcessIdentity{}, pidregistry.ErrStableProcessNotFound
		}, want: pidregistry.ErrStableProcessNotFound},
		{name: "digest mismatch", capture: func(int) (pidregistry.StableProcessIdentity, error) {
			return identity, nil
		}, digestErr: errors.New("secret executable path: /tmp/private"), want: ErrProcessReceiptExecutableDigestMismatch},
		{name: "PID overflow", mutateReceipt: func(value *PreparedReceipt) {
			value.Process.PID = -1
		}, capture: func(int) (pidregistry.StableProcessIdentity, error) {
			return identity, nil
		}, want: ErrInvalidPreparedReceipt},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			currentIdentity := identity
			receipt := base
			if test.mutateReceipt != nil {
				test.mutateReceipt(&receipt)
				receipt = mustFinalizePrepared(t, receipt)
			}
			digest := &executableDigestVerifierFixture{err: test.digestErr}
			capture := test.capture
			if test.name == "generation drift" {
				capture = func(int) (pidregistry.StableProcessIdentity, error) {
					currentIdentity.ProcessStartToken = "different"
					return currentIdentity, nil
				}
			}
			verifier := &ProcessReceiptVerifier{Capture: capture, ExecutableDigestVerifier: digest}
			err := verifier.VerifyPrepared(context.Background(), request, receipt)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, test.want)
			}
			if strings.Contains(err.Error(), "/tmp/private") {
				t.Fatalf("error leaked executable path: %v", err)
			}
		})
	}
}

func TestProcessReceiptVerifierRejectsMissingAndTypedNilDigestVerifier(t *testing.T) {
	request := validPrepareRequest(t, "verifier-nil")
	receipt := validPreparedReceipt(t, request, "verifier-nil")
	if _, err := NewProcessReceiptVerifier(nil); !errors.Is(err, ErrProductionNoGo) {
		t.Fatalf("NewProcessReceiptVerifier(nil) error = %v, want ErrProductionNoGo", err)
	}
	var typedNil *executableDigestVerifierFixture
	if _, err := NewProcessReceiptVerifier(typedNil); !errors.Is(err, ErrProductionNoGo) {
		t.Fatalf("NewProcessReceiptVerifier(typed nil) error = %v, want ErrProductionNoGo", err)
	}
	verifier := &ProcessReceiptVerifier{Capture: func(int) (pidregistry.StableProcessIdentity, error) {
		return pidregistry.StableProcessIdentity{}, nil
	}}
	if err := verifier.VerifyPrepared(context.Background(), request, receipt); !errors.Is(err, ErrProductionNoGo) {
		t.Fatalf("missing digest verifier error = %v, want ErrProductionNoGo", err)
	}
}

func TestProcessReceiptVerifierHonorsCancellationAndDoesNotCapture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	captures := 0
	verifier := &ProcessReceiptVerifier{Capture: func(int) (pidregistry.StableProcessIdentity, error) {
		captures++
		return pidregistry.StableProcessIdentity{}, nil
	}, ExecutableDigestVerifier: &executableDigestVerifierFixture{}}
	request := validPrepareRequest(t, "verifier-cancel")
	receipt := validPreparedReceipt(t, request, "verifier-cancel")
	err := verifier.VerifyPrepared(ctx, request, receipt)
	if !errors.Is(err, context.Canceled) || captures != 0 {
		t.Fatalf("error=%v captures=%d, want context.Canceled and zero captures", err, captures)
	}
}
