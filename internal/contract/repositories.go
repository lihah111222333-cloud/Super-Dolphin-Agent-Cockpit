package contract

import "context"

type ThreadRepository interface {
	List(ctx context.Context) ([]ThreadRef, error)
	Get(ctx context.Context, id string) (*ThreadRef, error)
}

type ThreadRef struct {
	ID   string
	Name string
}
