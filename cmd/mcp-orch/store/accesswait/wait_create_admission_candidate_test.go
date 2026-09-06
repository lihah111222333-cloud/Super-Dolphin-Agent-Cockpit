package accesswait

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

func TestCreateWaitSourceAdmissionRejectsBeforeInsert(t *testing.T) {
	tests := []struct {
		name   string
		source eventstore.SourceState
		after  int64
		want   error
	}{
		{name: "cursor ahead", source: eventstore.SourceState{NextSequence: 2, MaterializedSequence: 0, Status: "active"}, after: 1, want: ErrWaitCursorAhead},
		{name: "replay floor", source: eventstore.SourceState{NextSequence: 11, MaterializedSequence: 10, DedupFloor: 5, Status: "active"}, after: 1, want: ErrWaitReplayExpired},
		{name: "gap", source: eventstore.SourceState{NextSequence: 2, MaterializedSequence: 1, Status: "gap"}, want: ErrWaitGap},
		{name: "quarantined", source: eventstore.SourceState{NextSequence: 2, MaterializedSequence: 1, Status: "quarantined"}, want: ErrWaitQuarantined},
		{name: "closed", source: eventstore.SourceState{NextSequence: 2, MaterializedSequence: 1, Status: "closed"}, want: ErrWaitQuarantined},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAccessWaitFixture(t)
			grant := validGrantRequest("grant-admission-"+test.name, "grant/admission-"+test.name, strings.Repeat("a", 64))
			mustCreateGrant(t, fixture.store, grant)
			request := waitRequest(grant, "wait-admission-"+test.name, waitSelector(), "wait/admission-"+test.name, strings.Repeat("b", 64), strings.Repeat("c", 64))
			if test.after > 0 {
				request.AfterSequence = test.after
				request.AfterCursor = "task/source/inc-source/output/1"
			}
			fixture.events.sourceState = test.source
			if _, err := fixture.store.CreateWait(t.Context(), request); !errors.Is(err, test.want) {
				t.Fatalf("CreateWait() error = %v, want %v", err, test.want)
			}
			if got := accessWaitHandleCount(t, fixture); got != 0 {
				t.Fatalf("wait rows = %d, want zero after rejected admission", got)
			}
		})
	}
}

func TestCreateWaitUnknownSourceRejectsBeforeInsert(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-admission-missing", "grant/admission-missing", strings.Repeat("0", 64))
	mustCreateGrant(t, fixture.store, grant)
	request := waitRequest(grant, "wait-admission-missing", waitSelector(), "wait/admission-missing", strings.Repeat("1", 64), strings.Repeat("2", 64))
	fixture.events.err = eventstore.ErrEventNotFound
	if _, err := fixture.store.CreateWait(t.Context(), request); !errors.Is(err, ErrWaitNotFound) {
		t.Fatalf("CreateWait() error = %v, want %v", err, ErrWaitNotFound)
	}
	if got := accessWaitHandleCount(t, fixture); got != 0 {
		t.Fatalf("wait rows = %d, want zero after missing-source admission", got)
	}
}

func TestCreateWaitExactReplaySurvivesSourceQuarantine(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grant := validGrantRequest("grant-admission-replay", "grant/admission-replay", strings.Repeat("d", 64))
	mustCreateGrant(t, fixture.store, grant)
	request := waitRequest(grant, "wait-admission-replay", waitSelector(), "wait/admission-replay", strings.Repeat("e", 64), strings.Repeat("f", 64))
	fixture.events.sourceState = eventstore.SourceState{NextSequence: 1, MaterializedSequence: 0, Status: "active"}
	first := mustCreateWait(t, fixture.store, request)

	for _, state := range []eventstore.SourceState{
		{NextSequence: 2, MaterializedSequence: 1, Status: "quarantined"},
		{NextSequence: 11, MaterializedSequence: 10, DedupFloor: 5, Status: "active"},
		{NextSequence: 2, MaterializedSequence: 0, Status: "active"},
	} {
		fixture.events.sourceState = state
		replayed, err := fixture.store.CreateWait(t.Context(), request)
		if err != nil {
			t.Fatalf("CreateWait(replay) error = %v, want exact replay", err)
		}
		if !reflect.DeepEqual(replayed, first) {
			t.Fatalf("CreateWait(replay) = %#v, want %#v", replayed, first)
		}
		if got := accessWaitHandleCount(t, fixture); got != 1 {
			t.Fatalf("wait rows = %d, want one replayed handle", got)
		}
	}
}
