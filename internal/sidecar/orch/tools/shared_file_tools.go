package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sharedfile"
)

const (
	sharedFileUpdatedBy       = "agent"
	maxSharedFileContentBytes = 10 << 20
)

type sharedFileReadInput struct {
	Path string `json:"path"`
	Pos  string `json:"pos,omitempty"`
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

// HandleSharedFileRead 处理shared文件read。
func HandleSharedFileRead(store sharedfilestore.Store) ToolHandler {
	return makeHandler(store, "shared file store", func(ctx context.Context, in sharedFileReadInput) (sharedFileDTO, error) {
		return readSharedFile(ctx, store, in)
	})
}

// HandleSharedFileWrite 处理shared文件write。
func HandleSharedFileWrite(store sharedfilestore.Store) ToolHandler {
	return makeHandler(store, "shared file store", func(ctx context.Context, in sharedFileWriteInput) (sharedFileDTO, error) {
		return writeSharedFile(ctx, store, in)
	})
}

func sharedFileToolDefinitions(store sharedfilestore.Store) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("shared_file_read", "Read a shared file by path. Shared files are stored in the database and can be accessed by all agents.", ObjectSchema(map[string]Schema{
			"pos":  StringSchema("Flattened shared-file locator, e.g. shared:<path>. Preferred over legacy path."),
			"path": StringSchema("File path (for example 'config/settings.json')."),
		}), HandleSharedFileRead(store)),
		defineTool("shared_file_write", "Write content to a shared file. Creates or overwrites the file at the given path.", ObjectSchema(map[string]Schema{
			"path":    StringSchema("File path (for example 'config/settings.json')."),
			"content": StringSchema("File content to write."),
		}, "path", "content"), HandleSharedFileWrite(store)),
	)
}

func readSharedFile(ctx context.Context, store sharedfilestore.Store, input sharedFileReadInput) (sharedFileDTO, error) {
	if err := requireDependency(store, "shared file store"); err != nil {
		return sharedFileDTO{}, err
	}
	rawPath, err := resolveSharedPathInput(input.Path, input.Pos)
	if err != nil {
		return sharedFileDTO{}, err
	}
	cleaned, err := sharedfilepath.ValidateReadPath(rawPath)
	if err != nil {
		return sharedFileDTO{}, err
	}
	file, err := store.Get(ctx, cleaned)
	file, err = loadOrNotFound(file, err, "file", cleaned)
	if err != nil {
		return sharedFileDTO{}, err
	}
	return sharedFileFromStore(*file), nil
}

// writeSharedFile 写入shared文件。
func writeSharedFile(ctx context.Context, store sharedfilestore.Store, input sharedFileWriteInput) (sharedFileDTO, error) {
	if err := requireDependency(store, "shared file store"); err != nil {
		return sharedFileDTO{}, err
	}
	rawPath, err := requireTrimmed(input.Path, "path")
	if err != nil {
		return sharedFileDTO{}, err
	}
	cleaned, err := sharedfilepath.ValidateAgentWritePath(rawPath)
	if err != nil {
		return sharedFileDTO{}, err
	}
	if len(input.Content) > maxSharedFileContentBytes {
		return sharedFileDTO{}, fmt.Errorf("content exceeds %d byte limit", maxSharedFileContentBytes)
	}
	file, err := store.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      cleaned,
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
