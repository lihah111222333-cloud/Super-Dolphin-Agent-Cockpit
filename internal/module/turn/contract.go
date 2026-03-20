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
	SteerTurn(ctx context.Context, session contract.Session, prompt string) (contract.TurnHandle, error)
	InterruptTurn(ctx context.Context, session contract.Session, source string) error
	ForceCompleteTurn(ctx context.Context, session contract.Session) error
	TrackTurn(ctx context.Context, localID string) (TurnStatus, error)
}

type InputItem = shareddto.InputItem

type PrepareInput struct {
	Inputs          []InputItem
	Prompt          string
	Images          []string
	Files           []string
	Skills          []dto.SkillRef
	CandidateSkills []dto.SkillRef
	Model           string
	Effort          string
	OutputSchema    json.RawMessage
	AgentID         string
	CWD             string
	ThreadCaps      dto.CapabilitySet
	BinaryDir       string
}

type TurnStatus struct {
	LocalID    string `json:"localId"`
	ProviderID string `json:"providerId"`
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
}
