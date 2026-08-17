package lineprotocol

import "testing"

func TestParseRejectsEmptyRecordKind(t *testing.T) {
	_, err := Parse("OK total=1 showing=1 truncated=0 unit=diagnostic\n\tfile=a.go")
	if err == nil {
		t.Fatal("Parse() accepted an empty record kind")
	}
}
