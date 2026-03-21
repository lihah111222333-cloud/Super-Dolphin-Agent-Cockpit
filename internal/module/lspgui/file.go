package lspgui

import (
	"context"
	"errors"
	"os"
	"strings"
)

const maxReadFileBytes = 5 * 1024 * 1024

func (s *service) HandleFile(_ context.Context, p fileParams) (any, error) {
	switch strings.TrimSpace(p.Action) {
	case "read_file":
		return s.readFile(p)
	case "open_file":
		return s.openFile(p.FilePath)
	case "diagnostics":
		if _, err := s.resolvePath(p.FilePath); err != nil {
			return nil, err
		}
		return diagnosticsResult{Diagnostics: []any{}}, nil
	default:
		return nil, errors.New("unsupported lsp/gui_file action")
	}
}

func (s *service) readFile(p fileParams) (fileReadResult, error) {
	path, err := s.resolvePath(p.FilePath)
	if err != nil {
		return fileReadResult{}, err
	}
	info, err := requireExistingFile(path)
	if err != nil {
		return fileReadResult{}, err
	}
	if info.Size() > maxReadFileBytes {
		return fileReadResult{}, errors.New("file_path exceeds read size limit")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fileReadResult{}, err
	}
	content, total := sliceContent(string(raw), p.Offset, defaultLimit(p.Limit, 150))
	return fileReadResult{
		Content:    content,
		FilePath:   path,
		Offset:     max(p.Offset, 0),
		Limit:      defaultLimit(p.Limit, 150),
		TotalLines: total,
	}, nil
}

func (s *service) openFile(filePath string) (fileStatusResult, error) {
	path, err := s.resolvePath(filePath)
	if err != nil {
		return fileStatusResult{}, err
	}
	if _, err := requireExistingFile(path); err != nil {
		return fileStatusResult{}, err
	}
	return fileStatusResult{FilePath: path, Opened: true}, nil
}

func sliceContent(content string, offset, limit int) (string, int) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	total := len(lines)
	start := max(offset, 0)
	if start >= total {
		return "", total
	}
	end := min(start+limit, total)
	return strings.Join(lines[start:end], "\n"), total
}
