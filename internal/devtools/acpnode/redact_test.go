package acpnode

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type cycleError struct{}

func (*cycleError) Error() string   { return "cycle-secret" }
func (e *cycleError) Unwrap() error { return e }

func TestRedactorHandlesNilTypedNilJSONAndErrorCycles(t *testing.T) {
	r, err := NewRedactor()
	if err != nil {
		t.Fatal(err)
	}
	var typedNil *string
	if got := r.LogValue(typedNil); got != nil {
		t.Fatalf("typed nil = %#v, want nil", got)
	}
	cyclic := map[string]any{"secret-key": "secret-value"}
	cyclic["self"] = cyclic
	value := map[string]any{
		"plain":      "secret-plain",
		"json":       `{"token":"secret-token","nested":["secret-nested"]}`,
		"double":     `"{\"password\":\"secret-double\"}"`,
		"cycle":      cyclic,
		"error":      errors.Join(errors.New("secret-one"), fmt.Errorf("wrapped: %w", errors.New("secret-two"))),
		"cycleError": (*cycleError)(nil),
	}
	encoded, err := json.Marshal(r.LogValue(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, plain := range []string{"secret-plain", "secret-token", "secret-nested", "secret-double", "secret-one", "secret-two", "secret-key", "password"} {
		if strings.Contains(text, plain) {
			t.Fatalf("redacted output leaked %q: %s", plain, text)
		}
	}
}

func TestRedactorSaltIsProcessRandomAndBoundsOutput(t *testing.T) {
	first, err := NewRedactor()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRedactor()
	if err != nil {
		t.Fatal(err)
	}
	a := first.LogValue("same-secret")
	b := second.LogValue("same-secret")
	if a == b {
		t.Fatalf("redactors share a salt: %v", a)
	}
	large := strings.Repeat("secret", maxRedactBytes)
	encoded, err := json.Marshal(first.LogValue(large))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 256 || strings.Contains(string(encoded), "secret") {
		t.Fatalf("large output is not bounded/redacted: %d bytes", len(encoded))
	}
	deep := any("secret-deep")
	for range maxRedactDepth + 4 {
		deep = []any{deep}
	}
	if text := fmt.Sprint(first.LogValue(deep)); strings.Contains(text, "secret-deep") {
		t.Fatalf("deep output leaked plaintext: %s", text)
	}
}
