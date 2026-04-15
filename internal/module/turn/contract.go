package turn

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

type Service interface {
	PrepareTurn(ctx context.Context, session contract.Session, input PrepareInput) (dto.TurnRequest, error)
	StartTurn(ctx context.Context, session contract.Session, req dto.TurnRequest) (contract.TurnHandle, error)
	SteerTurn(ctx context.Context, session contract.Session, expectedTurnID string, input PrepareInput) (contract.TurnHandle, error)
	InterruptTurn(ctx context.Context, session contract.Session, source string) (TurnStatus, error)
	InterruptActiveTurn(ctx context.Context, session contract.Session, source string) error
	ForceCompleteTurn(ctx context.Context, session contract.Session) error
	CleanupThread(ctx context.Context, threadID, reason string) error
	TrackTurn(ctx context.Context, localID string) (TurnStatus, error)
}

type SessionProvider interface {
	GetSession(agentID string) (contract.Session, error)
}

type ThreadStateConfigReader interface {
	ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
}

type InputItem = shareddto.InputItem

type PrepareInput struct {
	Inputs                       []InputItem
	Prompt                       string
	Images                       []string
	Files                        []string
	Skills                       []dto.SkillRef
	CandidateSkills              []dto.SkillRef
	ManualSkillSelection         bool
	Provider                     string
	Model                        string
	Effort                       string
	OutputSchema                 json.RawMessage
	AgentID                      string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  contract.MCPSnapshot
	SessionFlags                 map[string]bool
	Summary                      string
	OutputStyleConfig            *contract.OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *contract.FRCConfig
	ThreadRuntimeConfig          map[string]any
	ThreadCaps                   dto.CapabilitySet
	BinaryDir                    string
}

type TurnStatus struct {
	LocalID    string `json:"localId"`
	ProviderID string `json:"providerId"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	interrupt  turnInterruptEnvelope
}
