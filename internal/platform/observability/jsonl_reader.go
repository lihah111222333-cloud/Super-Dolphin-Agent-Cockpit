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
	"time"
)

const (
	// tail 查询的目录和文件数量硬上限，防止诊断请求扫描过大的 trace 目录。
	maxTailDirectoryEntries = 1024
	maxTailCandidateFiles   = 128
)

// JSONLTailReader 从 trace JSONL 目录尾部读取有限字节，用于补齐内存索引之外的事件。
type JSONLTailReader struct {
	Dir      string
	MaxBytes int64
}

// TailReadResult 保存一次 tail 读取的事件、解码错误和预算使用情况。
type TailReadResult struct {
	Events       []TraceEvent
	DecodeErrors []TailDecodeError
	FilesScanned int
	BytesRead    int
	Truncated    bool
}

// TailDecodeError 描述 JSONL 单行解码失败，trailing 用于提示尾部半行风险。
type TailDecodeError struct {
	File     string         `json:"file"`
	Line     int            `json:"line"`
	Trailing bool           `json:"trailing"`
	Error    string         `json:"error"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// NewTailReader 根据配置创建 JSONL tail reader，读取预算以 MB 转换成字节。
func NewTailReader(dir string, cfg Config) JSONLTailReader {
	return JSONLTailReader{Dir: dir, MaxBytes: int64(cfg.JSONLQueryTailMB) * bytesPerMB}
}

// QueryTraceEvents 读取尾部文件后应用 Query 过滤，并把 tail 元数据写入 QueryResult。
func (r JSONLTailReader) QueryTraceEvents(ctx context.Context, query Query) (QueryResult, error) {
	startedAt := time.Now()
	result, err := r.Read(ctx)
	queryResult := tailQueryResultFromRead(result, time.Since(startedAt), err)
	if err != nil {
		return queryResult, err
	}
	events := filterTailEvents(result.Events, query)
	events, truncated := limitTailEvents(events, query.Limit)
	queryResult.Events = events
	queryResult.Truncated = truncated
	return queryResult, nil
}

// Read 按字节预算选择最近 trace 文件并顺序解析，目录或预算非法时直接返回错误。
func (r JSONLTailReader) Read(ctx context.Context) (TailReadResult, error) {
	if err := r.validate(ctx); err != nil {
		return TailReadResult{}, err
	}
	files, err := listTraceJSONLFiles(r.Dir)
	if err != nil {
		return TailReadResult{}, err
	}
	selected, truncated := selectTailFiles(files, r.MaxBytes)
	result, err := readSelectedTailFiles(ctx, selected)
	result.Truncated = result.Truncated || truncated
	return result, err
}

// validate 校验 tail reader 的上下文、目录和读取预算，避免后台诊断静默退化。
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

// selectTailFiles 从最新文件向前选择读取窗口，超出预算时标记 truncated。
func selectTailFiles(files []traceTailFile, maxBytes int64) ([]traceTailFile, bool) {
	remaining := maxBytes
	selected := make([]traceTailFile, 0, len(files))
	truncated := false
	for i := len(files) - 1; i >= 0 && remaining > 0; i-- {
		file := files[i]
		readBytes := min(file.size, remaining)
		if readBytes < file.size {
			truncated = true
		}
		selected = append(selected, traceTailFile{path: file.path, size: file.size, readBytes: readBytes})
		remaining -= readBytes
	}
	if len(selected) < len(files) {
		truncated = true
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].path < selected[j].path })
	return selected, truncated
}

// readSelectedTailFiles 顺序读取已选文件，遇到 ctx 取消或读取错误时返回已收集结果。
func readSelectedTailFiles(ctx context.Context, files []traceTailFile) (TailReadResult, error) {
	var result TailReadResult
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.FilesScanned++
		if err := readTailFile(ctx, file, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

// filterTailEvents 复用 matchesQuery 过滤 tail 事件。
func filterTailEvents(events []TraceEvent, query Query) []TraceEvent {
	out := make([]TraceEvent, 0, len(events))
	for _, event := range events {
		if matchesQuery(event, query) {
			out = append(out, event)
		}
	}
	return out
}

// limitTailEvents 保留最近 limit 条事件，limit 非正数表示不裁剪。
func limitTailEvents(events []TraceEvent, limit int) ([]TraceEvent, bool) {
	if limit > 0 && len(events) > limit {
		return events[len(events)-limit:], true
	}
	return events, false
}

// listTraceJSONLFiles 列出 trace 目录里的候选 JSONL 文件并按路径稳定排序。
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

// readTraceFileCandidates 分批读取目录项，并在目录过大时 fail-fast。
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

// appendTraceFileCandidates 只追加合法 trace JSONL 文件，候选数量超过硬上限时报错。
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

// traceTailFileForName 校验候选路径必须是文件并记录大小。
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

// traceTailFile 记录单个 trace 文件的总大小和本次实际读取字节数。
type traceTailFile struct {
	path      string
	size      int64
	readBytes int64
}

// readTailFile 从文件尾部读取预算内字节；从中间开始时会跳过第一段可能不完整的行。
func readTailFile(ctx context.Context, file traceTailFile, result *TailReadResult) error {
	f, err := os.Open(file.path)
	if err != nil {
		return fmt.Errorf("open trace tail file %s: %w", file.path, err)
	}
	defer f.Close()
	offset := max(file.size-file.readBytes, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek trace tail file %s: %w", file.path, err)
	}
	data, err := readTailFileBytes(ctx, f, file.readBytes)
	if err != nil {
		return fmt.Errorf("read trace tail file %s: %w", file.path, err)
	}
	result.BytesRead += len(data)
	if file.readBytes < file.size {
		result.Truncated = true
	}
	if offset > 0 {
		if cut := bytes.IndexByte(data, '\n'); cut >= 0 {
			data = data[cut+1:]
		} else {
			return nil
		}
	}
	return parseTailLines(ctx, file.path, data, result)
}

// readTailFileBytes 在 ctx 和字节上限约束下读取文件内容，避免一次性读取超预算文件。
func readTailFileBytes(ctx context.Context, reader io.Reader, limit int64) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(int(min(limit, int64(1024*1024))))
	chunk := make([]byte, 64*1024)
	remaining := limit
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return out.Bytes(), err
		}
		n, err := reader.Read(chunk[:min(len(chunk), int(remaining))])
		if n > 0 {
			out.Write(chunk[:n])
			remaining -= int64(n)
		}
		if errors.Is(err, io.EOF) {
			return out.Bytes(), nil
		}
		if err != nil {
			return out.Bytes(), err
		}
		if n == 0 {
			return out.Bytes(), io.ErrNoProgress
		}
	}
	return out.Bytes(), ctx.Err()
}

// parseTailLines 逐行解析 JSONL，坏行会记录 DecodeErrors 而不阻断其他事件。
func parseTailLines(ctx context.Context, path string, data []byte, result *TailReadResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
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
	return ctx.Err()
}

// tailQueryResultFromRead 把读取统计和错误状态投影到 QueryResult。
func tailQueryResultFromRead(result TailReadResult, duration time.Duration, err error) QueryResult {
	return QueryResult{
		Source:           QuerySourceJSONLTail,
		TailDecodeErrors: result.DecodeErrors,
		TailFilesScanned: result.FilesScanned,
		TailBytesRead:    result.BytesRead,
		TailDurationMS:   durationMillis(duration),
		TailTimedOut:     errors.Is(err, context.DeadlineExceeded),
		TailTruncated:    result.Truncated,
	}
}

// durationMillis 以毫秒记录读取耗时，亚毫秒非零耗时记为 1。
func durationMillis(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	if duration < time.Millisecond {
		return 1
	}
	return duration.Milliseconds()
}

// tailDecodeError 构造单行解码错误，并附带用于诊断提示的 metadata。
func tailDecodeError(path string, line int, trailing bool, err error) TailDecodeError {
	return TailDecodeError{
		File:     path,
		Line:     line,
		Trailing: trailing,
		Error:    err.Error(),
		Metadata: map[string]any{"decode_error": true, "trailing": trailing},
	}
}
