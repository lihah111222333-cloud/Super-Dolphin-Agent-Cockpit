package app_test

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
)

type externalTurnDedupeStore struct{}

func (externalTurnDedupeStore) Upsert(context.Context, turn.DedupeUpsertParams) error {
	return nil
}

func (externalTurnDedupeStore) BindProviderTurnID(context.Context, turn.DedupeBindProviderTurnIDParams) error {
	return nil
}

func (externalTurnDedupeStore) MarkTerminal(context.Context, string, time.Time) error {
	return nil
}

func (externalTurnDedupeStore) GetLive(context.Context, string) (turn.DedupeEntry, error) {
	return turn.DedupeEntry{}, nil
}

var _ turn.DedupeStore = externalTurnDedupeStore{}
var _ = turn.ErrDedupeNotFound
