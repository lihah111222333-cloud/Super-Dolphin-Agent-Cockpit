package memoryadapter_test

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/sharedfileport"
)

type externalMemorySharedFileReader struct{}

func (externalMemorySharedFileReader) Get(context.Context, string) (*sharedfileport.File, error) {
	return nil, nil
}

func (externalMemorySharedFileReader) List(
	context.Context,
	sharedfileport.ListFilter,
) ([]sharedfileport.File, error) {
	return nil, nil
}

type externalMemorySharedFileDeleter struct{}

func (externalMemorySharedFileDeleter) Delete(context.Context, string) (int64, error) {
	return 0, nil
}

var _ sharedfileport.Reader = externalMemorySharedFileReader{}
var _ sharedfileport.Deleter = externalMemorySharedFileDeleter{}
