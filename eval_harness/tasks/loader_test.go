// framework/eval_harness/tasks/loader_test.go
package tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTask(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadDirParsesTasksSorted(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "b.yaml", "id: b\nquestion: q2\nllm_turns: [\"t1\"]\nevaluators:\n  data_correctness:\n    tool: query_distribution\n")
	writeTask(t, dir, "a.yaml", "id: a\nquestion: q1\nllm_turns: [\"t1\", \"t2\"]\nevaluators:\n  reasoning_quality:\n    rubric: r\n")

	got, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" { // 按文件名排序，确定性
		t.Fatalf("tasks not sorted: %s, %s", got[0].ID, got[1].ID)
	}
	if len(got[0].LLMTurns) != 2 {
		t.Fatalf("a should have 2 turns")
	}
	if _, ok := got[0].Evaluators["reasoning_quality"]; !ok {
		t.Fatalf("a should have reasoning_quality evaluator node")
	}
}

func TestLoadDirRejectsBadYAML(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "bad.yaml", "id: [unterminated")
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("bad yaml must error")
	}
}

func TestLoadDirRejectsMissingID(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "noid.yaml", "question: q\n")
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("missing id must error")
	}
}

func TestLoadTaskFacetsOverride(t *testing.T) {
	dir := t.TempDir()
	writeTask(t, dir, "s.yaml", "id: s\nquestion: q\nfacets: [\"shape:sentinel\"]\nllm_turns: [\"t1\"]\nevaluators:\n  reasoning_quality:\n    rubric: r\n")
	got, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Facets) != 1 || got[0].Facets[0] != "shape:sentinel" {
		t.Fatalf("Facets=%v want [shape:sentinel]", got[0].Facets)
	}
}
