package schema_protocol

import (
	"strings"
	"testing"
)

func TestBuildProfile_NonBalance_Structure(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildProfile(DistQuery{Table: "player_basics", Column: "level"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("no filter → no args, got %v", args)
	}
	for _, want := range []string{
		"WITH base AS",
		"COUNT(*) AS tot",
		"COUNT(DISTINCT v) AS distinct_cnt",
		"MIN(v)",
		"MAX(v)",
		"AVG(v)",
		"ROW_NUMBER() OVER (ORDER BY v) AS rn",
		"FROM ordered WHERE rn = MAX(1, (s.tot * 10 + 99) / 100)",
		"FROM ordered WHERE rn = MAX(1, (s.tot * 50 + 99) / 100)",
		"FROM ordered WHERE rn = MAX(1, (s.tot * 99 + 99) / 100)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("profile SQL missing %q: %s", want, sql)
		}
	}
	// 非 balance：SUM(v) AS total 不应出现
	if strings.Contains(sql, "AS total") {
		t.Fatalf("non-balance must NOT include total: %s", sql)
	}
}

func TestBuildProfile_FilterArgs(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildProfile(DistQuery{
		Table: "player_basics", Column: "level",
		Filter: map[string]any{"last_online_time": map[string]any{"op": "<", "value": int64(1716800000)}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "WHERE last_online_time < ?") {
		t.Fatalf("filter missing: %s", sql)
	}
	if len(args) != 1 || args[0] != int64(1716800000) {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestBuildProfile_RejectsPII(t *testing.T) {
	s := loadTestSchema(t)
	if _, _, err := s.BuildProfile(DistQuery{Table: "player_basics", Column: "player_id"}); err == nil {
		t.Fatal("PII column must be rejected")
	}
}

func TestBuildProfile_RejectsOmitInLayer2(t *testing.T) {
	// loadOmitSchema / omitInLayer2SchemaYAML 定义在同包的 sql_builder_test.go。
	s := loadOmitSchema(t)
	if _, _, err := s.BuildProfile(DistQuery{Table: "t1", Column: "internal_flag"}); err == nil {
		t.Fatal("omit_in_layer2 column must be rejected by BuildProfile")
	} else if _, ok := err.(*SchemaError); !ok {
		t.Fatalf("expected *SchemaError, got %T", err)
	}
}

func TestBuildTopN_Structure(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildTopN(DistQuery{Table: "player_basics", Column: "level"}, 10)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("no filter → no args, got %v", args)
	}
	for _, want := range []string{
		"SELECT CAST(level AS TEXT) AS value, COUNT(*) AS n",
		"FROM player_basics",
		"GROUP BY level",
		"ORDER BY n DESC, level ASC",
		"LIMIT 10",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("topN SQL missing %q: %s", want, sql)
		}
	}
}

func TestBuildProfile_BalanceColumn_HasTotal(t *testing.T) {
	s := loadTestSchema(t)
	// player_currencies.balance 是 balance 列（role=balance）；BuildProfile 末尾应追加 SUM(v) AS total。
	sql, _, err := s.BuildProfile(DistQuery{
		Table: "player_currencies", Column: "balance",
		Filter: map[string]any{"currency_type": "coins"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, ", COALESCE((SELECT SUM(v) FROM base), 0) AS total") {
		t.Fatalf("balance column must emit `, COALESCE((SELECT SUM(v) FROM base), 0) AS total`: %s", sql)
	}
}

func TestBuildTopN_RejectsNonPositiveN(t *testing.T) {
	s := loadTestSchema(t)
	for _, n := range []int{0, -1, -100} {
		if _, _, err := s.BuildTopN(DistQuery{Table: "player_basics", Column: "level"}, n); err == nil {
			t.Fatalf("n=%d must be rejected", n)
		}
	}
}

func TestBuildTopN_FilterArgs(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildTopN(DistQuery{
		Table: "player_basics", Column: "level",
		Filter: map[string]any{"server_id": int64(1)},
	}, 5)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(sql, "WHERE server_id = ?") || !strings.Contains(sql, "LIMIT 5") {
		t.Fatalf("filter/limit missing: %s", sql)
	}
	if len(args) != 1 || args[0] != int64(1) {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestBuildGroupSummary_Structure(t *testing.T) {
	s := loadTestSchema(t)
	sql, args, err := s.BuildGroupSummary(DistQuery{
		Table: "player_basics", Column: "level", GroupBy: []string{"server_id"},
	}, 20)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("no filter → no args, got %v", args)
	}
	for _, want := range []string{
		"SELECT CAST(server_id AS TEXT) AS grp",
		"COUNT(*) AS n",
		"FROM player_basics",
		"GROUP BY server_id",
		"ORDER BY n DESC, server_id ASC",
		"LIMIT 20",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("group summary SQL missing %q: %s", want, sql)
		}
	}
}

func TestBuildGroupSummary_RejectsPIIGroupBy(t *testing.T) {
	s := loadTestSchema(t)
	if _, _, err := s.BuildGroupSummary(DistQuery{
		Table: "player_basics", Column: "level", GroupBy: []string{"player_id"},
	}, 20); err == nil {
		t.Fatal("PII group_by must be rejected")
	}
}

func TestBuildGroupSummary_RejectsMultiDim(t *testing.T) {
	s := loadTestSchema(t)
	if _, _, err := s.BuildGroupSummary(DistQuery{
		Table: "player_basics", Column: "level", GroupBy: []string{"server_id", "level"},
	}, 20); err == nil {
		t.Fatal("multi-dim group_by must be rejected")
	}
}
