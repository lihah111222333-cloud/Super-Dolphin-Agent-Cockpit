package multilsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// TestTransportReadMessageLogsMalformedHeaderWithoutPayload 验证 stdout 污染可定位且不会泄露原始行或 stderr。
func TestTransportReadMessageLogsMalformedHeaderWithoutPayload(t *testing.T) {
	const contaminated = "IJ_JAVA_OPTIONS=secret-value"
	var logs bytes.Buffer
	stderr := &limitedBuffer{limit: 8}
	if _, err := stderr.Write([]byte("stderr-secret")); err != nil {
		t.Fatalf("write stderr fixture: %v", err)
	}
	tr := &transport{
		stdout: bufio.NewReader(strings.NewReader(contaminated + "\r\n\r\n")),
		stderr: stderr,
		logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	_, err := tr.readMessage()
	var framingErr *lspFramingError
	if !errors.As(err, &framingErr) {
		t.Fatalf("readMessage() error = %v, want lspFramingError", err)
	}
	if framingErr.kind != "malformed_header" || framingErr.observedSHA256 == "" {
		t.Fatalf("framing diagnostic = %+v", framingErr)
	}
	if strings.Contains(err.Error(), contaminated) {
		t.Fatalf("framing error leaked stdout payload: %v", err)
	}
	tr.logReadFailure(err)
	output := logs.String()
	for _, want := range []string{
		"\"event\":\"lsp_stdio_read\"",
		"\"failure_kind\":\"malformed_header\"",
		"\"observed_sha256\":\"" + framingErr.observedSHA256 + "\"",
		"\"stderr_truncated\":true",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("read diagnostic log missing %q: %s", want, output)
		}
	}
	for _, secret := range []string{contaminated, "secret-value", "stderr-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("read diagnostic log leaked %q: %s", secret, output)
		}
	}
}

// TestTransportLifecycleFieldsIncludeExitCodeAndServerRole 验证 wait 后的安全日志可区分服务角色与真实退出码。
func TestTransportLifecycleFieldsIncludeExitCodeAndServerRole(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run exited helper process: %v", err)
	}
	fields := transportLifecycleLogFields("server.exe", "workspace", 2, 3, cmd, nil)
	values := make(map[string]any, len(fields)/2)
	for index := 0; index+1 < len(fields); index += 2 {
		key, ok := fields[index].(string)
		if ok {
			values[key] = fields[index+1]
		}
	}
	if values["server_role"] != "language_server_stdio" || values["process_exited"] != true || values["exit_code_present"] != true || values["exit_code"] != 0 {
		t.Fatalf("transport lifecycle fields = %#v, want role and exited code 0", values)
	}
}

// TestTransportReleaseLogsZeroRemainingEvidence 验证 exact owner 证明零成员后，日志明确记录 Remaining=0 与 released。
func TestTransportReleaseLogsZeroRemainingEvidence(t *testing.T) {
	var logs bytes.Buffer
	owner := &convergedTerminationFailureOwner{}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner
	tr.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	if err := tr.Close(); err != nil {
		t.Fatalf("Close() after verified natural exit: %v", err)
	}
	output := logs.String()
	for _, want := range []string{
		`"event":"lsp_process_tree_release"`,
		`"release_result":"released"`,
		`"remaining_checked":true`,
		`"remaining_count_present":true`,
		`"remaining_count":0`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("release evidence log missing %q: %s", want, output)
		}
	}
}

// TestTransportPrepareProcessTreeShutdownUsesExactOwner 验证关闭协议只调用 transport 持有的 exact owner。
func TestTransportPrepareProcessTreeShutdownUsesExactOwner(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	tr := &transport{processTree: owner}

	if err := tr.prepareProcessTreeShutdown(); err != nil {
		t.Fatalf("prepareProcessTreeShutdown() error = %v", err)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
}

func TestTransportPrepareProcessTreeShutdownSkipsReleasedOwner(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	tr := &transport{processTree: owner, treeReleased: true}

	if err := tr.prepareProcessTreeShutdown(); err != nil {
		t.Fatalf("prepareProcessTreeShutdown() after release error = %v", err)
	}
	if got := owner.prepareCalls.Load(); got != 0 {
		t.Fatalf("PrepareShutdown() calls after release = %d, want 0", got)
	}
}

// TestTransportPrepareProcessTreeShutdownPropagatesOwnerError 验证 owner 无法安全入册时关闭请求立即失败。
func TestTransportPrepareProcessTreeShutdownPropagatesOwnerError(t *testing.T) {
	want := errors.New("owner preparation failed")
	owner := &countingProcessTreeOwner{prepareErr: want}
	tr := &transport{processTree: owner}

	err := tr.prepareProcessTreeShutdown()
	if !errors.Is(err, want) {
		t.Fatalf("prepareProcessTreeShutdown() error = %v, want %v", err, want)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
}

// TestClientShutdownUninitializedPreparesExactOwner 验证未初始化 client 也必须先完成 owner 入册。
func TestClientShutdownUninitializedPreparesExactOwner(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	c := &client{transport: &transport{processTree: owner}}

	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
	if !c.isShutdown() {
		t.Fatal("Shutdown() did not mark uninitialized client as shutdown")
	}
}

// TestClientShutdownPreparationFailureSkipsExitAndPreservesOwner 验证准备失败时不发送 exit，Close 仍通过 exact owner 收敛。
func TestClientShutdownPreparationFailureSkipsExitAndPreservesOwner(t *testing.T) {
	want := errors.New("owner preparation failed")
	owner := &countingProcessTreeOwner{prepareErr: want}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	close(done)
	var tr *transport
	writer := &shutdownRecordingWriter{cancel: cancel}
	tr = &transport{
		processTree: owner,
		stdin:       writer,
		pending:     map[string]chan pendingResult{},
		logger:      logger,
		done:        done,
	}
	writer.transport = tr
	c := &client{
		transport:   tr,
		initialized: true,
	}

	err := c.Shutdown(ctx)
	assertPreparationFailureProtocolState(t, c, owner, writer, ctx, err, want)
	assertPreparationFailureLogs(t, logs.Bytes(), want)
	assertPreparationFailureClose(t, c, owner)
}

func assertPreparationFailureProtocolState(
	t *testing.T,
	c *client,
	owner *countingProcessTreeOwner,
	writer *shutdownRecordingWriter,
	ctx context.Context,
	err error,
	want error,
) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Shutdown() error = %v, want %v", err, want)
	}
	if got := owner.prepareCalls.Load(); got != 1 {
		t.Fatalf("PrepareShutdown() calls = %d, want 1", got)
	}
	if got := writer.methodsSnapshot(); len(got) != 1 || got[0] != "shutdown" {
		t.Fatalf("protocol methods after preparation failure = %v, want [shutdown]", got)
	}
	if c.isShutdown() {
		t.Fatal("Shutdown() marked client shutdown before exact-owner cleanup")
	}
	if ctx.Err() != nil {
		t.Fatalf("Shutdown() sent exit and canceled test context: %v", ctx.Err())
	}
}

func assertPreparationFailureLogs(t *testing.T, logs []byte, want error) {
	t.Helper()
	for _, stage := range []string{"prepare", "protocol_shutdown", "protocol_exit"} {
		if !bytes.Contains(logs, []byte("\"stage\":\""+stage+"\"")) {
			t.Fatalf("shutdown logs missing stage %q: %s", stage, logs)
		}
	}
	if !bytes.Contains(logs, []byte("\"stage\":\"protocol_exit\",\"action_result\":\"skipped\"")) {
		t.Fatalf("shutdown logs did not mark protocol exit skipped: %s", logs)
	}
	if bytes.Contains(logs, []byte(want.Error())) {
		t.Fatalf("shutdown logs leaked preparation error: %s", logs)
	}
}

func assertPreparationFailureClose(t *testing.T, c *client, owner *countingProcessTreeOwner) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Fatalf("Close() after preparation failure error = %v", err)
	}
	if got := owner.terminateCalls.Load(); got != 1 {
		t.Fatalf("Terminate() calls = %d, want 1", got)
	}
	if got := owner.releaseCalls.Load(); got != 1 {
		t.Fatalf("Release() calls = %d, want 1", got)
	}
}

type shutdownRecordingWriter struct {
	mu        sync.Mutex
	transport *transport
	cancel    context.CancelFunc
	buffer    bytes.Buffer
	methods   []string
}

func (w *shutdownRecordingWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.buffer.Write(payload)
	for {
		envelope, complete, err := w.nextEnvelope()
		if err != nil {
			return 0, err
		}
		if !complete {
			break
		}
		if err := w.recordEnvelope(envelope); err != nil {
			return 0, err
		}
	}
	return len(payload), nil
}

func (w *shutdownRecordingWriter) nextEnvelope() (protocol.Envelope, bool, error) {
	frame := w.buffer.Bytes()
	headerEnd := bytes.Index(frame, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		return protocol.Envelope{}, false, nil
	}
	var contentLength int
	if _, err := fmt.Sscanf(string(frame[:headerEnd]), "Content-Length: %d", &contentLength); err != nil {
		return protocol.Envelope{}, false, err
	}
	bodyStart := headerEnd + 4
	if len(frame) < bodyStart+contentLength {
		return protocol.Envelope{}, false, nil
	}
	body := append([]byte(nil), frame[bodyStart:bodyStart+contentLength]...)
	w.buffer.Next(bodyStart + contentLength)
	var envelope protocol.Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return protocol.Envelope{}, false, err
	}
	return envelope, true, nil
}

func (w *shutdownRecordingWriter) recordEnvelope(envelope protocol.Envelope) error {
	if envelope.Method == "" {
		return nil
	}
	w.methods = append(w.methods, envelope.Method)
	switch envelope.Method {
	case "shutdown":
		return w.respondToShutdown(envelope.ID)
	case methodExit:
		if w.cancel != nil {
			w.cancel()
		}
	}
	return nil
}

func (w *shutdownRecordingWriter) respondToShutdown(id json.RawMessage) error {
	response, err := protocol.BuildSuccessResponse(id, nil)
	if err != nil {
		return err
	}
	responsePayload, err := protocol.EncodeMessage(response)
	if err != nil {
		return err
	}
	return w.transport.handleResponse(responsePayload)
}

func (w *shutdownRecordingWriter) Close() error { return nil }

func (w *shutdownRecordingWriter) methodsSnapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.methods...)
}
