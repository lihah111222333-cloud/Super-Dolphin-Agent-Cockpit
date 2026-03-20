package codexapp

import (
	"path/filepath"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func mapTurnInput(item dto.InputItem) turnInputItem {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "", "text":
		return textTurnInput(item)
	case "image":
		return imageTurnInput(item)
	case "local_image", "localimage":
		return localImageTurnInput(item)
	case "file", "mention":
		return mentionTurnInput(item)
	default:
		return fallbackTurnInput(item)
	}
}

func textTurnInput(item dto.InputItem) turnInputItem {
	content := strings.TrimSpace(item.Content)
	return turnInputItem{Type: "text", Text: content, Content: content}
}

func imageTurnInput(item dto.InputItem) turnInputItem {
	if url := strings.TrimSpace(item.URL); url != "" {
		return turnInputItem{Type: "image", URL: url}
	}
	path := resolvedInputPath(item)
	if isRemoteTurnInput(path) {
		return turnInputItem{Type: "image", URL: path}
	}
	return turnInputItem{Type: "localImage", Path: path}
}

func localImageTurnInput(item dto.InputItem) turnInputItem {
	return turnInputItem{Type: "localImage", Path: resolvedInputPath(item)}
}

func mentionTurnInput(item dto.InputItem) turnInputItem {
	path := resolvedInputPath(item)
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	return turnInputItem{Type: "mention", Path: path, Name: name}
}

func fallbackTurnInput(item dto.InputItem) turnInputItem {
	content := strings.TrimSpace(item.Content)
	return turnInputItem{Type: item.Type, Content: content, Text: content}
}

func resolvedInputPath(item dto.InputItem) string {
	if path := strings.TrimSpace(item.Path); path != "" {
		return path
	}
	return strings.TrimSpace(item.Content)
}

func isRemoteTurnInput(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
