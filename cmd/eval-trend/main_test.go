// framework/cmd/eval-trend/main_test.go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_WritesHTML(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "h.jsonl")
	os.WriteFile(hist, []byte(`{"ran_at":100,"adapter":"b3","agent_version":"v0.1.0","task_id":"t1","evaluator":"data_correctness","pass":true,"value":1}`+"\n"), 0o644)
	out := filepath.Join(dir, "trend.html")
	if err := run(hist, "", out); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "<svg") {
		t.Fatalf("HTML 应含内联 SVG")
	}
}
