package etl

import (
	"testing"

	"github.com/RuntianLee/schema-driven-insight-agent/schema_protocol"
)

const miniSchema = `
version: 1
domain: test
data_sources:
  layer2: {type: sqlite, path: ./x.db}
state_tables:
  player_basics:
    nature: snapshot
    primary_key: [player_id]
    fields:
      player_id: {type: int64, role: actor_id, pk: true, pii: true}
      name:      {type: string, role: actor_name, pii: true, omit_in_layer2: true}
      level:     {type: int32, role: level}
      server_id: {type: int32, role: dimension}
`

func TestBasicsColumns_ExcludesPIIAndSorts(t *testing.T) {
	s, err := schema_protocol.Parse([]byte(miniSchema))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cols, err := BasicsColumns(s, "player_basics")
	if err != nil {
		t.Fatalf("BasicsColumns: %v", err)
	}
	got := make([]string, len(cols))
	for i, c := range cols {
		got[i] = c.Name
	}
	want := []string{"level", "server_id"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSqliteAffinity(t *testing.T) {
	if sqliteAffinity("string") != "TEXT" {
		t.Error("string → TEXT")
	}
	if sqliteAffinity("int64") != "INTEGER" {
		t.Error("int64 → INTEGER")
	}
}
