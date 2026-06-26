package skill

import (
	"path/filepath"
	"strings"
)

// resolutionPreviewPaths 根据动作计算 preview 的源路径、目标路径和 hash。
// 覆盖类动作会交换 source/target；新建 personal skill 会落到 imported 个人根。
func resolutionPreviewPaths(item skillResolutionItem, entry skillResolutionProviderEntry, p skillResolutionPreviewParams, superHome string) skillResolutionPreviewItem {
	preview := skillResolutionPreviewItem{Action: p.Action, Provider: entry.Provider, SourceProvider: entry.Provider, SourcePathID: entry.SourcePathID}
	switch p.Action {
	case ResolutionCanonicalOverwrite, ResolutionPersonalOverwrite:
		preview.SourcePath, preview.TargetPath = entry.TargetPath, entry.SourcePath
		preview.SourceHash, preview.TargetHash = entry.TargetHash, entry.SourceHash
	case ResolutionUseProjectSharedSkill:
		preview.SourcePath, preview.TargetPath = entry.TargetPath, entry.SourcePath
		preview.SourceHash, preview.TargetHash = entry.TargetHash, entry.SourceHash
	case ResolutionUseExternalProviderSkill:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, entry.TargetPath
		preview.SourceHash, preview.TargetHash = entry.SourceHash, entry.TargetHash
	case ResolutionKeepExternalProviderSkill:
		preview.SourcePath = entry.SourcePath
		preview.TargetPath = filepath.ToSlash(filepath.Join(defaultProjectSkillsRoot(projectRootForCWD(p.CWD, "")), projectSkillPolicyFile))
		preview.SourceHash, preview.TargetHash = entry.SourceHash, ""
	case ResolutionConfirmDeleteDriftedMirror:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, entry.SourcePath
		preview.SourceHash, preview.TargetHash = entry.SourceHash, entry.SourceHash
		preview.ConfirmDeleteMirrorHash = entry.SourceHash
	case ResolutionReplaceProviderRootSymlink:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, entry.TargetPath
		preview.SourceHash, preview.TargetHash = entry.SourceHash, ""
	case ResolutionImportPersonal:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, filepath.ToSlash(filepath.Join(superHome, "skills", "personal", personalSkillTypeImported, item.Name))
		preview.SourceHash, preview.TargetHash = entry.SourceHash, ""
	case ResolutionSaveAsNewPersonal:
		return saveAsNewPersonalPreviewPaths(item, entry, p, superHome, preview)
	default:
		preview.SourcePath, preview.TargetPath = entry.SourcePath, resolutionPreviewTargetPath(entry, p)
		preview.SourceHash, preview.TargetHash = entry.SourceHash, entry.TargetHash
	}
	return preview
}

func saveAsNewPersonalPreviewPaths(item skillResolutionItem, entry skillResolutionProviderEntry, p skillResolutionPreviewParams, superHome string, preview skillResolutionPreviewItem) skillResolutionPreviewItem {
	if item.Kind != skillConflictExternalPersonalProjectSameName {
		preview.SourcePath, preview.TargetPath = entry.SourcePath, resolutionPreviewTargetPath(entry, p)
		preview.SourceHash, preview.TargetHash = entry.SourceHash, entry.TargetHash
		return preview
	}
	preview.SourcePath = entry.SourcePath
	preview.TargetPath = resolutionPreviewPersonalImportedTargetPath(superHome, item.Name, p.NewName)
	preview.SourceHash, preview.TargetHash = entry.SourceHash, ""
	return preview
}

func resolutionPreviewPersonalImportedTargetPath(superHome, name, newName string) string {
	targetName := strings.TrimSpace(newName)
	if targetName == "" {
		targetName = strings.TrimSpace(name)
	}
	return filepath.ToSlash(filepath.Join(superHome, "skills", "personal", personalSkillTypeImported, targetName))
}
