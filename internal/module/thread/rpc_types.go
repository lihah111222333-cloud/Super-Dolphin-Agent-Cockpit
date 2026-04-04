package thread

import (
	"encoding/json"
	"errors"
	"strings"
)

type threadIDParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *threadIDParams) UnmarshalJSON(data []byte) error {
	type raw threadIDParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = threadIDParams(current)
	return nil
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
	Config                json.RawMessage `json:"config,omitempty"`
	Prompt                string          `json:"-"`
}

func (p *startParams) UnmarshalJSON(data []byte) error {
	type raw startParams
	var current raw
	if err := decodeLegacyParams(data, &current, nil); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = resumeParams(current)
	return nil
}

type messagesParams struct {
	ThreadID string `json:"thread_id"`
	Limit    int    `json:"limit,omitempty"`
	Before   string `json:"before,omitempty"`
}

type threadInfo struct {
	ID         string `json:"id"`
	Status     string `json:"status,omitempty"`
	ForkedFrom string `json:"forkedFrom,omitempty"`
}

func (p *messagesParams) UnmarshalJSON(data []byte) error {
	type raw struct {
		ThreadID string          `json:"thread_id"`
		Limit    int             `json:"limit,omitempty"`
		Before   json.RawMessage `json:"before,omitempty"`
	}
	var current raw
	if err := decodeLegacyParams(data, &current, nil); err != nil {
		return err
	}
	p.ThreadID = current.ThreadID
	p.Limit = current.Limit
	before, err := decodeMessagesBefore(current.Before)
	if err != nil {
		return err
	}
	p.Before = before
	return fillLegacyThreadID(data, &p.ThreadID)
}

func decodeMessagesBefore(raw json.RawMessage) (string, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return "", nil
	}
	var cursor string
	if err := json.Unmarshal(raw, &cursor); err == nil {
		return strings.TrimSpace(cursor), nil
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.UseNumber()
	var number json.Number
	if err := dec.Decode(&number); err == nil {
		return strings.TrimSpace(number.String()), nil
	}
	return "", errors.New("thread/messages: before must be a string or integer")
}

type nameSetParams struct {
	ThreadID string `json:"thread_id"`
	Name     string `json:"name"`
}

func (p *nameSetParams) UnmarshalJSON(data []byte) error {
	type raw nameSetParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = nameSetParams(current)
	return nil
}

type commandParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
}

func (p *commandParams) UnmarshalJSON(data []byte) error {
	type raw commandParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = commandParams(current)
	return nil
}

type approvalsSetParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
	Policy   string `json:"policy,omitempty"`
}

func (p *approvalsSetParams) UnmarshalJSON(data []byte) error {
	type raw approvalsSetParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = approvalsSetParams(current)
	return nil
}

type configGetParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *configGetParams) UnmarshalJSON(data []byte) error {
	type raw configGetParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = configGetParams(current)
	return nil
}

type configSetParams struct {
	ThreadID string  `json:"thread_id"`
	Model    *string `json:"model,omitempty"`
	Effort   *string `json:"effort,omitempty"`
}

func (p *configSetParams) UnmarshalJSON(data []byte) error {
	type raw configSetParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = configSetParams(current)
	return nil
}

type modelSetParams struct {
	ThreadID string `json:"thread_id"`
	Model    string `json:"model,omitempty"`
	Args     string `json:"args,omitempty"`
}

func (p *modelSetParams) UnmarshalJSON(data []byte) error {
	type raw modelSetParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = modelSetParams(current)
	return nil
}

type compactStartParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
}

func (p *compactStartParams) UnmarshalJSON(data []byte) error {
	type raw compactStartParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = compactStartParams(current)
	return nil
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
