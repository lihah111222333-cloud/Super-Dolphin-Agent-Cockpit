package kernel

import (
	"github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util/historyjsonl"
)

// ProviderHistoryReadRequest describes a provider history lookup.
type ProviderHistoryReadRequest = historyjsonl.ReadRequest

// JSONLPageResult is a paginated JSONL read result.
type JSONLPageResult[T any] = historyjsonl.JSONLPageResult[T]

// ExistingProviderPath returns the current provider history file path.
func ExistingProviderPath(req ProviderHistoryReadRequest) (string, error) {
	return historyjsonl.ExistingProviderPath(req)
}

// ReadProviderMessagesPage reads a provider message history page.
func ReadProviderMessagesPage(req ProviderHistoryReadRequest, pageReq provider.MessagePageRequest) (provider.MessagePageResult, error) {
	return historyjsonl.ReadProviderMessagesPage(req, pageReq)
}

// ReadProviderMessagesPageOrError reads a provider message page with a caller-supplied missing error.
func ReadProviderMessagesPageOrError(req ProviderHistoryReadRequest, pageReq provider.MessagePageRequest, missingErr error) (provider.MessagePageResult, error) {
	return historyjsonl.ReadProviderMessagesPageOrError(req, pageReq, missingErr)
}

// IsMissingProviderHistory reports whether err means the provider history file is absent.
func IsMissingProviderHistory(err error) bool {
	return historyjsonl.IsMissingProviderHistory(err)
}

// ReadJSONLPage reads a generic JSONL page backwards from a cursor.
func ReadJSONLPage[T any](path string, limit int, before string, parse func([]byte) (T, bool)) (JSONLPageResult[T], error) {
	return historyjsonl.ReadJSONLPage(path, limit, before, parse)
}
