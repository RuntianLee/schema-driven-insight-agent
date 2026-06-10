package tools

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"

	_ "modernc.org/sqlite"
)

// basicsDB 建 player_basics 并按行灌入。
func basicsDB(t *testing.T, seed func(insert func(server, level, adv, lastOnline int64))) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE player_basics (player_id TEXT, server_id INTEGER, level INTEGER, adventure_level INTEGER, last_online_time INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, _ := db.Begin()
	stmt, err := tx.Prepare(`INSERT INTO player_basics VALUES ('p', ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	seed(func(server, level, adv, lastOnline int64) {
		if _, err := stmt.Exec(server, level, adv, lastOnline); err != nil {
			t.Fatalf("insert: %v", err)
		}
	})
	stmt.Close()
	tx.Commit()
	return db
}

func bucketByLabel(data []contract.BucketRow, label string) (contract.BucketRow, bool) {
	for _, b := range data {
		if b.Bucket == label {
			return b, true
		}
	}
	return contract.BucketRow{}, false
}

// #5 关卡分布：按 adventure_level 原始值分布（无 bucket_key）。
func TestRegression_StageDistribution(t *testing.T) {
	s := fixtureSchema(t)
	db := basicsDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		add := func(adv int64, n int) {
			for i := 0; i < n; i++ {
				insert(1, 50, adv, 1716000000)
			}
		}
		add(10, 120)
		add(35, 60)
		add(80, 20)
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "adventure_level", // 无 bucket_key → 原始值分布
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("want OK, got %s (%s)", resp.Status, resp.Hint)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("want 3 distinct stages, got %d", len(resp.Data))
	}
	b, ok := bucketByLabel(resp.Data, "10") // 原始值作 label
	if !ok {
		t.Fatalf("missing stage 10 row: %+v", resp.Data)
	}
	if b.PlayerCount != 120 {
		t.Fatalf("stage 10 player_count = %d, want 120", b.PlayerCount)
	}
	if b.PctPlayers < 0.5999 || b.PctPlayers > 0.6001 {
		t.Fatalf("stage 10 pct_players = %f, want 0.6 (120/200)", b.PctPlayers)
	}
	// 非 balance 列：不得有价值列。
	if b.TotalValue != 0 || b.PctValue != 0 || b.AvgValue != 0 {
		t.Fatalf("non-balance row must have zero value columns: %+v", b)
	}
	// cum「该值及更高」：最高关卡 80 → cum = 20/200 = 0.1；最低关卡 10 → cum = 1.0。
	top, _ := bucketByLabel(resp.Data, "80")
	if top.CumPctPlayers < 0.0999 || top.CumPctPlayers > 0.1001 {
		t.Fatalf("stage 80 cum_pct_players = %f, want 0.1 (≥80 占比)", top.CumPctPlayers)
	}
	if b.CumPctPlayers < 0.9999 || b.CumPctPlayers > 1.0001 {
		t.Fatalf("stage 10 cum_pct_players = %f, want 1.0 (≥10 即全体)", b.CumPctPlayers)
	}
}

// #1 流失分级：filter last_online_time < cutoff 后按 level 原始值分布。
func TestRegression_ChurnByLevel(t *testing.T) {
	s := fixtureSchema(t)
	const cutoff = int64(1716500000)
	db := basicsDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		// 活跃（lastOnline >= cutoff）：不应计入
		for i := 0; i < 500; i++ {
			insert(1, 20, 10, cutoff+100)
		}
		// 流失（lastOnline < cutoff）：按 level 分布
		add := func(level int64, n int) {
			for i := 0; i < n; i++ {
				insert(1, level, 10, cutoff-100)
			}
		}
		add(20, 150)
		add(50, 90)
		add(95, 10)
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level", // 无 bucket_key → 原始值分布
		Filter: map[string]any{"last_online_time": map[string]any{"op": "<", "value": cutoff}},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("want OK, got %s (%s)", resp.Status, resp.Hint)
	}
	var total int64
	for _, b := range resp.Data {
		total += b.PlayerCount
	}
	if total != 250 {
		t.Fatalf("churned total = %d, want 250 (活跃 500 必须被 filter 排除)", total)
	}
	b, ok := bucketByLabel(resp.Data, "20")
	if !ok {
		t.Fatalf("missing level 20 row: %+v", resp.Data)
	}
	if b.PlayerCount != 150 {
		t.Fatalf("level 20 churn count = %d, want 150", b.PlayerCount)
	}
}

// group_by + 原始值：等级分布 × 服，组内占比和为 1.0。
func TestRegression_GroupByServer(t *testing.T) {
	s := fixtureSchema(t)
	db := basicsDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		add := func(server, level int64, n int) {
			for i := 0; i < n; i++ {
				insert(server, level, 10, 1716000000)
			}
		}
		add(1, 20, 80)
		add(1, 95, 20)
		add(2, 20, 120)
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level", // 无 bucket_key → 原始值分布
		GroupBy: []string{"server_id"},
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
			t.Fatalf("group missing Group key: %+v", g)
		}
		for _, b := range g.Data {
			sums[g.Group] += b.PctPlayers
		}
	}
	if len(sums) != 2 {
		t.Fatalf("want 2 server groups, got %d", len(sums))
	}
	for g, sum := range sums {
		if sum < 0.99 || sum > 1.01 {
			t.Fatalf("server %q pct sum = %f, want ≈1.0", g, sum)
		}
	}
}
