package evalcli

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const testSchema = `
version: 1
domain: test_game
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id: {type: int64, role: actor_id, pk: true, pii: true}
      level:     {type: int32, role: level}
`

const testTask = `
id: level_distribution
title: "等级分布（测试）"
question: "玩家等级分布如何？"
llm_turns:
  - '{"tool":"query_distribution","args":{"table":"player_basics","column":"level"}}'
  - "等级集中在 20 级。"
evaluators:
  data_correctness:
    tool: query_distribution
    expect_status: OK
    profile: {count: 150}
  reasoning_quality: {rubric: "是否量化？", min_score: 3}
`

func setup(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.yaml")
	tasksDir := filepath.Join(dir, "tasks")
	if err := os.WriteFile(schemaPath, []byte(testSchema), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "level.yaml"), []byte(testTask), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{
		Adapter:    "test",
		SchemaPath: schemaPath,
		TasksDir:   tasksDir,
		Fixtures: map[string]FixtureFunc{
			"level_distribution": func(fdir string) (*sql.DB, error) {
				db, err := sql.Open("sqlite", filepath.Join(fdir, "t.db"))
				if err != nil {
					return nil, err
				}
				if _, err := db.Exec(`CREATE TABLE player_basics (player_id TEXT, level INTEGER)`); err != nil {
					return nil, err
				}
				for i := 0; i < 150; i++ {
					if _, err := db.Exec(`INSERT INTO player_basics VALUES ('p', 20)`); err != nil {
						return nil, err
					}
				}
				return db, nil
			},
		},
	}
}

func TestRun_GatePassAndFinishExitCode(t *testing.T) {
	opts := setup(t)
	rep, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.GateFailed() {
		t.Fatalf("gate should pass:\n%s", rep.ConsoleTable())
	}
	opts.OutDir = filepath.Join(t.TempDir(), "out")
	if code := Finish(rep, opts); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	files, _ := os.ReadDir(opts.OutDir)
	if len(files) != 2 { // json + md
		t.Fatalf("report files = %d, want 2", len(files))
	}
}

func TestRun_MissingFixtureLoudFail(t *testing.T) {
	opts := setup(t)
	opts.Fixtures = map[string]FixtureFunc{}
	if _, err := Run(opts); err == nil {
		t.Fatal("缺 fixture 映射必须 loud-fail")
	}
}

func TestRun_OnlyTaskFilter(t *testing.T) {
	opts := setup(t)
	opts.OnlyTask = "no_such_task"
	rep, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Tasks) != 0 {
		t.Fatalf("过滤后应无任务, got %v", rep.Tasks)
	}
}
