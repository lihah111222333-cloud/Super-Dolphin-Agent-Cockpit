package datasource

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func extractDatasourceText(ctx context.Context, sourcePath, ext string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var (
		text string
		err  error
	)
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".txt":
		text, err = extractTextFile(sourcePath)
	case ".pdf":
		text, err = extractPDFText(sourcePath)
	default:
		return "", fmt.Errorf("%w: %s", errUnsupportedFileExtension, ext)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", errDatasourceContentEmpty
	}
	return text, nil
}

func extractTextFile(sourcePath string) (string, error) {
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read text datasource: %w", err)
	}
	return string(content), nil
}
