package lineprotocol

import (
	"os"
	"strings"
	"testing"
)

// TestLineProtocolV1CompatibilityContract 用 golden、往返与拒绝集冻结第一版文本语法。
func TestLineProtocolV1CompatibilityContract(t *testing.T) {
	if grammarVersion != 1 {
		t.Fatalf("grammarVersion = %d, want 1", grammarVersion)
	}
	t.Run("golden and roundtrip", testV1GoldenAndRoundtrip)
	t.Run("source guard", testV1SourceGuard)
	t.Run("invalid UTF-8 byte roundtrip", testV1InvalidUTF8Roundtrip)
	t.Run("ERROR document", testV1ErrorDocument)
	t.Run("strict rejection", testV1StrictRejection)
	t.Run("unknown record extension", testV1UnknownRecordExtension)
}

func testV1SourceGuard(t *testing.T) {
	source, err := os.ReadFile("protocol.go")
	if err != nil {
		t.Fatalf("read protocol source: %v", err)
	}
	for _, forbidden := range []string{"net/url", "QueryEscape", "QueryUnescape", "protocol=lsp-text", "version=1"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("protocol source retained forbidden wire/compat token %q", forbidden)
		}
	}
}

func testV1InvalidUTF8Roundtrip(t *testing.T) {
	value := string([]byte{'a', 0xff, 'b'})
	encoded := TextRecord("MESSAGE", value)
	if encoded != "MESSAGE\ta\\xFFb" {
		t.Fatalf("invalid UTF-8 escape = %q", encoded)
	}
	doc, err := Parse("OK total=0 showing=0 truncated=0 unit=bytes\n" + encoded)
	if err != nil {
		t.Fatalf("Parse(invalid UTF-8 escaped byte) error = %v", err)
	}
	if got := doc.Records[0].Value; got != value {
		t.Fatalf("invalid UTF-8 roundtrip bytes = %x, want %x", []byte(got), []byte(value))
	}
	raw := "OK total=0 showing=0 truncated=0 unit=bytes\nMESSAGE\t" + value
	if _, err := Parse(raw); err == nil {
		t.Fatal("Parse() accepted raw invalid UTF-8 byte")
	}
}

func testV1GoldenAndRoundtrip(t *testing.T) {
	value := "路径 /tmp/a+b%=中文\\code\tline\n雪\x01\x7f\u0085"
	got := strings.Join([]string{
		HeaderLine(1, 1, false, "example"),
		FieldsRecord("ROW", Field{Key: "line", Value: "1"}, Field{Key: "text", Value: value}),
		TextRecord("MESSAGE", value),
	}, "\n")
	want := strings.Join([]string{
		"OK total=1 showing=1 truncated=0 unit=example",
		"ROW\tline=1\ttext=路径 /tmp/a+b%=中文\\\\code\\tline\\n雪\\x01\\x7F\\u{85}",
		"MESSAGE\t路径 /tmp/a+b%=中文\\\\code\\tline\\n雪\\x01\\x7F\\u{85}",
	}, "\n")
	if got != want {
		t.Fatalf("v1 golden drift\ngot:  %q\nwant: %q", got, want)
	}
	doc, err := Parse(got)
	if err != nil {
		t.Fatalf("Parse(v1 golden) error = %v", err)
	}
	if doc.Records[0].Fields["text"] != value || doc.Records[1].Value != value {
		t.Fatalf("v1 roundtrip lost dynamic text: %#v", doc.Records)
	}
	reordered := "OK unit=example truncated=0 showing=1 total=1\nROW\ttext=value\tline=1"
	if _, err := Parse(reordered); err != nil {
		t.Fatalf("Parse() rejected semantically valid reordered fields: %v", err)
	}
}

func testV1ErrorDocument(t *testing.T) {
	header := ErrorLine("lsp_timeout", true)
	if header != "ERROR code=lsp_timeout retryable=1" {
		t.Fatalf("ERROR v1 header = %q", header)
	}
	text := header + "\n" +
		TextRecord("MESSAGE", "timeout + retry %\t雪") + "\n" +
		TextRecord("HINT", "retry after narrowing scope") + "\n" +
		FieldsRecord("ATTR", Field{Key: "tool", Value: "patch_edit"})
	doc, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse(ERROR v1 document) error = %v; text=%q", err, text)
	}
	if len(doc.Records) != 3 || doc.Records[0].Value != "timeout + retry %\t雪" {
		t.Fatalf("ERROR v1 records = %#v", doc.Records)
	}
}

func testV1StrictRejection(t *testing.T) {
	cases := map[string]string{
		"missing header":         "ROW\tline=1",
		"duplicate OK field":     "OK total=1 total=1 showing=1 truncated=0 unit=row\nROW\tline=1",
		"unknown OK field":       "OK total=1 showing=1 truncated=0 unit=row version=1\nROW\tline=1",
		"duplicate ERROR field":  "ERROR code=x retryable=1 retryable=0\nMESSAGE\tx",
		"unknown ERROR field":    "ERROR code=x retryable=0 version=1\nMESSAGE\tx",
		"ERROR missing MESSAGE":  "ERROR code=x retryable=0\nHINT\tonly-hint",
		"bad text escape":        "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\\q",
		"bad field escape":       "OK total=1 showing=1 truncated=0 unit=row\nROW\ttext=bad\\q",
		"truncated escape":       "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\\",
		"short hex escape":       "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\\x0",
		"invalid hex escape":     "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\\xGG",
		"empty unicode escape":   "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\\u{}",
		"invalid unicode scalar": "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\\u{110000}",
		"raw control":            "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tbad\x01",
		"raw CR":                 "OK total=0 showing=0 truncated=0 unit=row\r",
		"raw NUL":                "OK total=0 showing=0 truncated=0 unit=row\x00",
		"malformed MESSAGE":      "OK total=0 showing=0 truncated=0 unit=row\nMESSAGE\tone\ttwo",
		"empty known ROW":        "OK total=0 showing=0 truncated=0 unit=row\nROW",
		"empty known ATTR":       "OK total=0 showing=0 truncated=0 unit=row\nATTR",
		"malformed ROW":          "OK total=1 showing=1 truncated=0 unit=row\nROW\tmissing_equals",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(text); err == nil {
				t.Fatalf("Parse() accepted malformed v1 document %q", text)
			}
		})
	}
}

func testV1UnknownRecordExtension(t *testing.T) {
	text := "OK total=0 showing=0 truncated=0 unit=extension\nFUTURE\tfeature=enabled\tnote=space + snow 雪"
	doc, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse() rejected forward-compatible uppercase record: %v", err)
	}
	if len(doc.Records) != 1 || doc.Records[0].Kind != "FUTURE" || doc.Records[0].Fields["note"] != "space + snow 雪" {
		t.Fatalf("unknown record roundtrip = %#v", doc.Records)
	}
	if _, err := Parse("OK total=0 showing=0 truncated=0 unit=extension\nFUTURE\tnote=bad\\q"); err == nil {
		t.Fatal("Parse() accepted malformed unknown extension record")
	}
}
