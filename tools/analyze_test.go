package tools

import (
	"context"
	"database/sql"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"

	_ "modernc.org/sqlite"
)

func newAnalyzeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE player_basics (server_id INTEGER, level INTEGER, virtual_money INTEGER);`); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ s, l, m int }{
		{1, 20, 600000}, {1, 20, 600000}, {1, 50, 600000},
		{2, 20, 700000}, {2, 35, 10000},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO player_basics VALUES (?,?,?)`, r.s, r.l, r.m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func analyzeTestSchema() *schema_protocol.Schema {
	return &schema_protocol.Schema{
		StateTables: map[string]schema_protocol.Table{
			"player_basics": {Fields: map[string]schema_protocol.FieldDef{
				"server_id":     {Type: "int"},
				"level":         {Type: "int"},
				"virtual_money": {Type: "int", Role: "balance"},
			}},
		},
	}
}

func TestAnalyzeTool_Crosstab(t *testing.T) {
	tool := NewAnalyzeTool(analyzeTestSchema(), newAnalyzeTestDB(t))
	resp := tool.Run(context.Background(), AnalyzeInput{
		Table:      "player_basics",
		GroupBy:    []string{"server_id"},
		Aggregates: []schema_protocol.Aggregate{{Fn: "count", As: "players"}},
		OrderBy:    []schema_protocol.OrderKey{{Key: "server_id"}},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status=%s hint=%s", resp.Status, resp.Hint)
	}
	if resp.Table == nil || resp.Table.RowCount != 2 {
		t.Fatalf("table=%+v", resp.Table)
	}
	if got := resp.Table.Columns[0].Name; got != "server_id" {
		t.Fatalf("col0=%s", got)
	}
	if v, _ := resp.Table.Rows[0][1].(int64); v != 3 {
		t.Fatalf("server1 players=%v want 3", resp.Table.Rows[0][1])
	}
	if v, _ := resp.Table.Rows[1][1].(int64); v != 2 {
		t.Fatalf("server2 players=%v want 2", resp.Table.Rows[1][1])
	}
}

func TestAnalyzeTool_SchemaErrorToResponse(t *testing.T) {
	tool := NewAnalyzeTool(analyzeTestSchema(), newAnalyzeTestDB(t))
	resp := tool.Run(context.Background(), AnalyzeInput{Table: "nope", Aggregates: []schema_protocol.Aggregate{{Fn: "count", As: "n"}}})
	if resp.Status != contract.StatusSchemaError || resp.Hint == "" {
		t.Fatalf("want SCHEMA_ERROR with hint, got %+v", resp)
	}
}
