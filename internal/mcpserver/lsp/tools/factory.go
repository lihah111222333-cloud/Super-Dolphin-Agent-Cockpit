package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	lspexec "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/exec"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type ToolHandler = middleware.Handler

type Handler = ToolHandler

type decodeMode int

type actionHandler[T any] func(context.Context, T) (any, error)

const (
	decodeRaw decodeMode = iota
	decodeLenient
	decodeStrict
)

func newManagerTool[T any](
	name string,
	tier time.Duration,
	registry lspmanager.Registry,
	mode decodeMode,
	dispatch func(context.Context, lspmanager.Registry, T) (any, error),
) ToolHandler {
	if registry == nil {
		return missingManagerHandler()
	}
	return wrapToolHandler(name, tier, func(ctx context.Context, params json.RawMessage) (any, error) {
		req, err := decodeToolParams[T](params, mode)
		if err != nil {
			return nil, err
		}
		return dispatch(ctx, registry, req)
	})
}

func newSandboxTool(
	name string,
	sandbox SandboxRunner,
	handler func(context.Context, SandboxRunner, json.RawMessage) (any, error),
) middleware.Handler {
	return wrapToolHandler(name, middleware.TierExec, func(ctx context.Context, params json.RawMessage) (any, error) {
		if sandbox == nil {
			return nil, fmt.Errorf("%s sandbox is nil", name)
		}
		return handler(ctx, sandbox, params)
	})
}

func decodeToolParams[T any](raw json.RawMessage, mode decodeMode) (T, error) {
	var value T
	var err error
	switch mode {
	case decodeLenient:
		err = decodeLenientToolParams(raw, &value)
	case decodeStrict:
		err = decodeStrictToolParams(raw, &value)
	default:
		err = decodeRawToolParams(raw, &value)
	}
	if err != nil {
		return value, err
	}
	return value, nil
}

func decodeRawToolParams[T any](raw json.RawMessage, value *T) error {
	return unmarshalToolParams(raw, value)
}

func decodeLenientToolParams[T any](raw json.RawMessage, value *T) error {
	return unmarshalToolParams(normalizeOptionalToolParams(raw), value)
}

func decodeStrictToolParams[T any](raw json.RawMessage, value *T) error {
	decoder := json.NewDecoder(bytes.NewReader(normalizeOptionalToolParams(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("decode params: unexpected trailing JSON payload")
	}
	return nil
}

func normalizeOptionalToolParams(raw json.RawMessage) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []byte("{}")
	}
	return trimmed
}

func unmarshalToolParams[T any](raw []byte, value *T) error {
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	return nil
}

func dispatchToolAction[T any](
	ctx context.Context,
	label string,
	action string,
	req T,
	handlers map[string]actionHandler[T],
) (any, error) {
	handler, ok := handlers[normalizeAction(action)]
	if !ok {
		return nil, fmt.Errorf("unsupported %s action %q", label, action)
	}
	return handler(ctx, req)
}

func missingDependencyHandler(message string) ToolHandler {
	return func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New(message)
	}
}

func missingManagerHandler() ToolHandler {
	return missingDependencyHandler("lsp manager is required")
}

func requireFilePath(raw string) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		return "", errors.New("file_path is required")
	}
	return filePath, nil
}

func requirePosition(line, column int) (protocol.Position, error) {
	if line <= 0 {
		return protocol.Position{}, errors.New("line must be >= 1")
	}
	if column <= 0 {
		return protocol.Position{}, errors.New("column must be >= 1")
	}
	return protocol.Position{
		Line:      line - 1,
		Character: column - 1,
	}, nil
}

func resolveFilePositionRequest(params filePositionParams) (string, protocol.Position, error) {
	filePath, err := resolveFilePath(params.FilePath)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := requirePosition(params.Line, params.Column)
	if err != nil {
		return "", protocol.Position{}, err
	}
	return filePath, position, nil
}

func normalizeAction(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func renderListResult[T any](items []T, limit int, emptyMessage string, render func([]T, int) any) (any, error) {
	total := len(items)
	items = limitSlice(items, limit)
	if len(items) == 0 {
		return emptyMessage, nil
	}
	return render(items, total), nil
}

func renderByVerbosity[T any](
	items []T,
	total int,
	verbosity string,
	renderFull func([]T) any,
	renderCompact func([]T, int) any,
) any {
	if format.NormalizeVerbosity(verbosity) == format.VerbosityFull {
		return renderFull(items)
	}
	return renderCompact(items, total)
}

func executeSandbox(
	ctx context.Context,
	sandbox SandboxRunner,
	request lspexec.Request,
	language string,
	mode string,
) (any, error) {
	result, err := sandbox.Run(ctx, request)
	if err != nil {
		return CodeRunFailure{Error: err.Error(), ExitCode: -1}, nil
	}
	return CodeRunResult{
		Success:   result.ExitCode == 0,
		Output:    result.Output,
		ExitCode:  result.ExitCode,
		Duration:  result.Duration,
		Language:  language,
		Mode:      mode,
		Truncated: result.Truncated,
	}, nil
}

func wrapToolHandler(toolName string, tier time.Duration, handler middleware.Handler) middleware.Handler {
	log := pkglogger.Get()
	return middleware.Chain(
		handler,
		middleware.Recovery(log, toolName),
		middleware.Logging(log, toolName),
		middleware.Timeout(tier),
	)
}
