package identifier

import "testing"

func TestLooksLikeUUID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"whitespace", "   ", false},
		{"agent placeholder", "agent_1778254389737948000", false},
		{"short hex", "abcdef0123", false},
		{"32 hex no dash", "0123456789abcdef0123456789abcdef", true},
		{"v4 uuid lower", "11111111-2222-3333-4444-555555555555", true},
		{"v4 uuid upper", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", true},
		{"v4 uuid with surrounding spaces", "  11111111-2222-3333-4444-555555555555  ", true},
		{"non-hex char inside", "11111111-2222-3333-4444-55555555555g", false},
		{"underscore not allowed", "11111111_2222_3333_4444_555555555555", false},
		{"31 hex too short", "0123456789abcdef0123456789abcde", false},
		{"v4 uuid with extra dashes still ok if hex>=32", "1-1-1-1-1-1-1-1-1111-1111-1111-2222-3333-4444-555555555555", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LooksLikeUUID(c.in); got != c.want {
				t.Fatalf("LooksLikeUUID(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestIsClaudeCLISessionUUID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"agent placeholder", "agent_1778254389737948000", false},
		{"v4 uuid lower", "11111111-2222-3333-4444-555555555555", true},
		{"v4 uuid upper", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", true},
		{"v4 uuid trimmed", "  11111111-2222-3333-4444-555555555555\t", true},
		{"32 hex no dash rejected", "0123456789abcdef0123456789abcdef", false},
		{"loose dash form rejected", "1-1-1-1-1-1-1-1-1111-1111-1111-2222-3333-4444-555555555555", false},
		{"too short", "11111111-2222-3333-4444-55555555555", false},
		{"non-hex inside", "11111111-2222-3333-4444-55555555555g", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsClaudeCLISessionUUID(c.in); got != c.want {
				t.Fatalf("IsClaudeCLISessionUUID(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
