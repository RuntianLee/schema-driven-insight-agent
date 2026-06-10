package etl

import "testing"

func TestHashPID_StableAndSaltScoped(t *testing.T) {
	a := HashPID("salt_v0", 12345)
	b := HashPID("salt_v0", 12345)
	if a != b {
		t.Errorf("same salt+pid must be stable: %q != %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("expected 16-char hash, got %d", len(a))
	}
	if HashPID("salt_v1", 12345) == a {
		t.Errorf("different salt must produce different hash")
	}
}
