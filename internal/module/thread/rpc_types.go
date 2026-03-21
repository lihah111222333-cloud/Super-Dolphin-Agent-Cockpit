package thread

import (
	"encoding/json"
	"strings"
)

type threadIDParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *threadIDParams) UnmarshalJSON(data []byte) error {
	type raw threadIDParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = threadIDParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type startParams struct {
	Provider              string          `json:"provider,omitempty"`
	CWD                   string          `json:"cwd,omitempty"`
	Model                 string          `json:"model,omitempty"`
	ModelProvider         string          `json:"model_provider,omitempty"`
	ApprovalPolicy        string          `json:"approval_policy,omitempty"`
	BaseInstructions      string          `json:"base_instructions,omitempty"`
	DeveloperInstructions string          `json:"developer_instructions,omitempty"`
	Sandbox               json.RawMessage `json:"sandbox,omitempty"`
	Summary               string          `json:"summary,omitempty"`
	Effort                string          `json:"effort,omitempty"`
	Personality           string          `json:"personality,omitempty"`
	Prompt                string          `json:"-"`
}

func (p *startParams) UnmarshalJSON(data []byte) error {
	type raw startParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = startParams(current)
	return p.fillLegacyFields(data)
}

func (p *startParams) fillLegacyFields(data []byte) error {
	var legacy struct {
		ModelProvider         string `json:"modelProvider"`
		ModelProviderAlt      string `json:"model_provider"`
		ApprovalPolicy        string `json:"approvalPolicy"`
		ApprovalPolicyAlt     string `json:"approval_policy"`
		BaseInstructions      string `json:"baseInstructions"`
		BaseInstructionsAlt   string `json:"base_instructions"`
		DeveloperInstructions string `json:"developerInstructions"`
		DeveloperAlt          string `json:"developer_instructions"`
		Instructions          string `json:"instructions"`
		Prompt                string `json:"prompt"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.ModelProvider) == "" {
		p.ModelProvider = firstNonEmpty(legacy.ModelProvider, legacy.ModelProviderAlt)
	}
	if strings.TrimSpace(p.ApprovalPolicy) == "" {
		p.ApprovalPolicy = firstNonEmpty(legacy.ApprovalPolicy, legacy.ApprovalPolicyAlt)
	}
	if strings.TrimSpace(p.Prompt) == "" {
		p.Prompt = strings.TrimSpace(legacy.Prompt)
	}
	if strings.TrimSpace(p.DeveloperInstructions) == "" {
		p.DeveloperInstructions = firstNonEmpty(legacy.DeveloperInstructions, legacy.DeveloperAlt)
	}
	if strings.TrimSpace(p.BaseInstructions) == "" {
		p.BaseInstructions = firstNonEmpty(
			legacy.BaseInstructions,
			legacy.BaseInstructionsAlt,
			legacy.Instructions,
			legacy.Prompt,
		)
	}
	return nil
}

type resumeParams struct {
	ThreadID string `json:"thread_id"`
	Path     string `json:"path,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (p *resumeParams) UnmarshalJSON(data []byte) error {
	type raw resumeParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = resumeParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type messagesParams struct {
	ThreadID string `json:"thread_id"`
	Limit    int    `json:"limit,omitempty"`
	Before   string `json:"before,omitempty"`
}

type threadInfo struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

func (p *messagesParams) UnmarshalJSON(data []byte) error {
	type raw messagesParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = messagesParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type nameSetParams struct {
	ThreadID string `json:"thread_id"`
	Name     string `json:"name"`
}

func (p *nameSetParams) UnmarshalJSON(data []byte) error {
	type raw nameSetParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = nameSetParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type commandParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
}

func (p *commandParams) UnmarshalJSON(data []byte) error {
	type raw commandParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = commandParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type configGetParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *configGetParams) UnmarshalJSON(data []byte) error {
	type raw configGetParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = configGetParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type modelSetParams struct {
	ThreadID string `json:"thread_id"`
	Model    string `json:"model,omitempty"`
	Args     string `json:"args,omitempty"`
}

func (p *modelSetParams) UnmarshalJSON(data []byte) error {
	type raw modelSetParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = modelSetParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type compactStartParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
}

func (p *compactStartParams) UnmarshalJSON(data []byte) error {
	type raw compactStartParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = compactStartParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

func fillLegacyThreadID(data []byte, threadID *string) error {
	if strings.TrimSpace(*threadID) != "" {
		return nil
	}
	var legacy struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	*threadID = strings.TrimSpace(legacy.ThreadID)
	return nil
}
