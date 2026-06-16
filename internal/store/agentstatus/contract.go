package agentstatus

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store persists and lists current agent runtime statuses.
type Store = contract.AgentStatusStore

// UpsertParams updates the current status for one agent.
type UpsertParams = contract.AgentStatusUpsertParams

// AgentStatus is the dashboard projection of one agent runtime.
type AgentStatus = contract.AgentStatus
