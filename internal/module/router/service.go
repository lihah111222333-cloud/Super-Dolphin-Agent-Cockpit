package router

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	routerpkg "github.com/anthropic-ai/super-agent-v3/internal/router"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	rtstore "github.com/anthropic-ai/super-agent-v3/internal/store/routingtest"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type service struct {
	logger          *slog.Logger
	backend         routerpkg.Backend
	store           promptstore.Reader
	routingTestRead rtstore.Reader // optional
}

var _ Service = (*service)(nil)

// NewService wires the preview classifier. backend and store are both
// optional \u2014 a nil dependency returns Matched=false; callers should treat
// "no preview" as normal rather than an error. routingTestRead is optional
// too; when nil RunTests returns an empty result (useful for DBs that have
// not been seeded with tests yet).
func NewService(logger *slog.Logger, backend routerpkg.Backend, store promptstore.Reader, routingTestRead rtstore.Reader) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &service{logger: logger, backend: backend, store: store, routingTestRead: routingTestRead}
}

func (s *service) Classify(ctx context.Context, req ClassifyRequest) (ClassifyResult, error) {
	ctx = shared.NonNilContext(ctx)

	input := strings.TrimSpace(req.UserInput)
	if input == "" || s == nil || s.backend == nil || s.store == nil {
		return ClassifyResult{}, nil
	}

	templates, err := s.store.List(ctx, promptstore.ListFilter{Limit: 200})
	if err != nil {
		s.logger.Warn("router/classify: list prompt_templates failed",
			slog.String("error", err.Error()),
		)
		return ClassifyResult{}, nil
	}

	candidates := toCandidates(templates)
	if len(candidates) == 0 {
		return ClassifyResult{}, nil
	}

	decision, err := s.backend.Classify(ctx, input, candidates)
	if err != nil {
		s.logger.Warn("router/classify: backend classify failed",
			slog.String("error", err.Error()),
		)
		return ClassifyResult{}, nil
	}
	if !decision.Matched {
		return ClassifyResult{}, nil
	}
	return ClassifyResult{
		Matched:    true,
		PromptKey:  decision.PromptKey,
		AgentKey:   decision.AgentKey,
		Title:      templateTitleByPromptKey(templates, decision.PromptKey),
		Reason:     decision.Reason,
		Confidence: decision.Confidence,
	}, nil
}

func templateTitleByPromptKey(templates []promptstore.PromptTemplate, key string) string {
	for i := range templates {
		if templates[i].PromptKey == key {
			return templates[i].Title
		}
	}
	return ""
}

func toCandidates(templates []promptstore.PromptTemplate) []routerpkg.Candidate {
	out := make([]routerpkg.Candidate, 0, len(templates))
	for i := range templates {
		t := &templates[i]
		if !t.Enabled {
			continue
		}
		out = append(out, routerpkg.Candidate{
			PromptKey:   t.PromptKey,
			AgentKey:    t.AgentKey,
			Title:       t.Title,
			Description: t.Description,
			Tags:        parseTagsJSON(t.Tags),
		})
	}
	return out
}

func parseTagsJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var asStrings []string
	if err := json.Unmarshal(raw, &asStrings); err == nil {
		return asStrings
	}
	var asObjects []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &asObjects); err == nil {
		out := make([]string, 0, len(asObjects))
		for _, o := range asObjects {
			if s := strings.TrimSpace(o.Value); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
