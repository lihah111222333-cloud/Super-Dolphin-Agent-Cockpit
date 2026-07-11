//go:build phasefshim

package thread

import (
	"context"
	"log/slog"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
	threadstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
)

type service struct {
	cfg            *contract.Config
	toolRegistry   contract.ToolRegistry
	logger         *slog.Logger
	threadStore    threadstore.Store
	promptAssembly contract.PromptAssemblyService
}

type storedThreadConfig struct {
	Model       string         `json:"model,omitempty"`
	Effort      string         `json:"effort,omitempty"`
	Approvals   string         `json:"approvals,omitempty"`
	Personality string         `json:"personality,omitempty"`
	Runtime     map[string]any `json:"runtime,omitempty"`
}

type offlineConfigSnapshot struct {
	Config  dto.ThreadConfig
	Runtime map[string]any
}

type resumeState struct {
	Provider         string
	PublicThreadID   string
	AgentType        string
	AgentMemoryScope string
	ParentAgentID    string
	CWD              string
	Model            string
	Prompt           string
}

func (s *service) buildOfflineConfig(context.Context, string, *bindingstore.Binding) (offlineConfigSnapshot, error) {
	return offlineConfigSnapshot{}, nil
}
