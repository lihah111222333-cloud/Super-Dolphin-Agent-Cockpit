package messageassociation

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestCanonicalRoundTrip(t *testing.T) {
	want := validAssociation()
	payload, err := MarshalCanonical(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCanonical(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !bytes.Equal(payload, marshalForTest(t, want)) {
		t.Fatalf("round trip mismatch: got=%+v want=%+v", got, want)
	}
}

func TestValidationRejectsMissingOrMismatchedBindings(t *testing.T) {
	base := validAssociation()
	cases := []struct {
		name   string
		mutate func(*Association)
		want   error
	}{
		{"schema", func(a *Association) { a.SchemaVersion = 2 }, ErrInvalidAssociation},
		{"object kind", func(a *Association) { a.Object.Kind = "other" }, ErrInvalidAssociation},
		{"scope", func(a *Association) { a.Scope.Ref = "" }, ErrInvalidAssociation},
		{"principal", func(a *Association) { a.SourcePrincipalRef = "" }, ErrInvalidAssociation},
		{"principal newline", func(a *Association) { a.SourcePrincipalRef = "principal\nforged" }, ErrInvalidAssociation},
		{"scope tab", func(a *Association) { a.Scope.Ref = "scope\tforged" }, ErrInvalidAssociation},
		{"cardinality", func(a *Association) { a.Cardinality = "one_to_many" }, ErrInvalidAssociation},
		{"parent mismatch", func(a *Association) { a.Reply.ParentCommandID = "different" }, ErrAssociationConflict},
		{"parent version", func(a *Association) { a.Parent.CommandVersion = 2 }, ErrInvalidAssociation},
		{"same command", func(a *Association) { a.Reply.CommandID = a.Message.CommandID }, ErrInvalidAssociation},
		{"parent child command", func(a *Association) { a.Reply.CommandID = a.Parent.CommandID }, ErrInvalidAssociation},
		{"shared idempotency namespace", func(a *Association) { a.Reply.IdempotencyNamespace = a.Message.IdempotencyNamespace }, ErrInvalidAssociation},
		{"shared idempotency key", func(a *Association) { a.Reply.IdempotencyKeyDigest = a.Message.IdempotencyKeyDigest }, ErrInvalidAssociation},
		{"fence generation", func(a *Association) { a.Source.Execution.IncarnationGeneration = 0 }, ErrInvalidAssociation},
		{"digest", func(a *Association) { a.Message.IdempotencyKeyDigest = strings.Repeat("A", 64) }, ErrInvalidAssociation},
		{"content ref", func(a *Association) { a.ReplyContent.Ref = "" }, ErrInvalidAssociation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestStrictDecoderRejectsDuplicateUnknownNullAndBareFields(t *testing.T) {
	valid := marshalForTest(t, validAssociation())
	deep := []byte(`{"a":`)
	for index := 0; index < maxIntentJSONDepth+2; index++ {
		deep = append(deep, []byte(`{"a":`)...)
	}
	deep = append(deep, []byte(`0`)...)
	for index := 0; index < maxIntentJSONDepth+2; index++ {
		deep = append(deep, '}')
	}
	var members strings.Builder
	members.WriteByte('{')
	for index := 0; index < maxIntentObjectMembers+1; index++ {
		if index > 0 {
			members.WriteByte(',')
		}
		members.WriteString(`"x`)
		members.WriteString(strconv.Itoa(index))
		members.WriteString(`":0`)
	}
	members.WriteByte('}')
	cases := [][]byte{
		bytes.Repeat([]byte{'{'}, maxIntentJSONBytes+1),
		deep,
		[]byte(members.String()),
		append(bytes.TrimSuffix(valid, []byte("}")), []byte(`,"extra":true}`)...),
		append([]byte(`{"schema_version":1,`), valid[1:]...),
		bytes.Replace(valid, []byte(`"object":{`), []byte(`"object":null,"discarded":{`), 1),
		append(bytes.TrimSuffix(valid, []byte("}")), []byte(`,"correlation_id":"legacy"}`)...),
		append(bytes.TrimSuffix(valid, []byte("}")), []byte(`,"reply_to":"legacy"}`)...),
		append(valid, []byte(` {}`)...),
		append([]byte(" "), valid...),
	}
	for _, payload := range cases {
		if _, err := DecodeCanonical(payload); err == nil {
			t.Fatalf("payload accepted: %s", payload)
		} else if !errors.Is(err, ErrInvalidAssociation) && !errors.Is(err, ErrBareAssociation) {
			t.Fatalf("payload error=%v", err)
		}
	}
}

func TestComputeClientIntentDigest(t *testing.T) {
	assertCyclicIntentsRejected(t)
	largeMap := make(map[string]int, maxIntentObjectMembers+1)
	for index := 0; index <= maxIntentObjectMembers; index++ {
		largeMap[strconv.Itoa(index)] = index
	}
	assertInvalidIntents(t, []any{largeMap, make([]int, maxIntentObjectMembers+1)})
	assertInvalidIntents(t, []any{nil, "text", []int{1}, map[string]any{"x": nil}, true, 1, struct {
		Value string `json:"value"`
	}{Value: string([]byte{0xff})}, struct {
		Value float64 `json:"value"`
	}{Value: math.NaN()}})
	if _, err := ComputeClientIntentDigest(customIntentMarshaler{}); !errors.Is(err, ErrInvalidAssociation) {
		t.Fatalf("custom JSON marshaler error=%v", err)
	}
	if _, err := ComputeClientIntentDigest(pointerIntentMarshaler{}); !errors.Is(err, ErrInvalidAssociation) {
		t.Fatalf("pointer custom JSON marshaler error=%v", err)
	}
	one, err := ComputeClientIntentDigest(struct {
		Action string `json:"action"`
		Value  int    `json:"value"`
	}{Action: "send", Value: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ComputeClientIntentDigest(struct {
		Value string `json:"value"`
	}{Value: "合法替换符�"}); err != nil {
		t.Fatalf("explicit U+FFFD should remain valid: %v", err)
	}
	two, err := ComputeClientIntentDigest(struct {
		Action string `json:"action"`
		Value  int    `json:"value"`
	}{Action: "send", Value: 1})
	if err != nil || one != two || len(one) != 64 || strings.ToLower(one) != one {
		t.Fatalf("digest=%q second=%q err=%v", one, two, err)
	}
}

func assertCyclicIntentsRejected(t *testing.T) {
	t.Helper()
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := ComputeClientIntentDigest(cycle); !errors.Is(err, ErrInvalidAssociation) {
		t.Fatalf("cyclic map error=%v", err)
	}
	linked := &cyclicIntentNode{}
	linked.Next = linked
	if _, err := ComputeClientIntentDigest(linked); !errors.Is(err, ErrInvalidAssociation) {
		t.Fatalf("cyclic pointer error=%v", err)
	}
}

type cyclicIntentNode struct {
	Next *cyclicIntentNode `json:"next"`
}

func assertInvalidIntents(t *testing.T, intents []any) {
	t.Helper()
	for _, intent := range intents {
		if _, err := ComputeClientIntentDigest(intent); !errors.Is(err, ErrInvalidAssociation) {
			t.Fatalf("invalid intent %#v error=%v", intent, err)
		}
	}
}

type customIntentMarshaler struct{}

func (customIntentMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{"value":"forged"}`), nil }

type pointerIntentMarshaler struct{}

func (*pointerIntentMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`{"value":"forged"}`), nil
}

func validAssociation() Association {
	zero := strings.Repeat("0", 64)
	execution := common.ExecutionFence{AgentID: "agent-1", ReservationID: "reservation-1", ReservationGeneration: 1, IncarnationID: "incarnation-1", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: zero, SourceFenceRef: "fence-1"}
	turn := TurnFence{TurnID: "turn-1", TurnGeneration: 1, TurnDigest: zero}
	return Association{
		SchemaVersion:      SchemaVersion,
		Object:             TypedObject{Kind: ObjectKind, Version: SchemaVersion, Digest: zero},
		Scope:              Scope{Kind: ScopeKind, Ref: "scope-1", Version: SchemaVersion, Digest: zero},
		Cardinality:        CardinalityOneToOne,
		Parent:             ParentCommand{CommandID: "parent-1", CommandVersion: CommandVersion, IntentDigest: zero, FenceDigest: zero},
		Message:            MessageCommand{CommandID: "message-1", CommandVersion: CommandVersion, ParentCommandID: "parent-1", ParentCommandVersion: CommandVersion, IntentDigest: zero, IdempotencyNamespace: "message", IdempotencyKeyDigest: zero, FenceDigest: zero},
		Reply:              ReplyCommand{CommandID: "reply-1", CommandVersion: CommandVersion, ParentCommandID: "parent-1", ParentCommandVersion: CommandVersion, IntentDigest: zero, IdempotencyNamespace: "reply", IdempotencyKeyDigest: strings.Repeat("1", 64), FenceDigest: zero},
		Source:             EndpointFence{Execution: execution, Turn: turn},
		Target:             EndpointFence{Execution: execution, Turn: turn},
		SourcePrincipalRef: "principal-1",
		ClientIntentDigest: zero,
		MessageContent:     ContentRef{Ref: "content/message-1", Version: 1, Digest: zero},
		ReplyContent:       ContentRef{Ref: "content/reply-1", Version: 1, Digest: zero},
	}
}

func marshalForTest(t *testing.T, association Association) []byte {
	t.Helper()
	payload, err := MarshalCanonical(association)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
