package unifiedv2

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type countingSendCoordinator struct {
	calls int
}

func (c *countingSendCoordinator) Dispatch(context.Context, common.CallerPrincipal, common.CallerAttribution, SendInput) (SendResult, error) {
	c.calls++
	return SendResult{Status: "accepted", CommandID: "command", RequestDigest: strings.Repeat("a", 64), ReceiptRef: "receipt"}, nil
}

func TestDecodeSendInputStrictContract(t *testing.T) {
	valid := `{"target":{"kind":"grant","grant_ref":"grant://opaque"},"message":{"kind":"content.single","text":"hello"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1"}`
	if _, err := decodeSendInput(json.RawMessage(valid)); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	for _, raw := range []string{
		`{"target":{"kind":"grant","grant_ref":"grant://opaque"},"message":{"kind":"content.single","text":"hello"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1","agent_id":"legacy"}`,
		`{"target":{"kind":"grant","grant_ref":"grant://opaque"},"message":{"kind":"content.single","text":"hello"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1","idempotency_key":"other"}`,
		`{"target":{"kind":"grant","grant_ref":"grant://opaque"},"message":{"kind":"content.single","text":null},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1"}`,
		` {"target":{"kind":"grant","grant_ref":"grant://opaque"},"message":{"kind":"content.single","text":"hello"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1"}`,
	} {
		if _, err := decodeSendInput(json.RawMessage(raw)); err == nil {
			t.Errorf("decodeSendInput(%s) unexpectedly succeeded", raw)
		}
	}
}

func TestSendInputRejectsLegacyAndInvalidVariants(t *testing.T) {
	base := SendInput{Target: SendTarget{Kind: "grant", GrantRef: "grant://opaque"}, Message: SendMessage{Kind: "content.single", Text: "x"}, Delivery: SendDelivery{Kind: "new_turn", BusyPolicy: BusyPolicy{Action: "fail"}}, IdempotencyKey: "k"}
	for name, mutate := range map[string]func(*SendInput){
		"agent id is not a target":     func(in *SendInput) { in.Target.Kind = "agent" },
		"candidate content ref absent": func(in *SendInput) { in.Message.Kind = "content.ref" },
		"busy queue forbidden":         func(in *SendInput) { in.Delivery.BusyPolicy.Action = "queue" },
		"empty key":                    func(in *SendInput) { in.IdempotencyKey = "" },
		"invalid utf8":                 func(in *SendInput) { in.Message.Text = string([]byte{0xff}) },
	} {
		t.Run(name, func(t *testing.T) {
			in := base
			mutate(&in)
			if err := validateSendInput(in); err == nil {
				t.Fatal("validateSendInput() error = nil")
			}
		})
	}
	if err := validateSendInput(base); err != nil {
		t.Fatalf("base input invalid: %v", err)
	}
}

func TestSendDefinitionExactTopLevelFields(t *testing.T) {
	definition := SendDefinition()
	if definition.Name != "send_message" {
		t.Fatalf("definition name = %q", definition.Name)
	}
	fields := reflect.TypeOf(SendInput{})
	got := make([]string, fields.NumField())
	for i := range got {
		got[i] = strings.Split(fields.Field(i).Tag.Get("json"), ",")[0]
	}
	if strings.Contains(strings.Join(got, ","), "agent_id") || strings.Contains(strings.Join(got, ","), "thread_id") {
		t.Fatalf("legacy field in DTO: %v", got)
	}
}

func TestNewSendRequiresAllOwners(t *testing.T) {
	if _, err := NewSend(nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrSendDisabled) {
		t.Fatalf("NewSend() error = %v, want disabled", err)
	}
}

func TestNewSendWithCoordinatorRequiresAtomicOwner(t *testing.T) {
	if _, err := NewSendWithCoordinator(nil); !errors.Is(err, ErrSendDisabled) {
		t.Fatalf("nil coordinator error = %v", err)
	}
}

func TestHandleWithoutCoordinatorIsDisabledBeforeAnyOwner(t *testing.T) {
	var h SendHandler
	if _, err := h.Handle(context.Background(), json.RawMessage(`{}`)); !errors.Is(err, ErrSendDisabled) {
		t.Fatalf("Handle() error = %v, want disabled", err)
	}
}

func TestHandleRejectsOperatorPrincipalBeforeDispatchWithValidAttribution(t *testing.T) {
	for _, profile := range []string{"operator", "OPERATOR", "Operator"} {
		t.Run(profile, func(t *testing.T) {
			principal, err := common.NewCallerPrincipal("principal/operator", "scope/operator", profile, "auth/operator", time.Now().UTC().Add(time.Hour))
			if err != nil {
				t.Fatalf("NewCallerPrincipal() error = %v", err)
			}
			attribution, err := common.NewCallerAttribution("task/operator", common.ExecutionFence{
				AgentID: "task/operator", ReservationID: "reservation/operator", ReservationGeneration: 1,
				IncarnationID: "incarnation/operator", IncarnationGeneration: 1, BindingVersion: 1,
				BindingDigest: strings.Repeat("a", 64), SourceFenceRef: "fence/operator",
			}, common.IssuerBinding{
				Kind: common.CallerAttributionIssuerProviderBridge, BindingRef: "issuer/operator", Generation: 1,
				Digest: strings.Repeat("b", 64),
			})
			if err != nil {
				t.Fatalf("NewCallerAttribution() error = %v", err)
			}
			ctx, err := common.BindCallerPrincipal(context.Background(), principal)
			if err != nil {
				t.Fatalf("BindCallerPrincipal() error = %v", err)
			}
			ctx, err = common.BindCallerAttribution(ctx, attribution)
			if err != nil {
				t.Fatalf("BindCallerAttribution() error = %v", err)
			}
			coordinator := &countingSendCoordinator{}
			h, err := NewSendWithCoordinator(coordinator)
			if err != nil {
				t.Fatalf("NewSendWithCoordinator() error = %v", err)
			}
			_, err = h.Handle(ctx, json.RawMessage(`{"target":{"kind":"grant","grant_ref":"grant://opaque"},"message":{"kind":"content.single","text":"hello"},"delivery":{"kind":"new_turn","busy_policy":{"action":"fail"}},"idempotency_key":"key-1"}`))
			if !errors.Is(err, ErrSendUnavailable) {
				t.Fatalf("Handle() error = %v, want ErrSendUnavailable", err)
			}
			if coordinator.calls != 0 {
				t.Fatalf("coordinator Dispatch calls = %d, want zero", coordinator.calls)
			}
		})
	}
}
