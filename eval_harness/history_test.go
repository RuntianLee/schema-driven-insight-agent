package eval_harness

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/eval_harness/evaluators"
)

func sampleReport() *Report {
	r := NewReport([]string{"data_correctness", "reasoning_quality"})
	r.Add("churn_by_level", evaluators.Score{Evaluator: "data_correctness", Value: 1.0, Pass: true, Display: "1.00 ✓"}, true)
	r.Add("churn_by_level", evaluators.Score{Evaluator: "reasoning_quality", Value: 0.6, Pass: true, Display: "3/5"}, false)
	return r
}

func TestAppendHistoryJSONL_WritesWhitelistedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	meta := HistoryMeta{Commit: "abc1234", Adapter: "demo", AgentVersion: "v0.1.0", RanAt: 1733560000}
	if err := AppendHistoryJSONL(path, sampleReport(), meta); err != nil {
		t.Fatalf("append: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("want 2 rows, got %d", len(lines))
	}
	allowed := map[string]bool{
		"commit": true, "ran_at": true, "adapter": true, "agent_version": true,
		"task_id": true, "evaluator": true, "pass": true, "value": true,
	}
	var sawDC bool
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("line not JSON: %q (%v)", ln, err)
		}
		for k := range m {
			if !allowed[k] {
				t.Errorf("non-whitelisted key leaked: %q", k)
			}
		}
		if m["adapter"] != "demo" || m["commit"] != "abc1234" {
			t.Errorf("meta not stamped: %v", m)
		}
		if m["task_id"] == "churn_by_level" && m["evaluator"] == "data_correctness" {
			sawDC = true
			if m["pass"] != true || m["value"].(float64) != 1.0 {
				t.Errorf("data_correctness row wrong: %v", m)
			}
		}
	}
	if !sawDC {
		t.Error("missing data_correctness row")
	}
}

func TestAppendHistoryJSONL_Appends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	meta := HistoryMeta{Commit: "c1", Adapter: "demo", AgentVersion: "v0.1.0", RanAt: 1}
	if err := AppendHistoryJSONL(path, sampleReport(), meta); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryJSONL(path, sampleReport(), meta); err != nil {
		t.Fatal(err)
	}
	if got := len(readLines(t, path)); got != 4 {
		t.Errorf("append should accumulate: want 4 lines, got %d", got)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			out = append(out, sc.Text())
		}
	}
	return out
}
