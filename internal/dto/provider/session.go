package provider

import "encoding/json"

type PromptRegion int

const (
	PromptRegionStatic PromptRegion = iota
	PromptRegionDynamic
)

type ResolvedPromptSection struct {
	Name     string       `json:"name,omitempty"`
	Region   PromptRegion `json:"region,omitempty"`
	Volatile bool         `json:"volatile,omitempty"`
	Content  string       `json:"content,omitempty"`
}

type PromptAssemblySnapshot struct {
	DisplayName           string `json:"displayName,omitempty"`
	BaseInstructions      string `json:"baseInstructions,omitempty"`
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
	Provider              string `json:"provider,omitempty"`
	Version               int    `json:"version,omitempty"`
	Hash                  string `json:"hash,omitempty"`
	SectionSnapshot       map[string]string `json:"sectionSnapshot,omitempty"`
	Generation            uint64 `json:"generation,omitempty"`
}

type StartAssembly struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	ResolvedSections      []ResolvedPromptSection `json:"resolvedSections,omitempty"`
	Snapshot              PromptAssemblySnapshot  `json:"snapshot"`
}

type TurnAssembly struct {
	UserContextText  string                  `json:"userContextText,omitempty"`
	ResolvedSections []ResolvedPromptSection `json:"resolvedSections,omitempty"`
}

type StartSessionRequest struct {
	Provider      string         `json:"provider"`
	AgentID       string         `json:"agentId"`
	CWD           string         `json:"cwd"`
	Model         string         `json:"model,omitempty"`
	Instructions  string         `json:"instructions,omitempty"`
	StartAssembly StartAssembly  `json:"startAssembly"`
	Config        map[string]any `json:"config,omitempty"`
}

type ResumeSessionRequest struct {
	Provider         string                 `json:"provider"`
	AgentID          string                 `json:"agentId"`
	ThreadID         string                 `json:"threadId"`
	ProviderThreadID string                 `json:"providerThreadId,omitempty"`
	Path             string                 `json:"path,omitempty"`
	CWD              string                 `json:"cwd,omitempty"`
	Model            string                 `json:"model,omitempty"`
	Effort           string                 `json:"effort,omitempty"`
	PromptSnapshot   PromptAssemblySnapshot `json:"promptSnapshot"`
	ConfigOverride   ThreadConfigPatch      `json:"configOverride"`
}

var _ json.Marshaler = json.RawMessage(nil)
