// Package memory compatibility bridges for the team-memory
// subpackage migration. Owned by the subpackage split; keep here until
// root callers move to direct memory/team imports.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dedup "github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
)

var ErrInvalidProjectDir = errors.New("invalid project directory")

// ==== team-memory bridge ====

type TeamMemoryManager = teampkg.TeamMemoryManager
type TeamMemoryGuard = teampkg.TeamMemoryGuard
type TeamMemSecretFinding = teampkg.TeamMemSecretFinding
type TeamMemSkippedFile = teampkg.TeamMemSkippedFile
type TeamMemPrePushScanResult = teampkg.TeamMemPrePushScanResult
type TeamMemSecretError = teampkg.TeamMemSecretError

const teamMemoryRootDirName = teampkg.RootDirName

var ErrTeamMemSecretDetected = teampkg.ErrTeamMemSecretDetected

func provideTeamConfig(cfg *Config) teampkg.Config {
	return teamConfigAdapter{cfg: memoryConfig(cfg)}
}

func NewTeamMemoryManager(cfg *Config) *TeamMemoryManager {
	return teampkg.NewTeamMemoryManager(provideTeamConfig(cfg))
}

func NewTeamMemoryGuard(manager *TeamMemoryManager) *TeamMemoryGuard {
	return teampkg.NewTeamMemoryGuard(manager)
}

func teamMemoryConfigured(cfg Config) bool {
	return cfg.Enabled && cfg.Features.TeamMemory
}

func configuredTeamMemRoot(cfg *Config, buildCtx ...contract.BuildCtx) (string, error) {
	return provideTeamConfig(cfg).TeamRoot(firstTeamBuildCtx(buildCtx))
}

func configuredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	return teampkg.ConfiguredTeamMemPath(m, buildCtx...)
}

func teamMemPath(m *TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if m == nil {
		return ""
	}
	return m.GetTeamMemPath(buildCtx)
}

func isTeamMemoryEnabled(m *TeamMemoryManager, buildCtx contract.BuildCtx) bool {
	if m == nil {
		return false
	}
	return m.IsTeamMemoryEnabled(buildCtx)
}

func validateTeamMemWriteRequest(m *TeamMemoryManager, raw string) error {
	if m == nil {
		return ErrTeamMemoryDisabled
	}
	return m.ValidateTeamMemWritePath(raw)
}

func validateTeamMemKeyRequest(m *TeamMemoryManager, key string) error {
	if m == nil {
		return ErrTeamMemoryDisabled
	}
	return m.ValidateTeamMemKey(key)
}

func firstTeamBuildCtx(buildCtx []contract.BuildCtx) contract.BuildCtx {
	if len(buildCtx) == 0 {
		return contract.BuildCtx{}
	}
	return buildCtx[0]
}

func setTeamMemoryRuntimeReady(ready bool) {
	teampkg.SetRuntimeReady(ready)
}

func ScanTeamMemContent(content string) []TeamMemSecretFinding {
	return teampkg.ScanTeamMemContent(content)
}

type teamConfigAdapter struct {
	cfg *Config
}

func (a teamConfigAdapter) Gate(buildCtx contract.BuildCtx) teampkg.GateSnapshot {
	gate := ResolveMemoryGate(buildCtx, a.cfg)
	return teampkg.GateSnapshot{
		AutoEnabled:    gate.AutoEnabled,
		TeamMemEnabled: gate.TeamMemEnabled,
		KairosActive:   gate.KairosActive,
	}
}

func (a teamConfigAdapter) TeamRoot(buildCtx contract.BuildCtx) (string, error) {
	cfg := memoryConfig(a.cfg)
	projectRoot := a.ProjectRoot(buildCtx)
	if projectRoot == "" && strings.TrimSpace(cfg.AutoMemPathOverride) == "" {
		return "", ErrInvalidProjectDir
	}
	root, err := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return "", err
	}
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	return filepath.Join(cleaned, teampkg.RootDirName), nil
}

func (a teamConfigAdapter) ProjectRoot(buildCtx contract.BuildCtx) string {
	if buildCtx.GitRoot != "" {
		return strings.TrimSpace(buildCtx.GitRoot)
	}
	if buildCtx.CWD != "" {
		return strings.TrimSpace(buildCtx.CWD)
	}
	if a.cfg == nil {
		return ""
	}
	return strings.TrimSpace(a.cfg.ProjectRoot)
}

// ==== explicit memory write helpers ====

func buildExplicitMemoryWrite(intent SaveIntent) MemoryWriteRequest {
	content := normalizeHookContent(intent.Content)
	memoryType := intent.Type
	if !memoryType.IsKnown() {
		memoryType = inferMemoryType(content)
	}
	description := buildExplicitMemoryDescription(content)
	return MemoryWriteRequest{
		Name:        buildExplicitMemoryName(memoryType, description),
		Description: description,
		Type:        memoryType,
		Body:        buildExplicitMemoryBody(memoryType, content),
	}
}

func buildExplicitMemoryDescription(content string) string {
	description := truncateRunes(firstNonEmptyLine(content), memoryHookMaxRunes)
	if description == "" {
		description = truncateRunes(content, memoryHookMaxRunes)
	}
	return description
}

func buildExplicitMemoryBody(memoryType MemoryType, content string) string {
	content = strings.TrimSpace(content)
	switch memoryType {
	case MemoryTypeFeedback:
		if hasStructuredMemorySection(content, "why") && hasStructuredMemorySection(content, "how to apply") {
			return content
		}
		return strings.Join([]string{
			content,
			"Why: user explicitly asked to remember this working guidance.",
			"How to apply: follow this guidance when future work touches the same area.",
		}, "\n")
	case MemoryTypeProject:
		if hasStructuredMemorySection(content, "why") && hasStructuredMemorySection(content, "how to apply") {
			return content
		}
		return strings.Join([]string{
			content,
			"Why: user explicitly asked to preserve this project context.",
			"How to apply: use this context when making project recommendations or planning follow-up work.",
		}, "\n")
	default:
		return content
	}
}

func buildExplicitMemoryName(memoryType MemoryType, description string) string {
	prefix := "Saved memory"
	switch memoryType {
	case MemoryTypeUser:
		prefix = "User note"
	case MemoryTypeFeedback:
		prefix = "Feedback rule"
	case MemoryTypeProject:
		prefix = "Project note"
	case MemoryTypeReference:
		prefix = "Reference note"
	}
	if description == "" {
		return prefix
	}
	return truncateRunes(prefix+": "+description, 96)
}

func normalizeHookContent(text string) string {
	lines := make([]string, 0, 4)
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• "))
		line = strings.TrimSpace(strings.TrimLeft(line, ":：-—,，。.!！?？;；"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "that "))
		line = strings.TrimSpace(strings.TrimPrefix(line, "关于 "))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func hasMeaningfulMemoryContent(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}) >= 0
}

// ==== dedup write helpers ====

func mergeAndWriteMemory(store memoryWriteStore, targetPath string, merged MemoryEntry, options WriteOptions) error {
	if err := ValidateMemoryEntryContent(merged); err != nil {
		return err
	}
	root := store.Root()
	raw := formatMemoryEntry(merged)
	return withDiskStoreLock(root, func() error {
		validatedPath, err := ValidateMemoryWritePath(root, targetPath)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(validatedPath, []byte(raw), 0o644); err != nil {
			return err
		}
		return updateIndexAfterMutation(root, options)
	})
}

func overflowMergeAndDelete(store memoryWriteStore, keepPath string, merged MemoryEntry, deletePath string, options WriteOptions) error {
	if err := ValidateMemoryEntryContent(merged); err != nil {
		return err
	}
	root := store.Root()
	raw := formatMemoryEntry(merged)
	return withDiskStoreLock(root, func() error {
		validatedKeep, err := ValidateMemoryWritePath(root, keepPath)
		if err != nil {
			return err
		}
		if err := writeAtomicFile(validatedKeep, []byte(raw), 0o644); err != nil {
			return err
		}
		validatedDel, err := ValidateMemoryWritePath(root, deletePath)
		if err != nil {
			return err
		}
		_ = os.Remove(validatedDel)
		return updateIndexAfterMutation(root, options)
	})
}

func snapshotToMemoryEntry(s dedup.EntrySnapshot) MemoryEntry {
	t := ParseMemoryType(s.Type)
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        s.Name,
			Description: s.Description,
			Type:        &t,
			Lang:        s.Lang,
			Aliases:     s.Aliases,
			SearchKeys:  s.SearchKeys,
			Source:      s.Source,
		},
		Content:  s.Content,
		FilePath: s.Path,
	}
}
