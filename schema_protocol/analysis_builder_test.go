package schema_protocol

import "testing"

// testAnalysisSchema 构造带 player_basics 的最小 schema（含一个 PII 列用于拒绝测试）。
func testAnalysisSchema() *Schema {
	return &Schema{
		StateTables: map[string]Table{
			"player_basics": {
				Fields: map[string]FieldDef{
					"server_id":       {Type: "int"},
					"level":           {Type: "int"},
					"adventure_level": {Type: "int"},
					"virtual_money":   {Type: "int", Role: "balance"},
					"player_id":       {Type: "string", PII: true},
				},
			},
		},
	}
}

func TestBuildAnalysis_Crosstab(t *testing.T) {
	s := testAnalysisSchema()
	q := AnalysisQuery{
		Table:      "player_basics",
		GroupBy:    []string{"server_id", "level"},
		Aggregates: []Aggregate{{Fn: "count", As: "players"}},
		OrderBy:    []OrderKey{{Key: "players", Desc: true}},
	}
	sql, args, err := s.BuildAnalysis(q)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT server_id, level, COUNT(*) AS players FROM player_basics GROUP BY server_id, level ORDER BY players DESC, server_id ASC, level ASC LIMIT 1000"
	if sql != want {
		t.Fatalf("sql=\n%q\nwant\n%q", sql, want)
	}
	if len(args) != 0 {
		t.Fatalf("args=%v want empty", args)
	}
}

func TestBuildAnalysis_RejectsUnknownTable(t *testing.T) {
	s := testAnalysisSchema()
	_, _, err := s.BuildAnalysis(AnalysisQuery{Table: "nope", Aggregates: []Aggregate{{Fn: "count", As: "n"}}})
	if err == nil {
		t.Fatal("want SchemaError for unknown table")
	}
}

func TestBuildAnalysis_RejectsPIIGroupBy(t *testing.T) {
	s := testAnalysisSchema()
	_, _, err := s.BuildAnalysis(AnalysisQuery{Table: "player_basics", GroupBy: []string{"player_id"}, Aggregates: []Aggregate{{Fn: "count", As: "n"}}})
	if err == nil {
		t.Fatal("want SchemaError for PII group_by")
	}
}

func TestBuildAnalysis_RejectsNoOutput(t *testing.T) {
	s := testAnalysisSchema()
	_, _, err := s.BuildAnalysis(AnalysisQuery{Table: "player_basics"})
	if err == nil {
		t.Fatal("want SchemaError when no group_by and no aggregate")
	}
}

func TestBuildAnalysis_RejectsBadAlias(t *testing.T) {
	s := testAnalysisSchema()
	_, _, err := s.BuildAnalysis(AnalysisQuery{Table: "player_basics", Aggregates: []Aggregate{{Fn: "count", As: "DROP"}}})
	if err == nil {
		t.Fatal("want SchemaError for invalid alias")
	}
}
