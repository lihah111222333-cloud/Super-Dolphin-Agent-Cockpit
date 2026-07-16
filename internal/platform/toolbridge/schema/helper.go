package schema

import (
	"encoding/json"
	"fmt"
	"io"
)

// ServeOneShot 读取一个严格请求、执行一次 compile/validate 并在写出一个响应后返回。
func ServeOneShot(stdin io.Reader, stdout io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(stdin, maxEnvelopeBytes+1))
	if err != nil {
		return fmt.Errorf("read helper request: %w", err)
	}
	if len(raw) > maxEnvelopeBytes {
		return newDiagnostic(CodeInputTooLarge, "helper request exceeds 384 KiB", nil)
	}
	request, err := decodeProtocolRequest(raw)
	if err != nil {
		return err
	}
	response := executeLocal(request)
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("marshal helper response: %w", err)
	}
	if len(encoded) > maxStdoutBytes {
		return newDiagnostic(CodeOutputTooLarge, "helper response exceeds 64 KiB", nil)
	}
	written, err := stdout.Write(encoded)
	if err != nil {
		return fmt.Errorf("write helper response: %w", err)
	}
	if written != len(encoded) {
		return io.ErrShortWrite
	}
	return nil
}
