//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	runtimeMarkdownParseMethod           = "markdown/parse"
	runtimeMarkdownReadFileMethod        = "markdown/fs/readFile"
	runtimeMarkdownStatMethod            = "markdown/fs/stat"
	runtimeMarkdownReadDirectoryMethod   = "markdown/fs/readDirectory"
	runtimeMarkdownFindFilesMethod       = "markdown/findMarkdownFilesInWorkspace"
	runtimeMarkdownWatcherCreateMethod   = "markdown/fs/watcher/create"
	runtimeMarkdownWatcherDeleteMethod   = "markdown/fs/watcher/delete"
	runtimeMarkdownWatcherOnChangeMethod = "markdown/fs/watcher/onChange"
	runtimeMarkdownParserInputLimit      = 16 << 20
	runtimeMarkdownParserOutputLimit     = 16 << 20
	runtimeMarkdownParserErrorLimit      = 64 << 10
	runtimeMarkdownParserTimeout         = 30 * time.Second
	runtimeMarkdownWatcherRequestTimeout = 10 * time.Second
	runtimeMarkdownWatcherCloseTimeout   = 2 * time.Second
	// runtimeMarkdownWireLogEnv 仅在显式 E2E 收据运行时开启；普通生产进程不写额外日志。
	runtimeMarkdownWireLogEnv = "SUPER_DOLPHIN_MARKDOWN_WIRE_LOG"
)

var errRuntimeMarkdownWorkspaceEscape = errors.New("Markdown URI resolves outside workspace root")

// runtimeWindowsMarkdownClientProtocol 实现 VS Code Markdown client 侧的官方 custom requests。
// 所有文件请求先解析到 workspace root；watcher 的异步 onChange 通过真实 LSP client.Request 回发。
type runtimeWindowsMarkdownClientProtocol struct {
	workspace  *runtimeMarkdownWorkspace
	nodePath   string
	moduleRoot string
	env        []string

	mu       sync.RWMutex
	sender   func(context.Context, string, any) (json.RawMessage, error)
	watchers map[int]*runtimeMarkdownWatcher
	closed   bool
	asyncErr error
	closeErr error

	wireMu   sync.Mutex
	wireLog  *os.File
	wirePath string
}

// newRuntimeMarkdownClientSupport 只为 Windows Markdown adapter 构造协议处理器。
func newRuntimeMarkdownClientSupport(adapter multilsp.LanguageAdapter, root string, env []string, serverBinary string) (runtimeMarkdownClientSupport, error) {
	if adapter == nil || !runtimeMarkdownAdapter(adapter) {
		return nil, nil
	}
	workspace, err := newRuntimeMarkdownWorkspace(root)
	if err != nil {
		return nil, err
	}
	productRoot, err := runtimeMarkdownProductRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	lockedNodePath, err := installer.ResolveWindowsNodeRuntimeExecutablePath(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve Windows Markdown Node runtime from product root: %w", err)
	}
	lockedNodePath, err = runtimeMarkdownSecurePath(lockedNodePath, "locked Windows Node runtime", false)
	if err != nil {
		return nil, err
	}
	nodePath := strings.TrimSpace(runtimeServerEnvValue(env, runtimeServerWindowsNodeExecutableEnv))
	if nodePath == "" {
		nodePath = lockedNodePath
	} else {
		if !filepath.IsAbs(nodePath) {
			return nil, fmt.Errorf("Windows Markdown protocol requires absolute %s", runtimeServerWindowsNodeExecutableEnv)
		}
		nodePath, err = runtimeMarkdownSecurePath(nodePath, "locked Windows Node runtime override", false)
		if err != nil {
			return nil, err
		}
		actualInfo, actualErr := os.Stat(nodePath)
		lockedInfo, lockedErr := os.Stat(lockedNodePath)
		if actualErr != nil || lockedErr != nil || !os.SameFile(actualInfo, lockedInfo) {
			return nil, fmt.Errorf("%s must identify the production locked Windows Node runtime: %s", runtimeServerWindowsNodeExecutableEnv, nodePath)
		}
	}
	if !runtimeMarkdownWithin(productRoot, nodePath) {
		return nil, fmt.Errorf("locked Windows Node runtime escapes product root: %s", nodePath)
	}
	moduleRoot, err := runtimeMarkdownModuleRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	if err := runtimeMarkdownRequireExactPackage(moduleRoot); err != nil {
		return nil, err
	}
	var wireLog *os.File
	wirePath := strings.TrimSpace(runtimeServerEnvValue(env, runtimeMarkdownWireLogEnv))
	if wirePath != "" {
		if !filepath.IsAbs(wirePath) {
			return nil, fmt.Errorf("%s must be an absolute path", runtimeMarkdownWireLogEnv)
		}
		wirePath, err = filepath.Abs(wirePath)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", runtimeMarkdownWireLogEnv, err)
		}
		wireLog, err = os.OpenFile(wirePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", runtimeMarkdownWireLogEnv, err)
		}
	}
	protocol := &runtimeWindowsMarkdownClientProtocol{
		workspace:  workspace,
		nodePath:   nodePath,
		moduleRoot: moduleRoot,
		env:        runtimeMarkdownEnvWithNodePath(env, moduleRoot),
		watchers:   make(map[int]*runtimeMarkdownWatcher),
		wireLog:    wireLog,
		wirePath:   wirePath,
	}
	return protocol, nil
}

func runtimeMarkdownProductRoot(serverBinary string) (string, error) {
	productRoot, err := runtimeServerWindowsProductRootFromBinary(serverBinary)
	if err != nil {
		return "", fmt.Errorf("resolve locked Markdown product root: %w", err)
	}
	return runtimeMarkdownSecurePath(productRoot, "Markdown product root", true)
}

func (p *runtimeWindowsMarkdownClientProtocol) RequestHandler() multilsp.ServerRequestHandler {
	return p.handleRequest
}

// ServerNotificationHandler 保留通用通知 seam；VS Code watcher onChange 是 request，不是 notification。
func (p *runtimeWindowsMarkdownClientProtocol) ServerNotificationHandler() multilsp.ServerNotificationHandler {
	return nil
}

// Attach 绑定已创建的真实 LSP client，供 watcher 异步发送官方 request。
func (p *runtimeWindowsMarkdownClientProtocol) Attach(client multilsp.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || client == nil {
		return
	}
	p.sender = client.Request
}

func (p *runtimeWindowsMarkdownClientProtocol) Healthy() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.closed && p.asyncErr == nil
}

func (p *runtimeWindowsMarkdownClientProtocol) recordAsyncError(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	if p.asyncErr == nil {
		p.asyncErr = err
	}
	p.mu.Unlock()
}

func (p *runtimeWindowsMarkdownClientProtocol) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if p == nil {
		return nil, errors.New("Windows Markdown protocol is nil")
	}
	if err := p.logWire("server_request", method, params, nil); err != nil {
		return nil, err
	}
	switch method {
	case runtimeMarkdownParseMethod:
		return p.handleParse(ctx, params)
	case runtimeMarkdownReadFileMethod:
		return p.handleReadFile(params)
	case runtimeMarkdownStatMethod:
		return p.handleStat(params)
	case runtimeMarkdownReadDirectoryMethod:
		return p.handleReadDirectory(params)
	case runtimeMarkdownFindFilesMethod:
		return p.handleFindFiles()
	case runtimeMarkdownWatcherCreateMethod:
		return p.handleWatcherCreate(params)
	case runtimeMarkdownWatcherDeleteMethod:
		return p.handleWatcherDelete(params)
	default:
		return nil, multilsp.ErrMethodNotSupported
	}
}

func (p *runtimeWindowsMarkdownClientProtocol) handleParse(ctx context.Context, params json.RawMessage) (any, error) {
	var request struct {
		URI  string  `json:"uri"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", runtimeMarkdownParseMethod, err)
	}
	path, err := p.workspace.pathFromURI(request.URI)
	if err != nil {
		return nil, err
	}
	text := ""
	if request.Text != nil {
		text = *request.Text
	} else {
		contents, err := p.readFile(path)
		if err != nil {
			return nil, err
		}
		text = string(contents)
	}
	if len(text) > runtimeMarkdownParserInputLimit {
		return nil, fmt.Errorf("markdown-it input exceeds bounded limit of %d bytes", runtimeMarkdownParserInputLimit)
	}
	return p.parse(ctx, text)
}

func (p *runtimeWindowsMarkdownClientProtocol) handleReadFile(params json.RawMessage) (any, error) {
	path, err := p.pathParam(params, runtimeMarkdownReadFileMethod)
	if err != nil {
		return nil, err
	}
	contents, err := p.readFile(path)
	if err != nil {
		return nil, err
	}
	// VS Code 的 fs/readFile 响应类型是 number[]；Go 的 []byte 会被 JSON 编码器
	// 转成 base64 字符串，必须显式转换以保持官方协议的逐字节数组合同。
	result := make([]int, len(contents))
	for index, value := range contents {
		result[index] = int(value)
	}
	return result, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) handleStat(params json.RawMessage) (any, error) {
	path, err := p.pathParam(params, runtimeMarkdownStatMethod)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, runtimeMarkdownFilesystemError("stat", path, err)
	}
	return map[string]bool{"isDirectory": info.IsDir()}, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) handleReadDirectory(params json.RawMessage) (any, error) {
	path, err := p.pathParam(params, runtimeMarkdownReadDirectoryMethod)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, runtimeMarkdownFilesystemError("readDirectory", path, err)
	}
	result := make([][2]any, 0, len(entries))
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		if _, err := p.workspace.resolvePath(child); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, runtimeMarkdownFilesystemError("readDirectory entry", child, err)
		}
		result = append(result, [2]any{entry.Name(), map[string]bool{"isDirectory": info.IsDir()}})
	}
	return result, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) handleFindFiles() (any, error) {
	files := make([]string, 0)
	err := filepath.WalkDir(p.workspace.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return runtimeMarkdownFilesystemError("findMarkdownFiles", path, walkErr)
		}
		if _, err := p.workspace.resolvePath(path); err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.EqualFold(entry.Name(), "node_modules") {
				return fs.SkipDir
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") || strings.EqualFold(filepath.Ext(entry.Name()), ".markdown") {
			files = append(files, runtimeMarkdownFileURI(path))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) handleWatcherCreate(params json.RawMessage) (any, error) {
	var request struct {
		ID      int    `json:"id"`
		URI     string `json:"uri"`
		Options struct {
			IgnoreCreate bool `json:"ignoreCreate"`
			IgnoreChange bool `json:"ignoreChange"`
			IgnoreDelete bool `json:"ignoreDelete"`
		} `json:"options"`
		WatchParentDirs bool `json:"watchParentDirs"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", runtimeMarkdownWatcherCreateMethod, err)
	}
	if request.ID < 0 {
		return nil, fmt.Errorf("Markdown watcher id must be non-negative: %d", request.ID)
	}
	if strings.TrimSpace(request.URI) == "" {
		return nil, errors.New("Markdown watcher URI is required")
	}
	target, err := p.workspace.pathFromURI(request.URI)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("Markdown watcher client is closed")
	}
	if _, exists := p.watchers[request.ID]; exists {
		p.mu.Unlock()
		return nil, fmt.Errorf("Markdown watcher id %d already exists", request.ID)
	}
	p.mu.Unlock()
	watcher, err := newRuntimeMarkdownWatcher(p, request.ID, request.URI, target, request.Options.IgnoreCreate, request.Options.IgnoreChange, request.Options.IgnoreDelete, request.WatchParentDirs)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = watcher.Close()
		return nil, errors.New("Markdown watcher client closed during creation")
	}
	if _, exists := p.watchers[request.ID]; exists {
		p.mu.Unlock()
		_ = watcher.Close()
		return nil, fmt.Errorf("Markdown watcher id %d already exists", request.ID)
	}
	p.watchers[request.ID] = watcher
	p.mu.Unlock()
	return nil, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) handleWatcherDelete(params json.RawMessage) (any, error) {
	var request struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, fmt.Errorf("decode %s params: %w", runtimeMarkdownWatcherDeleteMethod, err)
	}
	if request.ID < 0 {
		return nil, fmt.Errorf("Markdown watcher id must be non-negative: %d", request.ID)
	}
	p.mu.Lock()
	watcher := p.watchers[request.ID]
	if watcher == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("Markdown watcher id %d does not exist", request.ID)
	}
	delete(p.watchers, request.ID)
	p.mu.Unlock()
	return nil, watcher.Close()
}

func (p *runtimeWindowsMarkdownClientProtocol) pathParam(params json.RawMessage, method string) (string, error) {
	var request struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return "", fmt.Errorf("decode %s params: %w", method, err)
	}
	return p.workspace.pathFromURI(request.URI)
}

func (p *runtimeWindowsMarkdownClientProtocol) readFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, runtimeMarkdownFilesystemError("stat before readFile", path, err)
	}
	if info.Size() > runtimeMarkdownParserInputLimit {
		return nil, fmt.Errorf("readFile exceeds bounded Markdown input limit of %d bytes: %s", runtimeMarkdownParserInputLimit, path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, runtimeMarkdownFilesystemError("readFile", path, err)
	}
	return contents, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) parse(ctx context.Context, text string) (any, error) {
	if len(text) > runtimeMarkdownParserInputLimit {
		return nil, fmt.Errorf("markdown-it input exceeds bounded limit of %d bytes", runtimeMarkdownParserInputLimit)
	}
	input, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, fmt.Errorf("encode markdown-it input: %w", err)
	}
	parseCtx, cancel := context.WithTimeout(ctx, runtimeMarkdownParserTimeout)
	defer cancel()
	command := exec.CommandContext(parseCtx, p.nodePath, "-e", runtimeMarkdownNodeScript)
	command.Dir = p.moduleRoot
	command.Env = append([]string(nil), p.env...)
	command.Stdin = bytes.NewReader(input)
	stdout := &runtimeMarkdownLimitedBuffer{limit: runtimeMarkdownParserOutputLimit}
	stderr := &runtimeMarkdownLimitedBuffer{limit: runtimeMarkdownParserErrorLimit}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if errors.Is(parseCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("markdown-it parse timed out: %w", parseCtx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, fmt.Errorf("markdown-it parse: %w", err)
		}
		return nil, fmt.Errorf("markdown-it parse: %w: %s", err, message)
	}
	if stdout.tooLarge {
		return nil, errors.New("markdown-it parse output exceeded bounded limit")
	}
	var tokens []json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &tokens); err != nil {
		return nil, fmt.Errorf("decode markdown-it tokens: %w", err)
	}
	return tokens, nil
}

func (p *runtimeWindowsMarkdownClientProtocol) sendWatcherChange(ctx context.Context, id int, uri, kind string) error {
	p.mu.RLock()
	sender := p.sender
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return errors.New("Markdown watcher client is closed")
	}
	if sender == nil {
		return errors.New("Markdown watcher LSP request sender is not attached")
	}
	requestCtx, cancel := context.WithTimeout(ctx, runtimeMarkdownWatcherRequestTimeout)
	defer cancel()
	payload := map[string]any{
		"id":   id,
		"uri":  uri,
		"kind": kind,
	}
	_, err := sender(requestCtx, runtimeMarkdownWatcherOnChangeMethod, payload)
	if logErr := p.logWire("client_request", runtimeMarkdownWatcherOnChangeMethod, payload, err); logErr != nil {
		p.recordAsyncError(logErr)
		return errors.Join(err, logErr)
	}
	return err
}

// logWire 将显式 E2E 的真实 Markdown client/server request 写成逐行 JSON；写失败直接进入
// 协议错误链，避免把缺失的 watcher 证据静默当成成功。
func (p *runtimeWindowsMarkdownClientProtocol) logWire(direction, method string, payload any, wireErr error) error {
	if p == nil || p.wireLog == nil {
		return nil
	}
	record := map[string]any{
		"direction": direction,
		"method":    method,
		"payload":   payload,
	}
	if wireErr != nil {
		record["error"] = wireErr.Error()
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode Markdown wire log %s: %w", method, err)
	}
	p.wireMu.Lock()
	defer p.wireMu.Unlock()
	if _, err := p.wireLog.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write Markdown wire log %s (%s): %w", method, p.wirePath, err)
	}
	return nil
}

// Close 停止所有 watcher，确保 client.Close 后没有异步文件句柄或 goroutine 残留。
func (p *runtimeWindowsMarkdownClientProtocol) Close() error {
	p.mu.Lock()
	if p.closed {
		err := errors.Join(p.asyncErr, p.closeErr)
		p.mu.Unlock()
		return err
	}
	p.closed = true
	watchers := make([]*runtimeMarkdownWatcher, 0, len(p.watchers))
	for id, watcher := range p.watchers {
		delete(p.watchers, id)
		watchers = append(watchers, watcher)
	}
	p.mu.Unlock()
	var errs []error
	for _, watcher := range watchers {
		if err := watcher.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	p.mu.Lock()
	p.closeErr = errors.Join(p.asyncErr, errors.Join(errs...))
	err := p.closeErr
	p.mu.Unlock()
	p.wireMu.Lock()
	if p.wireLog != nil {
		if wireErr := p.wireLog.Close(); wireErr != nil {
			err = errors.Join(err, fmt.Errorf("close Markdown wire log %s: %w", p.wirePath, wireErr))
		}
		p.wireLog = nil
	}
	p.wireMu.Unlock()
	return err
}

type runtimeMarkdownWatcher struct {
	protocol        *runtimeWindowsMarkdownClientProtocol
	id              int
	uri             string
	target          string
	directoryTarget bool
	parentDirs      map[string]struct{}
	watchPaths      map[string]struct{}
	ignoreCreate    bool
	ignoreChange    bool
	ignoreDelete    bool
	watchParentDirs bool
	watcher         *fsnotify.Watcher
	cancel          context.CancelFunc
	done            chan struct{}
	closeOnce       sync.Once
	closeErr        error
}

func newRuntimeMarkdownWatcher(protocol *runtimeWindowsMarkdownClientProtocol, id int, uri, target string, ignoreCreate, ignoreChange, ignoreDelete, watchParentDirs bool) (*runtimeMarkdownWatcher, error) {
	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		if !watchParentDirs {
			return nil, runtimeMarkdownFilesystemError("watcher target", target, err)
		}
	} else if err != nil {
		return nil, runtimeMarkdownFilesystemError("watcher target", target, err)
	}
	if protocol == nil || protocol.workspace == nil {
		return nil, errors.New("Markdown watcher workspace is unavailable")
	}
	target = filepath.Clean(target)
	watchPaths, parentDirs, err := runtimeMarkdownWatcherPaths(protocol.workspace, target, info, watchParentDirs)
	if err != nil {
		return nil, err
	}
	nativeWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, runtimeMarkdownFilesystemError("watcher create", target, err)
	}
	for _, watchPath := range watchPaths {
		if err := nativeWatcher.Add(watchPath); err != nil {
			_ = nativeWatcher.Close()
			return nil, runtimeMarkdownFilesystemError("watcher add", watchPath, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := &runtimeMarkdownWatcher{
		protocol:        protocol,
		id:              id,
		uri:             uri,
		target:          target,
		directoryTarget: info != nil && info.IsDir(),
		parentDirs:      parentDirs,
		watchPaths:      make(map[string]struct{}, len(watchPaths)),
		ignoreCreate:    ignoreCreate,
		ignoreChange:    ignoreChange,
		ignoreDelete:    ignoreDelete,
		watchParentDirs: watchParentDirs,
		watcher:         nativeWatcher,
		cancel:          cancel,
		done:            make(chan struct{}),
	}
	for _, watchPath := range watchPaths {
		result.watchPaths[runtimeMarkdownWatchKey(watchPath)] = struct{}{}
	}
	go result.run(ctx)
	return result, nil
}

// runtimeMarkdownWatchKey 统一 Windows watcher 路径的大小写和分隔符，避免同一
// 路径因事件来源大小写不同而漏掉 parent/watchPaths 查找。
func runtimeMarkdownWatchKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func (w *runtimeMarkdownWatcher) matchesEvent(eventPath string) (isTarget, isDirectoryChild, isParent bool) {
	eventPath = filepath.Clean(eventPath)
	isTarget = strings.EqualFold(eventPath, w.target)
	if !isTarget && w.directoryTarget && runtimeMarkdownWithin(w.target, eventPath) {
		isDirectoryChild = true
	}
	isTarget = isTarget || isDirectoryChild
	_, isParent = w.parentDirs[runtimeMarkdownWatchKey(eventPath)]
	return isTarget, isDirectoryChild, isParent
}

func (w *runtimeMarkdownWatcher) targetInfo() (os.FileInfo, error) {
	resolved, err := w.protocol.workspace.resolvePath(w.target)
	if err != nil {
		return nil, err
	}
	return os.Stat(resolved)
}

func (w *runtimeMarkdownWatcher) ensureWatch(path string) error {
	key := runtimeMarkdownWatchKey(path)
	if _, watched := w.watchPaths[key]; watched {
		return nil
	}
	if err := w.watcher.Add(path); err != nil {
		return err
	}
	w.watchPaths[key] = struct{}{}
	return nil
}

func runtimeMarkdownWatcherPaths(workspace *runtimeMarkdownWorkspace, target string, info os.FileInfo, watchParentDirs bool) ([]string, map[string]struct{}, error) {
	directPath := filepath.Dir(target)
	if info != nil && info.IsDir() {
		directPath = target
	}
	if _, err := workspace.resolvePath(directPath); err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(directPath); err != nil {
		if !errors.Is(err, fs.ErrNotExist) || !watchParentDirs {
			return nil, nil, runtimeMarkdownFilesystemError("watcher direct parent", directPath, err)
		}
		for {
			directPath = filepath.Dir(directPath)
			if _, parentErr := os.Stat(directPath); parentErr == nil {
				break
			} else if !errors.Is(parentErr, fs.ErrNotExist) {
				return nil, nil, runtimeMarkdownFilesystemError("watcher existing parent", directPath, parentErr)
			}
			if filepath.Clean(directPath) == filepath.Clean(workspace.root) {
				return nil, nil, runtimeMarkdownFilesystemError("watcher existing parent", directPath, fs.ErrNotExist)
			}
		}
	}
	watchPaths := []string{filepath.Clean(directPath)}
	seen := map[string]struct{}{runtimeMarkdownWatchKey(directPath): {}}
	parentDirs := make(map[string]struct{})
	if !watchParentDirs {
		return watchPaths, parentDirs, nil
	}
	for dir := filepath.Dir(target); runtimeMarkdownWithin(workspace.root, dir); dir = filepath.Dir(dir) {
		dir = filepath.Clean(dir)
		if dir != filepath.Clean(workspace.root) {
			parentDirs[runtimeMarkdownWatchKey(dir)] = struct{}{}
		}
		if _, err := os.Stat(dir); err == nil {
			if _, exists := seen[runtimeMarkdownWatchKey(dir)]; !exists {
				watchPaths = append(watchPaths, dir)
				seen[runtimeMarkdownWatchKey(dir)] = struct{}{}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, runtimeMarkdownFilesystemError("watcher parent", dir, err)
		}
		if dir == filepath.Clean(workspace.root) {
			break
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
	}
	if _, exists := seen[runtimeMarkdownWatchKey(workspace.root)]; !exists {
		watchPaths = append(watchPaths, filepath.Clean(workspace.root))
	}
	return watchPaths, parentDirs, nil
}

func (w *runtimeMarkdownWatcher) run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			kind := runtimeMarkdownWatcherKind(event.Op)
			if kind == "" {
				continue
			}
			eventPath := filepath.Clean(event.Name)
			isTarget, isDirectoryChild, isParent := w.matchesEvent(eventPath)
			if !isTarget && !isParent {
				continue
			}
			if _, err := w.protocol.workspace.resolvePath(eventPath); err != nil {
				w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d event path %s escaped workspace: %w", w.id, eventPath, err))
				return
			}
			if isTarget && !isDirectoryChild && kind == "create" {
				info, statErr := w.targetInfo()
				if statErr != nil {
					if !errors.Is(statErr, fs.ErrNotExist) {
						w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d validate recreated target %s failed: %w", w.id, w.target, statErr))
						return
					}
				} else {
					w.directoryTarget = info.IsDir()
					if w.directoryTarget {
						if addErr := w.ensureWatch(w.target); addErr != nil {
							w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d add recreated target %s failed: %w", w.id, w.target, addErr))
							return
						}
					}
				}
			} else if isTarget && !isDirectoryChild && kind == "delete" {
				delete(w.watchPaths, runtimeMarkdownWatchKey(w.target))
			}
			if isParent {
				if kind == "create" {
					info, statErr := os.Stat(eventPath)
					if statErr != nil {
						if !errors.Is(statErr, fs.ErrNotExist) {
							w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d validate recreated parent %s failed: %w", w.id, eventPath, statErr))
							return
						}
						continue
					}
					if !info.IsDir() {
						continue
					}
					if addErr := w.ensureWatch(eventPath); addErr != nil {
						w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d add recreated parent %s failed: %w", w.id, eventPath, addErr))
						return
					}
					targetInfo, targetErr := w.targetInfo()
					if targetErr != nil {
						if !errors.Is(targetErr, fs.ErrNotExist) {
							w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d validate target after parent %s create failed: %w", w.id, eventPath, targetErr))
							return
						}
						continue
					}
					w.directoryTarget = targetInfo.IsDir()
					if w.directoryTarget {
						if addErr := w.ensureWatch(w.target); addErr != nil {
							w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d add target %s after parent create failed: %w", w.id, w.target, addErr))
							return
						}
					}
				} else if kind == "delete" {
					delete(w.watchPaths, runtimeMarkdownWatchKey(eventPath))
				} else {
					continue
				}
			}
			if _, err := w.protocol.workspace.resolvePath(eventPath); err != nil && isTarget {
				w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d event path %s escaped workspace: %w", w.id, eventPath, err))
				return
			}
			if (kind == "create" && w.ignoreCreate) || (kind == "change" && w.ignoreChange) || (kind == "delete" && w.ignoreDelete) {
				continue
			}
			if err := w.protocol.sendWatcherChange(ctx, w.id, w.uri, kind); err != nil {
				if ctx.Err() == nil {
					w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d %s event send failed: %w", w.id, kind, err))
				}
				return
			}
		case watcherErr, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if ctx.Err() == nil && watcherErr != nil {
				w.protocol.recordAsyncError(fmt.Errorf("Markdown watcher id %d backend failed: %w", w.id, watcherErr))
				return
			}
		}
	}
}

func runtimeMarkdownWatcherKind(op fsnotify.Op) string {
	if op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		return "delete"
	}
	if op&fsnotify.Create != 0 {
		return "create"
	}
	if op&fsnotify.Write != 0 {
		return "change"
	}
	return ""
}

// Close 取消 watcher context、关闭底层句柄，并在有界窗口内等待 goroutine 退出。
func (w *runtimeMarkdownWatcher) Close() error {
	if w == nil {
		return nil
	}
	w.closeOnce.Do(func() {
		w.cancel()
		closeErr := w.watcher.Close()
		select {
		case <-w.done:
		case <-time.After(runtimeMarkdownWatcherCloseTimeout):
			closeErr = errors.Join(closeErr, errors.New("Markdown watcher close timed out"))
		}
		w.closeErr = closeErr
	})
	return w.closeErr
}

type runtimeMarkdownWorkspace struct {
	root string
}

func newRuntimeMarkdownWorkspace(root string) (*runtimeMarkdownWorkspace, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("Markdown workspace root is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Markdown workspace root: %w", err)
	}
	resolvedRoot, err := lspplatform.CanonicalDirectoryPath(absRoot)
	if err != nil {
		return nil, runtimeMarkdownFilesystemError("resolve Markdown workspace root", absRoot, err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("workspace root is not a directory")
		}
		return nil, runtimeMarkdownFilesystemError("validate Markdown workspace root", resolvedRoot, err)
	}
	return &runtimeMarkdownWorkspace{root: filepath.Clean(resolvedRoot)}, nil
}

func (w *runtimeMarkdownWorkspace) pathFromURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse Markdown file URI: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "file") || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Markdown client request requires a local file URI without host/query/fragment")
	}
	pathValue, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", fmt.Errorf("decode Markdown file URI path: %w", err)
	}
	if len(pathValue) >= 3 && pathValue[0] == '/' && pathValue[2] == ':' {
		pathValue = pathValue[1:]
	}
	pathValue = filepath.FromSlash(pathValue)
	if !filepath.IsAbs(pathValue) {
		return "", errors.New("Markdown file URI path must be absolute")
	}
	return w.resolvePath(pathValue)
}

func (w *runtimeMarkdownWorkspace) resolvePath(path string) (string, error) {
	if w == nil || filepath.Clean(w.root) == "." {
		return "", errors.New("Markdown workspace is unavailable")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Markdown path: %w", err)
	}
	absPath = filepath.Clean(absPath)
	if !runtimeMarkdownWithin(w.root, absPath) {
		return "", fmt.Errorf("%w: %s", errRuntimeMarkdownWorkspaceEscape, absPath)
	}
	evaluated, err := lspplatform.CanonicalExistingPath(absPath)
	if err == nil {
		if runtimeMarkdownWithin(w.root, evaluated) {
			return filepath.Clean(absPath), nil
		}
		// Windows 删除挂起句柄可能把已不存在的路径解析到 $Extend\\$Deleted；只有 Lstat
		// 已确认路径消失时才允许按现存父目录继续校验，真实存在的越界路径仍然拒绝。
		_, lstatErr := os.Lstat(absPath)
		if lstatErr == nil || !errors.Is(lstatErr, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", errRuntimeMarkdownWorkspaceEscape, absPath)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		// CanonicalExistingPath 可能在删除挂起文件消失期间报告拒绝访问；仅当 Lstat
		// 确认路径已消失时才回退，其他错误继续保持 fail-fast。
		_, lstatErr := os.Lstat(absPath)
		if !errors.Is(lstatErr, fs.ErrNotExist) {
			return "", runtimeMarkdownFilesystemError("resolve Markdown path", absPath, err)
		}
	}
	parent, suffix, err := runtimeMarkdownExistingParent(absPath)
	if err != nil {
		return "", runtimeMarkdownFilesystemError("resolve Markdown path parent", absPath, err)
	}
	evaluatedParent, err := lspplatform.CanonicalDirectoryPath(parent)
	if err != nil {
		return "", runtimeMarkdownFilesystemError("resolve Markdown path parent", parent, err)
	}
	evaluatedCandidate := evaluatedParent
	if suffix != "" {
		evaluatedCandidate = filepath.Join(evaluatedParent, suffix)
	}
	if !runtimeMarkdownWithin(w.root, evaluatedCandidate) {
		return "", fmt.Errorf("%w: %s", errRuntimeMarkdownWorkspaceEscape, absPath)
	}
	return absPath, nil
}

func runtimeMarkdownExistingParent(path string) (string, string, error) {
	current := filepath.Clean(path)
	suffixParts := make([]string, 0, 4)
	for {
		if _, err := os.Lstat(current); err == nil {
			suffix := ""
			for index := len(suffixParts) - 1; index >= 0; index-- {
				suffix = filepath.Join(suffix, suffixParts[index])
			}
			return current, suffix, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", fs.ErrNotExist
		}
		suffixParts = append(suffixParts, filepath.Base(current))
		current = parent
	}
}

func runtimeMarkdownWithin(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return true
	}
	if filepath.IsAbs(rel) {
		return false
	}
	first := strings.Split(rel, string(filepath.Separator))[0]
	return first != ".." && first != "." && !strings.EqualFold(first, "..")
}

func runtimeMarkdownFileURI(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if len(path) >= 2 && path[1] == ':' {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func runtimeMarkdownFilesystemError(operation, path string, err error) error {
	if err == nil {
		return errors.New(operation + " failed")
	}
	return securefs.WrapErrorForPath(fmt.Errorf("%s: %w", operation, err), path)
}

func runtimeMarkdownSecurePath(path, label string, directory bool) (string, error) {
	clean, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	clean = filepath.Clean(clean)
	if clean == "." || !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%s must be absolute: %q", label, path)
	}
	if err := runtimeMarkdownValidatePathComponents(clean, label); err != nil {
		return "", err
	}
	var resolved string
	if directory {
		resolved, err = lspplatform.CanonicalDirectoryPath(clean)
	} else {
		resolved, err = lspplatform.CanonicalExistingPath(clean)
	}
	if err != nil {
		return "", runtimeMarkdownFilesystemError("resolve "+label, clean, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve canonical %s: %w", label, err)
	}
	if !strings.EqualFold(filepath.Clean(clean), filepath.Clean(resolved)) {
		return "", fmt.Errorf("%s contains symlink/junction/reparse escape: %s", label, clean)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", runtimeMarkdownFilesystemError("stat "+label, clean, err)
	}
	if directory && !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory: %s", label, clean)
	}
	if !directory && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file: %s", label, clean)
	}
	return clean, nil
}

func runtimeMarkdownValidatePathComponents(path, label string) error {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return fmt.Errorf("%s has no Windows volume: %q", label, path)
	}
	remainder := strings.TrimPrefix(path, volume)
	for len(remainder) > 0 && (remainder[0] == '\\' || remainder[0] == '/') {
		remainder = remainder[1:]
	}
	current := volume + string(filepath.Separator)
	for _, part := range strings.FieldsFunc(remainder, func(r rune) bool { return r == '\\' || r == '/' }) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return runtimeMarkdownFilesystemError("inspect "+label, current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s contains symlink: %s", label, current)
		}
		attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
		if !ok || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("%s contains Windows reparse point: %s", label, current)
		}
	}
	return nil
}

func runtimeMarkdownModuleRoot(serverBinary string) (string, error) {
	serverBinary, err := runtimeMarkdownSecurePath(serverBinary, "Markdown server binary", false)
	if err != nil {
		return "", err
	}
	productRoot, err := runtimeMarkdownProductRoot(serverBinary)
	if err != nil {
		return "", err
	}
	if !runtimeMarkdownWithin(productRoot, serverBinary) {
		return "", fmt.Errorf("Markdown server binary escapes locked product root: %s", serverBinary)
	}
	binDir := filepath.Dir(serverBinary)
	if !strings.EqualFold(filepath.Base(binDir), ".bin") {
		return "", fmt.Errorf("Markdown server binary is outside locked npm .bin cohort: %q", serverBinary)
	}
	moduleRoot := filepath.Dir(binDir)
	if !strings.EqualFold(filepath.Base(moduleRoot), "node_modules") {
		return "", fmt.Errorf("Markdown server binary has invalid npm module root: %q", serverBinary)
	}
	return runtimeMarkdownSecurePath(moduleRoot, "Markdown npm module root", true)
}

func runtimeMarkdownRequireExactPackage(moduleRoot string) error {
	packagePath := filepath.Join(moduleRoot, "markdown-it", "package.json")
	lockedPackagePath, err := runtimeMarkdownSecurePath(packagePath, "locked markdown-it package", false)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(lockedPackagePath)
	if err != nil {
		return fmt.Errorf("read locked markdown-it package metadata: %w", err)
	}
	var metadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return fmt.Errorf("decode locked markdown-it package metadata: %w", err)
	}
	if strings.TrimSpace(metadata.Version) != runtimeMarkdownItInstallVersion {
		return fmt.Errorf("markdown-it version %q does not match locked %q", metadata.Version, runtimeMarkdownItInstallVersion)
	}
	return nil
}

func runtimeMarkdownEnvWithNodePath(env []string, moduleRoot string) []string {
	result := make([]string, 0, len(env)+1)
	replaced := false
	for _, value := range env {
		if strings.HasPrefix(strings.ToUpper(value), "NODE_PATH=") {
			result = append(result, "NODE_PATH="+moduleRoot)
			replaced = true
			continue
		}
		result = append(result, value)
	}
	if !replaced {
		result = append(result, "NODE_PATH="+moduleRoot)
	}
	return result
}

const runtimeMarkdownNodeScript = `const fs = require('fs');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const packageJSON = require('markdown-it/package.json');
if (packageJSON.version !== '14.2.0') {
  throw new Error('markdown-it version guard failed: ' + packageJSON.version);
}
const MarkdownIt = require('markdown-it');
const markdown = new MarkdownIt({ html: true, linkify: false, typographer: false });
process.stdout.write(JSON.stringify(markdown.parse(String(input.text), {})));`

type runtimeMarkdownLimitedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	tooLarge bool
}

func (b *runtimeMarkdownLimitedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buffer.Len()+len(value) > b.limit {
		remaining := b.limit - b.buffer.Len()
		if remaining > 0 {
			_, _ = b.buffer.Write(value[:remaining])
		}
		b.tooLarge = true
		return len(value), nil
	}
	return b.buffer.Write(value)
}

func (b *runtimeMarkdownLimitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *runtimeMarkdownLimitedBuffer) String() string {
	return string(b.Bytes())
}

type runtimeWindowsMarkdownClient struct {
	multilsp.Client
	support   *runtimeWindowsMarkdownClientProtocol
	closeOnce sync.Once
	closeErr  error
}

func wrapRuntimeMarkdownClient(client multilsp.Client, support runtimeMarkdownClientSupport) multilsp.Client {
	if support == nil {
		return client
	}
	protocol, ok := support.(*runtimeWindowsMarkdownClientProtocol)
	if !ok {
		return client
	}
	return &runtimeWindowsMarkdownClient{Client: client, support: protocol}
}

func (c *runtimeWindowsMarkdownClient) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return errors.Join(c.support.Close(), c.Client.Shutdown(ctx))
}

func (c *runtimeWindowsMarkdownClient) Initialize(ctx context.Context, rootURI string) error {
	if c == nil || c.Client == nil {
		return errors.New("Markdown LSP client is nil")
	}
	if err := c.Client.Initialize(ctx, rootURI); err != nil {
		return err
	}
	return runtimeMarkdownNotifyConfiguration(ctx, c.Client)
}

func (c *runtimeWindowsMarkdownClient) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closeErr = errors.Join(c.support.Close(), c.Client.Close())
	})
	return c.closeErr
}

// UnderlyingLSPClient 保留原始 transport owner，供 manager 进程树与资源观察穿透 wrapper。
func (c *runtimeWindowsMarkdownClient) UnderlyingLSPClient() multilsp.Client {
	if c == nil {
		return nil
	}
	return c.Client
}

// Healthy 穿透真实 LSP client 的健康状态，不把 Markdown watcher 状态伪装成 server capability。
func (c *runtimeWindowsMarkdownClient) Healthy() bool {
	if c == nil || c.Client == nil {
		return false
	}
	if c.support == nil || !c.support.Healthy() {
		return false
	}
	health, ok := c.Client.(multilsp.HealthCheckedClient)
	return ok && health.Healthy()
}

// ServerCapabilities 穿透真实 Markdown language server 的 initialize capability 快照。
func (c *runtimeWindowsMarkdownClient) ServerCapabilities() protocol.ServerCapabilities {
	if c == nil || c.Client == nil {
		return protocol.ServerCapabilities{}
	}
	capabilities, ok := c.Client.(multilsp.ServerCapabilitiesClient)
	if !ok {
		return protocol.ServerCapabilities{}
	}
	return capabilities.ServerCapabilities()
}

var (
	_ multilsp.Client                   = (*runtimeWindowsMarkdownClient)(nil)
	_ multilsp.WrappedClient            = (*runtimeWindowsMarkdownClient)(nil)
	_ multilsp.HealthCheckedClient      = (*runtimeWindowsMarkdownClient)(nil)
	_ multilsp.ServerCapabilitiesClient = (*runtimeWindowsMarkdownClient)(nil)
)
