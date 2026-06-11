package tools

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/contract"
	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"

	_ "modernc.org/sqlite"
)

// profileFixtureDB 复用 sp1_regression_test 的 basicsDB 思路：建 player_basics + 灌 (server,level,adv,lastOnline) 行。
func profileFixtureDB(t *testing.T, seed func(insert func(server, level, adv, lastOnline int64))) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE player_basics (player_id TEXT, server_id INTEGER, level INTEGER, quest_level INTEGER, last_online_time INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
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
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return db
}

func TestProfile_StageDistribution(t *testing.T) {
	s := fixtureSchema(t)
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		// 关卡：10 × 120、35 × 60、80 × 20 ⇒ count=200, distinct=3, top1=10
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
		Table: "player_basics", Column: "quest_level",
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s (%s)", resp.Status, resp.Hint)
	}
	if resp.Profile == nil {
		t.Fatal("Profile must be filled on OK")
	}
	p := resp.Profile
	if p.Count != 200 || p.Distinct != 3 {
		t.Fatalf("count/distinct = %d/%d, want 200/3", p.Count, p.Distinct)
	}
	if p.Min != 10 || p.Max != 80 {
		t.Fatalf("min/max = %v/%v, want 10/80", p.Min, p.Max)
	}
	// nearest-rank：p50 取第 max(1, ceil(200*0.50)) = 第 100 行，按 v 升序：前 120 行均为 10 → p50=10
	if p.Median != 10 {
		t.Fatalf("median = %v, want 10 (nearest-rank)", p.Median)
	}
	// p90 取第 180 行：120 行 10 + 60 行 35 = 180 ⇒ p90=35
	if p.P90 != 35 {
		t.Fatalf("p90 = %v, want 35", p.P90)
	}
	// Top-N：默认 10，但只有 3 个 distinct → Top 3 行
	if len(p.TopN) < 1 || p.TopN[0].Value != "10" || p.TopN[0].PlayerCount != 120 {
		t.Fatalf("TopN[0] = %+v, want value=10 count=120", p.TopN)
	}
	if p.TailCount != 0 {
		t.Fatalf("tail_count = %d, want 0 (all distinct ≤ TopN)", p.TailCount)
	}
	// 非 balance：Total 必为 nil
	if p.Total != nil {
		t.Fatalf("non-balance must have Total=nil, got %v", *p.Total)
	}
}

func TestProfile_InsufficientData(t *testing.T) {
	s := fixtureSchema(t)
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		insert(1, 50, 10, 1716000000) // 仅 1 行 < 100
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "quest_level",
	})
	if resp.Status != contract.StatusInsufficient {
		t.Fatalf("status = %s, want INSUFFICIENT_DATA", resp.Status)
	}
	if resp.Profile != nil {
		t.Fatal("Profile must NOT be filled when INSUFFICIENT")
	}
}

// 小组的语义：当某组 player_count < 100 时，runDistribution 返 INSUFFICIENT，
// 该组在 Groups[] 中保留 Profile（已就绪）但 Data 为空（被静默吸收，不影响整体）。
func TestGroupProfile_SmallGroupKeepsProfileEmptyData(t *testing.T) {
	s := fixtureSchema(t)
	// server_1: 200 行；server_2: 60 行 (<100)。总玩家 260 ≥ 100，过全局闸。
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for i := 0; i < 200; i++ {
			insert(1, 50, 10, 1716000000)
		}
		for i := 0; i < 60; i++ {
			insert(2, 50, 10, 1716000000)
		}
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level",
		GroupBy: []string{"server_id"},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s, want OK (totalPlayers=260 ≥ 100)", resp.Status)
	}
	if len(resp.Groups) != 2 {
		t.Fatalf("want 2 groups, got %d", len(resp.Groups))
	}
	for _, g := range resp.Groups {
		if g.Profile.Count == 0 {
			t.Fatalf("group %s: Profile.Count == 0, profile should always be populated", g.Group)
		}
		if g.Group == "2" {
			if g.Profile.Count != 60 {
				t.Fatalf("server_2 Profile.Count = %d, want 60", g.Profile.Count)
			}
			if len(g.Data) != 0 {
				t.Fatalf("server_2 (60<100) Data 应空（INSUFFICIENT 被吸收），got %d 行", len(g.Data))
			}
		}
	}
}

func TestGroupProfile_TopNAndTail(t *testing.T) {
	s := fixtureSchema(t)
	// 内联 test schema 含 demo tuning（groups_top_n=5）；此用例验证 framework 默认 Top-N=20 + 尾部行为，
	// 故清零 Tuning 让 tool 回退默认。
	s.Tuning.GroupsTopN = 0
	// 25 个 server，各 200 行 → server=1..25 等大。Top-N=20，尾部 5 组。
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for sid := int64(1); sid <= 25; sid++ {
			for i := 0; i < 200; i++ {
				insert(sid, 50, 10, 1716000000)
			}
		}
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level",
		GroupBy: []string{"server_id"},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if resp.Profile != nil {
		t.Fatal("group_by 模式 Profile 必须为 nil（数据进 Groups）")
	}
	if len(resp.Groups) != 20 {
		t.Fatalf("Groups 长度 = %d, want 20", len(resp.Groups))
	}
	for _, g := range resp.Groups {
		if g.Group == "" {
			t.Fatal("Group 维度值不能空")
		}
		if g.Profile.Count != 200 {
			t.Fatalf("group %s count = %d, want 200", g.Group, g.Profile.Count)
		}
	}
	if resp.GroupsTail == nil {
		t.Fatal("GroupsTail 必须填")
	}
	if resp.GroupsTail.GroupCount != 5 {
		t.Fatalf("GroupsTail.GroupCount = %d, want 5", resp.GroupsTail.GroupCount)
	}
	if resp.GroupsTail.PlayerCount != 5*200 {
		t.Fatalf("GroupsTail.PlayerCount = %d, want 1000", resp.GroupsTail.PlayerCount)
	}
}

func TestProfile_RowsAttachThreshold(t *testing.T) {
	s := fixtureSchema(t)

	// distinct=1000：恰阈值，Data 应填
	dbAt := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for v := int64(1); v <= 1000; v++ {
			insert(1, 50, v, 1716000000) // 每个 quest_level 灌 1 行 → distinct=1000, count=1000
		}
	})
	resp := NewDistributionTool(s, dbAt).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "quest_level",
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if resp.Profile == nil || resp.Profile.Distinct != 1000 {
		t.Fatalf("distinct = %d, want 1000", resp.Profile.Distinct)
	}
	if len(resp.Data) == 0 {
		t.Fatal("distinct=1000 (恰阈值) → Data 应该填")
	}

	// distinct=1001：超阈值，Data 应空
	dbOver := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for v := int64(1); v <= 1001; v++ {
			insert(1, 50, v, 1716000000)
		}
	})
	resp2 := NewDistributionTool(s, dbOver).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "quest_level",
	})
	if resp2.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp2.Status)
	}
	if resp2.Profile == nil || resp2.Profile.Distinct != 1001 {
		t.Fatalf("distinct = %d, want 1001", resp2.Profile.Distinct)
	}
	if len(resp2.Data) != 0 {
		t.Fatalf("distinct=1001 (超阈值) → Data 应空，got %d 行", len(resp2.Data))
	}
}

func TestProfile_BalanceTotal(t *testing.T) {
	s := fixtureSchema(t)
	db := fixtureDB(t, func(insert func(string, int64, int)) {
		insert("coins", 5000, 200)   // 100 万
		insert("coins", 50000, 150)  // 750 万
		insert("coins", 600000, 50)  // 3000 万
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_currencies", Column: "balance",
		Filter: map[string]any{"currency_type": "coins"}, BucketKey: "coins_balance",
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s (%s)", resp.Status, resp.Hint)
	}
	if resp.Profile == nil {
		t.Fatal("Profile must be filled")
	}
	if resp.Profile.Total == nil {
		t.Fatal("balance Profile must include Total")
	}
	want := int64(200*5000 + 150*50000 + 50*600000) // = 1_000_000 + 7_500_000 + 30_000_000 = 38_500_000
	if *resp.Profile.Total != want {
		t.Fatalf("Total = %d, want %d", *resp.Profile.Total, want)
	}
}

func TestProfile_NonBalance_TotalNil(t *testing.T) {
	s := fixtureSchema(t)
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for i := 0; i < 150; i++ {
			insert(1, 50, 10, 1716000000)
		}
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level",
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if resp.Profile.Total != nil {
		t.Fatalf("non-balance Total must be nil, got %v", *resp.Profile.Total)
	}
}

func TestProfile_LargeDistinct_DataOmittedProfileFilled(t *testing.T) {
	s := fixtureSchema(t)
	// 1500 distinct，每个 1 行 → count=1500, distinct=1500 > 1000 → Data 空但 Profile 完整
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for v := int64(1); v <= 1500; v++ {
			insert(1, 50, v, 1716000000)
		}
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "quest_level",
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("distinct=1500 → Data 应空")
	}
	p := resp.Profile
	if p == nil || p.Count != 1500 || p.Distinct != 1500 {
		t.Fatalf("profile not populated: %+v", p)
	}
	if p.Min != 1 || p.Max != 1500 {
		t.Fatalf("min/max = %v/%v", p.Min, p.Max)
	}
	// nearest-rank：p50 取第 750 行（v=750），p10 取第 150 行（v=150）
	if p.Median != 750 || p.P10 != 150 {
		t.Fatalf("p50/p10 = %v/%v, want 750/150", p.Median, p.P10)
	}
	if len(p.TopN) != defaultValueTopN {
		t.Fatalf("TopN length = %d, want %d", len(p.TopN), defaultValueTopN)
	}
	if p.TailCount != p.Count-int64(defaultValueTopN) { // 每值 1 行 → Top-N head=10
		t.Fatalf("tail_count = %d, want %d", p.TailCount, p.Count-int64(defaultValueTopN))
	}
}

// 非整除点回归：N=101 时 p50 rank = ceil(101*0.50) = 51（标准 nearest-rank）。
// 此前截断实现取第 50 行——本用例钉死 ceil 口径。
func TestProfile_NearestRankCeilOddN(t *testing.T) {
	s := fixtureSchema(t)
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for v := int64(1); v <= 101; v++ {
			insert(1, 50, v, 1716000000)
		}
	})
	tool := NewDistributionTool(s, db)
	resp := tool.Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "quest_level",
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s (%s)", resp.Status, resp.Hint)
	}
	p := resp.Profile
	if p == nil || p.Count != 101 {
		t.Fatalf("profile: %+v", p)
	}
	if p.Median != 51 {
		t.Fatalf("median = %v, want 51 (ceil nearest-rank)", p.Median)
	}
	if p.P25 != 26 { // ceil(101*0.25)=26
		t.Fatalf("p25 = %v, want 26", p.P25)
	}
	if len(p.TopN) != defaultValueTopN {
		t.Fatalf("TopN length = %d, want %d", len(p.TopN), defaultValueTopN)
	}
	if p.TailCount != p.Count-int64(defaultValueTopN) { // 每值 1 行 → Top-N head=10
		t.Fatalf("tail_count = %d, want %d", p.TailCount, p.Count-int64(defaultValueTopN))
	}
}

// M3：空表场景——0 行不应导致 SCHEMA_ERROR（NULL→float64 scan 失败），
// 应正常走 prof.Count<100 → INSUFFICIENT_DATA。
func TestProfile_EmptyTable_InsufficientNotSchemaError(t *testing.T) {
	s := fixtureSchema(t)
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		// 不灌任何数据 → player_basics 空表
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "quest_level",
	})
	if resp.Status != contract.StatusInsufficient {
		t.Fatalf("status = %s, want INSUFFICIENT (NOT SchemaError); detail=%v", resp.Status, resp.Detail)
	}
	if resp.Profile != nil {
		t.Fatal("Profile must NOT be filled when INSUFFICIENT")
	}
}

// C：阈值参数化——schema.yaml tuning: 节存在时工具应读取，否则回退 framework 默认。
func TestTool_TuningOverridesDefaults(t *testing.T) {
	const yamlSrc = `
version: 1
domain: test
tuning:
  rows_attach_threshold: 50
  value_top_n: 3
  groups_top_n: 4
  per_group_rows_attach_threshold: 7
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:        {type: int64, role: actor_id, pii: true}
      server_id:        {type: int32, role: dimension}
      level:            {type: int16, role: level}
      quest_level:  {type: int16, role: stage_progress}
      last_online_time: {type: unix_timestamp_seconds, role: last_seen}
glossary:
  buckets:
    placeholder:
      - {min: 0, max: null, label: "x"}
`
	s, err := schema_protocol.Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tool := NewDistributionTool(s, nil) // db 不需要——只测阈值 getter
	if got := tool.rowsAttachThreshold(); got != 50 {
		t.Fatalf("rowsAttachThreshold = %d, want 50", got)
	}
	if got := tool.valueTopN(); got != 3 {
		t.Fatalf("valueTopN = %d, want 3", got)
	}
	if got := tool.groupsTopN(); got != 4 {
		t.Fatalf("groupsTopN = %d, want 4", got)
	}
	if got := tool.perGroupRowsAttachThreshold(); got != 7 {
		t.Fatalf("perGroupRowsAttachThreshold = %d, want 7 (explicit tuning)", got)
	}
}

func TestTool_TuningFallbacksToDefaults(t *testing.T) {
	// 用内联无 tuning 节的 yaml 验证回退（不依赖 adapter schema）。
	const yamlSrc = `
version: 1
domain: test
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id: {type: int64, role: actor_id, pii: true}
      level:     {type: int16, role: level}
`
	s, err := schema_protocol.Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tool := NewDistributionTool(s, nil)
	if got := tool.rowsAttachThreshold(); got != defaultRowsAttachThreshold {
		t.Fatalf("rowsAttachThreshold = %d, want %d", got, defaultRowsAttachThreshold)
	}
	if got := tool.valueTopN(); got != defaultValueTopN {
		t.Fatalf("valueTopN = %d, want %d", got, defaultValueTopN)
	}
	if got := tool.groupsTopN(); got != defaultGroupsTopN {
		t.Fatalf("groupsTopN = %d, want %d", got, defaultGroupsTopN)
	}
	// F3: perGroupRowsAttachThreshold 缺省时由 rows_attach / groups_top_n 推导
	// → 默认 1000/20 = 50。
	wantPerGroup := int64(defaultRowsAttachThreshold / defaultGroupsTopN)
	if got := tool.perGroupRowsAttachThreshold(); got != wantPerGroup {
		t.Fatalf("perGroupRowsAttachThreshold = %d, want %d (derived from %d/%d)",
			got, wantPerGroup, defaultRowsAttachThreshold, defaultGroupsTopN)
	}
}

// F1：per-group Data 阈值默认推导 = rows_attach / groups_top_n。
// 用 rows_attach=20, groups_top_n=4 → per_group=5。两个 server，每个 8 distinct level（>5）→ Data 均空。
func TestGroupProfile_PerGroupRowsAttach_DerivedDefaultGatesData(t *testing.T) {
	const yamlSrc = `
version: 1
domain: test
tuning:
  rows_attach_threshold: 20
  groups_top_n: 4
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:        {type: int64, role: actor_id, pii: true}
      server_id:        {type: int32, role: dimension}
      level:            {type: int16, role: level}
      quest_level:  {type: int16, role: stage_progress}
      last_online_time: {type: unix_timestamp_seconds, role: last_seen}
`
	s, err := schema_protocol.Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 每个 server 灌 8 个不同 level × 20 行 = 160 行 → distinct=8 > per_group(=5)。
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for sid := int64(1); sid <= 2; sid++ {
			for lv := int64(1); lv <= 8; lv++ {
				for i := 0; i < 20; i++ {
					insert(sid, lv, 10, 1716000000)
				}
			}
		}
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level",
		GroupBy: []string{"server_id"},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	if len(resp.Groups) != 2 {
		t.Fatalf("Groups = %d, want 2", len(resp.Groups))
	}
	for _, g := range resp.Groups {
		if g.Profile.Distinct != 8 {
			t.Fatalf("group %s distinct = %d, want 8", g.Group, g.Profile.Distinct)
		}
		if len(g.Data) != 0 {
			t.Fatalf("group %s: distinct=8 > 推导阈值 5 → Data 应空, got %d 行", g.Group, len(g.Data))
		}
	}
}

// F1：当每组 distinct ≤ per_group 推导阈值时，Data 正常附带（确保不退化为「全部禁附带」）。
func TestGroupProfile_PerGroupRowsAttach_BelowDerivedCap_AttachesData(t *testing.T) {
	const yamlSrc = `
version: 1
domain: test
tuning:
  rows_attach_threshold: 20
  groups_top_n: 4
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id:        {type: int64, role: actor_id, pii: true}
      server_id:        {type: int32, role: dimension}
      level:            {type: int16, role: level}
      quest_level:  {type: int16, role: stage_progress}
      last_online_time: {type: unix_timestamp_seconds, role: last_seen}
`
	s, err := schema_protocol.Parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 每 server 灌 3 个不同 level × 50 行 → distinct=3 ≤ per_group(=5)，Data 应填。
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		for sid := int64(1); sid <= 2; sid++ {
			for lv := int64(1); lv <= 3; lv++ {
				for i := 0; i < 50; i++ {
					insert(sid, lv, 10, 1716000000)
				}
			}
		}
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level",
		GroupBy: []string{"server_id"},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s", resp.Status)
	}
	for _, g := range resp.Groups {
		if len(g.Data) != 3 {
			t.Fatalf("group %s distinct=3 ≤ 阈值 → Data 应 3 行, got %d", g.Group, len(g.Data))
		}
	}
}

// B：区间下钻 e2e——filter 用同字段数组形（[{>=15},{<=40}]）筛 15-40 段。
func TestProfile_FilterRange_EndToEnd(t *testing.T) {
	s := fixtureSchema(t)
	db := profileFixtureDB(t, func(insert func(server, level, adv, lastOnline int64)) {
		// 4 段：5×30、20×60、30×80、80×40 → 区间 [15,40] 内应只匹 level=20/30 段（共 140 行）
		add := func(level int64, n int) {
			for i := 0; i < n; i++ {
				insert(1, level, 10, 1716000000)
			}
		}
		add(5, 30)
		add(20, 60)
		add(30, 80)
		add(80, 40)
	})
	resp := NewDistributionTool(s, db).Run(context.Background(), QueryDistributionInput{
		Table: "player_basics", Column: "level",
		Filter: map[string]any{
			"level": []any{
				map[string]any{"op": ">=", "value": int64(15)},
				map[string]any{"op": "<=", "value": int64(40)},
			},
		},
	})
	if resp.Status != contract.StatusOK {
		t.Fatalf("status = %s (%s)", resp.Status, resp.Hint)
	}
	if resp.Profile == nil {
		t.Fatal("Profile must be filled")
	}
	if resp.Profile.Count != 140 {
		t.Fatalf("count = %d, want 140 (60+80, level∈[15,40])", resp.Profile.Count)
	}
	if resp.Profile.Min != 20 || resp.Profile.Max != 30 {
		t.Fatalf("min/max = %v/%v, want 20/30", resp.Profile.Min, resp.Profile.Max)
	}
}
