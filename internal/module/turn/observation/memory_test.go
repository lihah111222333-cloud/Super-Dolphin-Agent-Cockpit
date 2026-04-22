package observation

import (
	"sync"
	"testing"
)

func TestMapTurnIsImmutableOnceBound(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	if !m.MapTurn("local-1", "prov-1") {
		t.Fatal("first MapTurn should succeed")
	}
	if !m.MapTurn("local-1", "prov-1") {
		t.Fatal("idempotent re-registration should succeed")
	}
	if m.MapTurn("local-1", "prov-2") {
		t.Fatal("rebinding local-1 to a different provider must be rejected")
	}
	if m.MapTurn("local-2", "prov-1") {
		t.Fatal("rebinding prov-1 to a different local must be rejected")
	}
	if got, _ := m.ResolveLocalTurn("prov-1"); got != "local-1" {
		t.Fatalf("ResolveLocalTurn = %q, want local-1", got)
	}
	if got, _ := m.ResolveProviderTurn("local-1"); got != "prov-1" {
		t.Fatalf("ResolveProviderTurn = %q, want prov-1", got)
	}
}

func TestMapTurnRejectsEmpty(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	if m.MapTurn("", "prov") || m.MapTurn("local", "") {
		t.Fatal("empty side must be rejected")
	}
}

func TestAttributeCallBindsToTurn(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	if m.AttributeCall("", "local-1") || m.AttributeCall("call-1", "") {
		t.Fatal("empty callID or turnID must be rejected")
	}
	if !m.AttributeCall("call-1", "local-1") {
		t.Fatal("AttributeCall should succeed")
	}
	if got, _ := m.LookupCall("call-1"); got != "local-1" {
		t.Fatalf("LookupCall = %q, want local-1", got)
	}
	// Re-attributing is allowed: a later event may have better information
	// (for example, initial best-effort attribution getting corrected).
	if !m.AttributeCall("call-1", "local-2") {
		t.Fatal("re-attribution should be allowed")
	}
	if got, _ := m.LookupCall("call-1"); got != "local-2" {
		t.Fatalf("LookupCall after reattribution = %q, want local-2", got)
	}
}

func TestRecordTokensPreservesNonZero(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	m.RecordTokens("t", TokenSnapshot{Input: 10, Output: 20, Total: 30, Observed: true})

	// Zero-event must not overwrite existing counts.
	m.RecordTokens("t", TokenSnapshot{})
	got, _ := m.Tokens("t")
	if got.Input != 10 || got.Output != 20 || got.Total != 30 {
		t.Fatalf("zero-event clobbered counts: %+v", got)
	}
	if !got.Observed {
		t.Fatalf("Observed must stay true across zero-event: %+v", got)
	}

	// Context-window-only update must land without touching counts.
	m.RecordTokens("t", TokenSnapshot{ContextWindowTokens: 100_000, Projection: "thread"})
	got, _ = m.Tokens("t")
	if got.Input != 10 || got.Output != 20 || got.ContextWindowTokens != 100_000 {
		t.Fatalf("context-window merge wrong: %+v", got)
	}
	if got.Projection != "thread" {
		t.Fatalf("projection not recorded: %+v", got)
	}

	// Genuine non-zero update wins per field.
	m.RecordTokens("t", TokenSnapshot{Input: 15})
	got, _ = m.Tokens("t")
	if got.Input != 15 || got.Output != 20 {
		t.Fatalf("non-zero overwrite wrong: %+v", got)
	}
}

func TestRecordTerminalInterruptedIsSticky(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	m.RecordTerminal("t", Terminal{Kind: TerminalInterrupted, Reason: "user"})

	succ := true
	m.RecordTerminal("t", Terminal{Kind: TerminalCompleted, Success: &succ})

	got, _ := m.Terminal("t")
	if got.Kind != TerminalInterrupted {
		t.Fatalf("interrupted displaced by completed: %+v", got)
	}
	if got.Reason != "user" {
		t.Fatalf("interrupted reason lost: %+v", got)
	}
}

func TestRecordTerminalAbortedIsSticky(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	m.RecordTerminal("t", Terminal{Kind: TerminalAborted})
	succ := true
	m.RecordTerminal("t", Terminal{Kind: TerminalCompleted, Success: &succ})
	got, _ := m.Terminal("t")
	if got.Kind != TerminalAborted {
		t.Fatalf("aborted displaced: %+v", got)
	}
}

func TestRecordTerminalUpgradesToHigherPrecedence(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	succ := true
	m.RecordTerminal("t", Terminal{Kind: TerminalCompleted, Success: &succ})
	m.RecordTerminal("t", Terminal{Kind: TerminalFailed, Reason: "boom"})

	got, _ := m.Terminal("t")
	if got.Kind != TerminalFailed {
		t.Fatalf("failed must replace completed: %+v", got)
	}
	if got.Reason != "boom" {
		t.Fatalf("reason merge wrong: %+v", got)
	}
	// Success from the earlier completed event survives since the later
	// event left it nil.
	if got.Success == nil || *got.Success != true {
		t.Fatalf("earlier Success must survive when later event is nil: %+v", got)
	}
}

func TestRecordTerminalDoesNotDowngrade(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	m.RecordTerminal("t", Terminal{Kind: TerminalFailed, Reason: "err"})
	// Lower-precedence completed must not displace failed.
	m.RecordTerminal("t", Terminal{Kind: TerminalCompleted})
	got, _ := m.Terminal("t")
	if got.Kind != TerminalFailed {
		t.Fatalf("completed must not downgrade failed: %+v", got)
	}
}

func TestSkillsSelectedIsDefensiveCopy(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	m.SetSkillsSelected("t", []string{"skill-a", "skill-b"})

	got := m.SkillsSelected("t")
	if len(got) != 2 || got[0] != "skill-a" || got[1] != "skill-b" {
		t.Fatalf("SkillsSelected = %v", got)
	}
	// Caller mutation must not leak back into storage.
	got[0] = "mutated"
	if still := m.SkillsSelected("t"); still[0] != "skill-a" {
		t.Fatalf("internal state leaked: %v", still)
	}
}

func TestDedupeRejectsSecondObservation(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	k := DedupeKey{CallID: "call-1"}
	if !m.Dedupe(k) {
		t.Fatal("first Dedupe must return true")
	}
	if m.Dedupe(k) {
		t.Fatal("second Dedupe must return false")
	}
	// Different key kind does not collide.
	if !m.Dedupe(DedupeKey{RawEventID: "raw-1"}) {
		t.Fatal("independent DedupeKey must be treated as unique")
	}
	// Empty key is always unique — observation must not swallow events
	// that arrive with no identifying field.
	if !m.Dedupe(DedupeKey{}) || !m.Dedupe(DedupeKey{}) {
		t.Fatal("empty DedupeKey must be treated as always unique")
	}
}

func TestMemoryIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	m := NewMemory()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.MapTurn("l", "p")
			m.AttributeCall("c", "l")
			m.RecordTokens("l", TokenSnapshot{Input: 1, Observed: true})
			m.RecordTerminal("l", Terminal{Kind: TerminalCompleted})
			m.Dedupe(DedupeKey{CallID: "c"})
			_, _ = m.Tokens("l")
			_, _ = m.Terminal("l")
			_, _ = m.ResolveLocalTurn("p")
			_ = m.SkillsSelected("l")
		}()
	}
	wg.Wait()
}
