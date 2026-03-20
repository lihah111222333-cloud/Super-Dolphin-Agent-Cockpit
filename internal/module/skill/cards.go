package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
)

var cardPlaceholder = regexp.MustCompile(`\{([a-zA-Z_]\w*)\}`)

func (s *service) ListCards(ctx context.Context) ([]Card, error) {
	store, err := s.cardStore()
	if err != nil {
		return nil, err
	}
	return store.List(ctx, commandcardstore.ListFilter{Limit: 1000})
}

func (s *service) GetCard(ctx context.Context, key string) (*Card, error) {
	store, err := s.cardStore()
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, strings.TrimSpace(key))
}

func (s *service) CreateCard(ctx context.Context, card Card) (*Card, error) {
	card = normalizeCard(card)
	if err := validateCard(card); err != nil {
		return nil, err
	}
	if existing, err := s.lookupCard(ctx, card.CardKey); err == nil && existing != nil {
		return nil, fmt.Errorf("command card already exists: %s", card.CardKey)
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	store, err := s.cardStore()
	if err != nil {
		return nil, err
	}
	return store.Upsert(ctx, card)
}

func (s *service) UpdateCard(ctx context.Context, card Card) (*Card, error) {
	card = normalizeCard(card)
	if err := validateCard(card); err != nil {
		return nil, err
	}
	current, err := s.lookupCard(ctx, card.CardKey)
	if err != nil {
		return nil, err
	}
	if err := s.archiveCard(ctx, *current); err != nil {
		return nil, err
	}
	store, err := s.cardStore()
	if err != nil {
		return nil, err
	}
	return store.Upsert(ctx, mergeCard(card, *current))
}

func (s *service) DeleteCard(ctx context.Context, key string) error {
	current, err := s.lookupCard(ctx, key)
	if err != nil {
		return err
	}
	if err := s.archiveCard(ctx, *current); err != nil {
		return err
	}
	store, err := s.cardStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, strings.TrimSpace(key))
}

func (s *service) ListCardVersions(ctx context.Context, key string) ([]CardVersion, error) {
	store, err := s.cardStore()
	if err != nil {
		return nil, err
	}
	return store.ListVersions(ctx, strings.TrimSpace(key))
}

func (s *service) RunCard(ctx context.Context, key string, args map[string]any) (CardRunResult, error) {
	card, err := s.GetCard(ctx, key)
	if err != nil {
		return CardRunResult{}, err
	}
	if card == nil || !card.Enabled {
		return CardRunResult{}, fmt.Errorf("command card is disabled: %s", strings.TrimSpace(key))
	}
	rendered, err := renderCardCommand(card.CommandTemplate, args)
	if err != nil {
		return CardRunResult{}, err
	}
	cwd, _ := args["cwd"].(string)
	execResult, err := s.execShell(ctx, rendered, cwd)
	if err != nil {
		return CardRunResult{}, err
	}
	return CardRunResult{CardKey: card.CardKey, RenderedCommand: rendered, Exec: execResult}, nil
}

func (s *service) cardStore() (commandcardstore.Store, error) {
	if s.cards == nil {
		return nil, errors.New("command card store is not configured")
	}
	return s.cards, nil
}

func (s *service) lookupCard(ctx context.Context, key string) (*Card, error) {
	store, err := s.cardStore()
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, strings.TrimSpace(key))
}

func (s *service) archiveCard(ctx context.Context, card Card) error {
	store, err := s.cardStore()
	if err != nil {
		return err
	}
	return store.InsertVersion(ctx, CardVersion{
		CardKey:         card.CardKey,
		Title:           card.Title,
		Description:     card.Description,
		CommandTemplate: card.CommandTemplate,
		ArgsSchema:      card.ArgsSchema,
		RiskLevel:       card.RiskLevel,
		Enabled:         card.Enabled,
		CreatedBy:       card.CreatedBy,
		UpdatedBy:       card.UpdatedBy,
		SourceUpdatedAt: &card.UpdatedAt,
	})
}

func normalizeCard(card Card) Card {
	card.CardKey = strings.TrimSpace(card.CardKey)
	card.Title = strings.TrimSpace(card.Title)
	card.Description = strings.TrimSpace(card.Description)
	card.CommandTemplate = strings.TrimSpace(card.CommandTemplate)
	card.RiskLevel = strings.TrimSpace(card.RiskLevel)
	card.CreatedBy = strings.TrimSpace(card.CreatedBy)
	card.UpdatedBy = strings.TrimSpace(card.UpdatedBy)
	if len(strings.TrimSpace(string(card.ArgsSchema))) == 0 {
		card.ArgsSchema = json.RawMessage("{}")
	}
	if card.RiskLevel == "" {
		card.RiskLevel = "normal"
	}
	return card
}

func validateCard(card Card) error {
	switch {
	case card.CardKey == "":
		return errors.New("command card key is required")
	case card.Title == "":
		return errors.New("command card title is required")
	case card.CommandTemplate == "":
		return errors.New("command card command_template is required")
	default:
		return nil
	}
}

func mergeCard(next, current Card) Card {
	if next.CreatedBy == "" {
		next.CreatedBy = current.CreatedBy
	}
	if next.UpdatedBy == "" {
		next.UpdatedBy = current.UpdatedBy
	}
	if len(next.ArgsSchema) == 0 {
		next.ArgsSchema = current.ArgsSchema
	}
	return next
}

func renderCardCommand(tmpl string, args map[string]any) (string, error) {
	rendered := tmpl
	for key, value := range args {
		rendered = strings.ReplaceAll(rendered, "{"+key+"}", shellQuote(fmt.Sprint(value)))
	}
	if match := cardPlaceholder.FindString(rendered); match != "" {
		return "", fmt.Errorf("command template missing argument: %s", match)
	}
	return rendered, nil
}
