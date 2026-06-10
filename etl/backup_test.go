package etl

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateBackup_CreatesTodayAndPrunesOld(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.db")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 8 天前的旧备份（应删）+ 6 天前（应留）。
	old := src + ".bak." + time.Now().AddDate(0, 0, -8).Format("20060102")
	keep := src + ".bak." + time.Now().AddDate(0, 0, -6).Format("20060102")
	for _, p := range []string{old, keep} {
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := RotateBackup(src); err != nil {
		t.Fatalf("RotateBackup: %v", err)
	}
	today := src + ".bak." + time.Now().Format("20060102")
	if _, err := os.Stat(today); err != nil {
		t.Errorf("today backup missing: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("6-day backup should be kept: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("8-day backup should be pruned, stat err=%v", err)
	}
}
