package turn

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn/observation"
)

func newTrajectoryFixture() (*Collector, *observation.Memory) {
	mem := observation.NewMemory()
	c := NewTrajectoryCollector(mem, nil)
	return c, mem
}

func makeTurnHeader(turnID, threadID, agentID string, ts time.Time) shared.TurnHeader {
	h := shared.TurnHeader{}
	h.Timestamp = ts
	h.ThreadID = threadID
	h.AgentID = agentID
	h.TurnID = turnID
	return h
}

func makeToolHeader(turnID, threadID, agentID, callID, tool string, ts time.Time) shared.ToolCallHeader {
	h := shared.ToolCallHeader{}
	h.Timestamp = ts
	h.ThreadID = threadID
	h.AgentID = agentID
	h.TurnID = turnID
	h.CallID = callID
	h.ToolName = tool
	return h
}

// 1. Same callID firing Begin/End twice (raw + typed retransmit) only
// produces a single ToolCalls entry.
func TestTrajectoryCollector_DedupesRawAndTyped(t *testing.T) {
	c, _ := newTrajectoryFixture()
	now := time.Now()
	begin := tooldto.ToolCallBegin{
		ToolCallHeader:   makeToolHeader("T1", "th-1", "ag-1", "C1", "fs.read", now),
		ArgumentsPreview: "preview",
	}
	end := tooldto.ToolCallEnd{
		ToolCallHeader: makeToolHeader("T1", "th-1", "ag-1", "C1", "fs.read", now.Add(10*time.Millisecond)),
		Success:        true,
		Result:         "ok",
	}
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now)})
	c.onToolCallBegin(begin)
	c.onToolCallEnd(end)
	// Second pair: should be idempotent in the collector's own call map.
	c.onToolCallBegin(begin)
	c.onToolCallEnd(end)

	snap, ok := c.Snapshot("T1")
	if !ok {
		t.Fatalf("Snapshot ok=false")
	}
	if len(snap.ToolCalls) != 1 {
		t.Fatalf("want 1 ToolCall after dedup, got %d", len(snap.ToolCalls))
	}
	if snap.ToolCalls[0].CallID != "C1" || snap.ToolCalls[0].Result != "ok" {
		t.Fatalf("unexpected ToolCall: %+v", snap.ToolCalls[0])
	}
}

// 2. ToolDiffUpdated has no TurnID; collector must resolve via
// observation.LookupCall (which Begin populated via AttributeCall).
func TestTrajectoryCollector_AttributesToolDiffViaCallIDMap(t *testing.T) {
	c, mem := newTrajectoryFixture()
	now := time.Now()
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now)})
	c.onToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: makeToolHeader("T1", "th-1", "ag-1", "C1", "fs.write", now),
	})

	mem.AttributeCall("C1", "T1")
	// Sanity: observation has the binding supplied by the observation writer.
	if owner, ok := mem.LookupCall("C1"); !ok || owner != "T1" {
		t.Fatalf("LookupCall = (%q,%v); want (T1,true)", owner, ok)
	}

	// Diff event without TurnID:
	c.onToolDiffUpdated(tooldto.ToolDiffUpdated{
		Timestamp: now.Add(20 * time.Millisecond),
		ThreadID:  "th-1",
		AgentID:   "ag-1",
		CallID:    "C1",
		ToolName:  "fs.write",
		DiffText:  "+a",
		Files:     []string{"a.go"},
	})

	snap, ok := c.Snapshot("T1")
	if !ok || len(snap.ToolCalls) != 1 {
		t.Fatalf("snapshot bad: ok=%v calls=%d", ok, len(snap.ToolCalls))
	}
	if snap.ToolCalls[0].DiffCount != 1 {
		t.Fatalf("DiffCount = %d, want 1", snap.ToolCalls[0].DiffCount)
	}
}

// 3. Interrupted is sticky; a late Completed must not overwrite. The
// collector reads Terminal from the observation Contract; we drive the
// contract directly (mirroring what observation's subscriber does) so the
// test isolates collector behaviour from observation's bus wiring.
func TestTrajectoryCollector_InterruptedBeatsLateCompleted(t *testing.T) {
	c, mem := newTrajectoryFixture()
	now := time.Now()
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now)})

	// Interrupted first - observation records sticky terminal.
	mem.RecordTerminal("T1", observation.Terminal{Kind: observation.TerminalInterrupted, Reason: "user_cancel"})
	c.onTurnInterrupted(turndto.TurnInterrupted{
		TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now.Add(5*time.Millisecond)),
		Reason:     "user_cancel",
	})

	// Late Completed - observation refuses to displace sticky terminal,
	// and the collector's drained set keeps the second drain a no-op.
	successTrue := true
	mem.RecordTerminal("T1", observation.Terminal{Kind: observation.TerminalCompleted, Success: &successTrue})
	c.onTurnCompleted(turndto.TurnCompleted{
		TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now.Add(10*time.Millisecond)),
		Success:    true,
	})

	got := c.Drain()
	if len(got) != 1 {
		t.Fatalf("Drain returned %d trajectories, want 1", len(got))
	}
	tj := got[0]
	if tj.TerminalState != "interrupted" {
		t.Fatalf("TerminalState = %q, want interrupted", tj.TerminalState)
	}
	if tj.Success != nil && *tj.Success {
		t.Fatalf("Success should not be flipped to true after interrupted; got %v", *tj.Success)
	}
}

// 4. Token zero-event must not overwrite a prior non-zero snapshot when
// surfaced through Trajectory.TokenUsage.
func TestTrajectoryCollector_TokenZeroEventDoesNotOverwrite(t *testing.T) {
	c, mem := newTrajectoryFixture()
	now := time.Now()
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now)})

	// Non-zero first.
	mem.RecordTokens("T1", observation.TokenSnapshot{
		Input: 100, Output: 200, Total: 300, ContextWindowTokens: 8000, Observed: true,
	})
	// Zero-event after; mergeTokens preserves prior non-zero.
	mem.RecordTokens("T1", observation.TokenSnapshot{Observed: false})

	c.onTurnCompleted(turndto.TurnCompleted{
		TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now.Add(time.Millisecond)),
		Success:    true,
	})
	got := c.Drain()
	if len(got) != 1 {
		t.Fatalf("drain got %d", len(got))
	}
	if got[0].TokenUsage == nil {
		t.Fatal("TokenUsage nil, want preserved non-zero")
	}
	if got[0].TokenUsage.InputTokens != 100 || got[0].TokenUsage.OutputTokens != 200 {
		t.Fatalf("TokenUsage = %+v, want input=100 output=200", *got[0].TokenUsage)
	}
}

// 5. ToolDiffUpdated whose CallID has no observation attribution is dropped.
func TestTrajectoryCollector_DropsToolDiffWithUnknownCall(t *testing.T) {
	c, _ := newTrajectoryFixture()
	now := time.Now()
	c.onToolDiffUpdated(tooldto.ToolDiffUpdated{
		Timestamp: now,
		ThreadID:  "th-1",
		AgentID:   "ag-1",
		CallID:    "C-orphan",
	})
	// No partial created.
	if _, ok := c.Snapshot("anything"); ok {
		t.Fatal("Snapshot ok=true; collector should not have created a partial")
	}
	if got := c.Drain(); len(got) != 0 {
		t.Fatalf("Drain returned %d, want 0", len(got))
	}
}

// 6. Drain returns terminal turns only and is idempotent for already-drained.
func TestTrajectoryCollector_DrainEmptiesTerminalOnly(t *testing.T) {
	c, _ := newTrajectoryFixture()
	now := time.Now()
	// T1 in flight (no terminal yet).
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("T1", "th-1", "ag-1", now)})
	// T2 terminal.
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("T2", "th-1", "ag-1", now)})
	c.onTurnCompleted(turndto.TurnCompleted{
		TurnHeader: makeTurnHeader("T2", "th-1", "ag-1", now.Add(time.Millisecond)),
		Success:    true,
	})

	got := c.Drain()
	if len(got) != 1 {
		t.Fatalf("first Drain got %d, want 1", len(got))
	}
	if got[0].TurnID != "T2" {
		t.Fatalf("first Drain TurnID = %q, want T2", got[0].TurnID)
	}

	if got2 := c.Drain(); len(got2) != 0 {
		t.Fatalf("second Drain got %d, want 0", len(got2))
	}

	// T1 still snapshottable.
	if _, ok := c.Snapshot("T1"); !ok {
		t.Fatal("T1 partial gone before terminal")
	}
}

// 7. Architecture invariant: observation must not import the turn package.
// Step 2 is the only side that depends on observation; the reverse direction
// would create a cycle and re-introduce the dependency the P0b plan
// explicitly forbids ("观察 -> collector -> consumer 单向 push").
func TestImports_ObservationDoesNotImportTurn(t *testing.T) {
	dir := filepath.Join("observation")
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %q: %v", dir, err)
	}
	const turnPkg = `"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn"`
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %q: %v", path, err)
		}
		for _, imp := range file.Imports {
			if imp.Path == nil {
				continue
			}
			if imp.Path.Value == turnPkg {
				t.Fatalf("observation/%s imports %s", entry.Name(), imp.Path.Value)
			}
		}
	}
}
