package etl_health

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etl_health.json")
	in := Health{Status: StatusOK, Rows: 1080000, FinishedAt: time.Now(), SchemaVersion: 2}
	if err := Write(path, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Status != StatusOK || out.Rows != 1080000 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestReadyGate(t *testing.T) {
	cases := []struct {
		name string
		h    Health
		want bool
	}{
		{"ok", Health{Status: StatusOK, Rows: 1080000, FinishedAt: time.Now()}, true},
		{"failed", Health{Status: StatusFailed, Rows: 1080000, FinishedAt: time.Now()}, false},
		{"too-few-rows", Health{Status: StatusOK, Rows: 50000, FinishedAt: time.Now()}, false},
		{"stale", Health{Status: StatusOK, Rows: 1080000, FinishedAt: time.Now().Add(-48 * time.Hour)}, false},
		{"at-threshold", Health{Status: StatusOK, Rows: 100000, FinishedAt: time.Now()}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := c.h.Ready()
			if ok != c.want {
				t.Fatalf("Ready()=%v want %v (reason=%s)", ok, c.want, reason)
			}
		})
	}
}

func TestReadyGate_Overrides(t *testing.T) {
	small := int64(500)
	cases := []struct {
		name string
		h    Health
		want bool
	}{
		// 自定 min_rows：500 行 + override 500 → 过；无 override 则 500 < 10w 不过。
		{"override-min-rows-pass", Health{Status: StatusOK, Rows: 500, FinishedAt: time.Now(), MinRowsOverride: &small}, true},
		{"no-override-small-fails", Health{Status: StatusOK, Rows: 500, FinishedAt: time.Now()}, false},
		// frozen：48h 前完成本应判 stale，frozen 跳过失鲜 → 过（仍需 rows 达标）。
		{"frozen-skips-staleness", Health{Status: StatusOK, Rows: 500, FinishedAt: time.Now().Add(-48 * time.Hour), Frozen: true, MinRowsOverride: &small}, true},
		// frozen 但行数不足仍不过。
		{"frozen-still-checks-rows", Health{Status: StatusOK, Rows: 10, FinishedAt: time.Now(), Frozen: true, MinRowsOverride: &small}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := c.h.Ready()
			if ok != c.want {
				t.Fatalf("Ready()=%v want %v (reason=%s)", ok, c.want, reason)
			}
		})
	}
}

func TestWriteReadRoundTrip_NewFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.json")
	m := int64(500)
	in := Health{Status: StatusOK, Rows: 500, FinishedAt: time.Now(), SchemaVersion: 1, Frozen: true, MinRowsOverride: &m, DataAsOf: 1716500000}
	if err := Write(path, in); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := Read(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !out.Frozen || out.MinRowsOverride == nil || *out.MinRowsOverride != 500 || out.DataAsOf != 1716500000 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}
