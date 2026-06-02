package observability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxTailDirectoryEntries = 1024
	maxTailCandidateFiles   = 128
)

type JSONLTailReader struct {
	Dir      string
	MaxBytes int64
}

type TailReadResult struct {
	Events       []TraceEvent
	DecodeErrors []TailDecodeError
}

type TailDecodeError struct {
	File     string         `json:"file"`
	Line     int            `json:"line"`
	Trailing bool           `json:"trailing"`
	Error    string         `json:"error"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func NewTailReader(dir string, cfg Config) JSONLTailReader {
	return JSONLTailReader{Dir: dir, MaxBytes: int64(cfg.JSONLQueryTailMB) * bytesPerMB}
}

func (r JSONLTailReader) QueryTraceEvents(ctx context.Context, query Query) (QueryResult, error) {
	result, err := r.Read(ctx)
	if err != nil {
		return QueryResult{Source: QuerySourceJSONLTail}, err
	}
	events := filterTailEvents(result.Events, query)
	events, truncated := limitTailEvents(events, query.Limit)
	return QueryResult{Source: QuerySourceJSONLTail, Events: events, Truncated: truncated}, nil
}

func (r JSONLTailReader) Read(ctx context.Context) (TailReadResult, error) {
	if err := r.validate(ctx); err != nil {
		return TailReadResult{}, err
	}
	files, err := listTraceJSONLFiles(r.Dir)
	if err != nil {
		return TailReadResult{}, err
	}
	return readSelectedTailFiles(ctx, selectTailFiles(files, r.MaxBytes))
}

func (r JSONLTailReader) validate(ctx context.Context) error {
	if ctx == nil {
		return errors.New("observability trace tail context is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if strings.TrimSpace(r.Dir) == "" {
		return errors.New("observability trace tail directory is empty")
	}
	if r.MaxBytes <= 0 {
		return errors.New("observability trace tail max bytes must be positive")
	}
	return nil
}

func selectTailFiles(files []traceTailFile, maxBytes int64) []traceTailFile {
	remaining := maxBytes
	selected := make([]traceTailFile, 0, len(files))
	for i := len(files) - 1; i >= 0 && remaining > 0; i-- {
		file := files[i]
		readBytes := min(file.size, remaining)
		selected = append(selected, traceTailFile{path: file.path, size: file.size, readBytes: readBytes})
		remaining -= readBytes
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].path < selected[j].path })
	return selected
}

func readSelectedTailFiles(ctx context.Context, files []traceTailFile) (TailReadResult, error) {
	var result TailReadResult
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := readTailFile(file, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func filterTailEvents(events []TraceEvent, query Query) []TraceEvent {
	out := make([]TraceEvent, 0, len(events))
	for _, event := range events {
		if matchesTailQuery(event, query) {
			out = append(out, event)
		}
	}
	return out
}

func matchesTailQuery(event TraceEvent, query Query) bool {
	return matchesTraceID(event, query) && matchesThreadID(event, query) && matchesSlow(event, query) && matchesError(event, query)
}

func matchesTraceID(event TraceEvent, query Query) bool {
	return query.TraceID == "" || event.TraceID == query.TraceID
}

func matchesThreadID(event TraceEvent, query Query) bool {
	return query.ThreadID == "" || event.ThreadID == query.ThreadID
}

func matchesSlow(event TraceEvent, query Query) bool {
	return !query.Slow || event.Status == StatusSlow
}

func matchesError(event TraceEvent, query Query) bool {
	return !query.Errors || event.Status == StatusError || event.Status == StatusPanic
}

func limitTailEvents(events []TraceEvent, limit int) ([]TraceEvent, bool) {
	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:], true
	}
	return events, false
}

func listTraceJSONLFiles(dir string) ([]traceTailFile, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open trace tail directory %s: %w", dir, err)
	}
	defer file.Close()
	files, err := readTraceFileCandidates(file, dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

func readTraceFileCandidates(file *os.File, dir string) ([]traceTailFile, error) {
	files := make([]traceTailFile, 0, maxTailCandidateFiles)
	entriesRead := 0
	for {
		names, err := file.Readdirnames(64)
		entriesRead += len(names)
		if entriesRead > maxTailDirectoryEntries {
			return nil, fmt.Errorf("trace tail directory %s exceeds %d entries", dir, maxTailDirectoryEntries)
		}
		if err := appendTraceFileCandidates(dir, names, &files); err != nil {
			return nil, err
		}
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read trace tail directory %s: %w", dir, err)
		}
	}
}

func appendTraceFileCandidates(dir string, names []string, files *[]traceTailFile) error {
	for _, name := range names {
		if !isTraceJSONLName(name) {
			continue
		}
		if len(*files) >= maxTailCandidateFiles {
			return fmt.Errorf("trace tail directory %s exceeds %d trace files", dir, maxTailCandidateFiles)
		}
		file, err := traceTailFileForName(dir, name)
		if err != nil {
			return err
		}
		*files = append(*files, file)
	}
	return nil
}

func traceTailFileForName(dir, name string) (traceTailFile, error) {
	path := filepath.Join(dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return traceTailFile{}, fmt.Errorf("stat trace tail file %s: %w", path, err)
	}
	if info.IsDir() {
		return traceTailFile{}, fmt.Errorf("trace tail path %s is a directory", path)
	}
	return traceTailFile{path: path, size: info.Size(), readBytes: info.Size()}, nil
}

type traceTailFile struct {
	path      string
	size      int64
	readBytes int64
}

func readTailFile(file traceTailFile, result *TailReadResult) error {
	f, err := os.Open(file.path)
	if err != nil {
		return fmt.Errorf("open trace tail file %s: %w", file.path, err)
	}
	defer f.Close()
	offset := max(file.size-file.readBytes, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek trace tail file %s: %w", file.path, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, file.readBytes))
	if err != nil {
		return fmt.Errorf("read trace tail file %s: %w", file.path, err)
	}
	if offset > 0 {
		if cut := bytes.IndexByte(data, '\n'); cut >= 0 {
			data = data[cut+1:]
		} else {
			return nil
		}
	}
	parseTailLines(file.path, data, result)
	return nil
}

func parseTailLines(path string, data []byte, result *TailReadResult) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var event TraceEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			result.DecodeErrors = append(result.DecodeErrors, tailDecodeError(path, line, !bytes.HasSuffix(data, []byte("\n")), err))
			continue
		}
		result.Events = append(result.Events, event)
	}
	if err := scanner.Err(); err != nil {
		line++
		result.DecodeErrors = append(result.DecodeErrors, tailDecodeError(path, line, false, err))
	}
}

func tailDecodeError(path string, line int, trailing bool, err error) TailDecodeError {
	return TailDecodeError{
		File:     path,
		Line:     line,
		Trailing: trailing,
		Error:    err.Error(),
		Metadata: map[string]any{"decode_error": true, "trailing": trailing},
	}
}
