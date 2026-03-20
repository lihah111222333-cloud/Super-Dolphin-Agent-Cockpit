package thread

type threadIDParams struct {
	ThreadID string `json:"threadId"`
}

type startParams struct {
	Provider       string `json:"provider"`
	CWD            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Instructions   string `json:"instructions,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Personality    string `json:"personality,omitempty"`
}

type resumeParams struct {
	ThreadID string `json:"threadId"`
	Provider string `json:"provider,omitempty"`
}

type messagesParams struct {
	ThreadID string `json:"threadId"`
	Limit    int    `json:"limit,omitempty"`
	Before   string `json:"before,omitempty"`
}

type nameSetParams struct {
	ThreadID string `json:"threadId"`
	Name     string `json:"name"`
}

type commandParams struct {
	ThreadID string `json:"threadId"`
	Args     string `json:"args,omitempty"`
}
