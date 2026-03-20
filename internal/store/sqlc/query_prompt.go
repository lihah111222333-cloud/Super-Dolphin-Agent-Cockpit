package sqlc

import "context"

const (
	getPromptTemplateSQL    = `SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, created_at, updated_at FROM prompt_templates WHERE prompt_key = $1;`
	insertPromptVersionSQL  = `INSERT INTO prompt_versions ( prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, enabled, created_by, updated_by, source_updated_at ) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11);`
	upsertPromptTemplateSQL = `INSERT INTO prompt_templates ( prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, updated_at ) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10, $11, NOW()) ON CONFLICT (prompt_key) DO UPDATE SET title = EXCLUDED.title, agent_key = EXCLUDED.agent_key, tool_name = EXCLUDED.tool_name, prompt_text = EXCLUDED.prompt_text, variables = EXCLUDED.variables, tags = EXCLUDED.tags, description = EXCLUDED.description, enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = NOW() RETURNING id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, created_at, updated_at;`
	listPromptTemplatesSQL  = `SELECT id, prompt_key, title, agent_key, tool_name, prompt_text, variables, tags, description, enabled, created_by, updated_by, created_at, updated_at FROM prompt_templates WHERE ($1::text = '' OR agent_key = $1) AND ($2::text = '' OR prompt_key ILIKE '%' || $2 || '%' OR title ILIKE '%' || $2 || '%' OR prompt_text ILIKE '%' || $2 || '%') ORDER BY updated_at DESC LIMIT $3;`
)

func scanPromptTemplate(row rowScanner) (PromptTemplate, error) {
	var item PromptTemplate
	err := row.Scan(&item.ID, &item.PromptKey, &item.Title, &item.AgentKey, &item.ToolName, &item.PromptText, &item.Variables, &item.Tags, &item.Description, &item.Enabled, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) GetPromptTemplate(ctx context.Context, promptKey string) (PromptTemplate, error) {
	return queryOne(ctx, q, getPromptTemplateSQL, scanPromptTemplate, promptKey)
}

func (q *Queries) InsertPromptVersion(ctx context.Context, arg InsertPromptVersionParams) error {
	return q.exec(ctx, insertPromptVersionSQL, arg.PromptKey, arg.Title, arg.AgentKey, arg.ToolName, arg.PromptText, arg.Variables, arg.Tags, arg.Enabled, arg.CreatedBy, arg.UpdatedBy, arg.SourceUpdatedAt)
}

func (q *Queries) UpsertPromptTemplate(ctx context.Context, arg UpsertPromptTemplateParams) (PromptTemplate, error) {
	return queryOne(ctx, q, upsertPromptTemplateSQL, scanPromptTemplate, arg.PromptKey, arg.Title, arg.AgentKey, arg.ToolName, arg.PromptText, arg.Variables, arg.Tags, arg.Description, arg.Enabled, arg.CreatedBy, arg.UpdatedBy)
}

func (q *Queries) ListPromptTemplates(ctx context.Context, arg ListPromptTemplatesParams) ([]PromptTemplate, error) {
	return queryMany(ctx, q, listPromptTemplatesSQL, scanPromptTemplate, arg.AgentKey, arg.Keyword, arg.Limit)
}
