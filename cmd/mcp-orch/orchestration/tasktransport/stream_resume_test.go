package tasktransport

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestStreamResumeValidatorAcceptsListenAndSameStreamResume(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionRead)
	binding := streamBindingForTest(fixture)
	listen := StreamResumeRequest{
		Method: StreamMethodGet, Mode: StreamModeListen,
		SessionID: binding.SessionID, StreamID: binding.StreamID,
		Caller: binding.Caller, Scope: binding.Scope,
		DeadlineAt: fixture.now.Add(time.Second),
	}
	if err := listen.ValidateAt(binding, fixture.now); err != nil {
		t.Fatalf("listen validation error = %v", err)
	}
	cursor := validCursor(fixture.request)
	cursor.StreamID = binding.StreamID
	resume := listen
	resume.Mode = StreamModeResume
	resume.LastEventID = "stream/1:event/7"
	resume.Cursor = cursor
	if err := resume.ValidateAt(binding, fixture.now); err != nil {
		t.Fatalf("resume validation error = %v", err)
	}
}

func TestStreamResumeValidatorRejectsMethodCarrierConfusionBeforeOwner(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionRead)
	binding := streamBindingForTest(fixture)
	base := StreamResumeRequest{
		Method: StreamMethodPost, SessionID: binding.SessionID, StreamID: binding.StreamID,
		Caller: binding.Caller, Scope: binding.Scope, DeadlineAt: fixture.now.Add(time.Second),
	}
	for _, mutate := range []struct {
		name string
		fn   func(*StreamResumeRequest)
	}{
		{name: "post event id", fn: func(request *StreamResumeRequest) { request.LastEventID = "event/1" }},
		{name: "post resume mode", fn: func(request *StreamResumeRequest) { request.Mode = StreamModeResume }},
		{name: "get listen event id", fn: func(request *StreamResumeRequest) {
			request.Method, request.Mode, request.LastEventID = StreamMethodGet, StreamModeListen, "event/1"
		}},
		{name: "get resume missing event id", fn: func(request *StreamResumeRequest) {
			request.Method, request.Mode, request.Cursor = StreamMethodGet, StreamModeResume, validCursor(fixture.request)
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			request := base
			mutate.fn(&request)
			before := request
			if err := request.ValidateAt(binding, fixture.now); err == nil {
				t.Fatal("ValidateAt() unexpectedly accepted carrier mismatch")
			}
			if !reflect.DeepEqual(request, before) {
				t.Fatal("ValidateAt() mutated request on rejection")
			}
		})
	}
}

func TestStreamResumeValidatorRejectsCrossTaskRoomSessionAndCursor(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionRead)
	binding := streamBindingForTest(fixture)
	base := StreamResumeRequest{
		Method: StreamMethodGet, Mode: StreamModeResume,
		SessionID: binding.SessionID, StreamID: binding.StreamID, LastEventID: "event/7",
		Cursor: validCursor(fixture.request), Caller: binding.Caller, Scope: binding.Scope,
		DeadlineAt: fixture.now.Add(time.Second),
	}
	for _, mutate := range []struct {
		name string
		fn   func(*StreamResumeRequest)
	}{
		{name: "foreign session", fn: func(request *StreamResumeRequest) { request.SessionID = "session/foreign" }},
		{name: "foreign stream", fn: func(request *StreamResumeRequest) { request.StreamID = "stream/foreign" }},
		{name: "foreign task", fn: func(request *StreamResumeRequest) { request.Scope.TaskID = "task/foreign" }},
		{name: "foreign room", fn: func(request *StreamResumeRequest) { request.Scope.RoomID = "room/foreign" }},
		{name: "foreign cursor stream", fn: func(request *StreamResumeRequest) { request.Cursor.StreamID = "stream/foreign" }},
		{name: "foreign cursor source", fn: func(request *StreamResumeRequest) { request.Cursor.SourceFence.AgentID = "task/foreign" }},
		{name: "foreign caller", fn: func(request *StreamResumeRequest) { request.Caller.TaskID = "task/foreign" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			request := base
			if base.Cursor != nil {
				cursor := *base.Cursor
				request.Cursor = &cursor
			}
			mutate.fn(&request)
			if err := request.ValidateAt(binding, fixture.now); !errors.Is(err, ErrStreamResumeMismatch) && !errors.Is(err, ErrStreamScopeMismatch) && !errors.Is(err, ErrStreamUnauthorized) {
				t.Fatalf("ValidateAt() error = %v, want binding rejection", err)
			}
		})
	}

	binding.Scope.RoomID, binding.Scope.RoomScopeRef = "room/1", "run/1"
	base.Scope = binding.Scope
	if err := base.ValidateAt(binding, fixture.now); err != nil {
		t.Fatalf("room-bound resume validation error = %v", err)
	}
}

func TestStreamResumeValidatorRejectsExpiredDeadlineAndCancellationWithoutEffects(t *testing.T) {
	fixture := newAuthorizationFixture(t, ActionRead)
	binding := streamBindingForTest(fixture)
	request := StreamResumeRequest{
		Method: StreamMethodGet, Mode: StreamModeListen,
		SessionID: binding.SessionID, StreamID: binding.StreamID,
		Caller: binding.Caller, Scope: binding.Scope, DeadlineAt: fixture.now.Add(time.Second),
	}
	for _, deadline := range []time.Time{fixture.now, fixture.now.Add(61 * time.Second)} {
		request.DeadlineAt = deadline
		if err := request.ValidateAt(binding, fixture.now); !errors.Is(err, ErrStreamDeadline) {
			t.Fatalf("deadline %v error = %v, want ErrStreamDeadline", deadline, err)
		}
	}
	request.DeadlineAt = fixture.now.Add(time.Second)
	request.CancelSignal = true
	if err := request.ValidateAt(binding, fixture.now); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("cancel signal error = %v, want ErrStreamCancelled", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request.CancelSignal = false
	if err := request.ValidateContext(ctx, binding, fixture.now); !errors.Is(err, ErrStreamCancelled) {
		t.Fatalf("cancelled context error = %v, want ErrStreamCancelled", err)
	}
}

func streamBindingForTest(fixture authorizationFixture) StreamBinding {
	return StreamBinding{
		EndpointRef: "endpoint/orch", Profile: "strict-task-v1", BootEpoch: fixture.request.Caller.BootEpoch,
		SessionID: fixture.request.Caller.SessionID, StreamID: "stream/1", Caller: fixture.request.Caller,
		Scope: StreamScope{TaskID: fixture.request.Caller.TaskID, TaskScopeRef: fixture.request.Caller.ScopeRef},
	}
}
