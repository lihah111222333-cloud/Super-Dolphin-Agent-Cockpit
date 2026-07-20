//go:build linux

package pidregistry

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListLinuxProcessIDsReadsOnlyCanonicalProcessDirectories(t *testing.T) {
	procRoot := t.TempDir()
	for _, name := range []string{"2", "101", "not-a-pid"} {
		if err := os.Mkdir(filepath.Join(procRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(procRoot, "55"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := listLinuxProcessIDs(procRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 101}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listLinuxProcessIDs() = %#v, want %#v", got, want)
	}
}

func TestReadLinuxProcessArgumentsStrictlyParsesCmdline(t *testing.T) {
	procRoot := t.TempDir()
	cmdlinePath := filepath.Join(procRoot, "42", "cmdline")
	if err := os.MkdirAll(filepath.Dir(cmdlinePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cmdlinePath, []byte("process\x00--exact-token\x00\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readLinuxProcessArguments(procRoot, 42)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"process", "--exact-token", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readLinuxProcessArguments() = %#v, want %#v", got, want)
	}
}

func TestReadLinuxProcessArgumentsClassifiesGoneAndRealErrors(t *testing.T) {
	procRoot := t.TempDir()
	_, err := readLinuxProcessArguments(procRoot, 42)
	if !errors.Is(err, ErrStableProcessNotFound) {
		t.Fatalf("missing cmdline error = %v, want ErrStableProcessNotFound", err)
	}

	notDirectory := filepath.Join(procRoot, "not-a-directory")
	if err := os.WriteFile(notDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readLinuxProcessArguments(notDirectory, 42)
	if err == nil || errors.Is(err, ErrStableProcessNotFound) {
		t.Fatalf("non-directory proc root error = %v, want real read error", err)
	}
}

func TestParseLinuxProcCmdlineRejectsUnterminatedArguments(t *testing.T) {
	if got, err := parseLinuxProcCmdline([]byte("process\x00--exact-token")); err == nil {
		t.Fatalf("parseLinuxProcCmdline() = %#v, want error", got)
	}
}
