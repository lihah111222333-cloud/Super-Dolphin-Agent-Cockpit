package dbquery

import "context"

type Store interface {
	Placeholder(ctx context.Context) ([]PlaceholderRow, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
}

type PlaceholderRow struct {
	Placeholder *string
}
