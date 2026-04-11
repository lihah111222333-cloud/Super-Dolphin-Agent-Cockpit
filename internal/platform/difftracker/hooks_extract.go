package difftracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrReplaceRangePatchNotFound = errors.New("difftracker: replace_range patch not found")

type patchResultEnvelope struct {
	Content      []patchTextItem `json:"content"`
	ContentItems []patchTextItem `json:"contentItems"`
	Text         string          `json:"text"`
}

type patchTextItem struct {
	Text string `json:"text"`
}

type replaceRangePayload struct {
	Action      string   `json:"action"`
	Patch       string   `json:"patch,omitempty"`
	EditContext string   `json:"edit_context,omitempty"`
	Replaced    string   `json:"replaced,omitempty"`
	Replacement string   `json:"replacement,omitempty"`
	FilePath    string   `json:"file_path,omitempty"`
	Path        string   `json:"path,omitempty"`
	File        string   `json:"file,omitempty"`
	Files       []string `json:"files,omitempty"`
}

type patchLineKind int

const (
	patchLineMeta patchLineKind = iota
	patchLineOld
	patchLineNew
	patchLineInvalid
)

func ExtractPatchFromReplaceRange(toolResult json.RawMessage) (string, []string, error) {
	texts := toolResultTexts(toolResult)
	if len(texts) == 0 {
		return "", nil, ErrReplaceRangePatchNotFound
	}
	var errs []error
	for _, text := range texts {
		patch, files, err := extractPatchText(text, nil)
		if err == nil {
			return patch, files, nil
		}
		errs = append(errs, err)
	}
	return "", nil, errors.Join(errs...)
}

func ExtractPatch(content, filePath string) (string, error) {
	patch, _, err := extractPatchText(content, []string{filePath})
	return patch, err
}

func toolResultTexts(raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if texts := envelopeTexts(trimmed); len(texts) > 0 {
		return texts
	}
	if quoted := decodedText(trimmed); quoted != "" {
		return []string{quoted}
	}
	return []string{string(trimmed)}
}

func envelopeTexts(raw []byte) []string {
	var envelope patchResultEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	texts := make([]string, 0, len(envelope.Content)+len(envelope.ContentItems)+1)
	texts = appendTextItems(texts, envelope.Content)
	texts = appendTextItems(texts, envelope.ContentItems)
	if text := strings.TrimSpace(envelope.Text); text != "" {
		texts = append(texts, text)
	}
	if len(texts) == 0 {
		return nil
	}
	return texts
}

func appendTextItems(dst []string, items []patchTextItem) []string {
	for _, item := range items {
		if text := strings.TrimSpace(item.Text); text != "" {
			dst = append(dst, text)
		}
	}
	return dst
}

func decodedText(raw []byte) string {
	var quoted string
	if err := json.Unmarshal(raw, &quoted); err != nil {
		return ""
	}
	return strings.TrimSpace(quoted)
}

func extractPatchText(text string, fallbackFiles []string) (string, []string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", nil, ErrReplaceRangePatchNotFound
	}
	if looksLikePatch(trimmed) {
		files := extractPatchFiles(trimmed, fallbackFiles)
		patch := ensureUnifiedHeaders(normalizeNewlines(trimmed), files)
		return patch, files, validatePatchText(patch)
	}
	payload, err := decodeReplaceRangePayload(trimmed)
	if err != nil {
		return "", nil, err
	}
	patch, err := payloadPatch(payload)
	if err != nil {
		return "", nil, err
	}
	files := payloadFiles(payload, fallbackFiles)
	patch = ensureUnifiedHeaders(normalizeNewlines(patch), files)
	return patch, files, validatePatchText(patch)
}

func decodeReplaceRangePayload(text string) (replaceRangePayload, error) {
	var payload replaceRangePayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return replaceRangePayload{}, fmt.Errorf("%w: invalid content text", ErrReplaceRangePatchNotFound)
	}
	if action := strings.TrimSpace(payload.Action); action != "" && action != "replace_range" {
		return replaceRangePayload{}, fmt.Errorf("%w: unsupported action %q", ErrReplaceRangePatchNotFound, action)
	}
	return payload, nil
}

func payloadPatch(payload replaceRangePayload) (string, error) {
	if patch := strings.TrimSpace(payload.Patch); patch != "" {
		return patch, nil
	}
	return buildPatchFromPayload(payload)
}

func buildPatchFromPayload(payload replaceRangePayload) (string, error) {
	oldLines := prefixedPatchLines(payload.Replaced, '-')
	newLines := prefixedPatchLines(payload.Replacement, '+')
	if len(oldLines) == 0 && len(newLines) == 0 {
		return "", ErrReplaceRangePatchNotFound
	}
	lines := make([]string, 0, len(oldLines)+len(newLines)+1)
	if context := strings.TrimSpace(payload.EditContext); context != "" {
		lines = append(lines, "@@ "+context)
	}
	lines = append(lines, oldLines...)
	lines = append(lines, newLines...)
	return strings.Join(lines, "\n"), nil
}

func prefixedPatchLines(text string, prefix byte) []string {
	if text == "" {
		return nil
	}
	lines := splitPlainLines(text)
	prefixed := make([]string, 0, len(lines))
	for _, line := range lines {
		prefixed = append(prefixed, string(prefix)+line)
	}
	return prefixed
}

func splitPlainLines(text string) []string {
	parts := strings.Split(normalizeNewlines(text), "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

func looksLikePatch(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "@@") ||
		strings.HasPrefix(trimmed, "--- ") ||
		strings.HasPrefix(trimmed, "diff --git ") ||
		strings.HasPrefix(trimmed, "-") ||
		strings.HasPrefix(trimmed, "+")
}

func validatePatchText(patch string) error {
	lines := splitPlainLines(patch)
	if len(lines) == 0 {
		return fmt.Errorf("%w: empty patch", ErrReplaceRangePatchNotFound)
	}
	oldSeen := false
	changeSeen := false
	for _, line := range lines {
		kind := classifyPatchLine(line)
		if kind == patchLineInvalid {
			return fmt.Errorf("%w: invalid patch line %q", ErrReplaceRangePatchNotFound, line)
		}
		if kind == patchLineOld {
			oldSeen = true
			changeSeen = true
		}
		if kind == patchLineNew {
			changeSeen = true
		}
	}
	if !oldSeen || !changeSeen {
		return fmt.Errorf("%w: patch must include changed lines", ErrReplaceRangePatchNotFound)
	}
	return nil
}

func classifyPatchLine(line string) patchLineKind {
	switch {
	case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
		return patchLineMeta
	case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		return patchLineMeta
	case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, " "):
		return patchLineMeta
	case line == `\ No newline at end of file`, strings.TrimSpace(line) == "":
		return patchLineMeta
	case strings.HasPrefix(line, "-"):
		return patchLineOld
	case strings.HasPrefix(line, "+"):
		return patchLineNew
	default:
		return patchLineInvalid
	}
}

func payloadFiles(payload replaceRangePayload, fallbackFiles []string) []string {
	files := append([]string{}, payload.Files...)
	for _, candidate := range []string{payload.FilePath, payload.Path, payload.File} {
		if path := normalizeDiffPath(candidate); path != "" {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		files = append(files, fallbackFiles...)
	}
	return uniqueSorted(files)
}

func extractPatchFiles(patch string, fallbackFiles []string) []string {
	files := make([]string, 0, 2)
	lines := splitPlainLines(patch)
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			if path := diffGitPath(line); path != "" {
				files = append(files, path)
			}
		case strings.HasPrefix(line, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			if path := diffPath(lines[i], lines[i+1]); path != "" {
				files = append(files, path)
			}
		}
	}
	if len(files) == 0 {
		files = append(files, fallbackFiles...)
	}
	return uniqueSorted(files)
}

func ensureUnifiedHeaders(patch string, files []string) string {
	trimmed := strings.TrimSpace(patch)
	if trimmed == "" || hasUnifiedHeaders(trimmed) || len(files) != 1 {
		return trimmed
	}
	path := normalizeDiffPath(files[0])
	if path == "" {
		return trimmed
	}
	return strings.Join([]string{"--- a/" + path, "+++ b/" + path, trimmed}, "\n")
}

func hasUnifiedHeaders(patch string) bool {
	return strings.HasPrefix(patch, "diff --git ") || strings.HasPrefix(patch, "--- ")
}

func diffGitPath(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return ""
	}
	return headerPath(fields[3])
}

func normalizeNewlines(text string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
}
