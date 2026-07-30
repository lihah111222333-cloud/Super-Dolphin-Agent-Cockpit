package eventsurface

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestBindPublishesAuthoritativeAssistantCompletionBeforeTerminal(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 2)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	event.Publish(dispatcher, bindTestCanonicalCompletion(t, "权威最终回复"))
	assertAuthoritativeAssistantCompletion(t, mustReceivePublished(t, got))
	if terminal := mustReceivePublished(t, got); terminal.method != MethodTurnTerminal {
		t.Fatalf("second method = %q, want %q", terminal.method, MethodTurnTerminal)
	}
}

func assertAuthoritativeAssistantCompletion(t *testing.T, published publishedEvent) {
	t.Helper()
	if published.method != MethodItemCompleted {
		t.Fatalf("first method = %q, want %q", published.method, MethodItemCompleted)
	}
	payload := payloadMap(published.payload)
	if payload["threadId"] != "thread-1" || payload["turnId"] != "turn-1" {
		t.Fatalf("assistant completion identity = %#v", payload)
	}
	item := payloadMap(payload["item"])
	if item["id"] != "assistant-item-1" || item["type"] != "assistant" {
		t.Fatalf("assistant completion identity item = %#v", item)
	}
	if item["phase"] != "final_answer" || item["text"] != "权威最终回复" {
		t.Fatalf("assistant completion content item = %#v", item)
	}
}

func TestBindDoesNotPublishAssistantCompletionWithoutAuthoritativeResult(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 2)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	completed := bindTestCanonicalCompletion(t, "")
	event.Publish(dispatcher, completed)
	published := mustReceivePublished(t, got)
	if published.method != MethodTurnTerminal {
		t.Fatalf("method = %q, want only %q", published.method, MethodTurnTerminal)
	}
	select {
	case extra := <-got:
		t.Fatalf("unexpected extra event = %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBindDropsOutputDeltaAfterAuthoritativeTerminal(t *testing.T) {
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 3)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	completed := bindTestCanonicalCompletion(t, "权威最终回复")
	event.Publish(dispatcher, completed)
	assertAuthoritativeAssistantCompletion(t, mustReceivePublished(t, got))
	if terminal := mustReceivePublished(t, got); terminal.method != MethodTurnTerminal {
		t.Fatalf("second method = %q, want %q", terminal.method, MethodTurnTerminal)
	}

	event.Publish(dispatcher, turndto.TurnOutputDelta{
		TurnHeader: completed.TurnHeader,
		Stream:     "message",
		Delta:      "迟到片段",
	})
	select {
	case extra := <-got:
		t.Fatalf("unexpected late notification = %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func bindTestCanonicalCompletion(t *testing.T, result string) turndto.TurnCompleted {
	t.Helper()
	now := time.Unix(1710000000, 0).UTC()
	completed := turndto.TurnCompleted{
		TurnHeader:     bindTestTurnHeader(now),
		Success:        true,
		Status:         "completed",
		Result:         result,
		Summary:        "public success summary",
		PartialItemIDs: []string{"assistant-item-1"},
	}
	terminal, err := turndto.NewTurnTerminalV2(completed, "bind-test-terminal")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	completed, err = turndto.AttachCanonicalTurnTerminal(completed, terminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
	}
	return completed
}
