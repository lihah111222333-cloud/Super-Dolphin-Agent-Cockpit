package datasource

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"testing"
)

func TestExtractPDFStreamsPreservesFlateBinaryTail(t *testing.T) {
	for _, separator := range []string{"\r", "\n", "\r\n"} {
		for _, tail := range []byte{'\r', '\n', 0x7f} {
			decoded, compressed := compressedPDFPayloadWithTail(t, tail)
			content := fmt.Appendf(nil, "%%PDF-1.7\n1 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream%s", len(compressed), separator)
			content = append(content, compressed...)
			content = append(content, []byte(separator+"endstream\nendobj\n")...)
			streams, err := extractPDFStreams(content)
			if err != nil {
				t.Fatalf("separator=%q tail=%#x extractPDFStreams() error = %v", separator, tail, err)
			}
			if len(streams) != 1 || !bytes.Equal(streams[0], decoded) {
				t.Fatalf("separator=%q tail=%#x streams = %#v, want exact decoded payload", separator, tail, streams)
			}
		}
	}
}

func TestExtractPDFStreamBodyRejectsInvalidDirectLength(t *testing.T) {
	t.Run("declared boundary mismatch", func(t *testing.T) {
		_, _, err := extractPDFStreamBody([]byte("abc\nendstream"), 0, []byte("<< /Length 2 >>"))
		if err == nil {
			t.Fatal("extractPDFStreamBody() error = nil")
		}
	})
	t.Run("truncated", func(t *testing.T) {
		_, _, err := extractPDFStreamBody([]byte("abc"), 0, []byte("<< /Length 9 >>"))
		if err == nil {
			t.Fatal("extractPDFStreamBody() error = nil")
		}
	})
	t.Run("limit", func(t *testing.T) {
		_, _, err := extractPDFStreamBody(nil, 0, fmt.Appendf(nil, "<< /Length %d >>", datasourceMaxImportBytes+1))
		if !errors.Is(err, errDatasourceTextTooLarge) {
			t.Fatalf("extractPDFStreamBody() error = %v, want %v", err, errDatasourceTextTooLarge)
		}
	})
}

func TestPDFDirectStreamLengthIgnoresLength1(t *testing.T) {
	length, found, err := pdfDirectStreamLength([]byte("<< /Filter /FlateDecode /Length 14051 /Length1 45468 >>"))
	if err != nil || !found || length != 14051 {
		t.Fatalf("pdfDirectStreamLength() = (%d, %t, %v), want (14051, true, nil)", length, found, err)
	}
}

func TestPDFStreamDefinitelyNonText(t *testing.T) {
	for _, dictionary := range [][]byte{
		[]byte("<< /Length 10 /Length1 100 >>"),
		[]byte("<</Subtype\t/Image/Length 10>>"),
	} {
		if !pdfStreamDefinitelyNonText(dictionary) {
			t.Fatalf("pdfStreamDefinitelyNonText(%q) = false", dictionary)
		}
	}
	if pdfStreamDefinitelyNonText([]byte("<< /Subtype /Form /Length 10 >>")) {
		t.Fatal("form stream was classified as non-text")
	}
}

func TestExtractPDFStreamBodySupportsMarkerForMissingOrIndirectLength(t *testing.T) {
	for _, dictionary := range [][]byte{[]byte("<< >>"), []byte("<< /Length 9 0 R >>")} {
		body, _, err := extractPDFStreamBody([]byte("plain\nendstream"), 0, dictionary)
		if err != nil || !bytes.Equal(body, []byte("plain\n")) {
			t.Fatalf("dictionary=%q body=%q error=%v", dictionary, body, err)
		}
	}
}

func compressedPDFPayloadWithTail(t *testing.T, want byte) ([]byte, []byte) {
	t.Helper()
	for second := range 256 {
		// zlib trailer 的最后一个字节是 Adler-32 的低字节。将第一个 tail 字节固定为零，
		// 仍可覆盖所有可能的 trailer 字节，同时避免每个 fixture 之前的 65,536 次压缩尝试。
		decoded := append([]byte("BT (tail-safe) Tj ET\n%"), 0, byte(second))
		var buffer bytes.Buffer
		writer := zlib.NewWriter(&buffer)
		if _, err := writer.Write(decoded); err != nil {
			t.Fatalf("compress PDF fixture: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close PDF fixture compressor: %v", err)
		}
		compressed := buffer.Bytes()
		if compressed[len(compressed)-1] == want {
			return decoded, append([]byte(nil), compressed...)
		}
	}
	t.Fatalf("could not build compressed PDF fixture ending in %#x", want)
	return nil, nil
}
