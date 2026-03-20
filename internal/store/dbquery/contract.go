package dbquery

import "context"

type Store interface {
	Placeholder(ctx context.Context) ([]PlaceholderRow, error)
}

type PlaceholderRow struct {
	Placeholder *string
}
