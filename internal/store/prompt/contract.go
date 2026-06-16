package prompt

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Reader is kept as a compatibility alias for the prompt read port.
type Reader = contract.PromptReader

// Store is kept as a compatibility alias for the prompt persistence port.
type Store = contract.PromptStore

// ListFilter is kept as a compatibility alias for prompt list filters.
type ListFilter = contract.PromptListFilter

// RuntimeListFilter is kept as a compatibility alias for runtime prompt filters.
type RuntimeListFilter = contract.PromptRuntimeListFilter

// RuntimePromptCatalog is kept as a compatibility alias for runtime prompt reads.
type RuntimePromptCatalog = contract.RuntimePromptCatalog

// PromptTemplate is kept as a compatibility alias for prompt template DTOs.
type PromptTemplate = contract.PromptTemplate

// PromptTemplateSection is kept as a compatibility alias for prompt section DTOs.
type PromptTemplateSection = contract.PromptTemplateSection

// PromptTemplateVersion is kept as a compatibility alias for prompt version DTOs.
type PromptTemplateVersion = contract.PromptTemplateVersion

// PromptIntentDraft is kept as a compatibility alias for prompt intent draft DTOs.
type PromptIntentDraft = contract.PromptIntentDraft

// PromptIntentDraftListFilter is kept as a compatibility alias for draft list filters.
type PromptIntentDraftListFilter = contract.PromptIntentDraftListFilter
