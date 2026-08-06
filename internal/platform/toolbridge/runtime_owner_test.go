package toolbridge

import "testing"

func TestHandlerRuntimeOwnersAreIsolated(t *testing.T) {
	t.Parallel()

	first := &Handler{}
	second := &Handler{}
	first.persistentSubagentDefaultFallbackTotal.Add(1)
	if got := first.persistentSubagentDefaultFallbackCount(); got != 1 {
		t.Fatalf("first fallback count = %d, want 1", got)
	}
	if got := second.persistentSubagentDefaultFallbackCount(); got != 0 {
		t.Fatalf("second fallback count = %d, want 0", got)
	}
	if got := first.nextToolTraceSpan("trace"); got != "toolbridge:trace:1" {
		t.Fatalf("first trace span = %q", got)
	}
	if got := second.nextToolTraceSpan("trace"); got != "toolbridge:trace:1" {
		t.Fatalf("second trace span = %q, want independent sequence", got)
	}
	if got := first.nextToolTraceSpan("tool.call"); got != "toolbridge:tool.call:2" {
		t.Fatalf("first second trace span = %q", got)
	}
}

func TestHandlerTraceOwnerRejectsNil(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("nil Handler.nextToolTraceSpan() did not panic")
		}
	}()
	(*Handler)(nil).nextToolTraceSpan("trace")
}
