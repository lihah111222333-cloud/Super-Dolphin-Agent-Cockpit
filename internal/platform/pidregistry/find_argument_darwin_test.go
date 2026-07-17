//go:build darwin

package pidregistry

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

func TestParseDarwinProcargs2StopsAfterArgc(t *testing.T) {
	raw := darwinProcargsFixture(2, "/Applications/Test.app/Contents/MacOS/test", []string{"test", "--exact-token"}, []string{
		"FORGED=--environment-is-not-argv",
	})
	got, err := parseDarwinProcargs2(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"test", "--exact-token"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDarwinProcargs2() = %#v, want %#v", got, want)
	}
}

func TestParseDarwinProcargs2RejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "missing argc", raw: []byte{1, 2, 3}},
		{name: "zero argc", raw: darwinProcargsFixture(0, "/bin/test", nil, nil)},
		{name: "argc overflow", raw: darwinProcargsFixture(math.MaxUint32, "/bin/test", nil, nil)},
		{name: "truncated executable", raw: appendDarwinArgc(1, []byte("/bin/test")...)},
		{name: "truncated argv", raw: appendDarwinArgc(1, append([]byte("/bin/test\x00\x00"), []byte("test")...)...)},
		{name: "argc exceeds payload", raw: darwinProcargsFixture(2, "/bin/test", []string{"test"}, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseDarwinProcargs2(tt.raw); err == nil {
				t.Fatalf("parseDarwinProcargs2() = %#v, want error", got)
			}
		})
	}
}

func darwinProcargsFixture(argc uint32, executable string, argv, environ []string) []byte {
	raw := appendDarwinArgc(argc, []byte(executable)...)
	raw = append(raw, 0, 0)
	for _, value := range append(argv, environ...) {
		raw = append(raw, value...)
		raw = append(raw, 0)
	}
	return raw
}

func appendDarwinArgc(argc uint32, tail ...byte) []byte {
	raw := make([]byte, 4, 4+len(tail))
	binary.NativeEndian.PutUint32(raw, argc)
	return append(raw, tail...)
}
