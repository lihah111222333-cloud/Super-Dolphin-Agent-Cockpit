package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentDetectionAndUpwardExpansion_SingleLine(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		startLine int
		wantStart int
	}{
		{
			name: "Go single line comments with blank line",
			content: `package main

// Some unrelated config
// setup goes here

// This is the target function doc
// It does something cool.
func MyCoolFunc() {
}`,
			startLine: 8,
			wantStart: 6,
		},
		{
			name: "Python single line comments (#)",
			content: `
# Unrelated comment
# another one

# Target function description
# returns nothing
def my_func():
    pass`,
			startLine: 7,
			wantStart: 5,
		},
		{
			name: "SQL single line comments (--)",
			content: `
-- Some database migrations
-- setup tables

-- Get user info
-- by email
SELECT * FROM users;`,
			startLine: 7,
			wantStart: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(strings.ReplaceAll(tt.content, "\r\n", "\n"), "\n")
			got := expandStartToIncludeComments(lines, tt.startLine)
			if got != tt.wantStart {
				t.Errorf("expandStartToIncludeComments() = %d, want %d", got, tt.wantStart)
			}
		})
	}
}

func TestCommentDetectionAndUpwardExpansion_Block(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		startLine int
		wantStart int
	}{
		{
			name: "Python docstring triple quotes",
			content: `def unrelated():
    pass

"""
This is a python docstring.
It explains the logic.
"""
def my_func():
    pass`,
			startLine: 8,
			wantStart: 4,
		},
		{
			name: "Python docstring single-quote triple blocks (''')",
			content: `
'''
This is a single-quoted
triple-block docstring.
'''
def my_func():
    pass`,
			startLine: 6,
			wantStart: 2,
		},
		{
			name: "Go block comment /* ... */",
			content: `package main

/*
This is a multi-line
block comment.
*/
func anotherFunc() {
}`,
			startLine: 7,
			wantStart: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(strings.ReplaceAll(tt.content, "\r\n", "\n"), "\n")
			got := expandStartToIncludeComments(lines, tt.startLine)
			if got != tt.wantStart {
				t.Errorf("expandStartToIncludeComments() = %d, want %d", got, tt.wantStart)
			}
		})
	}
}

func TestCommentDetectionAndUpwardExpansion_BlockInline(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		startLine int
		wantStart int
	}{
		{
			name: "Inline block comment /* ... */",
			content: `
/* Inline comment */
func helper() {}`,
			startLine: 3,
			wantStart: 2,
		},
		{
			name: "Inline block comment triple quotes",
			content: `
"""Inline docstring"""
def helper():
    pass`,
			startLine: 3,
			wantStart: 2,
		},
		{
			name: "Trailing inline block comment on code line (no leakage)",
			content: `func unrelated() {}
func target() {} /* trailing comment */
func main() {
}`,
			startLine: 3,
			wantStart: 3,
		},
		{
			name: "Block comment starting on code line (no boundary leakage)",
			content: `func foo() {} /* comment start
comment body
*/
func bar() {}`,
			startLine: 4,
			wantStart: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(strings.ReplaceAll(tt.content, "\r\n", "\n"), "\n")
			got := expandStartToIncludeComments(lines, tt.startLine)
			if got != tt.wantStart {
				t.Errorf("expandStartToIncludeComments() = %d, want %d", got, tt.wantStart)
			}
		})
	}
}

func TestCommentDetectionAndUpwardExpansion_Safeguards(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		startLine int
		wantStart int
	}{
		{
			name: "License header filtering",
			content: `// Copyright (C) 2026 The Awesome Team.
// All rights reserved.
// Licensed under Apache 2.0.

// Actual docstring for helper
// explaining what it does
func helper() {
}`,
			startLine: 7,
			wantStart: 5,
		},
		{
			name: "License header filtering WITHOUT blank line",
			content: `// Copyright (C) 2026 The Awesome Team.
// All rights reserved.
// Licensed under Apache 2.0.
// Actual docstring for helper
// explaining what it does
func helper() {
}`,
			startLine: 6,
			wantStart: 4,
		},
		{
			name:      "Max expansion limit safety",
			content:   strings.Repeat("// comment line\n", 30) + "func limited() {}",
			startLine: 31,
			wantStart: 11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(strings.ReplaceAll(tt.content, "\r\n", "\n"), "\n")
			got := expandStartToIncludeComments(lines, tt.startLine)
			if got != tt.wantStart {
				t.Errorf("expandStartToIncludeComments() = %d, want %d", got, tt.wantStart)
			}
		})
	}
}

const sharedTestGoContent = `package main

import "fmt"

// Increment increments an integer by 1.
// It returns the updated value.
func Increment(x int) int {
	return x + 1
}

// Decrement decrements an integer by 1.
func Decrement(x int) int {
	return x - 1
}`

func setupTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	targetPath := filepath.Join(root, name)
	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return targetPath
}

func TestE2EFileToolSmartCommentExpansion_SingleRead(t *testing.T) {
	root := t.TempDir()
	targetPath := setupTestFile(t, root, "main.go", sharedTestGoContent)

	res, err := callFileTool(t, root, fileToolInput{
		Action: "read_file",
		Pos:    targetPath + ":7",
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	content, ok := res.(string)
	if !ok {
		t.Fatalf("unexpected response type: %T", res)
	}

	if !strings.Contains(content, "line=5") || !strings.Contains(content, "Increment increments an integer by 1.") {
		t.Errorf("expected output to contain line 5 comment, got:\n%s", content)
	}
	if !strings.Contains(content, "line=8") || !strings.Contains(content, "return x + 1") {
		t.Errorf("expected output to contain line 8 (tail mitigation), got:\n%s", content)
	}
}

func TestE2EFileToolSmartCommentExpansion_BatchRead(t *testing.T) {
	root := t.TempDir()
	targetPath := setupTestFile(t, root, "main.go", sharedTestGoContent)

	resBatch, err := callFileTool(t, root, fileToolInput{
		Action:    "read_file",
		FilePaths: []string{targetPath},
	})
	if err != nil {
		t.Fatalf("batch read failed: %v", err)
	}

	batchPayload, ok := resBatch.(batchReadResponse)
	if !ok {
		t.Fatalf("unexpected batch result type: %T", resBatch)
	}

	if len(batchPayload.Data) != 1 {
		t.Fatalf("expected 1 file in batch payload, got %d", len(batchPayload.Data))
	}

	batchContent := batchPayload.Data[0].Content
	if !strings.Contains(batchContent, "line=1") || !strings.Contains(batchContent, "package main") {
		t.Errorf("batch read should start from line 1, got:\n%s", batchContent)
	}
	if !strings.Contains(batchContent, "line=5") || !strings.Contains(batchContent, "Increment increments an integer by 1.") {
		t.Errorf("batch read should contain comments, got:\n%s", batchContent)
	}
	if !strings.Contains(batchContent, "line=7") || !strings.Contains(batchContent, "func Increment(x int) int {") {
		t.Errorf("batch read should contain function declaration, got:\n%s", batchContent)
	}
}

func TestE2EFileToolSmartCommentExpansion_LimitZero(t *testing.T) {
	root := t.TempDir()
	fileContent := `package main

// First comment
// Second comment
func TargetFunc() {
	// body
}`
	targetPath := setupTestFile(t, root, "main.go", fileContent)

	res, err := callFileTool(t, root, fileToolInput{
		Action: "read_file",
		Pos:    targetPath + ":5",
		Limit:  0,
	})
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	content, ok := res.(string)
	if !ok {
		t.Fatalf("unexpected response type: %T", res)
	}

	if !strings.Contains(content, "line=3") || !strings.Contains(content, "// First comment") {
		t.Errorf("expected output to contain line 3, got:\n%s", content)
	}
	if !strings.Contains(content, "line=6") || !strings.Contains(content, "// body") {
		t.Errorf("expected output to contain line 6, got:\n%s", content)
	}
}

func TestE2EFileToolSmartCommentExpansion_NegativeLimit(t *testing.T) {
	root := t.TempDir()
	targetPath := setupTestFile(t, root, "main.go", sharedTestGoContent)

	res, err := callFileTool(t, root, fileToolInput{
		Action: "read_file",
		Pos:    targetPath + ":7",
		Limit:  -5,
	})
	if err != nil {
		t.Fatalf("failed to read file with negative limit: %v", err)
	}

	content, ok := res.(string)
	if !ok {
		t.Fatalf("unexpected response type: %T", res)
	}

	if !strings.Contains(content, "line=5") || !strings.Contains(content, "Increment increments an integer by 1.") {
		t.Errorf("expected negative limit fallback to default limit and expand comments, got:\n%s", content)
	}
}
