//go:build !darwin

package appupdatefailure

import (
	"errors"
	"testing"
)

func TestSidecarIsExplicitlyUnsupportedOutsideDarwin(t *testing.T) {
	if !errors.Is(Begin("/unused", "00112233445566778899aabbccddeeff"), ErrUnsupported) {
		t.Fatal("Begin() did not return ErrUnsupported")
	}
	if _, _, err := ReadFailure("/unused"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("ReadFailure() did not return ErrUnsupported")
	}
	if !errors.Is(InvalidateAll("/unused"), ErrUnsupported) {
		t.Fatal("InvalidateAll() did not return ErrUnsupported")
	}
}
