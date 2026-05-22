package intent

import (
	"encoding/json"
	"errors"
	"strings"
)

type Kind string

const (
	KindExpert      Kind = "expert"
	KindRecall      Kind = "recall"
	KindDefaultRule Kind = "default_rule"
)

type DraftParams struct {
	Kind         string `json:"kind"`
	RawInput     string `json:"raw_input"`
	Cwd          string `json:"cwd,omitempty"`
	SourceType   string `json:"source_type,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	LicenseHint  string `json:"license_hint,omitempty"`
	EnableGlobal bool   `json:"enable_global,omitempty"`
}

type DryRunParams struct {
	DraftKey string          `json:"draft_key,omitempty"`
	Kind     string          `json:"kind"`
	Card     json.RawMessage `json:"card"`
	Question string          `json:"question"`
	Cwd      string          `json:"cwd,omitempty"`
}

type CommitParams struct {
	DraftKey      string `json:"draft_key"`
	Cwd           string `json:"cwd,omitempty"`
	ConfirmRisk   bool   `json:"confirm_risk,omitempty"`
	EnableGlobal  bool   `json:"enable_global,omitempty"`
	ConfirmGlobal bool   `json:"confirm_global,omitempty"`
}

type DiscardParams struct {
	DraftKey string `json:"draft_key"`
	Cwd      string `json:"cwd,omitempty"`
}

type E2EHealthParams struct{}

type E2EHealthResult struct {
	Provider        string `json:"provider"`
	FixturePathHash string `json:"fixture_path_hash,omitempty"`
}

type Card struct {
	Kind                 string         `json:"kind"`
	Title                string         `json:"title"`
	Summary              string         `json:"summary"`
	WhenToUse            string         `json:"when_to_use,omitempty"`
	WhenNotToUse         string         `json:"when_not_to_use,omitempty"`
	Workflow             []string       `json:"workflow,omitempty"`
	Constraints          []string       `json:"constraints,omitempty"`
	Output               string         `json:"output,omitempty"`
	SaveBoundary         string         `json:"save_boundary,omitempty"`
	RecallTopic          string         `json:"recall_topic,omitempty"`
	RecallBody           string         `json:"recall_body,omitempty"`
	DefaultRuleBody      string         `json:"default_rule_body,omitempty"`
	SourceProfile        string         `json:"source_profile,omitempty"`
	SourceFacts          []SourceFact   `json:"source_facts,omitempty"`
	HitExamples          []string       `json:"hit_examples"`
	MissExamples         []string       `json:"miss_examples"`
	ConflictingRules     []RuleConflict `json:"conflicting_rules,omitempty"`
	SuggestedAlternative *Alternative   `json:"suggested_alternative,omitempty"`
}

type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type RuleConflict struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type SourceFact struct {
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	Disposition string `json:"disposition"`
}

type Alternative struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

func requireCWD(cwd string) (string, error) {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return "", errors.New("dashboard: cwd is required")
	}
	return requestScope, nil
}
