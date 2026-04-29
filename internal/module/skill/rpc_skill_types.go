package skill

type pathParams struct {
	Path string `json:"path"`
	CWD  string `json:"cwd,omitempty"`
}

type contentParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Scope   string `json:"scope,omitempty"`
	CWD     string `json:"cwd,omitempty"`
}

type listSkillFilesParams struct {
	Dir  string `json:"dir"`
	Path string `json:"path,omitempty"`
	CWD  string `json:"cwd,omitempty"`
}

type importSkillDirParams struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths,omitempty"`
	Name  string   `json:"name,omitempty"`
	Mode  string   `json:"mode,omitempty"`
	Scope string   `json:"scope,omitempty"`
	CWD   string   `json:"cwd,omitempty"`
}

type deleteLocalSkillParams struct {
	Name string `json:"name"`
	CWD  string `json:"cwd,omitempty"`
}

// createSkillParams is the input to skills/create. It is the host-side entry
// point for project-scope self-learning writes (P21 P0a): the caller supplies
// a skill slug and SKILL.md content, scope is always project, and cwd is a
// required field. CreateSkill is a thin wrapper over WriteLocal — the second
// writer path for project scope is explicitly forbidden by the P21 plan.
type createSkillParams struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	CWD     string `json:"cwd"`
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
	CWD      string      `json:"cwd,omitempty"`
}

type skillListParams struct {
	CWD string `json:"cwd,omitempty"`
}

type skillListItem struct {
	Name                   string     `json:"name"`
	Summary                string     `json:"summary"`
	Description            string     `json:"description"`
	Trust                  TrustScope `json:"trust"`
	ContentHash            string     `json:"content_hash"`
	DisableModelInvocation bool       `json:"disable_model_invocation"`
}

type skillListResult struct {
	Skills []skillListItem `json:"skills"`
}

type skillExpandParams struct {
	Name          string `json:"name"`
	Section       string `json:"section,omitempty"`
	MaxBytes      int64  `json:"max_bytes,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	ApprovalScope string `json:"approval_scope,omitempty"`
	Scope         string `json:"scope,omitempty"`
	AgentID       string `json:"agentId,omitempty"`
	ThreadID      string `json:"threadId,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	TurnID        string `json:"turnId,omitempty"`
}

type skillExpandResult struct {
	Name        string     `json:"name"`
	Section     string     `json:"section"`
	Path        string     `json:"path"`
	Summary     string     `json:"summary"`
	Content     string     `json:"content"`
	Truncated   bool       `json:"truncated"`
	TotalBytes  int64      `json:"total_bytes"`
	ContentHash string     `json:"content_hash"`
	Trust       TrustScope `json:"trust"`
}
