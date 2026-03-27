package edit

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxPatchBytes = 256 * 1024
	maxPatchHunks = 64
	maxPatchLines = 8192
)

var (
	ErrEmptyPatch       = errors.New("patch is empty")
	ErrInvalidPatch     = errors.New("invalid patch")
	ErrSequenceNotFound = errors.New("sequence not found")
	ErrAmbiguousMatch   = errors.New("ambiguous match")
)

// Hunk is the parsed replace_range patch contract consumed by the edit engine.
type Hunk struct {
	OldText       string
	NewText       string
	ChangeContext []string
	BeforeContext []string
	AfterContext  []string
}

type patchBodyLine struct {
	kind byte
	text string
}

// Parse accepts either an implicit single hunk or a single explicit "@@ " hunk.
func Parse(patch string) (Hunk, error) {
	lines, err := normalizePatchLines(patch)
	if err != nil {
		return Hunk{}, err
	}
	if len(lines) == 0 {
		return Hunk{}, ErrEmptyPatch
	}
	if isPatchHeader(lines[0]) {
		headerLines, err := splitPatchHeaders(lines)
		if err != nil {
			return Hunk{}, err
		}
		if len(headerLines) != 1 {
			return Hunk{}, fmt.Errorf("%w: single-hunk parser does not accept multiple @@ headers", ErrInvalidPatch)
		}
		return parseHunkBody(headerLines[0])
	}
	for _, line := range lines[1:] {
		if isPatchHeader(line) {
			return Hunk{}, fmt.Errorf("%w: leading implicit hunk is not allowed before a second @@ header", ErrInvalidPatch)
		}
	}
	return parseHunkBody(lines)
}

// ParseMulti accepts a single implicit hunk or multiple explicit "@@ " hunks.
func ParseMulti(patch string) ([]Hunk, error) {
	lines, err := normalizePatchLines(patch)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, ErrEmptyPatch
	}
	if !isPatchHeader(lines[0]) {
		for _, line := range lines[1:] {
			if isPatchHeader(line) {
				return nil, fmt.Errorf("%w: leading implicit hunk is not allowed when multiple @@ blocks exist", ErrInvalidPatch)
			}
		}
		hunk, err := parseHunkBody(lines)
		if err != nil {
			return nil, err
		}
		return []Hunk{hunk}, nil
	}

	blocks, err := splitPatchHeaders(lines)
	if err != nil {
		return nil, err
	}
	if len(blocks) > maxPatchHunks {
		return nil, fmt.Errorf("%w: patch exceeds %d hunks", ErrInvalidPatch, maxPatchHunks)
	}
	hunks := make([]Hunk, 0, len(blocks))
	for _, block := range blocks {
		hunk, err := parseHunkBody(block)
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
	}
	return hunks, nil
}

func normalizePatchLines(patch string) ([]string, error) {
	if patch == "" {
		return nil, ErrEmptyPatch
	}
	if len(patch) > maxPatchBytes {
		return nil, fmt.Errorf("%w: patch exceeds %d bytes", ErrInvalidPatch, maxPatchBytes)
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(patch)
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxPatchLines {
		return nil, fmt.Errorf("%w: patch exceeds %d lines", ErrInvalidPatch, maxPatchLines)
	}
	return lines, nil
}

func splitPatchHeaders(lines []string) ([][]string, error) {
	var blocks [][]string
	var current []string
	for _, line := range lines {
		if isPatchHeader(line) {
			if _, err := parseHeaderLine(line); err != nil {
				return nil, err
			}
			if current != nil {
				blocks = append(blocks, current)
			}
			current = []string{line}
			continue
		}
		if looksLikeMalformedHeader(line) {
			return nil, fmt.Errorf("%w: patch header must start with \"@@ \"", ErrInvalidPatch)
		}
		if current == nil {
			return nil, fmt.Errorf("%w: patch must begin with an @@ header", ErrInvalidPatch)
		}
		current = append(current, line)
	}
	if current != nil {
		blocks = append(blocks, current)
	}
	return blocks, nil
}

func parseHunkBody(lines []string) (Hunk, error) {
	if len(lines) == 0 {
		return Hunk{}, fmt.Errorf("%w: patch hunk body is empty", ErrInvalidPatch)
	}
	start := 0
	if isPatchHeader(lines[0]) {
		if _, err := parseHeaderLine(lines[0]); err != nil {
			return Hunk{}, err
		}
		start = 1
	}
	body := lines[start:]
	if len(body) == 0 {
		return Hunk{}, fmt.Errorf("%w: patch hunk body is empty", ErrInvalidPatch)
	}
	parsed := make([]patchBodyLine, 0, len(body))
	firstChange, lastChange := -1, -1
	for idx, line := range body {
		entry, err := parsePatchBodyLine(line)
		if err != nil {
			return Hunk{}, fmt.Errorf("%w: line %d: %v", ErrInvalidPatch, start+idx+1, err)
		}
		parsed = append(parsed, entry)
		if entry.kind != ' ' {
			if firstChange < 0 {
				firstChange = idx
			}
			lastChange = idx
		}
	}
	if firstChange < 0 {
		return Hunk{}, fmt.Errorf("%w: patch body must contain at least one changed line", ErrInvalidPatch)
	}

	before := takeTexts(parsed[:firstChange])
	after := takeTexts(parsed[lastChange+1:])
	oldLines, newLines, changeContext := foldChangeRegion(parsed[firstChange : lastChange+1])
	if len(oldLines) == 0 {
		return Hunk{}, fmt.Errorf("%w: insertion-only hunks are not supported", ErrInvalidPatch)
	}
	return Hunk{
		OldText:       buildPatchText(oldLines),
		NewText:       buildPatchText(newLines),
		ChangeContext: changeContext,
		BeforeContext: before,
		AfterContext:  after,
	}, nil
}

func parseHeaderLine(line string) (string, error) {
	if !isPatchHeader(line) {
		return "", fmt.Errorf("%w: patch header must start with \"@@ \"", ErrInvalidPatch)
	}
	return strings.TrimPrefix(line, "@@ "), nil
}

func isPatchHeader(line string) bool {
	return strings.HasPrefix(line, "@@ ")
}

func looksLikeMalformedHeader(line string) bool {
	return strings.HasPrefix(line, "@@") && !isPatchHeader(line)
}

func parsePatchBodyLine(line string) (patchBodyLine, error) {
	if line == "" {
		return patchBodyLine{}, errors.New("patch body lines must start with ' ', '-', or '+'")
	}
	if looksLikeMalformedHeader(line) {
		return patchBodyLine{}, errors.New("patch header must start with \"@@ \"")
	}
	prefix := line[0]
	switch prefix {
	case ' ', '-', '+':
		return patchBodyLine{kind: prefix, text: line[1:]}, nil
	default:
		return patchBodyLine{}, errors.New("patch body lines must start with ' ', '-', or '+'")
	}
}

func foldChangeRegion(lines []patchBodyLine) ([]string, []string, []string) {
	oldLines := make([]string, 0, len(lines))
	newLines := make([]string, 0, len(lines))
	changeContext := make([]string, 0, len(lines))
	for _, line := range lines {
		switch line.kind {
		case ' ':
			oldLines = append(oldLines, line.text)
			newLines = append(newLines, line.text)
			changeContext = append(changeContext, line.text)
		case '-':
			oldLines = append(oldLines, line.text)
		case '+':
			newLines = append(newLines, line.text)
		}
	}
	return oldLines, newLines, changeContext
}

func takeTexts(lines []patchBodyLine) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.text)
	}
	return out
}

func buildPatchText(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func splitPatchText(text string) []string {
	if text == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}
