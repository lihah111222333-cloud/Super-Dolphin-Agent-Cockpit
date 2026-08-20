package multilsp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func (m *manager) formatPythonDocument(ctx context.Context, ref documentRef) ([]protocol.TextEdit, error) {
	productRoot, err := m.resolvePythonFormatterProductRoot(ctx)
	if err != nil {
		return nil, err
	}
	formatter, err := installer.ResolveOrInstallRuffFormatter(ctx, productRoot)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(ref.absPath)
	if err != nil {
		return nil, fmt.Errorf("read Python document for Ruff formatting: %w", err)
	}
	command := exec.CommandContext(ctx, formatter, "format", "--stdin-filename", ref.absPath, "-")
	command.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	formatted, err := command.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, fmt.Errorf("run product-owned Ruff formatter: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("run product-owned Ruff formatter: %w", err)
	}
	if bytes.Equal(content, formatted) {
		return []protocol.TextEdit{}, nil
	}
	return []protocol.TextEdit{{
		Range:   protocol.Range{Start: protocol.Position{}, End: pythonEndPosition(string(content))},
		NewText: string(formatted),
	}}, nil
}

func pythonEndPosition(content string) protocol.Position {
	lines := strings.Split(content, "\n")
	last := strings.TrimSuffix(lines[len(lines)-1], "\r")
	return protocol.Position{Line: len(lines) - 1, Character: len(utf16.Encode([]rune(last)))}
}
