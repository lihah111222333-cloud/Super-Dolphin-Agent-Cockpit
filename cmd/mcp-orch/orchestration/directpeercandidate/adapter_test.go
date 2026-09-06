package directpeercandidate

import (
	"context"
	"errors"
	"testing"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	command "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

type identityResolverStub struct {
	identity CanonicalIdentity
	err      error
}

func (s identityResolverStub) Resolve(context.Context, directpeer.Message) (CanonicalIdentity, error) {
	return s.identity, s.err
}

type submissionQueryStub struct {
	record command.Submission
	err    error
	keys   []command.SubmissionKey
}

func (s *submissionQueryStub) Query(_ context.Context, key command.SubmissionKey) (command.Submission, error) {
	s.keys = append(s.keys, key)
	return s.record, s.err
}

func candidateSubmission(commandID, submissionID string, status command.SubmissionStatus) command.Submission {
	return command.Submission{Request: command.SubmissionRequest{CommandID: commandID, SubmissionID: submissionID}, Status: status}
}

func TestAcceptedReplayQueriesStableCanonicalIdentity(t *testing.T) {
	query := &submissionQueryStub{record: candidateSubmission("cmd-1", "sub-1", command.SubmissionOutcomeUnknown)}
	adapter, err := New(identityResolverStub{identity: CanonicalIdentity{CommandID: "cmd-1", SubmissionID: "sub-1"}}, query)
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.ResolveAcceptedReplay(context.Background(), directpeer.Message{MessageID: "message-1", Status: "accepted"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Identity.CommandID != "cmd-1" || got.Identity.SubmissionID != "sub-1" || len(query.keys) != 1 || query.keys[0].CommandID != "cmd-1" {
		t.Fatalf("replay = %#v query keys = %#v", got, query.keys)
	}
}

func TestAcceptedReplayMissingCanonicalIdentityFailsClosed(t *testing.T) {
	query := &submissionQueryStub{}
	adapter, err := New(identityResolverStub{err: errors.New("mapping absent")}, query)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ResolveAcceptedReplay(context.Background(), directpeer.Message{MessageID: "message-1", Status: "accepted"})
	if !errors.Is(err, ErrCanonicalIdentity) || len(query.keys) != 0 {
		t.Fatalf("missing identity err=%v query keys=%#v", err, query.keys)
	}
}

func TestErrorOnlyProviderNeverWritesDefinitiveFailure(t *testing.T) {
	query := &submissionQueryStub{record: candidateSubmission("cmd-1", "sub-1", command.SubmissionPending)}
	adapter, err := New(identityResolverStub{identity: CanonicalIdentity{CommandID: "cmd-1", SubmissionID: "sub-1"}}, query)
	if err != nil {
		t.Fatal(err)
	}
	record, err := adapter.ObserveErrorOnlyProviderError(context.Background(), command.SubmissionKey{CommandID: "cmd-1", SubmissionID: "sub-1"}, errors.New("transport uncertain"))
	if !errors.Is(err, ErrProviderOutcome) || !errors.Is(err, command.ErrSubmissionUnresolved) {
		t.Fatalf("error-only result err=%v", err)
	}
	if record.Status != command.SubmissionPending {
		t.Fatalf("error-only provider changed pending status: %#v", record)
	}
}

func TestMessageIDCannotBeProviderReceipt(t *testing.T) {
	message := directpeer.Message{MessageID: "message-1"}
	if err := ValidateProviderReceipt(message, message.MessageID); !errors.Is(err, ErrMessageIDAsReceipt) {
		t.Fatalf("message id receipt err=%v", err)
	}
	if err := ValidateProviderReceipt(message, "provider-receipt-1"); err != nil {
		t.Fatalf("distinct provider receipt rejected: %v", err)
	}
}

func TestAcceptedReplayRejectsMessageIDAsCanonicalSubmissionID(t *testing.T) {
	query := &submissionQueryStub{record: candidateSubmission("cmd-1", "message-1", command.SubmissionPending)}
	adapter, err := New(identityResolverStub{identity: CanonicalIdentity{CommandID: "cmd-1", SubmissionID: "message-1"}}, query)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ResolveAcceptedReplay(context.Background(), directpeer.Message{MessageID: "message-1", Status: "accepted"})
	if !errors.Is(err, ErrMessageIDAsReceipt) || len(query.keys) != 0 {
		t.Fatalf("message identity reuse err=%v query keys=%#v", err, query.keys)
	}
}
