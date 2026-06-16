package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
)

var ErrConsolidationAgentMemoryPath = errors.New("dream cannot access agent memory path")

type consolidationDocument struct {
	Path    string
	Content string
}

type consolidationPromptInput struct {
	MemoryRoot     string
	Limit          int
	Index          consolidationDocument
	TopicEntries   []MemoryEntry
	TopicDocuments []consolidationDocument
	LogDocuments   []consolidationDocument
}

// loadConsolidationPromptInput 加载consolidationpromptinput。
func loadConsolidationPromptInput(root string, cfg *Config) (consolidationPromptInput, error) {
	normalizedRoot, err := normalizeStoreRoot(root)
	if err != nil {
		return consolidationPromptInput{}, err
	}
	if err := rejectConsolidationPath(cfg, normalizedRoot); err != nil {
		return consolidationPromptInput{}, err
	}
	entries, err := scanMemoryEntries(normalizedRoot)
	if err != nil {
		return consolidationPromptInput{}, err
	}
	topicDocs := make([]consolidationDocument, 0, len(entries))
	for _, entry := range entries {
		if err := rejectConsolidationPath(cfg, entry.FilePath); err != nil {
			return consolidationPromptInput{}, err
		}
		topicDocs = append(topicDocs, consolidationDocument{
			Path:    relativeMemoryPath(normalizedRoot, entry.FilePath),
			Content: strings.TrimSpace(entry.Content),
		})
	}
	indexDoc, err := loadConsolidationIndexDocument(normalizedRoot, cfg)
	if err != nil {
		return consolidationPromptInput{}, err
	}
	logDocs, err := scanConsolidationLogDocuments(normalizedRoot, cfg)
	if err != nil {
		return consolidationPromptInput{}, err
	}
	return consolidationPromptInput{
		MemoryRoot:     normalizedRoot,
		Index:          indexDoc,
		TopicEntries:   append([]MemoryEntry(nil), entries...),
		TopicDocuments: topicDocs,
		LogDocuments:   logDocs,
	}, nil
}

// buildConsolidationPrompt 构建consolidationprompt。
func buildConsolidationPrompt(input consolidationPromptInput) string {
	limit := extractLimit(input.Limit, defaultExtractMaxItems)
	parts := []string{
		renderSection("### Phase 1 — Orient", []string{
			"You are running manual dream / consolidation for durable memory.",
			"Use the current MEMORY.md, durable topic files, and KAIROS daily logs to refresh long-term memory.",
			"Never read from, summarize, or propose writes for any agent memory directory. If the inputs look agent-scoped, treat that as invalid.",
			fmt.Sprintf("Return JSON only in the form {\"memories\":[{\"content\":\"...\",\"type\":\"user|feedback|project|reference\",\"tags\":[\"...\"]}]}. Return at most %d memories.", limit),
		}),
		renderSection("### Phase 2 — Gather", []string{
			"Review the current MEMORY.md summary first.",
			"Then inspect durable topic files for overlap, drift, and stale duplicates.",
			"Finally inspect daily logs for new durable facts that have not been distilled yet.",
		}),
		renderConsolidationDocument("#### MEMORY.md", input.Index),
		renderConsolidationDocumentGroup("#### Topic files", input.TopicDocuments),
		renderConsolidationDocumentGroup("#### Daily logs", input.LogDocuments),
		renderSection("### Phase 3 — Consolidate", []string{
			"Merge duplicates and normalize wording into stable, typed memories.",
			"Prefer one durable memory per stable fact or rule; combine overlapping notes when they clearly describe the same thing.",
			"Carry forward durable details from logs only when they remain worth remembering beyond the original session.",
		}),
		renderSection("### Phase 4 — Prune", []string{
			"Drop stale, empty, contradictory, or purely transient notes.",
			"Do not keep references to daily-log bookkeeping once the durable fact has been distilled.",
			"The returned memories will replace outdated topic files and regenerate MEMORY.md, so keep only the durable set.",
		}),
	}
	return strings.Join(nonEmpty(parts), "\n\n")
}

// loadConsolidationIndexDocument 加载consolidation索引document。
func loadConsolidationIndexDocument(root string, cfg *Config) (consolidationDocument, error) {
	path := memoryIndexPath(root)
	if err := rejectConsolidationPath(cfg, path); err != nil {
		return consolidationDocument{}, err
	}
	validatedPath, err := ValidateMemoryReadPath(root, path)
	switch {
	case errors.Is(err, ErrInvalidMemoryReadPath), errors.Is(err, os.ErrNotExist):
		return consolidationDocument{}, err
	case err != nil:
		return consolidationDocument{}, err
	}
	raw, err := os.ReadFile(validatedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return consolidationDocument{}, err
		}
		return consolidationDocument{}, err
	}
	content := strings.TrimSpace(parse.StripUTF8BOM(string(raw)))
	if content == "" {
		content = "(empty)"
	}
	return consolidationDocument{Path: memoryIndexFileName, Content: content}, nil
}

// scanConsolidationLogDocuments 扫描consolidation日志documents。
func scanConsolidationLogDocuments(root string, cfg *Config) ([]consolidationDocument, error) {
	logRoot := filepath.Join(root, "logs")
	if err := rejectConsolidationPath(cfg, logRoot); err != nil {
		return nil, err
	}
	exists, err := consolidationLogRootExists(logRoot)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	docs := make([]consolidationDocument, 0, 8)
	err = filepath.WalkDir(logRoot, func(path string, d os.DirEntry, walkErr error) error {
		doc, ok, readErr := readConsolidationLogDocument(root, cfg, path, d, walkErr)
		if readErr != nil {
			return readErr
		}
		if ok {
			docs = append(docs, doc)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

func consolidationLogRootExists(logRoot string) (bool, error) {
	if _, err := os.Stat(logRoot); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// readConsolidationLogDocument 读取consolidation日志document。
func readConsolidationLogDocument(root string, cfg *Config, path string, d os.DirEntry, walkErr error) (consolidationDocument, bool, error) {
	if walkErr != nil {
		return consolidationDocument{}, false, walkErr
	}
	if d.IsDir() || filepath.Ext(path) != ".md" {
		return consolidationDocument{}, false, nil
	}
	if err := rejectConsolidationPath(cfg, path); err != nil {
		return consolidationDocument{}, false, err
	}
	validatedPath, err := ValidateMemoryReadPath(root, path)
	if err != nil {
		return consolidationDocument{}, false, err
	}
	raw, err := os.ReadFile(validatedPath)
	if err != nil {
		return consolidationDocument{}, false, err
	}
	return consolidationDocument{
		Path:    relativeMemoryPath(root, validatedPath),
		Content: strings.TrimSpace(parse.StripUTF8BOM(string(raw))),
	}, true, nil
}

func rejectConsolidationPath(_ *Config, path string) error {
	if memshared.IsHistoricalAgentMemoryPath(path) {
		return ErrConsolidationAgentMemoryPath
	}
	return nil
}

func renderConsolidationDocument(title string, doc consolidationDocument) string {
	content := strings.TrimSpace(doc.Content)
	if content == "" {
		content = "(empty)"
	}
	parts := []string{title}
	if path := strings.TrimSpace(doc.Path); path != "" {
		parts = append(parts, "Path: `"+path+"`")
	}
	parts = append(parts, content)
	return strings.Join(parts, "\n")
}

func renderConsolidationDocumentGroup(title string, docs []consolidationDocument) string {
	if len(docs) == 0 {
		return strings.Join([]string{title, "(empty)"}, "\n")
	}
	parts := []string{title}
	for i, doc := range docs {
		label := fmt.Sprintf("##### Document %d", i+1)
		if path := strings.TrimSpace(doc.Path); path != "" {
			label += " — `" + path + "`"
		}
		parts = append(parts, label)
		content := strings.TrimSpace(doc.Content)
		if content == "" {
			content = "(empty)"
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n")
}

func relativeMemoryPath(root, path string) string {
	if strings.TrimSpace(root) == "" {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func isConsolidationLogPath(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "logs" || strings.HasPrefix(rel, "logs/")
}
