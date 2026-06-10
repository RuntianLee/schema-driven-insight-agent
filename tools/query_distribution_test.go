package tools

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"

	_ "modernc.org/sqlite"
)

// fixtureDB 建 player_currencies 表并按 (currency_type, balance, n) 灌数据。
func fixtureDB(t *testing.T, seed func(insert func(ct string, balance int64, n int))) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE player_currencies (player_id TEXT, currency_type TEXT, balance INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO player_currencies VALUES ('p', ?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	seed(func(ct string, balance int64, n int) {
		for i := 0; i < n; i++ {
			if _, err := stmt.Exec(ct, balance); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	})
	stmt.Close()
	tx.Commit()
	return db
}

var wedgeInput = QueryDistributionInput{
	Table: "player_currencies", Column: "balance",
	Filter: map[string]any{"currency_type": "coins"}, BucketKey: "coins_balance",
}

func TestQueryDistribution_OK(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {
		insert("coins", 5000, 200)   // 0~1w
		insert("coins", 50000, 150)  // 1~10w
		insert("coins", 600000, 50)  // 50w+
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), wedgeInput)
	if resp.Status != contract.StatusOK {
		t.Fatalf("want OK, got %s (%s)", resp.Status, resp.Hint)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("want 3 buckets, got %d", len(resp.Data))
	}
	// SP1.A: OK 时 Profile 必填（画像始终输出）。
	if resp.Profile == nil {
		t.Fatal("OK Response must include Profile (SP1.A)")
	}
}

func TestQueryDistribution_Insufficient(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {
		insert("coins", 5000, 50) // total < 100
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), wedgeInput)
	if resp.Status != contract.StatusInsufficient {
		t.Fatalf("want INSUFFICIENT_DATA, got %s", resp.Status)
	}
}

func TestQueryDistribution_Degenerate(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {
		insert("coins", 5000, 1000) // 单值 100% > 99%
		insert("coins", 50000, 1)
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), wedgeInput)
	if resp.Status != contract.StatusOK {
		t.Fatalf("画像模式不再返 DEGENERATE，应得 OK；got %s", resp.Status)
	}
	if resp.Profile == nil {
		t.Fatal("OK 必填 Profile")
	}
	// 退化信号：Top-1 占比 > 0.99
	if len(resp.Profile.TopN) == 0 || resp.Profile.TopN[0].PctPlayers <= 0.99 {
		t.Fatalf("Top-1 应承载退化信号 (>0.99)，got %+v", resp.Profile.TopN)
	}
}

func TestQueryDistribution_SchemaError(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_currencies", Column: "no_such_col", BucketKey: "coins_balance",
	})
	if resp.Status != contract.StatusSchemaError {
		t.Fatalf("want SCHEMA_ERROR, got %s", resp.Status)
	}
	if resp.SchemaPath == "" {
		t.Fatal("SCHEMA_ERROR should carry SchemaPath")
	}
}

func TestQueryDistribution_GroupBy(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {
		// coins 分两桶；basic 全落一桶
		insert("coins", 5000, 200)   // 0~1w
		insert("coins", 600000, 100) // 50w+
		insert("basic", 5000, 150)   // 0~1w
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_currencies", Column: "balance",
		BucketKey: "coins_balance", GroupBy: []string{"currency_type"},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("want OK, got %s (%s)", resp.Status, resp.Hint)
	}
	if len(resp.Groups) == 0 {
		t.Fatalf("group_by mode must return Groups, got Data: %+v", resp.Data)
	}
	sums := map[string]float64{}
	for _, g := range resp.Groups {
		if g.Group == "" {
			t.Fatalf("grouped row missing Group: %+v", g)
		}
		for _, b := range g.Data {
			sums[g.Group] += b.PctPlayers
		}
	}
	if len(sums) != 2 {
		t.Fatalf("want 2 groups, got %d", len(sums))
	}
	for g, sum := range sums {
		if sum < 0.99 || sum > 1.01 {
			t.Fatalf("group %q pct_players sum = %f, want ≈1.0", g, sum)
		}
	}
	// grouped + balance：价值列必须被正确 scan（每组 Data 内）。
	var sawValue bool
	for _, g := range resp.Groups {
		for _, b := range g.Data {
			if b.TotalValue > 0 && b.AvgValue > 0 && b.PctValue > 0 {
				sawValue = true
			}
		}
	}
	if !sawValue {
		t.Fatalf("grouped+balance must populate value columns (total/avg/pct_value): %+v", resp.Groups)
	}
}

func TestQueryDistribution_GroupBySchemaError(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_currencies", Column: "balance", BucketKey: "coins_balance",
		GroupBy: []string{"no_such_col"},
	})
	if resp.Status != contract.StatusSchemaError {
		t.Fatalf("want SCHEMA_ERROR for invalid group_by field, got %s", resp.Status)
	}
}
