package protocol

import (
	"errors"
	"testing"
)

type structuredNotificationHandler struct {
	log    LogMessageParams
	called bool
}

func (h *structuredNotificationHandler) PublishDiagnostics(PublishDiagnosticsParams) error {
	return nil
}
func (h *structuredNotificationHandler) LogMessage(params LogMessageParams) error {
	h.log = params
	h.called = true
	return nil
}

func TestDispatchNotificationPreservesStructuredLogMessage(t *testing.T) {
	handler := &structuredNotificationHandler{}
	payload := []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{"type":3,"message":{"code":"MD001","path":"docs/api.md"}}}`)
	if err := DispatchNotification(payload, handler); err != nil {
		t.Fatalf("DispatchNotification() error = %v", err)
	}
	if !handler.called {
		t.Fatal("LogMessage handler was not called")
	}
	if got, want := handler.log.Message, `{"code":"MD001","path":"docs/api.md"}`; got != want {
		t.Fatalf("structured message = %q, want compact JSON %q", got, want)
	}
}

func TestDispatchNotificationPreservesStringLogMessage(t *testing.T) {
	handler := &structuredNotificationHandler{}
	payload := []byte(`{"jsonrpc":"2.0","method":"window/logMessage","params":{"type":3,"message":"plain message"}}`)
	if err := DispatchNotification(payload, handler); err != nil {
		t.Fatalf("DispatchNotification() error = %v", err)
	}
	if got := handler.log.Message; got != "plain message" {
		t.Fatalf("string message = %q, want %q", got, "plain message")
	}
}

func TestDispatchNotificationIgnoresStructuredShowMessage(t *testing.T) {
	handler := &structuredNotificationHandler{}
	payload := []byte(`{"jsonrpc":"2.0","method":"window/showMessage","params":{"type":3,"message":["Markdown",{"code":"MD001"}]}}`)
	if err := DispatchNotification(payload, handler); err != nil && !errors.Is(err, ErrUnsupportedNotification) {
		t.Fatalf("DispatchNotification() error = %v, want nil or ErrUnsupportedNotification", err)
	}
}
