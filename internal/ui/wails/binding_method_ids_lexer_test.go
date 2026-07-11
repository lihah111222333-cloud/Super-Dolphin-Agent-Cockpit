package wails

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseFrontendMethodIDsIgnoresJavaScriptNonCode(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		want    map[string]uint32
		wantErr string
	}{
		{
			name: "block comment",
			source: `/*
const METHOD_IDS = Object.freeze({
  CALL_API: 1,
});
*/`,
			wantErr: "METHOD_IDS declaration",
		},
		{
			name:    "line comment",
			source:  "// const METHOD_IDS = Object.freeze({ CALL_API: 1, });",
			wantErr: "METHOD_IDS declaration",
		},
		{
			name:    "quoted string",
			source:  `const archived = "const METHOD_IDS = Object.freeze({ CALL_API: 1, });";`,
			wantErr: "METHOD_IDS declaration",
		},
		{
			name:    "template literal",
			source:  "const archived = `const METHOD_IDS = Object.freeze({\n  CALL_API: 1,\n});`;",
			wantErr: "METHOD_IDS declaration",
		},
		{
			name: "fake before real",
			source: `/* const METHOD_IDS = Object.freeze({ CALL_API: 99, }); */
export const METHOD_IDS = Object.freeze({
  CALL_API: 1,
});`,
			want: map[string]uint32{"CALL_API": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFrontendMethodIDs(tt.source)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseFrontendMethodIDs() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFrontendMethodIDs() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseFrontendMethodIDs() = %#v, want %#v", got, tt.want)
			}
			for key, wantID := range tt.want {
				if got[key] != wantID {
					t.Errorf("parseFrontendMethodIDs()[%q] = %d, want %d", key, got[key], wantID)
				}
			}
		})
	}
}

func javascriptCodeMask(source string) (string, error) {
	masked := []byte(source)
	for index := 0; index < len(source); {
		end, matched, err := javascriptNonCodeEnd(source, index)
		if err != nil {
			return "", err
		}
		if !matched {
			index++
			continue
		}
		maskJavaScriptRange(masked, index, end)
		index = end
	}
	return string(masked), nil
}

func javascriptNonCodeEnd(source string, start int) (int, bool, error) {
	switch source[start] {
	case '\'', '"', '`':
		end, err := javascriptQuotedEnd(source, start)
		return end, true, err
	case '/':
		if start+1 >= len(source) {
			return start, false, nil
		}
		switch source[start+1] {
		case '/':
			end := strings.IndexByte(source[start+2:], '\n')
			if end < 0 {
				return len(source), true, nil
			}
			return start + 2 + end, true, nil
		case '*':
			end := strings.Index(source[start+2:], "*/")
			if end < 0 {
				return 0, true, fmt.Errorf("unterminated JavaScript block comment at byte %d", start)
			}
			return start + 2 + end + 2, true, nil
		default:
			if javascriptRegexCanStart(source, start) {
				end, err := javascriptRegexEnd(source, start)
				return end, true, err
			}
		}
	}
	return start, false, nil
}

func javascriptQuotedEnd(source string, start int) (int, error) {
	quote := source[start]
	for index := start + 1; index < len(source); index++ {
		if source[index] == '\\' {
			index++
			continue
		}
		if source[index] == quote {
			return index + 1, nil
		}
		if quote != '`' && (source[index] == '\n' || source[index] == '\r') {
			return 0, fmt.Errorf("unterminated JavaScript %q string at byte %d", quote, start)
		}
	}
	return 0, fmt.Errorf("unterminated JavaScript %q string at byte %d", quote, start)
}

func javascriptRegexCanStart(source string, slash int) bool {
	for index := slash - 1; index >= 0; index-- {
		if strings.ContainsRune(" \t\r\n", rune(source[index])) {
			continue
		}
		return strings.ContainsRune("=(:,[!&|?{};+-*%^~<>", rune(source[index]))
	}
	return true
}

func javascriptRegexEnd(source string, start int) (int, error) {
	inClass := false
	for index := start + 1; index < len(source); index++ {
		switch source[index] {
		case '\\':
			index++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				index++
				for index < len(source) && isASCIIJavaScriptLetter(source[index]) {
					index++
				}
				return index, nil
			}
		case '\n', '\r':
			return 0, fmt.Errorf("unterminated JavaScript regex at byte %d", start)
		}
	}
	return 0, fmt.Errorf("unterminated JavaScript regex at byte %d", start)
}

func maskJavaScriptRange(masked []byte, start, end int) {
	for index := start; index < end; index++ {
		if masked[index] != '\n' && masked[index] != '\r' {
			masked[index] = ' '
		}
	}
}

func frontendMethodIDDeclarationStarts(code string) []int {
	var starts []int
	for offset := 0; offset < len(code); {
		relative := strings.Index(code[offset:], frontendMethodIDsDeclaration)
		if relative < 0 {
			break
		}
		start := offset + relative
		if start == 0 || !isJavaScriptIdentifierByte(code[start-1]) {
			starts = append(starts, start)
		}
		offset = start + len(frontendMethodIDsDeclaration)
	}
	return starts
}

func isJavaScriptIdentifierByte(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func isASCIIJavaScriptLetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
