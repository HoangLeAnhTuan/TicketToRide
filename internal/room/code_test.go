package room_test

import (
	"testing"

	"ticket-to-ride/internal/room"
)

func TestRoomCodeGenerationAndValidation(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		code, err := room.GenerateRoomCode()
		if err != nil {
			t.Fatalf("GenerateRoomCode failed: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("expected length 6, got %d (%s)", len(code), code)
		}
		norm, err := room.NormalizeRoomCode(code)
		if err != nil {
			t.Fatalf("NormalizeRoomCode rejected generated code %s: %v", code, err)
		}
		if norm != code {
			t.Fatalf("expected %s, got %s", code, norm)
		}
		seen[code] = true
	}
	// With 30^6 = 729 million combinations, 500 codes should be unique
	if len(seen) != 500 {
		t.Errorf("collision in 500 random codes: %d unique", len(seen))
	}

	// Ambiguous characters must be rejected
	for _, bad := range []string{"ABCDE0", "ABCDE1", "ABCDEI", "ABCDEO", "abc", "TOOLONG123", "!@#$%^"} {
		if _, err := room.NormalizeRoomCode(bad); err == nil {
			t.Errorf("expected rejection for invalid code %q", bad)
		}
	}

	// Lowercase valid characters should normalize to uppercase
	norm, err := room.NormalizeRoomCode("abcdef")
	if err == nil && norm != "ABCDEF" {
		t.Errorf("expected ABCDEF, got %s", norm)
	}
}
