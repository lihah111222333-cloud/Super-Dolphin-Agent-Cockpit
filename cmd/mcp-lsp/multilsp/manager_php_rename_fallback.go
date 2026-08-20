package multilsp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// buildSemanticPHPRenameEdit turns only LSP references plus a confirmed definition into edits.
// It deliberately rejects a result that cannot prove the declaration, avoiding text-search rename.
func buildSemanticPHPRenameEdit(_ context.Context, uri string, position protocol.Position, newName string, references, definitions []protocol.LocationResult) (*protocol.WorkspaceEdit, error) {
	if !validPHPRenameIdentifier(newName) {
		return nil, fmt.Errorf("PHP rename new name %q is not a valid identifier", newName)
	}
	originPath, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return nil, fmt.Errorf("resolve PHP rename origin: %w", err)
	}
	origin, err := os.ReadFile(originPath)
	if err != nil {
		return nil, fmt.Errorf("read PHP rename origin: %w", err)
	}
	oldName, err := identifierAtPosition(string(origin), position)
	if err != nil {
		return nil, fmt.Errorf("read PHP rename origin identifier: %w", err)
	}
	if !validPHPRenameIdentifier(oldName) {
		return nil, fmt.Errorf("PHP rename origin is not an identifier: %q", oldName)
	}
	if oldName == newName {
		return nil, fmt.Errorf("PHP rename new name is unchanged: %q", oldName)
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("PHP semantic rename requires an LSP declaration")
	}
	if len(references) == 0 {
		return nil, fmt.Errorf("PHP semantic rename requires LSP references")
	}

	definitionLocations := make([]*protocol.Location, 0, len(definitions))
	for _, result := range definitions {
		loc := result.PrimaryLocation()
		if loc == nil {
			continue
		}
		definitionLocations = append(definitionLocations, loc)
	}
	if len(definitionLocations) == 0 {
		return nil, fmt.Errorf("PHP semantic rename returned no usable declaration location")
	}

	edit := &protocol.WorkspaceEdit{Changes: make(map[string][]protocol.TextEdit)}
	seen := make(map[string]struct{}, len(references))
	declarationFound := false
	for _, result := range references {
		loc := result.PrimaryLocation()
		if loc == nil {
			return nil, fmt.Errorf("PHP semantic rename returned a reference without a location")
		}
		key := phpRenameLocationKey(loc)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		for _, definition := range definitionLocations {
			if definition.URI == loc.URI && phpRenameRangesOverlap(definition.Range, loc.Range) {
				declarationFound = true
				break
			}
		}
		path, err := format.AbsolutePathFromURI(loc.URI)
		if err != nil {
			return nil, fmt.Errorf("resolve PHP reference %q: %w", loc.URI, err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read PHP reference %q: %w", path, err)
		}
		got, err := textAtRange(string(content), loc.Range)
		if err != nil {
			return nil, fmt.Errorf("validate PHP reference %q: %w", path, err)
		}
		if got != oldName {
			return nil, fmt.Errorf("PHP reference %q contains %q, want %q", path, got, oldName)
		}
		edit.Changes[loc.URI] = append(edit.Changes[loc.URI], protocol.TextEdit{Range: loc.Range, NewText: newName})
	}
	if !declarationFound {
		return nil, fmt.Errorf("PHP semantic rename references omitted the LSP declaration")
	}
	return edit, nil
}

func validPHPRenameIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range value {
		if index == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func phpRenameLocationKey(loc *protocol.Location) string {
	return fmt.Sprintf("%s:%d:%d:%d:%d", loc.URI, loc.Range.Start.Line, loc.Range.Start.Character, loc.Range.End.Line, loc.Range.End.Character)
}

func phpRenameRangesOverlap(left, right protocol.Range) bool {
	if left.Start.Line > right.End.Line || right.Start.Line > left.End.Line {
		return false
	}
	if left.End.Line == right.Start.Line && left.End.Character <= right.Start.Character {
		return false
	}
	if right.End.Line == left.Start.Line && right.End.Character <= left.Start.Character {
		return false
	}
	return true
}

func identifierAtPosition(content string, position protocol.Position) (string, error) {
	start, end, err := lineCharacterBounds(content, position.Line, position.Character)
	if err != nil {
		return "", err
	}
	for start > 0 && isPHPIdentifierPartAt(content, start-1) {
		_, size := utf8.DecodeLastRuneInString(content[:start])
		start -= size
	}
	for end < len(content) && isPHPIdentifierPartAt(content, end) {
		_, size := utf8.DecodeRuneInString(content[end:])
		end += size
	}
	return content[start:end], nil
}

func textAtRange(content string, rng protocol.Range) (string, error) {
	start, _, err := lineCharacterBounds(content, rng.Start.Line, rng.Start.Character)
	if err != nil {
		return "", err
	}
	_, end, err := lineCharacterBounds(content, rng.End.Line, rng.End.Character)
	if err != nil {
		return "", err
	}
	if start > end {
		return "", fmt.Errorf("range start follows end")
	}
	return content[start:end], nil
}

func lineCharacterBounds(content string, line, character int) (int, int, error) {
	if line < 0 || character < 0 {
		return 0, 0, fmt.Errorf("negative LSP position")
	}
	lines := strings.SplitAfter(content, "\n")
	if line >= len(lines) {
		return 0, 0, fmt.Errorf("line %d is outside document", line)
	}
	lineText := strings.TrimSuffix(lines[line], "\n")
	if strings.HasSuffix(lineText, "\r") {
		lineText = strings.TrimSuffix(lineText, "\r")
	}
	byteOffset := 0
	utf16Units := 0
	for byteOffset < len(lineText) && utf16Units < character {
		r, size := utf8.DecodeRuneInString(lineText[byteOffset:])
		if r == utf8.RuneError && size == 1 {
			return 0, 0, fmt.Errorf("invalid UTF-8 in document")
		}
		units := 1
		if r > 0xffff {
			units = 2
		}
		if utf16Units+units > character {
			return 0, 0, fmt.Errorf("position splits a UTF-16 code point")
		}
		utf16Units += units
		byteOffset += size
	}
	if utf16Units != character {
		return 0, 0, fmt.Errorf("character %d is outside line", character)
	}
	lineStart := 0
	for i := 0; i < line; i++ {
		lineStart += len(lines[i])
	}
	return lineStart + byteOffset, lineStart + byteOffset, nil
}

func isPHPIdentifierPartAt(content string, offset int) bool {
	if offset < 0 || offset >= len(content) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(content[offset:])
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
