package sqlc

import "context"

const (
	getCommandCardSQL           = `SELECT id, card_key, title, description, command_template, args_schema, risk_level, enabled, created_by, updated_by, created_at, updated_at FROM command_cards WHERE card_key = $1;`
	insertCommandCardVersionSQL = `INSERT INTO command_card_versions ( card_key, title, description, command_template, args_schema, risk_level, enabled, created_by, updated_by, source_updated_at ) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, $10);`
	upsertCommandCardSQL        = `INSERT INTO command_cards ( card_key, title, description, command_template, args_schema, risk_level, enabled, created_by, updated_by, updated_at ) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, NOW()) ON CONFLICT (card_key) DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description, command_template = EXCLUDED.command_template, args_schema = EXCLUDED.args_schema, risk_level = EXCLUDED.risk_level, enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = NOW() RETURNING id, card_key, title, description, command_template, args_schema, risk_level, enabled, created_by, updated_by, created_at, updated_at;`
	listCommandCardsSQL         = `SELECT c.id, c.card_key, c.title, c.description, c.command_template, c.args_schema, c.risk_level, c.enabled, c.created_by, c.updated_by, c.created_at, c.updated_at, stats.last_run_at, COALESCE(stats.run_count, 0)::bigint AS run_count FROM command_cards AS c LEFT JOIN ( SELECT card_key, MAX(created_at) AS last_run_at, COUNT(*)::bigint AS run_count FROM command_card_runs GROUP BY card_key ) AS stats ON stats.card_key = c.card_key WHERE ($1::text = '' OR c.card_key ILIKE '%' || $1 || '%' OR c.title ILIKE '%' || $1 || '%' OR c.description ILIKE '%' || $1 || '%' OR c.command_template ILIKE '%' || $1 || '%') ORDER BY c.updated_at DESC, c.id DESC LIMIT $2;`
)

func scanCommandCard(row rowScanner) (CommandCard, error) {
	var item CommandCard
	err := row.Scan(&item.ID, &item.CardKey, &item.Title, &item.Description, &item.CommandTemplate, &item.ArgsSchema, &item.RiskLevel, &item.Enabled, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanListCommandCardsRow(row rowScanner) (ListCommandCardsRow, error) {
	var item ListCommandCardsRow
	err := row.Scan(&item.ID, &item.CardKey, &item.Title, &item.Description, &item.CommandTemplate, &item.ArgsSchema, &item.RiskLevel, &item.Enabled, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.LastRunAt, &item.RunCount)
	return item, err
}

func (q *Queries) GetCommandCard(ctx context.Context, cardKey string) (CommandCard, error) {
	return queryOne(ctx, q, getCommandCardSQL, scanCommandCard, cardKey)
}

func (q *Queries) InsertCommandCardVersion(ctx context.Context, arg InsertCommandCardVersionParams) error {
	return q.exec(ctx, insertCommandCardVersionSQL, arg.CardKey, arg.Title, arg.Description, arg.CommandTemplate, arg.ArgsSchema, arg.RiskLevel, arg.Enabled, arg.CreatedBy, arg.UpdatedBy, arg.SourceUpdatedAt)
}

func (q *Queries) UpsertCommandCard(ctx context.Context, arg UpsertCommandCardParams) (CommandCard, error) {
	return queryOne(ctx, q, upsertCommandCardSQL, scanCommandCard, arg.CardKey, arg.Title, arg.Description, arg.CommandTemplate, arg.ArgsSchema, arg.RiskLevel, arg.Enabled, arg.CreatedBy, arg.UpdatedBy)
}

func (q *Queries) ListCommandCards(ctx context.Context, arg ListCommandCardsParams) ([]ListCommandCardsRow, error) {
	return queryMany(ctx, q, listCommandCardsSQL, scanListCommandCardsRow, arg.Keyword, arg.Limit)
}
