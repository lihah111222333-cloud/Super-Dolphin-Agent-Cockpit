package recoverycustody

import (
	"context"
	"errors"
	"testing"
)

type verifiedClientFixture struct {
	submit func(context.Context, SubmissionRequest) (Outcome, error)
	query  func(context.Context, SubmissionHandle) (Outcome, error)
}

func (f verifiedClientFixture) Submit(ctx context.Context, request SubmissionRequest) (Outcome, error) {
	return f.submit(ctx, request)
}

func (f verifiedClientFixture) Query(ctx context.Context, handle SubmissionHandle) (Outcome, error) {
	return f.query(ctx, handle)
}

type countingReceiptVerifier struct {
	calls int
	err   error
}

func (v *countingReceiptVerifier) VerifyReceipt(context.Context, SubmissionHandle, ExternalReceipt) error {
	v.calls++
	return v.err
}

func TestNewVerifiedClientRejectsMissingDependencies(t *testing.T) {
	client := verifiedClientFixture{
		submit: func(context.Context, SubmissionRequest) (Outcome, error) { return Outcome{}, nil },
		query:  func(context.Context, SubmissionHandle) (Outcome, error) { return Outcome{}, nil },
	}
	var typedNilClient *UnavailableClient
	var typedNilVerifier *countingReceiptVerifier
	for name, backend := range map[string]Client{"nil": nil, "typed_nil": typedNilClient} {
		t.Run("backend_"+name, func(t *testing.T) {
			if got, err := NewVerifiedClient(backend, &countingReceiptVerifier{}); got != nil || err == nil {
				t.Fatalf("NewVerifiedClient() = (%T, %v), want nil/error", got, err)
			}
		})
	}
	for name, verifier := range map[string]ReceiptVerifier{"nil": nil, "typed_nil": typedNilVerifier} {
		t.Run("verifier_"+name, func(t *testing.T) {
			if got, err := NewVerifiedClient(client, verifier); got != nil || err == nil {
				t.Fatalf("NewVerifiedClient() = (%T, %v), want nil/error", got, err)
			}
		})
	}
	if _, err := NewVerifiedClient(client, &countingReceiptVerifier{}); err != nil {
		t.Fatalf("NewVerifiedClient(valid) error = %v", err)
	}
}

func TestVerifiedClientOnlyExposesVerifierAcceptedTerminal(t *testing.T) {
	request := validRequest(t)
	handle := validHandle(t, request)
	receipt := validReceipt(t, handle, ReceiptAvailability, "verified-client")
	terminal := Outcome{Handle: handle, Status: StatusAvailable, Receipt: &receipt}
	verifier := &countingReceiptVerifier{}
	client, err := NewVerifiedClient(verifiedClientFixture{
		submit: func(context.Context, SubmissionRequest) (Outcome, error) { return terminal, nil },
		query:  func(context.Context, SubmissionHandle) (Outcome, error) { return terminal, nil },
	}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() (Outcome, error){
		"submit": func() (Outcome, error) { return client.Submit(t.Context(), request) },
		"query":  func() (Outcome, error) { return client.Query(t.Context(), handle) },
	} {
		t.Run(name, func(t *testing.T) {
			got, callErr := call()
			if callErr != nil || got.Status != StatusAvailable || got.Receipt == nil {
				t.Fatalf("call() = (%+v, %v), want verified terminal", got, callErr)
			}
		})
	}
	if verifier.calls != 2 {
		t.Fatalf("verifier calls = %d, want 2", verifier.calls)
	}
}

func TestVerifiedClientRejectsNilContext(t *testing.T) {
	request := validRequest(t)
	handle := validHandle(t, request)
	client, err := NewVerifiedClient(verifiedClientFixture{
		submit: func(context.Context, SubmissionRequest) (Outcome, error) {
			t.Fatal("nil context reached submit")
			return Outcome{}, nil
		},
		query: func(context.Context, SubmissionHandle) (Outcome, error) {
			t.Fatal("nil context reached query")
			return Outcome{}, nil
		},
	}, &countingReceiptVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	var nilContext context.Context
	if _, err := client.Submit(nilContext, request); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("Submit(nil) error = %v, want ErrContextRequired", err)
	}
	if _, err := client.Query(nilContext, handle); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("Query(nil) error = %v, want ErrContextRequired", err)
	}
}

func TestApplyVerifiedReceiptRejectsNilContext(t *testing.T) {
	handle := validHandle(t, validRequest(t))
	receipt := validReceipt(t, handle, ReceiptAvailability, "nil-context")
	var nilContext context.Context
	if _, err := ApplyVerifiedReceipt(nilContext, Outcome{Handle: handle, Status: StatusPending}, receipt, &countingReceiptVerifier{}); !errors.Is(err, ErrContextRequired) {
		t.Fatalf("ApplyVerifiedReceipt(nil) error = %v, want ErrContextRequired", err)
	}
	var typedNilVerifier *countingReceiptVerifier
	if _, err := ApplyVerifiedReceipt(context.Background(), Outcome{Handle: handle, Status: StatusPending}, receipt, typedNilVerifier); !errors.Is(err, ErrReceiptRejected) {
		t.Fatalf("ApplyVerifiedReceipt(typed nil verifier) error = %v, want ErrReceiptRejected", err)
	}
}

func TestVerifiedClientFoldsTerminalHandleOrStatusMismatch(t *testing.T) {
	request := validRequest(t)
	handle := validHandle(t, request)
	receipt := validReceipt(t, handle, ReceiptAvailability, "terminal-mismatch")
	cases := []struct {
		name   string
		mutate func(*Outcome)
	}{
		{name: "handle", mutate: func(outcome *Outcome) { outcome.Handle.OperationID = "other-operation" }},
		{name: "status", mutate: func(outcome *Outcome) { outcome.Status = StatusAborted }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			outcome := Outcome{Handle: handle, Status: StatusAvailable, Receipt: &receipt}
			test.mutate(&outcome)
			client, err := NewVerifiedClient(verifiedClientFixture{
				submit: func(context.Context, SubmissionRequest) (Outcome, error) { return outcome, nil },
				query:  func(context.Context, SubmissionHandle) (Outcome, error) { return outcome, nil },
			}, &countingReceiptVerifier{})
			if err != nil {
				t.Fatal(err)
			}
			got, callErr := client.Query(t.Context(), handle)
			if !errors.Is(callErr, ErrCustodyUnavailable) || got.Status != StatusIndeterminate || got.Receipt != nil || got.Handle != handle {
				t.Fatalf("Query() = (%+v, %v), want unavailable same-handle outcome", got, callErr)
			}
		})
	}
}

func TestVerifiedClientFoldsVerifierRejectedTerminalToUnavailable(t *testing.T) {
	request := validRequest(t)
	handle := validHandle(t, request)
	receipt := validReceipt(t, handle, ReceiptAbort, "rejected-client")
	terminal := Outcome{Handle: handle, Status: StatusAborted, Receipt: &receipt}
	verifier := &countingReceiptVerifier{err: errors.New("test verifier rejection")}
	client, err := NewVerifiedClient(verifiedClientFixture{
		submit: func(context.Context, SubmissionRequest) (Outcome, error) { return terminal, nil },
		query:  func(context.Context, SubmissionHandle) (Outcome, error) { return terminal, nil },
	}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	got, callErr := client.Query(t.Context(), handle)
	if !errors.Is(callErr, ErrCustodyUnavailable) || got.Status != StatusIndeterminate || got.Receipt != nil || got.Handle != handle {
		t.Fatalf("Query() = (%+v, %v), want unavailable same-handle outcome", got, callErr)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
}

func TestVerifiedClientNeverPassesTerminalOnBackendError(t *testing.T) {
	request := validRequest(t)
	handle := validHandle(t, request)
	receipt := validReceipt(t, handle, ReceiptAvailability, "error-client")
	terminal := Outcome{Handle: handle, Status: StatusAvailable, Receipt: &receipt}
	client, err := NewVerifiedClient(verifiedClientFixture{
		submit: func(context.Context, SubmissionRequest) (Outcome, error) {
			return terminal, errors.New("backend failed")
		},
		query: func(context.Context, SubmissionHandle) (Outcome, error) {
			return terminal, errors.New("backend failed")
		},
	}, &countingReceiptVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	got, callErr := client.Query(t.Context(), handle)
	if !errors.Is(callErr, ErrCustodyUnavailable) || got.Status != StatusIndeterminate || got.Receipt != nil {
		t.Fatalf("Query() = (%+v, %v), want unavailable outcome", got, callErr)
	}
}
