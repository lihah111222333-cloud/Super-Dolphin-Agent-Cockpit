package unifiedv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type testRouteReader struct{}

func (*testRouteReader) ReadRoute(context.Context, taskprincipal.TaskPrincipal, taskeventsource.Source) (RouteSnapshot, error) {
	return RouteSnapshot{}, nil
}

type testSourceReader struct{}

func (*testSourceReader) ReadSource(context.Context, taskeventsource.Source) error { return nil }

func TestDecodeListInputStrictRouteOnly(t *testing.T) {
	for _, raw := range []string{
		`{"view":"route","limit":1,"limit":2}`,
		`{"view":"route","agent_id":"forged"}`,
		`{"view":"route","cwd":"/tmp"}`,
		`{"view":"route","metadata":{}}`,
		`{"view":"diagnostic"}`,
		`{"view":"route","cursor":null}`,
	} {
		if _, err := decodeListInput(json.RawMessage(raw)); err == nil {
			t.Errorf("decodeListInput(%s) error = nil", raw)
		}
	}
	in, err := decodeListInput(json.RawMessage(`{"view":"route"}`))
	if err != nil || in.Limit != maxRouteAgents {
		t.Fatalf("default route input = %#v, %v", in, err)
	}
}

func TestDecodeListInputRejectsOversizedCursor(t *testing.T) {
	_, err := decodeListInput(json.RawMessage(`{"view":"route","cursor":"` + strings.Repeat("x", maxRouteCursorLength+1) + `"}`))
	if !errors.Is(err, ErrListInvalidRequest) {
		t.Fatalf("decodeListInput oversized cursor = %v, want invalid request", err)
	}
}

func TestListDefinitionIsSeparateAndNarrow(t *testing.T) {
	def := ListDefinition()
	if def.Name != Name {
		t.Fatalf("definition name = %q", def.Name)
	}
	props := def.InputSchema["properties"].(map[string]any)
	for _, forbidden := range []string{"agent_id", "cwd", "metadata", "include_inactive"} {
		if _, ok := props[forbidden]; ok {
			t.Fatalf("definition exposes forbidden field %q", forbidden)
		}
	}
}

func TestMintNodeOnlyEmitsServerHandles(t *testing.T) {
	node := RouteNode{LogicalID: "logical", ActiveExecutionRef: "provider-thread-secret", ActiveTurnRef: "turn-secret", NextActions: []NextAction{{Action: "recover", SourceKind: "last_closed", Source: &RouteSource{Kind: "full_source", ID: "fence-secret"}}}}
	got, err := mintNode(node)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, secret := range []string{"provider-thread-secret", "turn-secret", "fence-secret", "full_source"} {
		if strings.Contains(text, secret) {
			t.Fatalf("wire node leaked %q: %s", secret, text)
		}
	}
	if got.ActiveExecution == nil || !strings.HasPrefix(got.ActiveExecution.Handle, "rh1_") || got.ActiveTurn == nil || len(got.NextActions) != 1 || got.NextActions[0].SourceHandle == "" {
		t.Fatalf("server handles missing: %#v", got)
	}
}

func TestAssembleStablePaginationAndBounds(t *testing.T) {
	h := &ListHandler{clock: compatibilityListClock, cursors: make(map[string]cursorState)}
	snapshot := RouteSnapshot{Schema: RouteSchema, Version: RouteVersion, Profile: "strict", ManifestHash: strings.Repeat("a", 64), TargetSetDigest: strings.Repeat("b", 64), Nodes: make([]RouteNode, 3), ManifestVerified: true, CapabilitiesReady: true}
	for i := range snapshot.Nodes {
		snapshot.Nodes[i] = RouteNode{LogicalID: string(rune('c' - i)), State: "idle", ControlStatus: "verified"}
	}
	first, err := h.assemble(snapshot, ListAgentsInput{View: "route", Limit: 2})
	assertFirstRoutePage(t, first, err)
	second, err := h.assemble(snapshot, ListAgentsInput{View: "route", Cursor: first.NextCursor, Limit: 2})
	assertSecondRoutePage(t, second, err)
	if _, err := h.assemble(snapshot, ListAgentsInput{View: "route", Cursor: first.NextCursor, Limit: 2}); err != nil && errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
}

func TestAssembleUsesOneExplicitClockObservation(t *testing.T) {
	now := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	clockCalls := 0
	h := &ListHandler{clock: func() time.Time {
		clockCalls++
		return now
	}, cursors: make(map[string]cursorState)}
	snapshot := RouteSnapshot{Schema: RouteSchema, Version: RouteVersion, Profile: "strict", ManifestHash: strings.Repeat("a", 64), TargetSetDigest: strings.Repeat("b", 64), Nodes: []RouteNode{{LogicalID: "a", ActiveExecutionRef: "opaque"}, {LogicalID: "b"}}, ManifestVerified: true, CapabilitiesReady: true}
	if _, err := h.assemble(snapshot, ListAgentsInput{View: "route", Limit: 1}); err != nil {
		t.Fatalf("assemble() error = %v", err)
	}
	if clockCalls != 1 {
		t.Fatalf("security clock calls = %d, want one observation", clockCalls)
	}
}

func TestListRejectsInvalidExplicitClockObservation(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{name: "zero", now: time.Time{}},
		{name: "non UTC", now: time.Date(2040, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 3600))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &ListHandler{clock: func() time.Time { return tc.now }, cursors: make(map[string]cursorState)}
			_, err := h.assemble(RouteSnapshot{}, ListAgentsInput{View: "route", Limit: 1})
			if !errors.Is(err, ErrListInvalidClock) {
				t.Fatalf("assemble() error = %v, want ErrListInvalidClock", err)
			}
		})
	}
}

func TestNewListAgentsWithClockRejectsNilClock(t *testing.T) {
	if _, err := NewListAgentsWithClock(&taskprincipal.Resolver{}, &testRouteReader{}, &testSourceReader{}, nil); !errors.Is(err, ErrListNilClock) {
		t.Fatalf("NewListAgentsWithClock(nil) error = %v, want ErrListNilClock", err)
	}
}

func TestListRejectsNilClockObservation(t *testing.T) {
	if _, err := (&ListHandler{}).assemble(RouteSnapshot{}, ListAgentsInput{View: "route", Limit: 1}); !errors.Is(err, ErrListNilClock) {
		t.Fatalf("assemble() on nil handler clock error = %v, want ErrListNilClock", err)
	}
}

func TestCursorAtUsesProvidedObservationForIssueAndExpiry(t *testing.T) {
	now := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	h := &ListHandler{cursors: make(map[string]cursorState)}
	out := ListAgentsOutput{}
	source := taskeventsource.Source{SourceID: "source", SourceEpoch: "epoch", BindingVersion: 1, BindingDigest: "digest", SourceFenceRef: "fence"}
	if err := h.setNextCursorAt(&out, strings.Repeat("a", 64), 2, "principal", source, now); err != nil {
		t.Fatalf("setNextCursorAt() error = %v", err)
	}
	if got, err := h.pageOffsetAt(out.NextCursor, strings.Repeat("a", 64), 3, "principal", source, now); err != nil || got != 2 {
		t.Fatalf("pageOffsetAt() = %d, %v; want offset 2", got, err)
	}
	if _, err := h.pageOffsetAt(out.NextCursor, strings.Repeat("a", 64), 3, "principal", source, now.Add(routeCursorTTL)); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("expired pageOffsetAt() error = %v, want reconciliation", err)
	}
}

func TestSetNextCursorPrunesExpiredEntriesBeforeApplyingBound(t *testing.T) {
	h := &ListHandler{clock: compatibilityListClock, cursors: make(map[string]cursorState, maxRouteCursors)}
	for i := 0; i < maxRouteCursors; i++ {
		h.cursors[string(rune(i+1))] = cursorState{ExpiresAt: time.Now().Add(-time.Second)}
	}
	out := ListAgentsOutput{}
	if err := h.setNextCursor(&out, strings.Repeat("a", 64), 1, "principal", taskeventsource.Source{}); err != nil {
		t.Fatalf("setNextCursor with expired entries = %v, want success", err)
	}
	if out.NextCursor == "" || !out.HasMore {
		t.Fatalf("setNextCursor output = %#v, want next cursor", out)
	}
	if len(h.cursors) != 1 {
		t.Fatalf("cursor store size = %d, want one live cursor", len(h.cursors))
	}
}

func TestSetNextCursorFailsClosedWhenCursorStoreMissing(t *testing.T) {
	h := &ListHandler{clock: compatibilityListClock}
	if err := h.setNextCursor(&ListAgentsOutput{}, strings.Repeat("a", 64), 1, "principal", taskeventsource.Source{}); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("setNextCursor with nil cursor store = %v, want reconciliation error", err)
	}
}

func TestPageOffsetPrunesExpiredCursor(t *testing.T) {
	const token = "expired"
	h := &ListHandler{clock: compatibilityListClock, cursors: map[string]cursorState{token: {ExpiresAt: time.Now().Add(-time.Second)}}}
	if _, err := h.pageOffset(token, strings.Repeat("a", 64), 1, "principal", taskeventsource.Source{}); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("pageOffset expired cursor = %v, want reconciliation error", err)
	}
	if _, ok := h.cursors[token]; ok {
		t.Fatal("expired cursor remains in store after lookup")
	}
}

func TestPageOffsetRejectsOversizedCursorBeforeLookup(t *testing.T) {
	h := &ListHandler{clock: compatibilityListClock, cursors: map[string]cursorState{}}
	cursor := strings.Repeat("x", maxRouteCursorLength+1)
	if _, err := h.pageOffset(cursor, "digest", 1, "principal", taskeventsource.Source{}); !errors.Is(err, ErrListInvalidRequest) {
		t.Fatalf("pageOffset oversized cursor = %v, want invalid request", err)
	}
}

func TestListDefinitionBoundsCursorLength(t *testing.T) {
	def := ListDefinition()
	props := def.InputSchema["properties"].(map[string]any)
	cursor := props["cursor"].(map[string]any)
	if cursor["maxLength"] != maxRouteCursorLength {
		t.Fatalf("cursor maxLength = %#v, want %d", cursor["maxLength"], maxRouteCursorLength)
	}
}

func TestPageOffsetRejectsNegativeOffset(t *testing.T) {
	const token = "negative"
	source := taskeventsource.Source{SourceID: "source", SourceEpoch: "epoch", BindingVersion: 1, BindingDigest: "digest", SourceFenceRef: "fence"}
	h := &ListHandler{clock: compatibilityListClock, cursors: map[string]cursorState{token: {
		Digest: "digest", Offset: -1, Principal: "principal", SourceID: source.SourceID,
		SourceEpoch: source.SourceEpoch, BindingVersion: source.BindingVersion,
		BindingDigest: source.BindingDigest, SourceFenceRef: source.SourceFenceRef,
		ExpiresAt: time.Now().Add(time.Minute),
	}}}
	if _, err := h.pageOffset(token, "digest", 1, "principal", source); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("pageOffset negative offset = %v, want reconciliation error", err)
	}
}

func TestValidateSnapshotRejectsUnboundedNodes(t *testing.T) {
	s := RouteSnapshot{Schema: RouteSchema, Version: RouteVersion, Profile: "strict", ManifestHash: strings.Repeat("a", 64), TargetSetDigest: strings.Repeat("b", 64), ManifestVerified: true, CapabilitiesReady: true, Nodes: make([]RouteNode, maxRouteSnapshotNodes+1)}
	if err := validateSnapshot(s); !errors.Is(err, ErrResultItemTooLarge) {
		t.Fatalf("validateSnapshot error = %v, want bounded snapshot error", err)
	}
}

func TestListValidateCallerRejectsOperatorProfileCaseInsensitively(t *testing.T) {
	for _, profile := range []string{"operator", "OPERATOR", "Operator"} {
		t.Run(profile, func(t *testing.T) {
			h := &ListHandler{clock: compatibilityListClock}
			principal := taskprincipal.TaskPrincipal{Principal: common.CallerPrincipal{Profile: profile}}
			if err := h.validateCaller(principal); !errors.Is(err, ErrReconciliation) {
				t.Fatalf("validateCaller(%q) error = %v, want reconciliation", profile, err)
			}
		})
	}
}

func assertFirstRoutePage(t *testing.T, page ListAgentsOutput, err error) {
	t.Helper()
	if err != nil || !page.HasMore || page.NextCursor == "" || len(page.Nodes) != 2 {
		t.Fatalf("first page = %#v, %v", page, err)
	}
}

func assertSecondRoutePage(t *testing.T, page ListAgentsOutput, err error) {
	t.Helper()
	if err != nil || page.HasMore || len(page.Nodes) != 1 || page.Nodes[0].LogicalID != "c" {
		t.Fatalf("second page = %#v, %v", page, err)
	}
}
