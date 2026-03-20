package skill

type pathParams struct {
	Path string `json:"path"`
}

type contentParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type listSkillFilesParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path,omitempty"`
}

type importSkillDirParams struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths,omitempty"`
	Name  string   `json:"name,omitempty"`
	CWD   string   `json:"cwd,omitempty"`
}

type deleteLocalSkillParams struct {
	Name string `json:"name"`
}

type skillConfigReadParams struct {
	AgentID string `json:"agent_id"`
}

type skillNamedContentParams struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type skillSummaryWriteParams struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

type skillRemoteReadParams struct {
	URL string `json:"url"`
}

type UserInput struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type skillMatchPreviewParams struct {
	ThreadID string      `json:"threadId,omitempty"`
	AgentID  string      `json:"agent_id,omitempty"` // Falls back when threadId is empty.
	Text     string      `json:"text"`
	Input    []UserInput `json:"input,omitempty"`
}
