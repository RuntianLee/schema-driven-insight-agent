package introspect

import (
	"os"
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

func sampleTables() []TableInfo {
	return []TableInfo{{
		Name: "player_basics",
		Columns: []Column{
			{Name: "player_id", PGType: "bigint", IsPK: true},
			{Name: "server_id", PGType: "integer"},
			{Name: "name", PGType: "character varying"},
			{Name: "coins", PGType: "bigint"},
			{Name: "last_online_time", PGType: "bigint"},
			{Name: "player_attr", PGType: "jsonb"}, // 不支持 → 注释列出
		},
	}}
}

func TestTypeMap(t *testing.T) {
	cases := map[string]string{
		"bigint": "int64", "integer": "int32", "smallint": "int32",
		"text": "string", "character varying": "string",
	}
	for pg, want := range cases {
		got, ok := TypeMap(pg)
		if !ok || got != want {
			t.Errorf("TypeMap(%s) = %q, %v", pg, got, ok)
		}
	}
	if _, ok := TypeMap("jsonb"); ok {
		t.Error("jsonb 应为不支持")
	}
}

func TestRenderSchema_Golden(t *testing.T) {
	got := RenderSchema("my_game", "MY_PG_DSN", sampleTables())
	golden, err := os.ReadFile("testdata/schema_draft.golden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(golden) {
		t.Errorf("草稿与 golden 不一致。\n--- got ---\n%s\n--- want ---\n%s", got, golden)
	}
}

func TestRenderSchema_DraftIsRejectedByParser(t *testing.T) {
	// 安全闸验收：init 草稿必然被 Parse 拒绝（role: TODO），人工标注前不可运行。
	got := RenderSchema("my_game", "MY_PG_DSN", sampleTables())
	if _, err := schema_protocol.Parse(got); err == nil {
		t.Fatal("草稿不应能直接通过 schema_protocol.Parse")
	}
}
