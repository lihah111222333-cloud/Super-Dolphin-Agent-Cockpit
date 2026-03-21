package thread

import "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"

// Started reports a thread becoming active and routable.
type Started struct {
	shared.EventHeader
	ThreadID         string `json:"thread_id"`
	AgentID          string `json:"agent_id,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
}

// Stopped reports a thread becoming inactive.
type Stopped struct {
	shared.EventHeader
	ThreadID string `json:"thread_id"`
	AgentID  string `json:"agent_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// MessagesPage reports a thread message page refresh.
type MessagesPage struct {
	shared.EventHeader
	ThreadID   string `json:"thread_id"`
	TotalCount int    `json:"total_count"`
	Pages      int    `json:"pages"`
}

func (Started) Type() uint32      { return shared.EventTypeThreadStarted }
func (Stopped) Type() uint32      { return shared.EventTypeThreadStopped }
func (MessagesPage) Type() uint32 { return shared.EventTypeThreadMessagesPage }
