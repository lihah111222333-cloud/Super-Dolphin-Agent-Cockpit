package skill

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	ResolutionViewDiff                   = "view_diff"
	ResolutionViewUnmanaged              = "view_unmanaged"
	ResolutionImportPersonal             = "import_to_personal_imported"
	ResolutionImportProject              = "import_to_project"
	ResolutionTakeoverProvider           = "takeover_provider_skill"
	ResolutionSaveAsNewSkill             = "save_as_new_skill"
	ResolutionSaveAsNewPersonal          = "save_as_new_personal_skill"
	ResolutionSyncBackCanonical          = "sync_back_to_canonical"
	ResolutionSyncBackPersonal           = "sync_back_to_personal"
	ResolutionCanonicalOverwrite         = "canonical_overwrite_mirror"
	ResolutionPersonalOverwrite          = "personal_overwrite_mirror"
	ResolutionRenamePersonal             = "rename_personal"
	ResolutionDisablePersonalForProject  = "disable_personal_for_project"
	ResolutionRenamePersonalType         = "rename_personal_type"
	ResolutionMergeManually              = "merge_manually"
	ResolutionKeepSelected               = "keep_selected"
	ResolutionConfirmDeleteDriftedMirror = "confirm_delete_drifted_mirror"
)

type skillResolutionListParams struct {
	CWD             string `json:"cwd"`
	IncludeResolved bool   `json:"include_resolved,omitempty"`
}

type skillResolutionListResult struct {
	Items []skillResolutionItem `json:"items"`
}

type skillResolutionItem struct {
	ConflictID       string                         `json:"conflict_id"`
	Kind             string                         `json:"kind"`
	Scope            string                         `json:"scope,omitempty"`
	PersonalType     string                         `json:"personal_type,omitempty"`
	Name             string                         `json:"name"`
	AvailableActions []string                       `json:"available_actions"`
	ProviderEntries  []skillResolutionProviderEntry `json:"provider_entries,omitempty"`
	Sources          []skillResolutionSource        `json:"sources,omitempty"`
}

type skillResolutionProviderEntry struct {
	Provider     string `json:"provider"`
	SourcePath   string `json:"source_path,omitempty"`
	TargetPath   string `json:"target_path,omitempty"`
	SourceHash   string `json:"source_hash,omitempty"`
	TargetHash   string `json:"target_hash,omitempty"`
	TargetID     string `json:"target_id,omitempty"`
	SourcePathID string `json:"source_path_id,omitempty"`
}

type skillResolutionSource struct {
	Scope         string `json:"scope"`
	PersonalType  string `json:"personal_type,omitempty"`
	CanonicalID   string `json:"canonical_id"`
	ContentHash   string `json:"content_hash,omitempty"`
	CanonicalHash string `json:"canonical_hash,omitempty"`
	Path          string `json:"path,omitempty"`
	SkillFile     string `json:"skill_file,omitempty"`
}

type skillResolutionPreviewParams struct {
	CWD                 string   `json:"cwd"`
	Scope               string   `json:"scope,omitempty"`
	PersonalType        string   `json:"personal_type,omitempty"`
	Provider            string   `json:"provider,omitempty"`
	Providers           []string `json:"providers,omitempty"`
	SourceProvider      string   `json:"source_provider,omitempty"`
	SourcePathID        string   `json:"source_path_id,omitempty"`
	Name                string   `json:"name"`
	ConflictID          string   `json:"conflict_id"`
	Action              string   `json:"action"`
	NewName             string   `json:"new_name,omitempty"`
	KeepSourceID        string   `json:"keep_source_id,omitempty"`
	MergeContentHash    string   `json:"merge_content_hash,omitempty"`
	DisablePolicyTarget string   `json:"disable_policy_target,omitempty"`
	IncludeDiff         bool     `json:"include_diff,omitempty"`
}

type skillResolutionPreviewResult struct {
	ConflictID string                       `json:"conflict_id"`
	Kind       string                       `json:"kind"`
	Items      []skillResolutionPreviewItem `json:"items"`
}

type skillResolutionPreviewItem struct {
	Action                  string `json:"action"`
	Provider                string `json:"provider,omitempty"`
	PreviewID               string `json:"preview_id,omitempty"`
	SourceProvider          string `json:"source_provider,omitempty"`
	SourcePathID            string `json:"source_path_id,omitempty"`
	SourcePath              string `json:"source_path,omitempty"`
	TargetPath              string `json:"target_path,omitempty"`
	SourceHash              string `json:"source_hash,omitempty"`
	TargetHash              string `json:"target_hash,omitempty"`
	PreviewHash             string `json:"preview_hash,omitempty"`
	BackupPath              string `json:"backup_path,omitempty"`
	ConfirmDeleteMirrorHash string `json:"confirm_delete_mirror_hash,omitempty"`
	Diff                    string `json:"diff,omitempty"`
}

type skillResolutionStoredPreview struct {
	Item       skillResolutionPreviewItem
	ConflictID string
	Action     string
	ExpiresAt  time.Time
}

type execParams struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"-"`
}

type execParamsWire struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	Argv    []string          `json:"argv,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (p *execParams) UnmarshalJSON(data []byte) error {
	var wire execParamsWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	next := execParams{
		Command: wire.Command,
		Args:    append([]string(nil), wire.Args...),
		CWD:     wire.CWD,
		Env:     cloneExecEnv(wire.Env),
	}
	if strings.TrimSpace(next.Command) == "" && len(wire.Argv) > 0 {
		next.Command, next.Args = splitLegacyArgv(wire.Argv)
	}
	*p = next
	return nil
}

func splitLegacyArgv(argv []string) (string, []string) {
	if len(argv) == 0 {
		return "", nil
	}
	return argv[0], append([]string(nil), argv[1:]...)
}

func cloneExecEnv(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
