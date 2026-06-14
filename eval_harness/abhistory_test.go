// framework/eval_harness/abhistory_test.go
package eval_harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendABHistoryJSONL_WritesAggregateRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ab-history.jsonl")
	ab := &ABReport{LabelA: "baseline", LabelB: "reflection", Runs: 5,
		MeanPassRateA: 0.4, MeanPassRateB: 0.7, MeanDelta: 0.3, Meets20Pct: true}
	meta := ABHistoryMeta{Commit: "abc1234", Adapter: "b3", AgentVersion: "v0.2.0", RanAt: 1733560000}
	if err := AppendABHistoryJSONL(path, ab, meta); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &row); err != nil {
		t.Fatal(err)
	}
	if row["mean_delta"].(float64) != 0.3 || row["adapter"] != "b3" {
		t.Fatalf("行内容不符: %v", row)
	}
	for _, banned := range []string{"prompt", "answer", "final_output", "question"} {
		if strings.Contains(string(data), banned) {
			t.Fatalf("ab-history 不应含 %q", banned)
		}
	}
}
