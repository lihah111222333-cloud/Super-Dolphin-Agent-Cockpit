package memory

import (
	"context"
	"errors"
	"strings"
	"time"
)

type uiSharedFileGetParams struct {
	Path string `json:"path"`
}

type uiSharedFileDeleteParams struct {
	Path string `json:"path"`
}

type uiSharedFilePromoteParams struct {
	CWD         string `json:"cwd,omitempty"`
	SharedPath  string `json:"sharedPath"`
	Target      string `json:"target,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Content     string `json:"content,omitempty"`
}

type UISharedFileDetail struct {
	Path      string    `json:"path"`
	Content   string    `json:"content,omitempty"`
	UpdatedBy string    `json:"updatedBy,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

func getUISharedFile(ctx context.Context, deps memoryHandlerDeps, req uiSharedFileGetParams) (UISharedFileDetail, error) {
	if deps.SharedFiles == nil {
		return UISharedFileDetail{}, errors.New("shared file store is not configured")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return UISharedFileDetail{}, publicValidationErr("path is required")
	}
	item, err := deps.SharedFiles.Get(ctx, path)
	if err != nil {
		return UISharedFileDetail{}, err
	}
	return UISharedFileDetail{
		Path:      item.Path,
		Content:   item.Content,
		UpdatedBy: item.UpdatedBy,
		UpdatedAt: item.UpdatedAt,
	}, nil
}

func deleteUISharedFile(ctx context.Context, deps memoryHandlerDeps, req uiSharedFileDeleteParams) (bool, error) {
	if deps.SharedFilesDeleter == nil {
		return false, errors.New("shared file store is not configured for deletion")
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		return false, publicValidationErr("path is required")
	}
	count, err := deps.SharedFilesDeleter.Delete(ctx, path)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func promoteSharedFileToMemory(ctx context.Context, deps memoryHandlerDeps, req uiSharedFilePromoteParams) (UIMemoryEntryDetail, error) {
	if deps.SharedFiles == nil {
		return UIMemoryEntryDetail{}, errors.New("shared file store is not configured")
	}
	file, err := getUISharedFile(ctx, deps, uiSharedFileGetParams{Path: req.SharedPath})
	if err != nil {
		return UIMemoryEntryDetail{}, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = strings.TrimSpace(file.Content)
	}
	return upsertUIMemoryEntry(ctx, deps, uiMemoryEntryUpsertParams{
		CWD:         req.CWD,
		Target:      req.Target,
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		Content:     content,
	})
}
