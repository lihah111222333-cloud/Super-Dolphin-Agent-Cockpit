package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

const (
	sharedFileUpdatedBy       = "agent"
	maxSharedFileContentBytes = 10 << 20
)

type sharedFileReadInput struct {
	Path string `json:"path"`
}

type sharedFileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type sharedFileDTO struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func HandleSharedFileRead(store sharedfilestore.Store) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in sharedFileReadInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		in.Path = normalizePath(in.Path)
		return readSharedFile(ctx, store, in)
	}
}

func HandleSharedFileWrite(store sharedfilestore.Store) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in sharedFileWriteInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		in.Path = normalizePath(in.Path)
		return writeSharedFile(ctx, store, in)
	}
}

func sharedFileToolDefinitions(store sharedfilestore.Store) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "shared_file_read",
			Description: "Read a shared file by path. Shared files are stored in the database and can be accessed by all agents.",
			InputSchema: ObjectSchema(map[string]Schema{
				"path": StringSchema("File path (for example 'config/settings.json')."),
			}, "path"),
			Handler: HandleSharedFileRead(store),
		},
		{
			Name:        "shared_file_write",
			Description: "Write content to a shared file. Creates or overwrites the file at the given path.",
			InputSchema: ObjectSchema(map[string]Schema{
				"path":    StringSchema("File path (for example 'config/settings.json')."),
				"content": StringSchema("File content to write."),
			}, "path", "content"),
			Handler: HandleSharedFileWrite(store),
		},
	}
}

func readSharedFile(ctx context.Context, store sharedfilestore.Store, input sharedFileReadInput) (sharedFileDTO, error) {
	if store == nil {
		return sharedFileDTO{}, errors.New("shared file store is not configured")
	}
	path, err := requireTrimmed(input.Path, "path")
	if err != nil {
		return sharedFileDTO{}, err
	}
	file, err := store.Get(ctx, path)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return sharedFileDTO{}, fmt.Errorf("file %s not found", path)
		}
		return sharedFileDTO{}, err
	}
	if file == nil {
		return sharedFileDTO{}, fmt.Errorf("file %s not found", path)
	}
	return sharedFileFromStore(*file), nil
}

func writeSharedFile(ctx context.Context, store sharedfilestore.Store, input sharedFileWriteInput) (sharedFileDTO, error) {
	if store == nil {
		return sharedFileDTO{}, errors.New("shared file store is not configured")
	}
	path, err := requireTrimmed(input.Path, "path")
	if err != nil {
		return sharedFileDTO{}, err
	}
	if len(input.Content) > maxSharedFileContentBytes {
		return sharedFileDTO{}, fmt.Errorf("content exceeds %d byte limit", maxSharedFileContentBytes)
	}
	file, err := store.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      path,
		Content:   input.Content,
		UpdatedBy: sharedFileUpdatedBy,
	})
	if err != nil {
		return sharedFileDTO{}, err
	}
	if file == nil {
		return sharedFileDTO{}, errors.New("shared file write returned no result")
	}
	return sharedFileFromStore(*file), nil
}

func sharedFileFromStore(file sharedfilestore.SharedFile) sharedFileDTO {
	return sharedFileDTO{
		Path:      file.Path,
		Content:   file.Content,
		UpdatedBy: file.UpdatedBy,
		CreatedAt: file.CreatedAt,
		UpdatedAt: file.UpdatedAt,
	}
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	return strings.TrimPrefix(p, "/")
}
