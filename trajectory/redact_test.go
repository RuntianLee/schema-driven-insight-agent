package trajectory

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedact_PlayerIDHash(t *testing.T) {
	in := Event{Kind: "tool_call", Input: "player a1b2c3d4e5f60789 balance"}
	out := Redact(in)
	s := out.Input.(string)
	if strings.Contains(s, "a1b2c3d4e5f60789") {
		t.Fatalf("16-hex player hash must be masked: %s", s)
	}
	if !strings.Contains(s, "<player>") {
		t.Fatalf("expected <player> token: %s", s)
	}
}

func TestRedact_SampleRowsTruncatedTo10(t *testing.T) {
	rows := make([]any, 25)
	for i := range rows {
		rows[i] = map[string]any{"i": i}
	}
	out := Redact(Event{Kind: "tool_call", Output: rows})
	got := out.Output.([]any)
	if len(got) != 10 {
		t.Fatalf("sample rows must truncate to 10, got %d", len(got))
	}
}

func TestRedact_SQLWhereLiteralMasked(t *testing.T) {
	long := "SELECT * FROM t WHERE name = 'this_is_a_very_long_secret_literal_value'"
	out := Redact(Event{Kind: "tool_call", Output: long})
	s := out.Output.(string)
	if strings.Contains(s, "this_is_a_very_long_secret_literal_value") {
		t.Fatalf("long WHERE literal (>20 bytes) must be masked: %s", s)
	}
}

func TestRedact_PlayerDigitsNearKeyword(t *testing.T) {
	out := Redact(Event{Kind: "reasoning", Input: "玩家 1234567 余额异常; player id 9876543"})
	s := out.Input.(string)
	if strings.Contains(s, "1234567") || strings.Contains(s, "9876543") {
		t.Fatalf("6+ digit player ids near keyword must be masked: %s", s)
	}
}

func TestRedact_DoesNotMutateInput(t *testing.T) {
	orig := "player a1b2c3d4e5f60789"
	ev := Event{Kind: "x", Input: orig}
	_ = Redact(ev)
	if ev.Input.(string) != orig {
		t.Fatal("Redact must not mutate the original Event")
	}
}

// TestRedact_TypedStructTraversed verifies that a typed Go struct/slice (not []any)
// is normalized via JSON round-trip so redactValue can traverse it and mask PII.
func TestRedact_TypedStructTraversed(t *testing.T) {
	type row struct {
		PlayerID string `json:"player_id"`
	}
	out := Redact(Event{Kind: "tool_call", Output: []row{{PlayerID: "a1b2c3d4e5f60789"}}})

	// out.Output is []any after normalize; marshal then unmarshal to inspect values.
	b, err := json.Marshal(out.Output)
	if err != nil {
		t.Fatalf("marshal redacted output: %v", err)
	}
	// Verify raw hash is gone.
	if strings.Contains(string(b), "a1b2c3d4e5f60789") {
		t.Fatalf("16-hex player hash in typed struct must be masked, got: %s", b)
	}
	// Unmarshal back to check actual string value (avoids HTML-escape confusion).
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
	got, _ := rows[0]["player_id"].(string)
	if got != "<player>" {
		t.Fatalf("expected player_id=<player>, got: %q", got)
	}
}

// TestRedact_UppercaseHashMasked verifies uppercase hex hashes are also masked.
func TestRedact_UppercaseHashMasked(t *testing.T) {
	in := Event{Kind: "tool_call", Input: "player A1B2C3D4E5F60789 balance"}
	out := Redact(in)
	s := out.Input.(string)
	if strings.Contains(s, "A1B2C3D4E5F60789") {
		t.Fatalf("uppercase 16-hex player hash must be masked: %s", s)
	}
	if !strings.Contains(s, "<player>") {
		t.Fatalf("expected <player> token: %s", s)
	}
}
