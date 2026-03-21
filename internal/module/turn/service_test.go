package turn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestPrepareTurnKeepsSkillPromptsAndNormalizesInputs(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt: "Please use @debug and [skill:deploy-tool] on this issue.",
		Images: []string{"https://example.com/screen.png", "https://example.com/screen.png"},
		Files:  []string{"./README.md", "./README.md", "./malware.exe"},
		Skills: []dto.SkillRef{{Name: "explicit", Prompt: "explicit guidance"}},
		CandidateSkills: []dto.SkillRef{
			{Name: "debug", Prompt: "debug guidance"},
			{Name: "deploy-tool", Prompt: "deploy guidance"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	if got := len(req.Inputs); got != 3 {
		t.Fatalf("len(req.Inputs) = %d, want 3", got)
	}
	if req.Inputs[0].Type != "text" || req.Inputs[0].Content != "Please use @debug and [skill:deploy-tool] on this issue." {
		t.Fatalf("first input = %#v, want prompt text", req.Inputs[0])
	}
	if req.Inputs[1].Type != "image" || req.Inputs[1].URL != "https://example.com/screen.png" {
		t.Fatalf("second input = %#v, want remote image", req.Inputs[1])
	}
	if req.Inputs[2].Type != "mention" || req.Inputs[2].Path != "./README.md" {
		t.Fatalf("third input = %#v, want deduped mention", req.Inputs[2])
	}

	gotNames := skillNames(req.Skills)
	if len(gotNames) != 3 || gotNames[0] != "explicit" || gotNames[1] != "debug" || gotNames[2] != "deploy-tool" {
		t.Fatalf("skill names = %#v, want explicit + auto-matched", gotNames)
	}
	if req.Skills[1].Prompt != "debug guidance" || req.Skills[2].Prompt != "deploy guidance" {
		t.Fatalf("skill prompts were not preserved: %#v", req.Skills)
	}
}

func TestPrepareTurnManualSkillSelectionDisablesAutoMatch(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:               "Please use @debug on this issue.",
		ManualSkillSelection: true,
		CandidateSkills:      []dto.SkillRef{{Name: "debug", Prompt: "debug guidance"}},
	})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if req.ManualSkillSelection != true {
		t.Fatal("ManualSkillSelection = false, want true")
	}
	if len(req.Skills) != 0 {
		t.Fatalf("Skills = %#v, want no auto-matched skills in manual mode", req.Skills)
	}
}

func TestPrepareTurnTruncatesInputCount(t *testing.T) {
	t.Parallel()

	items := make([]InputItem, 0, maxTurnInputItems+32)
	for i := 0; i < maxTurnInputItems+32; i++ {
		items = append(items, InputItem{Type: "mention", Path: fmt.Sprintf("./doc-%03d.md", i)})
	}

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{Inputs: items})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}
	if got := len(req.Inputs); got != maxTurnInputItems {
		t.Fatalf("len(req.Inputs) = %d, want %d", got, maxTurnInputItems)
	}
}

func TestPrepareTurnUsesExecutableBinaryDirForManifest(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	want := filepath.Join(filepath.Dir(exe), "go-agent-mcp-lsp")
	if got := commandForBinary(req.MCP, "go-agent-mcp-lsp"); got != want {
		t.Fatalf("lsp command = %q, want %q", got, want)
	}
}

func TestPrepareTurnPrefersExplicitBinaryDir(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger())
	session := &stubSession{threadID: "thread-1"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{BinaryDir: "/tmp/turn-bin"})
	if err != nil {
		t.Fatalf("PrepareTurn() error = %v", err)
	}

	want := filepath.Join("/tmp/turn-bin", "go-agent-mcp-lsp")
	if got := commandForBinary(req.MCP, "go-agent-mcp-lsp"); got != want {
		t.Fatalf("lsp command = %q, want %q", got, want)
	}
}

func TestInterruptTurnWaitsForSettle(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-1", "provider-1")
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(errors.New("turn aborted"))
			})
			return nil
		},
	}

	svc := NewService(silentLogger())
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-1",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	interruptStatus, err := svc.InterruptTurn(context.Background(), session, "user")
	if err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	if interruptStatus.LocalID != "local-1" || interruptStatus.State != "interrupted" {
		t.Fatalf("InterruptTurn() status = %#v, want local-1/interrupted", interruptStatus)
	}
	if session.lastInterrupt.ThreadID != "thread-1" {
		t.Fatalf("InterruptTurn thread id = %q, want thread-1", session.lastInterrupt.ThreadID)
	}

	status, err := svc.TrackTurn(context.Background(), "local-1")
	if err != nil {
		t.Fatalf("TrackTurn() error = %v", err)
	}
	if status.State != "interrupted" {
		t.Fatalf("status.State = %q, want interrupted", status.State)
	}
}

func TestSteerTurnAppendsToActiveTurn(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			handle := newStubTurnHandle(req.LocalID, "provider-2")
			return handle, nil
		},
		steer: func(_ context.Context, req dto.SteerRequest) error {
			if req.ExpectedTurnID != "provider-2" {
				t.Fatalf("SteerTurn expected turn id = %q, want provider-2", req.ExpectedTurnID)
			}
			if len(req.Inputs) != 1 || req.Inputs[0].Type != "text" || req.Inputs[0].Content != "steer this" {
				t.Fatalf("SteerTurn request = %#v", req)
			}
			return nil
		},
	}

	svc := NewService(silentLogger())
	started, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-2",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	handle, err := svc.SteerTurn(context.Background(), session, "local-2", PrepareInput{Prompt: "steer this"})
	if err != nil {
		t.Fatalf("SteerTurn() error = %v", err)
	}
	if handle != started {
		t.Fatalf("SteerTurn() handle = %#v, want active handle %#v", handle, started)
	}
}

func TestForceCompleteTurnLeavesFinalStateToWatcher(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-2", "provider-2")
	session := &stubSession{
		threadID: "thread-2",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		forceComplete: func(context.Context, dto.ForceCompleteRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(nil)
			})
			return nil
		},
	}

	svc := NewService(silentLogger())
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-2",
		ThreadID: "thread-2",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if err := svc.ForceCompleteTurn(context.Background(), session); err != nil {
		t.Fatalf("ForceCompleteTurn() error = %v", err)
	}
	if session.lastForceComplete.ThreadID != "thread-2" {
		t.Fatalf("ForceCompleteTurn thread id = %q, want thread-2", session.lastForceComplete.ThreadID)
	}
	if session.lastForceComplete.ProviderID != "provider-2" {
		t.Fatalf("ForceCompleteTurn provider id = %q, want provider-2", session.lastForceComplete.ProviderID)
	}
	deadline := time.Now().Add(time.Second)
	for {
		status, err := svc.TrackTurn(context.Background(), "local-2")
		if err != nil {
			t.Fatalf("TrackTurn() error = %v", err)
		}
		if status.State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status.State = %q, want completed", status.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type stubSession struct {
	threadID          string
	caps              dto.CapabilitySet
	startTurn         func(context.Context, dto.TurnRequest) (contract.TurnHandle, error)
	steer             func(context.Context, dto.SteerRequest) error
	interrupt         func(context.Context, dto.InterruptRequest) error
	forceComplete     func(context.Context, dto.ForceCompleteRequest) error
	lastInterrupt     dto.InterruptRequest
	lastSteer         dto.SteerRequest
	lastForceComplete dto.ForceCompleteRequest
}

func (s *stubSession) ThreadID() string { return s.threadID }

func (s *stubSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s *stubSession) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	if s.startTurn != nil {
		return s.startTurn(ctx, req)
	}
	return nil, errors.New("startTurn not configured")
}

func (s *stubSession) Interrupt(ctx context.Context, req dto.InterruptRequest) error {
	s.lastInterrupt = req
	if s.interrupt != nil {
		return s.interrupt(ctx, req)
	}
	return nil
}

func (s *stubSession) Steer(ctx context.Context, req dto.SteerRequest) error {
	s.lastSteer = req
	if s.steer != nil {
		return s.steer(ctx, req)
	}
	return nil
}

func (s *stubSession) ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error {
	s.lastForceComplete = req
	if s.forceComplete != nil {
		return s.forceComplete(ctx, req)
	}
	return nil
}

func (s *stubSession) ListThreads(context.Context) ([]dto.ThreadRef, error) { return nil, nil }

func (s *stubSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}

func (s *stubSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}

func (s *stubSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }

func (s *stubSession) Close(context.Context) error { return nil }

func (s *stubSession) ForceStop() error { return nil }

type stubTurnHandle struct {
	localID    string
	providerID string
	done       chan struct{}
	err        error
}

func newStubTurnHandle(localID, providerID string) *stubTurnHandle {
	return &stubTurnHandle{
		localID:    localID,
		providerID: providerID,
		done:       make(chan struct{}),
	}
}

func (h *stubTurnHandle) LocalID() string       { return h.localID }
func (h *stubTurnHandle) ProviderID() string    { return h.providerID }
func (h *stubTurnHandle) Done() <-chan struct{} { return h.done }
func (h *stubTurnHandle) Err() error            { return h.err }

func (h *stubTurnHandle) complete(err error) {
	h.err = err
	close(h.done)
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func commandForBinary(manifest dto.MCPManifest, name string) string {
	for _, binary := range manifest.Binaries {
		if binary.Name == name && len(binary.Command) > 0 {
			return binary.Command[0]
		}
	}
	return ""
}

func skillNames(refs []dto.SkillRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}
