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

func TestBuildAnalysis_RichFilters(t *testing.T) {
	s := testAnalysisSchema()
	q := AnalysisQuery{
		Table: "player_basics",
		Filters: []Filter{
			{Field: "server_id", Op: "IN", Values: []any{1, 2}},
			{Field: "level", Op: "BETWEEN", Values: []any{20, 50}},
			{Field: "virtual_money", Op: ">=", Value: 1000},
		},
		GroupBy:    []string{"server_id"},
		Aggregates: []Aggregate{{Fn: "count", As: "n"}},
	}
	sql, args, err := s.BuildAnalysis(q)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT server_id, COUNT(*) AS n FROM player_basics WHERE server_id IN (?, ?) AND level BETWEEN ? AND ? AND virtual_money >= ? GROUP BY server_id ORDER BY server_id ASC LIMIT 1000"
	if sql != want {
		t.Fatalf("sql=\n%q\nwant\n%q", sql, want)
	}
	wantArgs := []any{1, 2, 20, 50, 1000}
	if len(args) != len(wantArgs) {
		t.Fatalf("args=%v want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("args[%d]=%v want %v", i, args[i], wantArgs[i])
		}
	}
}

func TestBuildAnalysis_IsNull(t *testing.T) {
	s := testAnalysisSchema()
	q := AnalysisQuery{
		Table:      "player_basics",
		Filters:    []Filter{{Field: "virtual_money", Op: "IS NULL"}},
		Aggregates: []Aggregate{{Fn: "count", As: "n"}},
	}
	sql, args, err := s.BuildAnalysis(q)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT COUNT(*) AS n FROM player_basics WHERE virtual_money IS NULL LIMIT 1000"
	if sql != want {
		t.Fatalf("sql=\n%q\nwant\n%q", sql, want)
	}
	if len(args) != 0 {
		t.Fatalf("args=%v want empty", args)
	}
}

func TestBuildAnalysis_RejectsBadFilter(t *testing.T) {
	s := testAnalysisSchema()
	cases := []AnalysisQuery{
		{Table: "player_basics", Filters: []Filter{{Field: "level", Op: "LIKE", Value: "x"}}, Aggregates: []Aggregate{{Fn: "count", As: "n"}}},
		{Table: "player_basics", Filters: []Filter{{Field: "level", Op: "BETWEEN", Values: []any{1}}}, Aggregates: []Aggregate{{Fn: "count", As: "n"}}},
		{Table: "player_basics", Filters: []Filter{{Field: "level", Op: "IN", Values: []any{}}}, Aggregates: []Aggregate{{Fn: "count", As: "n"}}},
		{Table: "player_basics", Filters: []Filter{{Field: "player_id", Op: "=", Value: "x"}}, Aggregates: []Aggregate{{Fn: "count", As: "n"}}},
	}
	for i, q := range cases {
		if _, _, err := s.BuildAnalysis(q); err == nil {
			t.Errorf("case %d: want SchemaError", i)
		}
	}
}
