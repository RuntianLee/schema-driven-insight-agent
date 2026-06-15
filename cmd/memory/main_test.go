package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInitManualAndSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	notePath := filepath.Join(dir, "notes.yaml")
	writeFile(t, notePath, `notes:
  - id: whale-retention-note
    task_id: retention-task
    question: How should whale retention be analyzed?
    summary: Whale retention analysis should compare cohort survival and repeat activity.
    answer_outline: Use analyze to compute whale retention cohorts.
    tools: [analyze]
    tags: [retention, whale]
    score: 1
`)

	var out bytes.Buffer
	if code := run([]string{"-memory-db", dbPath, "-init"}, &out); code != 0 {
		t.Fatalf("init exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "initialized") {
		t.Fatalf("init output = %q, want initialized", out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", dbPath, "-adapter", "b3", "-manual", notePath}, &out); code != 0 {
		t.Fatalf("manual ingest exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "manual ingest inserted=1") {
		t.Fatalf("manual ingest output = %q, want inserted count", out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", dbPath, "-adapter", "b3", "-search", "whale retention analyze"}, &out); code != 0 {
		t.Fatalf("search exit code = %d, want 0; output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Whale retention analysis should compare cohort survival") {
		t.Fatalf("search output = %q, want manual note summary", out.String())
	}
}

func TestRunRequiresAction(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{}, &out); code != 2 {
		t.Fatalf("exit code = %d, want 2; output=%s", code, out.String())
	}
}

func TestRunIngestRequiresAdapter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "memory.db")
	notePath := filepath.Join(dir, "notes.yaml")
	writeFile(t, notePath, "notes: []\n")

	var out bytes.Buffer
	if code := run([]string{"-memory-db", dbPath, "-manual", notePath}, &out); code != 2 {
		t.Fatalf("manual without adapter exit code = %d, want 2; output=%s", code, out.String())
	}

	out.Reset()
	if code := run([]string{"-memory-db", dbPath, "-trajectory-db", filepath.Join(dir, "trajectory.db")}, &out); code != 2 {
		t.Fatalf("trajectory without adapter exit code = %d, want 2; output=%s", code, out.String())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
