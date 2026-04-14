package memory

import "context"

type Service interface {
	Config() Config
	RootDir() string
	EnsureRoot(ctx context.Context) error
}
